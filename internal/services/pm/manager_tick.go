package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type ManagerTickOptions struct {
	MaxRunningWorkers int
	DryRun            bool
	SyncDispatch      bool
	DispatchTimeout   time.Duration
}

type ManagerTickResult struct {
	At time.Time

	AutopilotEnabled bool
	MaxRunning       int
	Running          int
	RunningBlocked   int
	Capacity         int

	EventsConsumed int
	InboxUpserts   int

	StartedTickets    []uint
	DispatchedTickets []uint
	MergeProposed     []uint

	Errors []string
}

type consumeEventsResult struct {
	EventsConsumed int
	InboxUpserts   int
	NewLastEventID uint
	Errors         []string
}

type scanWorkersResult struct {
	Running          int
	RunningBlocked   int
	Progressable     int
	RunningTicketIDs map[uint]bool
	InboxUpserts     int
	Errors           []string
}

type mergeProposalResult struct {
	MergeProposed []uint
	Errors        []string
}

type scheduleOptions struct {
	Capacity         int
	RunningTicketIDs map[uint]bool
	DryRun           bool
	SyncDispatch     bool
	DispatchTimeout  time.Duration
}

type scheduleResult struct {
	StartedTickets    []uint
	DispatchedTickets []uint
	Errors            []string
}

func (s *Service) managerDispatchTimeout() time.Duration {
	p, _, err := s.require()
	if err != nil {
		return 0
	}
	cfg := p.Config.WithDefaults()
	d := time.Duration(cfg.PMDispatchTimeoutMS) * time.Millisecond
	if d <= 0 {
		return 0
	}
	return d
}

func (s *Service) managerStartTimeout() time.Duration {
	// StartTicket 包含 PM bootstrap（默认 5 分钟），manager 侧至少要覆盖这段时间。
	return managerStartTimeout
}

func workerBlocksAutopilot(v *store.TaskStatusView) bool {
	if v == nil {
		return false
	}
	if v.RuntimeNeedsUser {
		return true
	}
	switch strings.TrimSpace(v.RuntimeHealthState) {
	case string(contracts.TaskHealthWaitingUser), string(contracts.TaskHealthStalled):
		return true
	}
	switch strings.TrimSpace(strings.ToLower(v.SemanticNextAction)) {
	case string(contracts.NextWaitUser):
		return true
	}
	return false
}

