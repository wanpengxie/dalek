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
	return 5*time.Minute + 30*time.Second
}

func workerBlocksAutopilot(v *store.TaskStatusView) bool {
	if v == nil {
		return false
	}
	if v.RuntimeNeedsUser {
		return true
	}
	switch strings.TrimSpace(v.RuntimeHealthState) {
	case string(store.TaskHealthWaitingUser), string(store.TaskHealthStalled):
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
		At:                now,
		AutopilotEnabled:  st.AutopilotEnabled,
		MaxRunning:        maxRunning,
		Running:           0,
		RunningBlocked:    0,
		Capacity:          0,
		EventsConsumed:    0,
		InboxUpserts:      0,
		StartedTickets:    nil,
		DispatchedTickets: nil,
		MergeProposed:     nil,
		Errors:            nil,
	}

	// 2) 增量消费 task_events（event-driven）
	taskRuntime, err := s.taskRuntimeForDB(db)
	if err != nil {
		return res, err
	}
	lastEventID := st.LastEventID
	if newEvents, err := taskRuntime.ListEventsAfterID(ctx, st.LastEventID, 2000); err == nil {
		res.EventsConsumed = len(newEvents)
		for _, ev := range newEvents {
			if ev.ID > lastEventID {
				lastEventID = ev.ID
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
				created, uerr := s.upsertOpenInbox(ctx, store.InboxItem{
					Key:      key,
					Status:   store.InboxOpen,
					Severity: store.InboxWarn,
					Reason:   store.InboxIncident,
					Title:    title,
					Body:     body,
					TicketID: ev.TicketID,
					WorkerID: ev.WorkerID,
				})
				if uerr != nil {
					res.Errors = append(res.Errors, uerr.Error())
				} else if created {
					res.InboxUpserts++
				}

			case "runtime_observation", "semantic_reported":
				needsUser, runtimeHealth, nextAction, summary := parseTaskEventSignals(ev)
				if summary == "" {
					summary = taskEventBody(ev)
				}

				// needs_user 的强信号：直接建 inbox（并避免等到下一轮扫描）
				if needsUser || runtimeHealth == string(store.TaskHealthWaitingUser) || nextAction == string(contracts.NextWaitUser) {
					key := inboxKeyNeedsUser(ev.WorkerID)
					title := fmt.Sprintf("需要你输入：t%d w%d", ev.TicketID, ev.WorkerID)
					created, uerr := s.upsertOpenInbox(ctx, store.InboxItem{
						Key:      key,
						Status:   store.InboxOpen,
						Severity: store.InboxBlocker,
						Reason:   store.InboxNeedsUser,
						Title:    title,
						Body:     summary,
						TicketID: ev.TicketID,
						WorkerID: ev.WorkerID,
					})
					if uerr != nil {
						res.Errors = append(res.Errors, uerr.Error())
					} else if created {
						res.InboxUpserts++
					}
					continue
				}

				// continue 仅用于观测，不再驱动 ManagerTick 自动 redispatch。
			}
		}
	} else {
		res.Errors = append(res.Errors, err.Error())
	}

	// 3) 扫描 running workers：补齐 needs_user / stalled 的 inbox，并计算容量
	var running []store.Worker
	if err := db.WithContext(ctx).
		Where("status = ?", store.WorkerRunning).
		Order("id asc").
		Find(&running).Error; err != nil {
		return res, err
	}
	runningTicketIDs := map[uint]bool{}
	taskViews, err := taskRuntime.ListStatus(ctx, core.TaskRuntimeListStatusOptions{
		OwnerType:       store.TaskOwnerWorker,
		IncludeTerminal: true,
		Limit:           5000,
	})
	if err != nil {
		return res, err
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

	progressable := 0
	for _, w := range running {
		runningTicketIDs[w.TicketID] = true
		res.Running++

		tv, hasTV := runtimeByWorker[w.ID]
		if !hasTV {
			progressable++
			continue
		}
		if workerBlocksAutopilot(&tv) {
			res.RunningBlocked++
			key := inboxKeyNeedsUser(w.ID)
			reason := store.InboxNeedsUser
			severity := store.InboxBlocker
			title := fmt.Sprintf("需要你输入：t%d w%d", w.TicketID, w.ID)
			body := strings.TrimSpace(tv.RuntimeSummary)
			if body == "" {
				body = strings.TrimSpace(tv.SemanticSummary)
			}
			if body == "" {
				body = strings.TrimSpace(tv.LastEventNote)
			}
			if strings.TrimSpace(tv.RuntimeHealthState) == string(store.TaskHealthStalled) {
				key = inboxKeyWorkerIncident(w.ID, "runtime_stalled")
				reason = store.InboxIncident
				severity = store.InboxWarn
				title = fmt.Sprintf("运行受阻：t%d w%d", w.TicketID, w.ID)
				if body == "" {
					body = strings.TrimSpace(tv.ErrorMessage)
				}
			}
			created, uerr := s.upsertOpenInbox(ctx, store.InboxItem{
				Key:      key,
				Status:   store.InboxOpen,
				Severity: severity,
				Reason:   reason,
				Title:    title,
				Body:     body,
				TicketID: w.TicketID,
				WorkerID: w.ID,
			})
			if uerr != nil {
				res.Errors = append(res.Errors, uerr.Error())
			} else if created {
				res.InboxUpserts++
			}
			continue
		}

		progressable++
	}

	capacity := maxRunning - progressable
	if capacity < 0 {
		capacity = 0
	}
	res.Capacity = capacity

	// 若 autopilot 被暂停，仅做观测 + inbox，不做调度/派发。
	if !st.AutopilotEnabled {
		st.LastTickAt = &now
		st.LastEventID = lastEventID
		_ = db.WithContext(ctx).Save(st).Error
		return res, nil
	}

	// 4) merge queue：当 ticket 标成 done 时，自动创建 merge 提案（不自动合并）
	var doneTickets []store.Ticket
	_ = db.WithContext(ctx).
		Where("workflow_status = ?", store.TicketDone).
		Order("id asc").
		Find(&doneTickets).Error
	for _, t := range doneTickets {
		w, werr := s.worker.LatestWorker(ctx, t.ID)
		if werr != nil || w == nil {
			continue
		}
		branch := strings.TrimSpace(w.Branch)
		if branch == "" {
			// 没有分支就无法进入 merge 队列（通常意味着还没 start 过）。
			continue
		}

		var cnt int64
		if err := db.WithContext(ctx).
			Model(&store.MergeItem{}).
			Where("ticket_id = ? AND status NOT IN ?", t.ID, mergeTerminalStatuses()).
			Count(&cnt).Error; err != nil {
			continue
		}
		if cnt > 0 {
			continue
		}

		if opt.DryRun {
			res.MergeProposed = append(res.MergeProposed, t.ID)
			continue
		}

		mi := store.MergeItem{
			Status:   store.MergeProposed,
			TicketID: t.ID,
			WorkerID: w.ID,
			Branch:   branch,
		}
		if err := db.WithContext(ctx).Create(&mi).Error; err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		res.MergeProposed = append(res.MergeProposed, t.ID)

		_, _ = s.upsertOpenInbox(ctx, store.InboxItem{
			Key:         inboxKeyMergeApproval(mi.ID),
			Status:      store.InboxOpen,
			Severity:    store.InboxWarn,
			Reason:      store.InboxApprovalRequired,
			Title:       fmt.Sprintf("待合并审批：t%d", t.ID),
			Body:        fmt.Sprintf("merge_item=%d  branch=%s\n\n请确认是否允许合并，以及合并策略（squash/merge）。", mi.ID, strings.TrimSpace(mi.Branch)),
			TicketID:    t.ID,
			MergeItemID: mi.ID,
		})
	}

	// 5) 调度：从 queued 拉起直到占满并发（start + dispatch）
	if capacity > 0 && !opt.DryRun {
		var queued []store.Ticket
		if err := db.WithContext(ctx).
			Where("workflow_status = ?", store.TicketQueued).
			Order("priority desc").
			Order("updated_at asc").
			Order("id asc").
			Find(&queued).Error; err == nil {
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
					key := inboxKeyTicketIncident(t.ID, "start_failed")
					title := fmt.Sprintf("启动失败：t%d", t.ID)
					body := serr.Error()
					_, _ = s.upsertOpenInbox(ctx, store.InboxItem{
						Key:      key,
						Status:   store.InboxOpen,
						Severity: store.InboxWarn,
						Reason:   store.InboxIncident,
						Title:    title,
						Body:     body,
						TicketID: t.ID,
					})
					continue
				}
				if w == nil || w.Status != store.WorkerRunning {
					workerID := uint(0)
					workerStatus := store.WorkerStatus("")
					if w != nil {
						workerID = w.ID
						workerStatus = w.Status
					}
					msg := fmt.Sprintf("start 返回后 worker 未处于 running（t%d w%d status=%s）", t.ID, workerID, workerStatus)
					_, _ = s.upsertOpenInbox(ctx, store.InboxItem{
						Key:      inboxKeyTicketIncident(t.ID, "start_not_running"),
						Status:   store.InboxOpen,
						Severity: store.InboxBlocker,
						Reason:   store.InboxIncident,
						Title:    fmt.Sprintf("启动后 worker 未就绪：t%d", t.ID),
						Body:     msg,
						TicketID: t.ID,
						WorkerID: workerID,
					})
					if derr := s.demoteTicketBlockedOnWorkerNotReady(ctx, t.ID, workerID, msg, time.Now()); derr != nil {
						res.Errors = append(res.Errors, fmt.Sprintf("worker 未就绪降级失败：t%d w%d: %v", t.ID, workerID, derr))
					}
					res.Errors = append(res.Errors, msg)
					continue
				}

				res.StartedTickets = append(res.StartedTickets, t.ID)
				runningTicketIDs[t.ID] = true

				dispatched := false
				if opt.SyncDispatch {
					dispatchCtx := ctx
					cancelDispatch := func() {}
					if opt.DispatchTimeout > 0 {
						dispatchCtx, cancelDispatch = context.WithTimeout(ctx, opt.DispatchTimeout)
					}
					_, derr := s.DispatchTicket(dispatchCtx, t.ID)
					cancelDispatch()
					if derr != nil {
						key := inboxKeyWorkerIncident(w.ID, "dispatch_failed")
						title := fmt.Sprintf("派发失败：t%d w%d", t.ID, w.ID)
						_, _ = s.upsertOpenInbox(ctx, store.InboxItem{
							Key:      key,
							Status:   store.InboxOpen,
							Severity: store.InboxWarn,
							Reason:   store.InboxIncident,
							Title:    title,
							Body:     derr.Error(),
							TicketID: t.ID,
							WorkerID: w.ID,
						})
						if isWorkerReadyTimeout(derr) {
							if berr := s.demoteTicketBlockedOnWorkerNotReady(ctx, t.ID, w.ID, derr.Error(), time.Now()); berr != nil {
								res.Errors = append(res.Errors, fmt.Sprintf("worker 未就绪降级失败：t%d w%d: %v", t.ID, w.ID, berr))
							}
						}
						res.Errors = append(res.Errors, fmt.Sprintf("sync dispatch 失败：t%d w%d: %v", t.ID, w.ID, derr))
					} else {
						dispatched = true
					}
				} else if submitter := s.getDispatchSubmitter(); submitter != nil {
					submitCtx := context.WithoutCancel(ctx)
					derr := submitter.SubmitTicketDispatch(submitCtx, t.ID)
					if derr != nil {
						key := inboxKeyWorkerIncident(w.ID, "dispatch_failed")
						title := fmt.Sprintf("派发失败：t%d w%d", t.ID, w.ID)
						_, _ = s.upsertOpenInbox(ctx, store.InboxItem{
							Key:      key,
							Status:   store.InboxOpen,
							Severity: store.InboxWarn,
							Reason:   store.InboxIncident,
							Title:    title,
							Body:     derr.Error(),
							TicketID: t.ID,
							WorkerID: w.ID,
						})
						if isWorkerReadyTimeout(derr) {
							if berr := s.demoteTicketBlockedOnWorkerNotReady(ctx, t.ID, w.ID, derr.Error(), time.Now()); berr != nil {
								res.Errors = append(res.Errors, fmt.Sprintf("worker 未就绪降级失败：t%d w%d: %v", t.ID, w.ID, berr))
							}
						}
						res.Errors = append(res.Errors, fmt.Sprintf("submit dispatch 失败：t%d w%d: %v", t.ID, w.ID, derr))
					} else {
						dispatched = true
					}
				} else {
					errMsg := fmt.Sprintf("dispatch submitter 未配置：t%d w%d", t.ID, w.ID)
					_, _ = s.upsertOpenInbox(ctx, store.InboxItem{
						Key:      inboxKeyTicketIncident(t.ID, "dispatch_no_submitter"),
						Status:   store.InboxOpen,
						Severity: store.InboxBlocker,
						Reason:   store.InboxIncident,
						Title:    fmt.Sprintf("派发失败：t%d w%d", t.ID, w.ID),
						Body:     errMsg,
						TicketID: t.ID,
						WorkerID: w.ID,
					})
					res.Errors = append(res.Errors, errMsg)
				}
				if dispatched {
					res.DispatchedTickets = append(res.DispatchedTickets, t.ID)
				}
				capacity--
			}
		}
	}

	// 6) 落盘 manager state
	st.LastTickAt = &now
	st.LastEventID = lastEventID
	if opt.MaxRunningWorkers > 0 && opt.MaxRunningWorkers != st.MaxRunningWorkers {
		st.MaxRunningWorkers = maxRunning
	}
	_ = db.WithContext(ctx).Save(st).Error

	return res, nil
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
