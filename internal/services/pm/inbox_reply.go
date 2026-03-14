package pm

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"dalek/internal/contracts"
	workersvc "dalek/internal/services/worker"

	"gorm.io/gorm"
)

const (
	maxWaitUserRounds = 3

	inboxReplyModeSingle = "single_ticket"
	inboxReplyModeFocus  = "focus_batch"

	inboxReplyExcerptLimit = 120
)

var needsUserTitleSuffixRE = regexp.MustCompile(`[：:]\s*t\d+\s+w\d+\s*$`)

type InboxReplyResult struct {
	InboxID     uint
	TicketID    uint
	WorkerID    uint
	Action      contracts.InboxReplyAction
	Mode        string
	RunID       uint
	NextAction  string
	FocusID     uint
	Accepted    bool
	FocusedItem uint
}

func (s *Service) ReplyInboxItem(ctx context.Context, id uint, rawAction, reply string) (InboxReplyResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action, err := contracts.ParseInboxReplyAction(rawAction)
	if err != nil {
		return InboxReplyResult{}, err
	}
	if strings.TrimSpace(reply) == "" {
		return InboxReplyResult{}, fmt.Errorf("reply 不能为空")
	}

	item, err := s.GetInboxItem(ctx, id)
	if err != nil {
		return InboxReplyResult{}, err
	}
	if item == nil {
		return InboxReplyResult{}, gorm.ErrRecordNotFound
	}
	if item.Status != contracts.InboxOpen {
		return InboxReplyResult{}, fmt.Errorf("inbox#%d 不是 open 状态", id)
	}
	if item.Reason != contracts.InboxNeedsUser {
		return InboxReplyResult{}, fmt.Errorf("inbox#%d 不是 needs_user 类型", id)
	}
	if item.TicketID == 0 {
		return InboxReplyResult{}, fmt.Errorf("inbox#%d 缺少 ticket_id", id)
	}
	if item.OriginTaskRunID == 0 {
		item, err = s.ensureNeedsUserInboxAnchor(ctx, item)
		if err != nil {
			return InboxReplyResult{}, err
		}
	}
	if item.WaitRoundCount >= maxWaitUserRounds {
		_ = s.ensureWaitUserRoundLimitVisible(context.WithoutCancel(ctx), item.ID, item.Title, item.Body)
		return InboxReplyResult{}, fmt.Errorf("wait_user 链已达到 %d 次上限，当前 inbox 不再自动恢复；需要 PM/用户手工处理", maxWaitUserRounds)
	}

	ticket, err := s.loadInboxReplyTicket(ctx, item.TicketID)
	if err != nil {
		return InboxReplyResult{}, err
	}

	focusRun, focusItem, err := s.focusReplyTarget(ctx, item.TicketID)
	if err != nil {
		return InboxReplyResult{}, err
	}
	if focusRun != nil && focusItem != nil {
		if err := s.storeInboxReplyIntent(ctx, item.ID, action, reply); err != nil {
			return InboxReplyResult{}, err
		}
		if err := s.focusAppendEvent(ctx, focusRun.ID, focusItem.ID, contracts.FocusEventInboxReplyAccepted, "focus inbox reply accepted", inboxReplyAuditPayload(*item, action, reply)); err != nil {
			return InboxReplyResult{}, err
		}
		out := InboxReplyResult{
			InboxID:     item.ID,
			TicketID:    item.TicketID,
			WorkerID:    item.WorkerID,
			Action:      action,
			Mode:        inboxReplyModeFocus,
			FocusID:     focusRun.ID,
			Accepted:    true,
			FocusedItem: focusItem.ID,
		}
		return out, nil
	}

	if contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus) != contracts.TicketBlocked {
		return InboxReplyResult{}, fmt.Errorf("ticket t%d 当前不是 blocked，不能走单 ticket inbox 恢复", ticket.ID)
	}

	if err := s.storeInboxReplyIntent(ctx, item.ID, action, reply); err != nil {
		return InboxReplyResult{}, err
	}
	prompt := buildInboxReplyPrompt(*item, action, reply)
	baseBranch, err := requiredWorkerBaseBranch(*ticket)
	if err != nil {
		return InboxReplyResult{}, err
	}
	if _, err := s.prepareInboxReplyWorker(ctx, *ticket, baseBranch); err != nil {
		return InboxReplyResult{}, err
	}
	autoStart := false
	runResult, err := s.RunTicketWorker(ctx, ticket.ID, WorkerRunOptions{
		EntryPrompt: prompt,
		AutoStart:   &autoStart,
		BaseBranch:  baseBranch,
	})
	if err != nil {
		return InboxReplyResult{}, err
	}
	if err := s.markInboxReplyConsumed(ctx, item.ID); err != nil {
		return InboxReplyResult{}, err
	}
	return InboxReplyResult{
		InboxID:    item.ID,
		TicketID:   runResult.TicketID,
		WorkerID:   runResult.WorkerID,
		Action:     action,
		Mode:       inboxReplyModeSingle,
		RunID:      runResult.RunID,
		NextAction: strings.TrimSpace(runResult.LastNextAction),
		Accepted:   true,
	}, nil
}