func (s *Service) ManagerTick(ctx context.Context, opt ManagerTickOptions) (ManagerTickResult, error) {
	_, db, err := s.require()
	if err != nil {
		return ManagerTickResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()

	st, err := s.getOrInitPMState(ctx)
	if err != nil {
		return ManagerTickResult{}, err
	}

	maxRunning := opt.MaxRunningWorkers
	if maxRunning <= 0 {
		maxRunning = st.MaxRunningWorkers
	}
	maxRunning = clampMaxRunning(maxRunning)

	res := ManagerTickResult{
		At:               now,
		AutopilotEnabled: st.AutopilotEnabled,
		MaxRunning:       maxRunning,
	}

	taskRuntime, err := s.taskRuntimeForDB(db)
	if err != nil {
		return res, err
	}

	eventsResult := s.consumeTaskEvents(ctx, taskRuntime, st.LastEventID)
	lastEventID := res.applyConsumeEventsResult(eventsResult)

	scanResult, err := s.scanRunningWorkers(ctx, db, taskRuntime)
	if err != nil {
		return res, err
	}
	res.applyScanWorkersResult(scanResult)

	capacity := maxRunning - scanResult.Progressable
	if capacity < 0 {
		capacity = 0
	}
	res.Capacity = capacity

	if !st.AutopilotEnabled {
		s.saveManagerTickState(ctx, db, st, now, lastEventID, maxRunning, opt)
		return res, nil
	}

	mergeResult := s.proposeMergesForDoneTickets(ctx, db, opt.DryRun)
	res.applyMergeProposalResult(mergeResult)

	scheduleResult := s.scheduleQueuedTickets(ctx, db, scheduleOptions{
		Capacity:         capacity,
		RunningTicketIDs: scanResult.RunningTicketIDs,
		DryRun:           opt.DryRun,
		SyncDispatch:     opt.SyncDispatch,
		DispatchTimeout:  opt.DispatchTimeout,
	})
	res.applyScheduleResult(scheduleResult)
	s.saveManagerTickState(ctx, db, st, now, lastEventID, maxRunning, opt)

	return res, nil
}

func (res *ManagerTickResult) applyConsumeEventsResult(step consumeEventsResult) uint {
	res.EventsConsumed = step.EventsConsumed
	res.InboxUpserts += step.InboxUpserts
	res.Errors = append(res.Errors, step.Errors...)
	return step.NewLastEventID
}

func (res *ManagerTickResult) applyScanWorkersResult(step scanWorkersResult) {
	res.Running = step.Running
	res.RunningBlocked = step.RunningBlocked
	res.InboxUpserts += step.InboxUpserts
	res.Errors = append(res.Errors, step.Errors...)
}

func (res *ManagerTickResult) applyMergeProposalResult(step mergeProposalResult) {
	res.MergeProposed = append(res.MergeProposed, step.MergeProposed...)
	res.Errors = append(res.Errors, step.Errors...)
}

func (res *ManagerTickResult) applyScheduleResult(step scheduleResult) {
	res.StartedTickets = append(res.StartedTickets, step.StartedTickets...)
	res.DispatchedTickets = append(res.DispatchedTickets, step.DispatchedTickets...)
	res.Errors = append(res.Errors, step.Errors...)
}

func (s *Service) saveManagerTickState(ctx context.Context, db *gorm.DB, st *contracts.PMState, now time.Time, lastEventID uint, maxRunning int, opt ManagerTickOptions) {
	st.LastTickAt = &now
	st.LastEventID = lastEventID
	if opt.MaxRunningWorkers > 0 && opt.MaxRunningWorkers != st.MaxRunningWorkers {
		st.MaxRunningWorkers = maxRunning
	}
	_ = db.WithContext(ctx).Save(st).Error
}

func (s *Service) consumeTaskEvents(ctx context.Context, taskRuntime core.TaskRuntime, lastEventID uint) consumeEventsResult {
	if ctx == nil {
		ctx = context.Background()
	}
	out := consumeEventsResult{
		NewLastEventID: lastEventID,
	}
	if taskRuntime == nil {
		out.Errors = append(out.Errors, "task runtime 为空")
		return out
	}

	newEvents, err := taskRuntime.ListEventsAfterID(ctx, lastEventID, 2000)
	if err != nil {
		out.Errors = append(out.Errors, err.Error())
		return out
	}
	out.EventsConsumed = len(newEvents)
	for _, ev := range newEvents {
		if ev.ID > out.NewLastEventID {
			out.NewLastEventID = ev.ID
		}

		typ := strings.TrimSpace(ev.EventType)
		switch typ {
		case "watch_error", "interrupt_error":
			key := inboxKeyTicketIncident(ev.TicketID, typ)
			if ev.WorkerID != 0 {
				key = inboxKeyWorkerIncident(ev.WorkerID, typ)
			}
			title := fmt.Sprintf("%s：t%d w%d", typ, ev.TicketID, ev.WorkerID)
			body := taskEventBody(ev)
			created, uerr := s.upsertOpenInbox(ctx, contracts.InboxItem{
				Key:      key,
				Status:   contracts.InboxOpen,
				Severity: contracts.InboxWarn,
				Reason:   contracts.InboxIncident,
				Title:    title,
				Body:     body,
				TicketID: ev.TicketID,
				WorkerID: ev.WorkerID,
			})
			if uerr != nil {
				out.Errors = append(out.Errors, uerr.Error())
			} else if created {
				out.InboxUpserts++
			}

		case "runtime_observation", "semantic_reported":
			needsUser, runtimeHealth, nextAction, summary := parseTaskEventSignals(ev)
			if summary == "" {
				summary = taskEventBody(ev)
			}
			if needsUser || runtimeHealth == string(contracts.TaskHealthWaitingUser) || nextAction == string(contracts.NextWaitUser) {
				key := inboxKeyNeedsUser(ev.WorkerID)
				title := fmt.Sprintf("需要你输入：t%d w%d", ev.TicketID, ev.WorkerID)
				created, uerr := s.upsertOpenInbox(ctx, contracts.InboxItem{
					Key:      key,
					Status:   contracts.InboxOpen,
					Severity: contracts.InboxBlocker,
					Reason:   contracts.InboxNeedsUser,
					Title:    title,
					Body:     summary,
					TicketID: ev.TicketID,
					WorkerID: ev.WorkerID,
				})
				if uerr != nil {
					out.Errors = append(out.Errors, uerr.Error())
				} else if created {
					out.InboxUpserts++
				}
			}
		}
	}
	return out
}

func (s *Service) scanRunningWorkers(ctx context.Context, db *gorm.DB, taskRuntime core.TaskRuntime) (scanWorkersResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	out := scanWorkersResult{
		RunningTicketIDs: map[uint]bool{},
	}
	if taskRuntime == nil {
		return out, fmt.Errorf("task runtime 为空")
	}

	var running []contracts.Worker
	if err := db.WithContext(ctx).
		Where("status = ?", contracts.WorkerRunning).
		Order("id asc").
		Find(&running).Error; err != nil {
		return out, err
	}

	taskViews, err := taskRuntime.ListStatus(ctx, core.TaskRuntimeListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		IncludeTerminal: true,
		Limit:           5000,
	})
	if err != nil {
		return out, err
	}
	runtimeByWorker := map[uint]store.TaskStatusView{}
	for _, tv := range taskViews {
		if tv.WorkerID == 0 {
			continue
		}
		if _, ok := runtimeByWorker[tv.WorkerID]; ok {
			continue
		}
		runtimeByWorker[tv.WorkerID] = tv
	}

	for _, w := range running {
		out.RunningTicketIDs[w.TicketID] = true
		out.Running++

		tv, hasTV := runtimeByWorker[w.ID]
		if !hasTV {
			out.Progressable++
			continue
		}
		if workerBlocksAutopilot(&tv) {
			out.RunningBlocked++
			key := inboxKeyNeedsUser(w.ID)
			reason := contracts.InboxNeedsUser
			severity := contracts.InboxBlocker
			title := fmt.Sprintf("需要你输入：t%d w%d", w.TicketID, w.ID)
			body := strings.TrimSpace(tv.RuntimeSummary)
			if body == "" {
				body = strings.TrimSpace(tv.SemanticSummary)
			}
			if body == "" {
				body = strings.TrimSpace(tv.LastEventNote)
			}
			if strings.TrimSpace(tv.RuntimeHealthState) == string(contracts.TaskHealthStalled) {
				key = inboxKeyWorkerIncident(w.ID, "runtime_stalled")
				reason = contracts.InboxIncident
				severity = contracts.InboxWarn
				title = fmt.Sprintf("运行受阻：t%d w%d", w.TicketID, w.ID)
				if body == "" {
					body = strings.TrimSpace(tv.ErrorMessage)
				}
			}
			created, uerr := s.upsertOpenInbox(ctx, contracts.InboxItem{
				Key:      key,
				Status:   contracts.InboxOpen,
				Severity: severity,
				Reason:   reason,
				Title:    title,
				Body:     body,
				TicketID: w.TicketID,
				WorkerID: w.ID,
			})
			if uerr != nil {
				out.Errors = append(out.Errors, uerr.Error())
			} else if created {
				out.InboxUpserts++
			}
			continue
		}

		out.Progressable++
	}
	return out, nil
}

