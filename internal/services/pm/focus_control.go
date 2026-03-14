package pm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	ticketsvc "dalek/internal/services/ticket"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

var focusActiveStatuses = []string{contracts.FocusQueued, contracts.FocusRunning, contracts.FocusBlocked}

func (s *Service) FocusStart(ctx context.Context, in contracts.FocusStartInput) (contracts.FocusStartResult, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.FocusStartResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	mode := strings.TrimSpace(in.Mode)
	if mode == "" {
		mode = contracts.FocusModeBatch
	}
	if mode != contracts.FocusModeBatch {
		return contracts.FocusStartResult{}, fmt.Errorf("暂只支持 batch 模式")
	}
	scope := normalizeFocusTicketIDs(in.ScopeTicketIDs)
	if len(scope) == 0 {
		return contracts.FocusStartResult{}, fmt.Errorf("scope ticket 不能为空")
	}
	budget := in.AgentBudget
	if budget <= 0 {
		budget = defaultAgentBudget
	}
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		requestID = newPMRequestID("focus")
	}

	scopeJSON, err := json.Marshal(scope)
	if err != nil {
		return contracts.FocusStartResult{}, fmt.Errorf("序列化 scope 失败: %w", err)
	}

	projectKey := strings.TrimSpace(s.p.Key)
	now := time.Now()
	focus := contracts.FocusRun{}
	created := false
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var active contracts.FocusRun
		txErr := tx.WithContext(ctx).
			Where("project_key = ? AND status IN ?", projectKey, focusActiveStatuses).
			Order("id desc").
			First(&active).Error
		switch {
		case txErr == nil:
			if requestID != "" && strings.TrimSpace(active.RequestID) == requestID {
				focus = active
				return nil
			}
			return fmt.Errorf("已存在 active focus（id=%d status=%s），请先 stop", active.ID, active.Status)
		case !errors.Is(txErr, gorm.ErrRecordNotFound):
			return fmt.Errorf("检查 active focus 失败: %w", txErr)
		}

		focus = contracts.FocusRun{
			ProjectKey:     projectKey,
			Mode:           mode,
			RequestID:      requestID,
			DesiredState:   contracts.FocusDesiredRunning,
			Status:         contracts.FocusQueued,
			ScopeTicketIDs: string(scopeJSON),
			AgentBudget:    budget,
			AgentBudgetMax: budget,
			StartedAt:      &now,
		}
		if err := tx.WithContext(ctx).Create(&focus).Error; err != nil {
			return err
		}

		items := make([]contracts.FocusRunItem, 0, len(scope))
		for i, ticketID := range scope {
			items = append(items, contracts.FocusRunItem{
				FocusRunID: focus.ID,
				Seq:        i + 1,
				TicketID:   ticketID,
				Status:     contracts.FocusItemPending,
			})
		}
		if len(items) > 0 {
			if err := tx.WithContext(ctx).Create(&items).Error; err != nil {
				return err
			}
		}
		if _, err := appendFocusEventTx(ctx, tx, focus.ID, nil, "run.created", "focus run created", map[string]any{
			"mode":       mode,
			"request_id": requestID,
			"scope":      scope,
			"budget":     budget,
		}, now); err != nil {
			return err
		}
		created = true
		return nil
	})
	if err != nil {
		return contracts.FocusStartResult{}, err
	}

	view, err := s.FocusGet(ctx, focus.ID)
	if err != nil {
		return contracts.FocusStartResult{}, err
	}
	out := contracts.FocusStartResult{
		Created:   created,
		FocusID:   focus.ID,
		RequestID: requestID,
		View:      view,
	}
	s.projectWake()
	return out, nil
}

func (s *Service) FocusGet(ctx context.Context, focusID uint) (contracts.FocusRunView, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.FocusRunView{}, err
	}
	return s.focusViewForDB(ctx, db, focusID)
}