func buildInboxReplyPrompt(item contracts.InboxItem, action contracts.InboxReplyAction, reply string) string {
	title := strings.TrimSpace(item.Title)
	title = needsUserTitleSuffixRE.ReplaceAllString(title, "")
	title = strings.TrimSpace(title)
	if title == "" {
		title = "需要你输入"
	}
	body := strings.TrimSpace(item.Body)
	if body == "" {
		body = "(当前 inbox 未提供额外阻塞说明)"
	}

	checkLines := []string{
		"1. 先判断 `<reply>` 是否足以解除 `<context>` 里的阻塞；如果不够，不要猜测，必须再次执行 `dalek worker report --next wait_user --needs-user true --summary \"...\"`。",
		"2. 如果 `<reply>` 提到任何文件、目录、日志、文档或资料路径，必须先验证这些路径在当前机器上真实存在且可读；若不存在或不可读，必须再次 wait_user，并建议用户把资料放到稳定路径（例如 `/tmp/xxx.md`）。",
		"3. 只围绕这次阻塞恢复执行，不要把 ticket_id、inbox_id 或其他外部标识重新带回 worker prompt。",
	}
	switch action {
	case contracts.InboxReplyDone:
		checkLines = append(checkLines, "4. 如果检查通过，本轮只允许做最小收尾执行：核对代码、测试、`.dalek/state.json` 与 git/worktree 事实，并由你自己决定 report `done`、`continue` 或 `wait_user`；不要直接改 ticket 字段，也不要扩展任务范围。")
	default:
		checkLines = append(checkLines, "4. 如果检查通过，继续推进当前 ticket 的最小必要实现、验证与状态收口；若仍未完成，按真实状态继续 report。")
	}

	return strings.Join([]string{
		"你正在恢复一个因需要人工输入而阻塞的 ticket。",
		"",
		"<context>",
		"你此前因需要人工输入而暂停。",
		"",
		"以下内容来自当前 needs_user inbox 中记录的阻塞说明：",
		"",
		title,
		"",
		body,
		"",
		"当前动作：" + inboxReplyActionLabel(action),
		"</context>",
		"",
		"<reply>",
		strings.TrimRight(reply, "\n"),
		"</reply>",
		"",
		"<check>",
		strings.Join(checkLines, "\n"),
		"</check>",
	}, "\n")
}

func inboxReplyActionLabel(action contracts.InboxReplyAction) string {
	switch action {
	case contracts.InboxReplyDone:
		return string(contracts.InboxReplyDone)
	default:
		return string(contracts.InboxReplyContinue)
	}
}