func (s *Service) proposeMergesForDoneTickets(ctx context.Context, db *gorm.DB, dryRun bool) mergeProposalResult {
	if ctx == nil {
		ctx = context.Background()
	}
	out := mergeProposalResult{}

	var doneTickets []contracts.Ticket
	if err := db.WithContext(ctx).
		Where("workflow_status = ?", contracts.TicketDone).
		Order("id asc").
		Find(&doneTickets).Error; err != nil {
		return out
	}

	for _, t := range doneTickets {
		w, werr := s.worker.LatestWorker(ctx, t.ID)
		if werr != nil || w == nil {
			continue
		}
		branch := strings.TrimSpace(w.Branch)
		if branch == "" {
			continue
		}

		var cnt int64
		if err := db.WithContext(ctx).
			Model(&contracts.MergeItem{}).
			Where("ticket_id = ? AND status NOT IN ?", t.ID, mergeTerminalStatuses()).
			Count(&cnt).Error; err != nil {
			continue
		}
		if cnt > 0 {
			continue
		}

		if dryRun {
			out.MergeProposed = append(out.MergeProposed, t.ID)
			continue
		}

		mi := contracts.MergeItem{
			Status:   contracts.MergeProposed,
			TicketID: t.ID,
			WorkerID: w.ID,
			Branch:   branch,
		}
		if err := db.WithContext(ctx).Create(&mi).Error; err != nil {
			out.Errors = append(out.Errors, err.Error())
			continue
		}
		out.MergeProposed = append(out.MergeProposed, t.ID)

		_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
			Key:         inboxKeyMergeApproval(mi.ID),
			Status:      contracts.InboxOpen,
			Severity:    contracts.InboxWarn,
			Reason:      contracts.InboxApprovalRequired,
			Title:       fmt.Sprintf("待合并审批：t%d", t.ID),
			Body:        fmt.Sprintf("merge_item=%d  branch=%s\n\n请确认是否允许合并，以及合并策略（squash/merge）。", mi.ID, strings.TrimSpace(mi.Branch)),
			TicketID:    t.ID,
			MergeItemID: mi.ID,
		})
	}

	return out
}