func (s *Service) FocusPoll(ctx context.Context, focusID uint, sinceEventID uint) (contracts.FocusPollResult, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.FocusPollResult{}, err
	}
	view, err := s.focusViewForDB(ctx, db, focusID)
	if err != nil {
		return contracts.FocusPollResult{}, err
	}
	if view.Run.ID == 0 {
		return contracts.FocusPollResult{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var events []contracts.FocusEvent
	if err := db.WithContext(ctx).
		Where("focus_run_id = ? AND id > ?", view.Run.ID, sinceEventID).
		Order("id asc").
		Find(&events).Error; err != nil {
		return contracts.FocusPollResult{}, err
	}
	return contracts.FocusPollResult{
		View:   view,
		Events: events,
	}, nil
}

func (s *Service) FocusStop(ctx context.Context, focusID uint, requestID string) error {
	if err := s.updateFocusDesiredState(ctx, focusID, contracts.FocusDesiredStopping, requestID); err != nil {
		return err
	}
	s.projectWake()
	return nil
}

func (s *Service) FocusCancel(ctx context.Context, focusID uint, requestID string) error {
	if err := s.updateFocusDesiredState(ctx, focusID, contracts.FocusDesiredCanceling, requestID); err != nil {
		return err
	}
	s.projectWake()
	return nil
}

func (s *Service) CreateIntegrationTicket(ctx context.Context, in contracts.CreateIntegrationTicketInput) (contracts.CreateIntegrationTicketResult, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.CreateIntegrationTicketResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	sourceTicketIDs := normalizeFocusTicketIDs(in.SourceTicketIDs)
	if len(sourceTicketIDs) == 0 {
		return contracts.CreateIntegrationTicketResult{}, fmt.Errorf("source tickets 不能为空")
	}
	targetRef, err := normalizeIntegrationTargetRefInput(in.TargetRef)
	if err != nil {
		return contracts.CreateIntegrationTicketResult{}, err
	}
	if err := verifyTicketsExist(ctx, db, sourceTicketIDs); err != nil {
		return contracts.CreateIntegrationTicketResult{}, err
	}

	ticketService := ticketsvc.New(db)
	title := buildIntegrationTicketTitle(sourceTicketIDs, targetRef)
	description := buildIntegrationTicketDescription(contracts.CreateIntegrationTicketInput{
		SourceTicketIDs:       sourceTicketIDs,
		TargetRef:             targetRef,
		ConflictTargetHeadSHA: strings.TrimSpace(in.ConflictTargetHeadSHA),
		SourceAnchorSHAs:      trimNonEmptyStrings(in.SourceAnchorSHAs),
		ConflictFiles:         trimNonEmptyStrings(in.ConflictFiles),
		MergeSummary:          strings.TrimSpace(in.MergeSummary),
		EvidenceRefs:          trimNonEmptyStrings(in.EvidenceRefs),
	})
	ticket, err := ticketService.CreateWithDescriptionAndLabelAndPriorityAndTarget(
		ctx,
		title,
		description,
		"integration",
		contracts.TicketPriorityHigh,
		targetRef,
	)
	if err != nil {
		return contracts.CreateIntegrationTicketResult{}, err
	}
	return contracts.CreateIntegrationTicketResult{TicketID: ticket.ID}, nil
}

func (s *Service) FinalizeTicketSuperseded(ctx context.Context, sourceTicketID, replacementTicketID uint, reason string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if sourceTicketID == 0 || replacementTicketID == 0 {
		return fmt.Errorf("source/replacement ticket_id 不能为空")
	}
	if sourceTicketID == replacementTicketID {
		return fmt.Errorf("source/replacement ticket_id 不能相同")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("superseded by integration ticket t%d", replacementTicketID)
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var replacement contracts.Ticket
		if err := tx.WithContext(ctx).
			Select("id", "integration_status").
			First(&replacement, replacementTicketID).Error; err != nil {
			return err
		}
		if contracts.CanonicalIntegrationStatus(replacement.IntegrationStatus) != contracts.IntegrationMerged {
			return fmt.Errorf("replacement ticket t%d 尚未 merged", replacementTicketID)
		}

		var source contracts.Ticket
		if err := tx.WithContext(ctx).First(&source, sourceTicketID).Error; err != nil {
			return err
		}
		if source.SupersededByTicketID != nil && *source.SupersededByTicketID != 0 && *source.SupersededByTicketID != replacementTicketID {
			return fmt.Errorf("source ticket t%d 已被 t%d supersede，不能改写为 t%d", sourceTicketID, *source.SupersededByTicketID, replacementTicketID)
		}
		now := time.Now()
		if contracts.CanonicalIntegrationStatus(source.IntegrationStatus) != contracts.IntegrationAbandoned {
			lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
				TicketID:       sourceTicketID,
				EventType:      contracts.TicketLifecycleMergeAbandoned,
				Source:         "pm.focus.finalize_superseded",
				ActorType:      contracts.TicketLifecycleActorSystem,
				IdempotencyKey: fmt.Sprintf("ticket:%d:merge_abandoned:superseded:%d", sourceTicketID, replacementTicketID),
				Payload: map[string]any{
					"ticket_id":               sourceTicketID,
					"reason":                  reason,
					"integration_status":      string(contracts.IntegrationAbandoned),
					"superseded_by_ticket_id": replacementTicketID,
					"replacement_ticket_id":   replacementTicketID,
				},
				CreatedAt: now,
			})
			if err != nil {
				return err
			}
			if lifecycleResult.IntegrationChanged() {
				if err := s.applyAbandonedIntegrationSnapshotTx(ctx, tx, sourceTicketID, reason, now); err != nil {
					return err
				}
			}
		}

		return tx.WithContext(ctx).Model(&contracts.Ticket{}).
			Where("id = ?", sourceTicketID).
			Updates(map[string]any{
				"superseded_by_ticket_id": replacementTicketID,
				"abandoned_reason":        reason,
				"updated_at":              now,
			}).Error
	})
}