func inboxReplyAuditPayload(inbox contracts.InboxItem, action contracts.InboxReplyAction, reply string) map[string]any {
	return map[string]any{
		"ticket_id":     inbox.TicketID,
		"inbox_id":      inbox.ID,
		"action":        string(action),
		"reply":         reply,
		"reply_excerpt": inboxReplyExcerpt(reply),
	}
}

func inboxReplyExcerpt(reply string) string {
	reply = strings.TrimSpace(reply)
	reply = strings.ReplaceAll(reply, "\r\n", "\n")
	reply = strings.ReplaceAll(reply, "\n", " ")
	reply = strings.Join(strings.Fields(reply), " ")
	if reply == "" {
		return ""
	}
	runes := []rune(reply)
	if len(runes) <= inboxReplyExcerptLimit {
		return reply
	}
	return strings.TrimSpace(string(runes[:inboxReplyExcerptLimit])) + "..."
}

func needsUserManualInterventionMessage() string {
	return "当前 wait_user 链已达到 3 轮上限，需要 PM/用户手工处理"
}

func waitUserRoundLimitTitle(title string) string {
	title = strings.TrimSpace(title)
	if strings.Contains(title, "已达 3 轮上限") {
		return title
	}
	if title == "" {
		title = "需要人工处理"
	}
	return title + "（已达 3 轮上限，需要 PM/用户手工处理）"
}

func waitUserRoundLimitBody(body string) string {
	notice := "系统提示：" + needsUserManualInterventionMessage() + "。"
	body = strings.TrimSpace(body)
	if strings.Contains(body, notice) {
		return body
	}
	if body == "" {
		return notice
	}
	return body + "\n\n" + notice
}

func (s *Service) ensureWaitUserRoundLimitVisible(ctx context.Context, inboxID uint, title, body string) error {
	if inboxID == 0 {
		return nil
	}
	title = waitUserRoundLimitTitle(title)
	body = waitUserRoundLimitBody(body)
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	return db.WithContext(ctx).
		Model(&contracts.InboxItem{}).
		Where("id = ?", inboxID).
		Updates(map[string]any{
			"title":      title,
			"body":       body,
			"updated_at": now,
		}).Error
}

func (s *Service) prepareInboxReplyWorker(ctx context.Context, ticket contracts.Ticket, baseBranch string) (*contracts.Worker, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	preStartWorker, err := s.worker.LatestWorker(ctx, ticket.ID)
	if err != nil {
		return nil, err
	}
	preStartReady := false
	if preStartWorker != nil && (preStartWorker.Status == contracts.WorkerRunning || preStartWorker.Status == contracts.WorkerStopped) {
		ready, rerr := s.workerDispatchReady(ctx, preStartWorker)
		if rerr != nil {
			return nil, rerr
		}
		preStartReady = ready
	}
	w, err := s.worker.StartTicketResourcesWithOptions(ctx, ticket.ID, workersvc.StartOptions{
		BaseBranch: strings.TrimSpace(baseBranch),
	})
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, fmt.Errorf("回复 inbox 时启动 worker 资源失败：未返回 worker")
	}
	if preStartReady && (w.Status == contracts.WorkerRunning || w.Status == contracts.WorkerStopped) {
		ready, rerr := s.workerDispatchReady(ctx, w)
		if rerr != nil {
			return nil, rerr
		}
		if ready {
			if err := s.ensureTicketTargetRefOnStart(ctx, ticket.ID, baseBranch); err != nil {
				return nil, err
			}
			return w, nil
		}
	}
	if err := s.executePMBootstrapEntrypoint(ctx, ticket, *w); err != nil {
		return nil, err
	}
	out, err := s.worker.WorkerByID(ctx, w.ID)
	if err != nil {
		return nil, fmt.Errorf("读取 worker 失败（w%d）：%w", w.ID, err)
	}
	if out.Status != contracts.WorkerStopped && out.Status != contracts.WorkerRunning {
		return nil, fmt.Errorf("回复 inbox 后 worker 未进入可调度状态（w%d status=%s）", out.ID, out.Status)
	}
	if err := s.ensureTicketTargetRefOnStart(ctx, ticket.ID, baseBranch); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) focusReplyTarget(ctx context.Context, ticketID uint) (*contracts.FocusRun, *contracts.FocusRunItem, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, nil, err
	}
	view, err := s.focusViewForDB(ctx, db, 0)
	switch {
	case err == nil:
	case err == gorm.ErrRecordNotFound:
		return nil, nil, nil
	default:
		return nil, nil, err
	}
	if view.Run.ID == 0 || view.Run.IsTerminal() {
		return nil, nil, nil
	}
	inScope := false
	for _, item := range view.Items {
		if item.TicketID == ticketID && !focusItemTerminalStatus(item.Status) {
			inScope = true
			break
		}
	}
	if !inScope {
		return nil, nil, nil
	}
	if view.ActiveItem == nil || view.ActiveItem.TicketID != ticketID {
		activeTicketID := uint(0)
		if view.ActiveItem != nil {
			activeTicketID = view.ActiveItem.TicketID
		}
		return nil, nil, fmt.Errorf("focus batch 当前活动项是 t%d，不能直接恢复 t%d", activeTicketID, ticketID)
	}
	if strings.TrimSpace(view.ActiveItem.Status) != contracts.FocusItemBlocked {
		return nil, nil, fmt.Errorf("focus batch 中 t%d 当前不是 blocked 状态", ticketID)
	}
	run := view.Run
	item := *view.ActiveItem
	return &run, &item, nil
}