func (s *Service) scheduleQueuedTickets(ctx context.Context, db *gorm.DB, opt scheduleOptions) scheduleResult {
	if ctx == nil {
		ctx = context.Background()
	}
	out := scheduleResult{}
	if opt.Capacity <= 0 || opt.DryRun {
		return out
	}

	runningTicketIDs := opt.RunningTicketIDs
	if runningTicketIDs == nil {
		runningTicketIDs = map[uint]bool{}
	}

	var queued []contracts.Ticket
	if err := db.WithContext(ctx).
		Where("workflow_status = ?", contracts.TicketQueued).
		Order("priority desc").
		Order("updated_at asc").
		Order("id asc").
		Find(&queued).Error; err != nil {
		return out
	}

	capacity := opt.Capacity
	for _, t := range queued {
		if capacity <= 0 {
			break
		}
		if runningTicketIDs[t.ID] {
			continue
		}

		startCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.managerStartTimeout())
		w, serr := s.StartTicket(startCtx, t.ID)
		cancel()
		if serr != nil {
			_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
				Key:      inboxKeyTicketIncident(t.ID, "start_failed"),
				Status:   contracts.InboxOpen,
				Severity: contracts.InboxWarn,
				Reason:   contracts.InboxIncident,
				Title:    fmt.Sprintf("启动失败：t%d", t.ID),
				Body:     serr.Error(),
				TicketID: t.ID,
			})
			continue
		}
		if w == nil || w.Status != contracts.WorkerRunning {
			workerID := uint(0)
			workerStatus := contracts.WorkerStatus("")
			if w != nil {
				workerID = w.ID
				workerStatus = w.Status
			}
			msg := fmt.Sprintf("start 返回后 worker 未处于 running（t%d w%d status=%s）", t.ID, workerID, workerStatus)
			_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
				Key:      inboxKeyTicketIncident(t.ID, "start_not_running"),
				Status:   contracts.InboxOpen,
				Severity: contracts.InboxBlocker,
				Reason:   contracts.InboxIncident,
				Title:    fmt.Sprintf("启动后 worker 未就绪：t%d", t.ID),
				Body:     msg,
				TicketID: t.ID,
				WorkerID: workerID,
			})
			if derr := s.demoteTicketBlockedOnWorkerNotReady(ctx, t.ID, workerID, msg, time.Now()); derr != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("worker 未就绪降级失败：t%d w%d: %v", t.ID, workerID, derr))
			}
			out.Errors = append(out.Errors, msg)
			continue
		}

		out.StartedTickets = append(out.StartedTickets, t.ID)
		runningTicketIDs[t.ID] = true

		dispatched, errs := s.dispatchScheduledTicket(ctx, t.ID, w.ID, opt)
		out.Errors = append(out.Errors, errs...)
		if dispatched {
			out.DispatchedTickets = append(out.DispatchedTickets, t.ID)
		}
		capacity--
	}
	return out
}

func (s *Service) dispatchScheduledTicket(ctx context.Context, ticketID, workerID uint, opt scheduleOptions) (bool, []string) {
	if opt.SyncDispatch {
		dispatchCtx := ctx
		cancelDispatch := func() {}
		if opt.DispatchTimeout > 0 {
			dispatchCtx, cancelDispatch = context.WithTimeout(ctx, opt.DispatchTimeout)
		}
		_, derr := s.DispatchTicket(dispatchCtx, ticketID)
		cancelDispatch()
		if derr != nil {
			return false, s.handleDispatchFailure(ctx, ticketID, workerID, derr, "sync dispatch 失败")
		}
		return true, nil
	}

	if submitter := s.getDispatchSubmitter(); submitter != nil {
		derr := submitter.SubmitTicketDispatch(context.WithoutCancel(ctx), ticketID)
		if derr != nil {
			return false, s.handleDispatchFailure(ctx, ticketID, workerID, derr, "submit dispatch 失败")
		}
		return true, nil
	}

	errMsg := fmt.Sprintf("dispatch submitter 未配置：t%d w%d", ticketID, workerID)
	_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
		Key:      inboxKeyTicketIncident(ticketID, "dispatch_no_submitter"),
		Status:   contracts.InboxOpen,
		Severity: contracts.InboxBlocker,
		Reason:   contracts.InboxIncident,
		Title:    fmt.Sprintf("派发失败：t%d w%d", ticketID, workerID),
		Body:     errMsg,
		TicketID: ticketID,
		WorkerID: workerID,
	})
	return false, []string{errMsg}
}