func (s *Service) focusViewForDB(ctx context.Context, db *gorm.DB, focusID uint) (contracts.FocusRunView, error) {
	if db == nil {
		return contracts.FocusRunView{}, fmt.Errorf("db 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectKey := strings.TrimSpace(s.p.Key)

	var run contracts.FocusRun
	query := db.WithContext(ctx).Model(&contracts.FocusRun{}).Where("project_key = ?", projectKey)
	if focusID == 0 {
		query = query.Where("status IN ?", focusActiveStatuses).Order("id desc")
	} else {
		query = query.Where("id = ?", focusID)
	}
	if err := query.First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return contracts.FocusRunView{}, gorm.ErrRecordNotFound
		}
		return contracts.FocusRunView{}, err
	}

	var items []contracts.FocusRunItem
	if err := db.WithContext(ctx).
		Where("focus_run_id = ?", run.ID).
		Order("seq asc").
		Find(&items).Error; err != nil {
		return contracts.FocusRunView{}, err
	}

	var row struct {
		MaxID uint `gorm:"column:max_id"`
	}
	if err := db.WithContext(ctx).
		Model(&contracts.FocusEvent{}).
		Select("COALESCE(MAX(id), 0) AS max_id").
		Where("focus_run_id = ?", run.ID).
		Scan(&row).Error; err != nil {
		return contracts.FocusRunView{}, err
	}

	return contracts.FocusRunView{
		Run:           run,
		Items:         items,
		ActiveItem:    selectActiveFocusRunItem(items),
		LatestEventID: row.MaxID,
	}, nil
}

func (s *Service) updateFocusDesiredState(ctx context.Context, focusID uint, desiredState, requestID string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		requestID = newPMRequestID("focus")
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		query := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("project_key = ?", strings.TrimSpace(s.p.Key))
		if focusID == 0 {
			query = query.Where("status IN ?", focusActiveStatuses).Order("id desc")
		} else {
			query = query.Where("id = ?", focusID)
		}
		var run contracts.FocusRun
		if err := query.First(&run).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if focusID == 0 {
					return fmt.Errorf("当前无 active focus")
				}
			}
			return err
		}
		if run.IsTerminal() {
			return nil
		}

		now := time.Now()
		updates := map[string]any{
			"desired_state": desiredState,
			"request_id":    requestID,
			"updated_at":    now,
		}
		switch run.Status {
		case contracts.FocusQueued, contracts.FocusBlocked:
			if desiredState == contracts.FocusDesiredCanceling {
				updates["status"] = contracts.FocusCanceled
			} else {
				updates["status"] = contracts.FocusStopped
			}
			updates["finished_at"] = &now
		}
		if err := tx.WithContext(ctx).
			Model(&contracts.FocusRun{}).
			Where("id = ?", run.ID).
			Updates(updates).Error; err != nil {
			return err
		}
		_, err := appendFocusEventTx(ctx, tx, run.ID, nil, "run.desired_state_changed", "focus desired_state changed", map[string]any{
			"from":       run.DesiredState,
			"to":         desiredState,
			"request_id": requestID,
		}, now)
		return err
	})
}