func (s *Service) loadInboxReplyTicket(ctx context.Context, ticketID uint) (*contracts.Ticket, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var ticket contracts.Ticket
	if err := db.WithContext(ctx).First(&ticket, ticketID).Error; err != nil {
		return nil, err
	}
	return &ticket, nil
}

func (s *Service) ensureNeedsUserInboxAnchor(ctx context.Context, item *contracts.InboxItem) (*contracts.InboxItem, error) {
	if item == nil || item.ID == 0 {
		return item, nil
	}
	if item.OriginTaskRunID != 0 {
		return item, nil
	}
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var latest contracts.TicketLifecycleEvent
	query := db.WithContext(ctx).
		Where("ticket_id = ? AND event_type = ?", item.TicketID, contracts.TicketLifecycleWaitUserReported)
	if item.WorkerID != 0 {
		query = query.Where("(worker_id = ? OR worker_id IS NULL)", item.WorkerID)
	}
	if err := query.Order("sequence desc").First(&latest).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return item, nil
		}
		return nil, err
	}
	if latest.TaskRunID == nil || *latest.TaskRunID == 0 {
		return item, nil
	}
	originTaskRunID := *latest.TaskRunID
	now := time.Now()
	key := inboxKeyNeedsUserChain(item.TicketID, originTaskRunID)
	if strings.TrimSpace(key) == "" {
		key = item.Key
	}
	if err := db.WithContext(ctx).Model(&contracts.InboxItem{}).Where("id = ?", item.ID).Updates(map[string]any{
		"key":                 key,
		"origin_task_run_id":  originTaskRunID,
		"current_task_run_id": originTaskRunID,
		"wait_round_count":    1,
		"updated_at":          now,
	}).Error; err != nil {
		return nil, err
	}
	refreshed, err := s.GetInboxItem(ctx, item.ID)
	if err != nil {
		return nil, err
	}
	return refreshed, nil
}

func (s *Service) storeInboxReplyIntent(ctx context.Context, inboxID uint, action contracts.InboxReplyAction, reply string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	return db.WithContext(ctx).
		Model(&contracts.InboxItem{}).
		Where("id = ? AND status = ? AND reason = ?", inboxID, contracts.InboxOpen, contracts.InboxNeedsUser).
		Updates(map[string]any{
			"reply_action":      action,
			"reply_markdown":    reply,
			"reply_received_at": &now,
			"reply_consumed_at": nil,
			"updated_at":        now,
		}).Error
}