func (s *Service) handleDispatchFailure(ctx context.Context, ticketID, workerID uint, dispatchErr error, prefix string) []string {
	if dispatchErr == nil {
		return nil
	}
	errs := []string{}

	_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
		Key:      inboxKeyWorkerIncident(workerID, "dispatch_failed"),
		Status:   contracts.InboxOpen,
		Severity: contracts.InboxWarn,
		Reason:   contracts.InboxIncident,
		Title:    fmt.Sprintf("派发失败：t%d w%d", ticketID, workerID),
		Body:     dispatchErr.Error(),
		TicketID: ticketID,
		WorkerID: workerID,
	})

	if isWorkerReadyTimeout(dispatchErr) {
		if berr := s.demoteTicketBlockedOnWorkerNotReady(ctx, ticketID, workerID, dispatchErr.Error(), time.Now()); berr != nil {
			errs = append(errs, fmt.Sprintf("worker 未就绪降级失败：t%d w%d: %v", ticketID, workerID, berr))
		}
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "dispatch 失败"
	}
	errs = append(errs, fmt.Sprintf("%s：t%d w%d: %v", prefix, ticketID, workerID, dispatchErr))
	return errs
}

func taskEventBody(ev core.TaskRuntimeEventScopeRow) string {
	body := strings.TrimSpace(ev.Note)
	if body != "" {
		return body
	}
	_, _, _, summary := parseTaskEventSignals(ev)
	if summary != "" {
		return summary
	}
	return strings.TrimSpace(ev.PayloadJSON)
}

func parseTaskEventSignals(ev core.TaskRuntimeEventScopeRow) (needsUser bool, runtimeHealth string, nextAction string, summary string) {
	toState := parseJSONMap(ev.ToStateJSON)
	payload := parseJSONMap(ev.PayloadJSON)

	if v, ok := mapBool(toState, "runtime_needs_user"); ok {
		needsUser = v
	}
	if !needsUser {
		if v, ok := mapBool(toState, "needs_user"); ok {
			needsUser = v
		}
	}
	if !needsUser {
		if v, ok := mapBool(payload, "needs_user"); ok {
			needsUser = v
		}
	}

	runtimeHealth = strings.TrimSpace(mapString(toState, "runtime_health_state"))
	if runtimeHealth == "" {
		runtimeHealth = strings.TrimSpace(mapString(payload, "runtime_health_state"))
	}

	nextAction = strings.TrimSpace(strings.ToLower(mapString(toState, "next_action")))
	if nextAction == "" {
		nextAction = strings.TrimSpace(strings.ToLower(mapString(payload, "next_action")))
	}

	summary = strings.TrimSpace(mapString(toState, "runtime_summary"))
	if summary == "" {
		summary = strings.TrimSpace(mapString(toState, "summary"))
	}
	if summary == "" {
		summary = strings.TrimSpace(mapString(payload, "summary"))
	}
	return needsUser, runtimeHealth, nextAction, summary
}

func parseJSONMap(raw string) map[string]any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]any{}
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return map[string]any{}
	}
	return out
}

func mapString(m map[string]any, key string) string {
	if len(m) == 0 {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case fmt.Stringer:
		return strings.TrimSpace(t.String())
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

func mapBool(m map[string]any, key string) (bool, bool) {
	if len(m) == 0 {
		return false, false
	}
	v, ok := m[key]
	if !ok || v == nil {
		return false, false
	}
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		if s == "true" || s == "1" || s == "yes" {
			return true, true
		}
		if s == "false" || s == "0" || s == "no" {
			return false, true
		}
		return false, false
	case float64:
		return t != 0, true
	default:
		return false, false
	}
}