func appendFocusEventTx(ctx context.Context, tx *gorm.DB, focusRunID uint, focusItemID *uint, kind, summary string, payload any, createdAt time.Time) (*contracts.FocusEvent, error) {
	if tx == nil {
		return nil, fmt.Errorf("tx 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if focusRunID == 0 {
		return nil, fmt.Errorf("focus_run_id 不能为空")
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	ev := contracts.FocusEvent{
		FocusRunID:  focusRunID,
		FocusItemID: focusItemID,
		Kind:        strings.TrimSpace(kind),
		Summary:     strings.TrimSpace(summary),
		PayloadJSON: marshalJSON(payload),
		CreatedAt:   createdAt,
	}
	if ev.PayloadJSON == "" {
		ev.PayloadJSON = "{}"
	}
	if err := tx.WithContext(ctx).Create(&ev).Error; err != nil {
		return nil, err
	}
	return &ev, nil
}

func normalizeFocusTicketIDs(ids []uint) []uint {
	if len(ids) == 0 {
		return nil
	}
	out := make([]uint, 0, len(ids))
	seen := make(map[uint]struct{}, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}

func trimNonEmptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

func verifyTicketsExist(ctx context.Context, db *gorm.DB, ticketIDs []uint) error {
	if len(ticketIDs) == 0 {
		return fmt.Errorf("ticket_ids 不能为空")
	}
	type row struct {
		ID uint `gorm:"column:id"`
	}
	var rows []row
	if err := db.WithContext(ctx).
		Model(&contracts.Ticket{}).
		Select("id").
		Where("id IN ?", ticketIDs).
		Find(&rows).Error; err != nil {
		return err
	}
	seen := make(map[uint]struct{}, len(rows))
	for _, row := range rows {
		seen[row.ID] = struct{}{}
	}
	for _, id := range ticketIDs {
		if _, ok := seen[id]; !ok {
			return fmt.Errorf("ticket t%d 不存在", id)
		}
	}
	return nil
}

func buildIntegrationTicketTitle(sourceTicketIDs []uint, targetRef string) string {
	shortTarget := shortIntegrationTargetRef(targetRef)
	if shortTarget == "" {
		shortTarget = strings.TrimSpace(targetRef)
	}
	if len(sourceTicketIDs) == 1 {
		return fmt.Sprintf("集成 t%d 到 %s", sourceTicketIDs[0], shortTarget)
	}
	parts := make([]string, 0, len(sourceTicketIDs))
	for _, id := range sourceTicketIDs {
		parts = append(parts, fmt.Sprintf("t%d", id))
	}
	return fmt.Sprintf("解决 %s 在 %s 上的集成冲突", strings.Join(parts, " / "), shortTarget)
}

func buildIntegrationTicketDescription(in contracts.CreateIntegrationTicketInput) string {
	sourceParts := make([]string, 0, len(in.SourceTicketIDs))
	for _, id := range in.SourceTicketIDs {
		sourceParts = append(sourceParts, fmt.Sprintf("t%d", id))
	}
	lines := []string{
		"## 来源",
		fmt.Sprintf("- source_tickets: %s", strings.Join(sourceParts, ", ")),
		"- trigger: merge_conflict",
		fmt.Sprintf("- target_ref: %s", strings.TrimSpace(in.TargetRef)),
		"",
		"## 现场",
		fmt.Sprintf("- conflict_target_head_sha: %s", firstNonEmpty(strings.TrimSpace(in.ConflictTargetHeadSHA), "(unknown)")),
	}
	if len(in.SourceAnchorSHAs) > 0 {
		lines = append(lines, fmt.Sprintf("- source_anchor_shas: %s", strings.Join(in.SourceAnchorSHAs, ", ")))
	} else {
		lines = append(lines, "- source_anchor_shas: (none)")
	}
	lines = append(lines, "- conflict_files:")
	if len(in.ConflictFiles) == 0 {
		lines = append(lines, "  - (none)")
	} else {
		for _, file := range in.ConflictFiles {
			lines = append(lines, "  - "+file)
		}
	}
	lines = append(lines,
		"",
		"## 目标",
		"- 基于当前 target_ref 的干净基线重新整合 source tickets 的交付意图",
		"- 产出新的可交付 anchor",
		"",
		"## 约束",
		"- 不得丢失 source tickets 的需求语义",
		"- 允许修改产品实现文件",
		"- 不得依赖 repo root 的冲突现场",
		"",
		"## 输入证据",
		fmt.Sprintf("- merge stderr/log: %s", firstNonEmpty(strings.TrimSpace(in.MergeSummary), "(none)")),
	)
	if len(in.EvidenceRefs) == 0 {
		lines = append(lines, "- docs: (none)")
	} else {
		for i, ref := range in.EvidenceRefs {
			prefix := "- docs: "
			if i > 0 {
				prefix = "  "
			}
			lines = append(lines, prefix+ref)
		}
	}
	lines = append(lines,
		"",
		"## 完成标准",
		"- 在干净 target_ref 基线上完成实现",
		"- 编译/测试通过",
		"- 本 ticket done 后进入 needs_merge",
	)
	return strings.Join(lines, "\n")
}

func selectActiveFocusRunItem(items []contracts.FocusRunItem) *contracts.FocusRunItem {
	for i := range items {
		switch items[i].Status {
		case contracts.FocusItemQueued,
			contracts.FocusItemExecuting,
			contracts.FocusItemMerging,
			contracts.FocusItemAwaitingMergeObservation,
			contracts.FocusItemBlocked:
			item := items[i]
			return &item
		}
	}
	return nil
}