func (s *Service) markInboxReplyConsumed(ctx context.Context, inboxID uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	return db.WithContext(ctx).
		Model(&contracts.InboxItem{}).
		Where("id = ? AND status = ? AND reason = ?", inboxID, contracts.InboxOpen, contracts.InboxNeedsUser).
		Updates(map[string]any{
			"status":            contracts.InboxDone,
			"closed_at":         &now,
			"reply_consumed_at": &now,
			"updated_at":        now,
		}).Error
}

func (s *Service) reopenInboxReplyIntent(ctx context.Context, inboxID uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if inboxID == 0 {
		return nil
	}
	now := time.Now()
	return db.WithContext(ctx).
		Model(&contracts.InboxItem{}).
		Where("id = ? AND reason = ? AND chain_resolved_at IS NULL", inboxID, contracts.InboxNeedsUser).
		Updates(map[string]any{
			"status":            contracts.InboxOpen,
			"closed_at":         nil,
			"reply_consumed_at": nil,
			"updated_at":        now,
		}).Error
}

func (s *Service) loadPendingNeedsUserInbox(ctx context.Context, ticketID, workerID uint) (*contracts.InboxItem, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	return loadPendingNeedsUserInboxWithDB(ctx, db, ticketID, workerID)
}

func loadPendingNeedsUserInboxWithDB(ctx context.Context, db *gorm.DB, ticketID, workerID uint) (*contracts.InboxItem, error) {
	if db == nil {
		return nil, fmt.Errorf("db 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var item contracts.InboxItem
	query := db.WithContext(ctx).
		Where("status = ? AND reason = ? AND ticket_id = ?", contracts.InboxOpen, contracts.InboxNeedsUser, ticketID).
		Where("COALESCE(reply_action, '') <> ''").
		Where("reply_consumed_at IS NULL")
	if workerID != 0 {
		query = query.Where("(worker_id = ? OR worker_id = 0)", workerID)
	}
	if err := query.Order("id desc").First(&item).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func (s *Service) pendingInboxReplyForTicket(ctx context.Context, ticketID uint) (*contracts.InboxItem, error) {
	return s.loadPendingNeedsUserInbox(ctx, ticketID, 0)
}

func (s *Service) upsertNeedsUserInboxFromReportTx(ctx context.Context, tx *gorm.DB, ticket contracts.Ticket, report contracts.WorkerReport, now time.Time) (bool, error) {
	item := contracts.InboxItem{
		Key:      inboxKeyNeedsUserChain(ticket.ID, report.TaskRunID),
		Status:   contracts.InboxOpen,
		Severity: contracts.InboxBlocker,
		Reason:   contracts.InboxNeedsUser,
		Title:    "需要补充信息后继续执行",
		Body:     buildNeedsUserInboxBodyFromReport(report),
		TicketID: ticket.ID,
		WorkerID: report.WorkerID,
	}
	return s.upsertNeedsUserInboxItemTx(ctx, tx, item, report.TaskRunID, now)
}

func (s *Service) upsertNeedsUserInboxItemTx(ctx context.Context, tx *gorm.DB, item contracts.InboxItem, taskRunID uint, now time.Time) (bool, error) {
	if tx == nil {
		return false, fmt.Errorf("tx 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	existing, err := loadActiveNeedsUserChainInboxByTicketWithDB(ctx, tx, item.TicketID)
	if err != nil {
		return false, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	if existing != nil {
		originTaskRunID := existing.OriginTaskRunID
		if originTaskRunID == 0 {
			originTaskRunID = taskRunID
		}
		waitRoundCount := existing.WaitRoundCount
		if waitRoundCount <= 0 {
			waitRoundCount = 1
		}
		if existing.CurrentTaskRunID != 0 && existing.CurrentTaskRunID != taskRunID {
			waitRoundCount++
		}
		key := inboxKeyNeedsUserChain(item.TicketID, originTaskRunID)
		if strings.TrimSpace(key) == "" {
			key = strings.TrimSpace(item.Key)
		}
		title := item.Title
		body := item.Body
		if waitRoundCount >= maxWaitUserRounds {
			title = waitUserRoundLimitTitle(title)
			body = waitUserRoundLimitBody(body)
		}
		if err := tx.WithContext(ctx).
			Model(&contracts.InboxItem{}).
			Where("id = ?", existing.ID).
			Updates(map[string]any{
				"key":                 key,
				"severity":            item.Severity,
				"reason":              item.Reason,
				"title":               title,
				"body":                body,
				"ticket_id":           item.TicketID,
				"worker_id":           item.WorkerID,
				"merge_item_id":       item.MergeItemID,
				"origin_task_run_id":  originTaskRunID,
				"current_task_run_id": taskRunID,
				"wait_round_count":    waitRoundCount,
				"chain_resolved_at":   nil,
				"reply_action":        contracts.InboxReplyNone,
				"reply_markdown":      "",
				"reply_received_at":   nil,
				"reply_consumed_at":   nil,
				"updated_at":          now,
				"closed_at":           nil,
			}).Error; err != nil {
			return false, err
		}
		if err := closeDuplicateNeedsUserInboxesTx(ctx, tx, item.TicketID, existing.ID); err != nil {
			return false, err
		}
		return false, nil
	}

	item.Status = contracts.InboxOpen
	if key := inboxKeyNeedsUserChain(item.TicketID, taskRunID); strings.TrimSpace(key) != "" {
		item.Key = key
	}
	item.OriginTaskRunID = taskRunID
	item.CurrentTaskRunID = taskRunID
	item.WaitRoundCount = 1
	item.ChainResolvedAt = nil
	item.ReplyAction = contracts.InboxReplyNone
	item.ReplyMarkdown = ""
	item.ReplyReceivedAt = nil
	item.ReplyConsumedAt = nil
	if err := tx.WithContext(ctx).Create(&item).Error; err != nil {
		return false, err
	}
	if err := closeDuplicateNeedsUserInboxesTx(ctx, tx, item.TicketID, item.ID); err != nil {
		return false, err
	}
	return true, nil
}

func loadActiveNeedsUserChainInboxByTicketWithDB(ctx context.Context, db *gorm.DB, ticketID uint) (*contracts.InboxItem, error) {
	if db == nil {
		return nil, fmt.Errorf("db 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var item contracts.InboxItem
	if err := db.WithContext(ctx).
		Where("reason = ? AND ticket_id = ? AND chain_resolved_at IS NULL", contracts.InboxNeedsUser, ticketID).
		Order("updated_at desc").
		Order("id desc").
		First(&item).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &item, nil
}

func closeDuplicateNeedsUserInboxesTx(ctx context.Context, tx *gorm.DB, ticketID, keepID uint) error {
	if tx == nil || ticketID == 0 || keepID == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	return tx.WithContext(ctx).
		Model(&contracts.InboxItem{}).
		Where("ticket_id = ? AND reason = ? AND status = ? AND id <> ?", ticketID, contracts.InboxNeedsUser, contracts.InboxOpen, keepID).
		Updates(map[string]any{
			"status":            contracts.InboxDone,
			"closed_at":         &now,
			"chain_resolved_at": &now,
			"updated_at":        now,
		}).Error
}

func (s *Service) resolveNeedsUserChainTx(ctx context.Context, tx *gorm.DB, ticketID uint, now time.Time) error {
	if tx == nil || ticketID == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	return tx.WithContext(ctx).
		Model(&contracts.InboxItem{}).
		Where("ticket_id = ? AND reason = ? AND chain_resolved_at IS NULL", ticketID, contracts.InboxNeedsUser).
		Updates(map[string]any{
			"status":            contracts.InboxDone,
			"closed_at":         &now,
			"chain_resolved_at": &now,
			"updated_at":        now,
			"reply_action":      contracts.InboxReplyNone,
			"reply_markdown":    "",
			"reply_received_at": nil,
			"reply_consumed_at": nil,
		}).Error
}
