package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/services/core"
	"dalek/internal/services/ticketlifecycle"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type ManagerTickOptions struct {
	MaxRunningWorkers int
	DryRun            bool
	SyncWorkerRun     bool
	WorkerRunTimeout  time.Duration
}

type ManagerTickResult struct {
	At time.Time

	MaxRunning      int
	Running         int
	RunningBlocked  int
	ZombieRecovered int
	ZombieBlocked   int
	ZombieIllegal   int
	ZombieUndefined int
	Capacity        int

	EventsConsumed int
	InboxUpserts   int

	StartedTickets   []uint
	ActivatedTickets []uint
	SerialDeferred   []uint
	MergeFrozen      []uint
	SurfaceConflicts []SurfaceConflict

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

type mergeFrozenResult struct {
	MergeFrozen []uint
	Errors      []string
}

type scheduleOptions struct {
	Capacity         int
	RunningTicketIDs map[uint]bool
	DryRun           bool
	SyncWorkerRun    bool
	WorkerRunTimeout time.Duration
	PMState          *contracts.PMState
	Source           string
}

type scheduleResult struct {
	StartedTickets   []uint
	ActivatedTickets []uint
	SerialDeferred   []uint
	SurfaceConflicts []SurfaceConflict
	Errors           []string
}

const managerTickFinalizeTimeout = 30 * time.Second

func managerTickFinalizeContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		return context.WithTimeout(context.Background(), managerTickFinalizeTimeout)
	}
	return context.WithTimeout(context.WithoutCancel(parent), managerTickFinalizeTimeout)
}

func (s *Service) managerWorkerRunTimeout() time.Duration {
	return 0
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
		At:         now,
		MaxRunning: maxRunning,
	}

	taskRuntime, err := s.taskRuntimeForDB(db)
	if err != nil {
		return res, err
	}

	zombieResult := s.checkZombieWorkers(ctx, db, taskRuntime)
	res.applyZombieCheckResult(zombieResult)

	eventsResult := s.consumeTaskEvents(ctx, taskRuntime, st, st.LastEventID)
	lastEventID := res.applyConsumeEventsResult(eventsResult)

	scanResult, err := s.scanRunningWorkers(ctx, db, taskRuntime, st)
	if err != nil {
		return res, err
	}
	res.applyScanWorkersResult(scanResult)

	capacity := maxRunning - scanResult.Progressable
	if capacity < 0 {
		capacity = 0
	}
	res.Capacity = capacity

	// merge 观测：被动检测 git 事实，不产生 start/activation 副作用。
	mergeResult := s.freezeMergesForDoneTickets(ctx, db, st, opt.DryRun)
	res.applyMergeFrozenResult(mergeResult)

	if !opt.DryRun {
		if err := s.AdvanceFocusController(ctx); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("focus controller advance failed: %v", err))
		}
	}

	finalizeCtx, finalizeCancel := managerTickFinalizeContext(ctx)
	defer finalizeCancel()
	if err := s.saveManagerTickState(finalizeCtx, db, st, now, lastEventID, maxRunning, opt); err != nil {
		res.Errors = append(res.Errors, err.Error())
	}

	return res, nil
}

func (res *ManagerTickResult) applyConsumeEventsResult(step consumeEventsResult) uint {
	res.EventsConsumed = step.EventsConsumed
	res.InboxUpserts += step.InboxUpserts
	res.Errors = append(res.Errors, step.Errors...)
	return step.NewLastEventID
}

func (res *ManagerTickResult) applyZombieCheckResult(step zombieCheckResult) {
	res.ZombieRecovered += step.Recovered
	res.ZombieBlocked += step.Blocked
	res.ZombieIllegal += step.Illegal
	res.ZombieUndefined += step.Undefined
	res.Errors = append(res.Errors, step.Errors...)
}

func (res *ManagerTickResult) applyScanWorkersResult(step scanWorkersResult) {
	res.Running = step.Running
	res.RunningBlocked = step.RunningBlocked
	res.InboxUpserts += step.InboxUpserts
	res.Errors = append(res.Errors, step.Errors...)
}

func (res *ManagerTickResult) applyMergeFrozenResult(step mergeFrozenResult) {
	res.MergeFrozen = append(res.MergeFrozen, step.MergeFrozen...)
	res.Errors = append(res.Errors, step.Errors...)
}

func (res *ManagerTickResult) applyScheduleResult(step scheduleResult) {
	res.StartedTickets = append(res.StartedTickets, step.StartedTickets...)
	res.ActivatedTickets = append(res.ActivatedTickets, step.ActivatedTickets...)
	res.SerialDeferred = append(res.SerialDeferred, step.SerialDeferred...)
	res.SurfaceConflicts = append(res.SurfaceConflicts, step.SurfaceConflicts...)
	res.Errors = append(res.Errors, step.Errors...)
}

func (s *Service) saveManagerTickState(ctx context.Context, db *gorm.DB, st *contracts.PMState, now time.Time, lastEventID uint, maxRunning int, opt ManagerTickOptions) error {
	if st == nil {
		return nil
	}
	if db == nil {
		return fmt.Errorf("db 不能为空")
	}
	st.LastTickAt = &now
	st.LastEventID = lastEventID
	if opt.MaxRunningWorkers > 0 && opt.MaxRunningWorkers != st.MaxRunningWorkers {
		st.MaxRunningWorkers = maxRunning
	}
	updates := map[string]any{
		"last_tick_at":        st.LastTickAt,
		"last_event_id":       st.LastEventID,
		"max_running_workers": st.MaxRunningWorkers,
		"updated_at":          now,
	}
	res := db.WithContext(ctx).Model(&contracts.PMState{}).
		Where("id = ?", st.ID).
		Updates(updates)
	if res.Error != nil {
		s.slog().Warn("pm manager tick save state failed",
			"pm_state_id", st.ID,
			"error", res.Error,
		)
		return res.Error
	}
	if res.RowsAffected == 0 {
		err := fmt.Errorf("pm state 保存失败：id=%d 未命中", st.ID)
		s.slog().Warn("pm manager tick save state missed row",
			"pm_state_id", st.ID,
		)
		return err
	}
	s.slog().Debug("pm manager tick state persisted",
		"pm_state_id", st.ID,
	)
	return nil
}

func (s *Service) consumeTaskEvents(ctx context.Context, taskRuntime core.TaskRuntime, st *contracts.PMState, lastEventID uint) consumeEventsResult {
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
		case "worker_loop_terminated":
			if err := s.consumeWorkerLoopTerminatedEvent(ctx, ev); err != nil {
				out.Errors = append(out.Errors, err.Error())
			}
		}
	}
	return out
}

func (s *Service) scanRunningWorkers(ctx context.Context, db *gorm.DB, taskRuntime core.TaskRuntime, st *contracts.PMState) (scanWorkersResult, error) {
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

	taskViews, err := taskRuntime.ListStatus(ctx, contracts.TaskListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		IncludeTerminal: false,
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
		tv, hasTV := runtimeByWorker[w.ID]
		if !hasTV {
			// 不再把裸 worker.status=running 视为执行真相。
			// 没有可信 active task run 时，这个 worker 不能占用 queued capacity，
			// 也不能阻止同 ticket 再次被 queue consumer 激活。
			continue
		}

		out.RunningTicketIDs[w.TicketID] = true
		out.Running++
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

func (s *Service) freezeMergesForDoneTickets(ctx context.Context, db *gorm.DB, st *contracts.PMState, dryRun bool) mergeFrozenResult {
	if ctx == nil {
		ctx = context.Background()
	}
	out := mergeFrozenResult{}

	var doneTickets []contracts.Ticket
	if err := db.WithContext(ctx).
		Where("workflow_status = ?", contracts.TicketDone).
		Order("id asc").
		Find(&doneTickets).Error; err != nil {
		return out
	}

	for _, t := range doneTickets {
		status := contracts.CanonicalIntegrationStatus(t.IntegrationStatus)
		switch status {
		case contracts.IntegrationNone:
			if dryRun {
				out.MergeFrozen = append(out.MergeFrozen, t.ID)
				continue
			}
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				freeze, err := s.resolveDoneIntegrationFreezeTx(ctx, tx, t.ID, 0, 0, "")
				if err != nil {
					return err
				}
				lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
					TicketID:       t.ID,
					EventType:      contracts.TicketLifecycleRepaired,
					Source:         "pm.manager_tick",
					ActorType:      contracts.TicketLifecycleActorSystem,
					IdempotencyKey: ticketlifecycle.RepairedIdempotencyKey(t.ID, "pm.manager_tick.freeze_done", time.Now()),
					Payload: lifecycleRepairPayload(contracts.TicketDone, contracts.IntegrationNeedsMerge, map[string]any{
						"ticket_id":  t.ID,
						"reason":     "manager tick backfilled missing integration freeze for done ticket",
						"anchor_sha": freeze.AnchorSHA,
						"target_ref": freeze.TargetRef,
					}),
					CreatedAt: time.Now(),
				})
				if err != nil {
					return err
				}
				if !lifecycleResult.IntegrationChanged() {
					return nil
				}
				return s.applyDoneIntegrationFreezeTx(ctx, tx, t.ID, freeze, time.Now())
			}); err != nil {
				out.Errors = append(out.Errors, err.Error())
				continue
			}
			out.MergeFrozen = append(out.MergeFrozen, t.ID)
			continue
		case contracts.IntegrationNeedsMerge:
			anchor := strings.TrimSpace(t.MergeAnchorSHA)
			target := strings.TrimSpace(t.TargetBranch)
			if target == "" {
				target = s.defaultIntegrationTargetBranch(ctx)
			}
			if !fsm.CanObserveTicketMerged(t.WorkflowStatus, status, anchor, target) {
				continue
			}
			merged, merr := s.isAnchorMergedIntoTarget(ctx, anchor, target)
			if merr != nil {
				out.Errors = append(out.Errors, merr.Error())
				continue
			}
			if !merged {
				continue
			}
			if dryRun {
				continue
			}
			now := time.Now()
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
					TicketID:       t.ID,
					EventType:      contracts.TicketLifecycleMergeObserved,
					Source:         "pm.manager_tick",
					ActorType:      contracts.TicketLifecycleActorSystem,
					IdempotencyKey: ticketlifecycle.MergeObservedIdempotencyKey(t.ID, anchor),
					Payload: map[string]any{
						"ticket_id":          t.ID,
						"target_ref":         target,
						"anchor_sha":         anchor,
						"integration_status": string(contracts.IntegrationMerged),
					},
					CreatedAt: now,
				})
				if err != nil {
					return err
				}
				if !lifecycleResult.IntegrationChanged() {
					return nil
				}
				return s.applyMergedIntegrationSnapshotTx(ctx, tx, t.ID, target, now)
			}); err != nil {
				out.Errors = append(out.Errors, err.Error())
				continue
			}
		default:
			continue
		}
	}

	return out
}

func (s *Service) scheduleQueuedTickets(ctx context.Context, db *gorm.DB, opt scheduleOptions) scheduleResult {
	if ctx == nil {
		ctx = context.Background()
	}
	out := scheduleResult{}
	surfaceIndex, hasSurfaceIndex := s.tryLoadSurfaceConflictIndex()
	if hasSurfaceIndex {
		out.SurfaceConflicts = append(out.SurfaceConflicts, detectSurfaceConflictsFromIndex(surfaceIndex, surfaceIndex.ActiveTicketIDs)...)
	}

	runningTicketIDs := opt.RunningTicketIDs
	if runningTicketIDs == nil {
		runningTicketIDs = map[uint]bool{}
	}
	focusManagedTicketIDs, err := s.focusManagedTicketIDs(ctx, db)
	if err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("读取 active focus scope 失败: %v", err))
		focusManagedTicketIDs = map[uint]struct{}{}
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
	if opt.Capacity <= 0 || opt.DryRun {
		out.SurfaceConflicts = uniqueSurfaceConflicts(out.SurfaceConflicts)
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
		if _, managed := focusManagedTicketIDs[t.ID]; managed {
			continue
		}
		if hasSurfaceIndex {
			strategy, conflicts := evaluateSurfaceConflictStrategyForTicket(t.ID, runningTicketIDs, surfaceIndex)
			if len(conflicts) > 0 {
				out.SurfaceConflicts = append(out.SurfaceConflicts, conflicts...)
			}
			if strategy == SurfaceConflictIntegration {
				_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
					Key:      inboxKeyTicketIncident(t.ID, "surface_conflict_integration"),
					Status:   contracts.InboxOpen,
					Severity: contracts.InboxWarn,
					Reason:   contracts.InboxIncident,
					Title:    fmt.Sprintf("建议 integration 策略：t%d", t.ID),
					Body:     renderSurfaceConflictSummary(conflicts),
					TicketID: t.ID,
				})
			}
			if strategy == SurfaceConflictSerial {
				out.SerialDeferred = append(out.SerialDeferred, t.ID)
				_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
					Key:      inboxKeyTicketIncident(t.ID, "surface_conflict_serial"),
					Status:   contracts.InboxOpen,
					Severity: contracts.InboxWarn,
					Reason:   contracts.InboxIncident,
					Title:    fmt.Sprintf("串行策略触发：t%d", t.ID),
					Body:     renderSurfaceConflictSummary(conflicts),
					TicketID: t.ID,
				})
				continue
			}
		}

		allowedByFocus, serialDeferredByFocus, ferr := s.focusAllowsQueuedActivation(ctx, db, t.ID)
		if ferr != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("focus activation gate 失败：t%d: %v", t.ID, ferr))
			continue
		}
		if !allowedByFocus {
			if serialDeferredByFocus {
				out.SerialDeferred = append(out.SerialDeferred, t.ID)
			}
			continue
		}

		if workerID, remaining, err := s.queuedRetryBackoffRemaining(ctx, t.ID, time.Now()); err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("读取重试退避失败：t%d: %v", t.ID, err))
			continue
		} else if remaining > 0 {
			s.slog().Debug("pm queue consumer defer queued retry by backoff",
				"ticket_id", t.ID,
				"worker_id", workerID,
				"retry_after", remaining.String(),
			)
			continue
		}

		workerID := uint(0)
		if w, werr := s.worker.LatestWorker(ctx, t.ID); werr != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("读取最新 worker 失败：t%d: %v", t.ID, werr))
			continue
		} else if w != nil {
			workerID = w.ID
		}

		dispatched, errs := s.submitScheduledWorkerRun(ctx, t.ID, workerID, opt)
		out.Errors = append(out.Errors, errs...)
		if dispatched {
			out.ActivatedTickets = append(out.ActivatedTickets, t.ID)
			runningTicketIDs[t.ID] = true
			capacity--
		}
	}
	out.SurfaceConflicts = uniqueSurfaceConflicts(out.SurfaceConflicts)
	return out
}

func (s *Service) submitScheduledWorkerRun(ctx context.Context, ticketID, workerID uint, opt scheduleOptions) (bool, []string) {
	baseBranch, berr := s.workerBaseBranchForTicket(ctx, ticketID, "")
	if berr != nil {
		return false, s.handleActivationFailure(ctx, ticketID, workerID, berr, "resolve worker base branch 失败", opt.Source)
	}
	if opt.SyncWorkerRun {
		activationCtx := ctx
		cancelActivation := func() {}
		if opt.WorkerRunTimeout > 0 {
			activationCtx, cancelActivation = context.WithTimeout(ctx, opt.WorkerRunTimeout)
		}
		_, derr := s.RunTicketWorker(activationCtx, ticketID, WorkerRunOptions{BaseBranch: baseBranch})
		cancelActivation()
		if derr != nil {
			return false, s.handleActivationFailure(ctx, ticketID, workerID, derr, "sync activation 失败", opt.Source)
		}
		return true, nil
	}

	if submitter := s.getWorkerRunSubmitter(); submitter != nil {
		submission, derr := submitter.SubmitTicketWorkerRun(context.WithoutCancel(ctx), ticketID, WorkerRunSubmitOptions{
			BaseBranch: baseBranch,
		})
		if derr != nil {
			return false, s.handleActivationFailure(ctx, ticketID, workerID, derr, "submit worker run 失败", opt.Source)
		}
		if submission.TaskRunID == 0 {
			return false, s.handleActivationFailure(ctx, ticketID, workerID, fmt.Errorf("submit worker run 未返回 task_run_id"), "submit worker run 失败", opt.Source)
		}
		return true, nil
	}

	errMsg := fmt.Sprintf("worker run submitter 未配置：t%d w%d", ticketID, workerID)
	_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
		Key:      inboxKeyTicketIncident(ticketID, "worker_run_no_submitter"),
		Status:   contracts.InboxOpen,
		Severity: contracts.InboxBlocker,
		Reason:   contracts.InboxIncident,
		Title:    fmt.Sprintf("激活失败：t%d w%d", ticketID, workerID),
		Body:     errMsg,
		TicketID: ticketID,
		WorkerID: workerID,
	})
	return false, []string{errMsg}
}

func (s *Service) handleActivationFailure(ctx context.Context, ticketID, workerID uint, activationErr error, prefix, source string) []string {
	if activationErr == nil {
		return nil
	}
	errs := []string{}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "pm.queue_consumer"
	}
	inboxKey := inboxKeyTicketIncident(ticketID, "activation_failed")
	title := fmt.Sprintf("激活失败：t%d", ticketID)
	if workerID != 0 {
		inboxKey = inboxKeyWorkerIncident(workerID, "activation_failed")
		title = fmt.Sprintf("激活失败：t%d w%d", ticketID, workerID)
	}

	_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
		Key:      inboxKey,
		Status:   contracts.InboxOpen,
		Severity: contracts.InboxWarn,
		Reason:   contracts.InboxIncident,
		Title:    title,
		Body:     activationErr.Error(),
		TicketID: ticketID,
		WorkerID: workerID,
	})

	if isWorkerReadyTimeout(activationErr) {
		if berr := s.demoteTicketBlockedOnWorkerNotReady(ctx, ticketID, workerID, activationErr.Error(), source, time.Now()); berr != nil {
			errs = append(errs, fmt.Sprintf("worker 未就绪降级失败：t%d w%d: %v", ticketID, workerID, berr))
		}
	}
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		prefix = "激活失败"
	}
	errs = append(errs, fmt.Sprintf("%s：t%d w%d: %v", prefix, ticketID, workerID, activationErr))
	return errs
}

func taskEventBody(ev contracts.TaskEventScopeRow) string {
	body := strings.TrimSpace(ev.Note)
	if body != "" {
		return body
	}
	_, _, _, summary := parseTaskEventSignals(ev)
	if summary != "" {
		return summary
	}
	return strings.TrimSpace(ev.PayloadJSON.String())
}

func (s *Service) consumeWorkerLoopTerminatedEvent(ctx context.Context, ev contracts.TaskEventScopeRow) error {
	payload := map[string]any(ev.PayloadJSON)
	source := strings.TrimSpace(mapString(payload, "source"))
	if source == "" {
		source = "pm.manager_tick"
	}
	reason := taskEventBody(ev)
	if reason == "" {
		reason = "worker loop terminated before terminal closure"
	}
	extra := map[string]any{
		"event_id":   ev.ID,
		"event_type": strings.TrimSpace(ev.EventType),
	}
	if phase := strings.TrimSpace(mapString(payload, "phase")); phase != "" {
		extra["phase"] = phase
	}
	if requestID := strings.TrimSpace(mapString(payload, "request_id")); requestID != "" {
		extra["request_id"] = requestID
	}
	if summary := strings.TrimSpace(mapString(payload, "summary")); summary != "" {
		extra["summary"] = summary
	}
	if cancelRequested, ok := mapBool(payload, "cancel_requested"); ok {
		extra["cancel_requested"] = cancelRequested
	}
	_, err := s.convergeExecutionLost(ctx, executionLossInput{
		TicketID:        ev.TicketID,
		WorkerID:        ev.WorkerID,
		TaskRunID:       ev.TaskRunID,
		Source:          source,
		ObservationKind: strings.TrimSpace(mapString(payload, "observation_kind")),
		FailureCode:     strings.TrimSpace(mapString(payload, "failure_code")),
		Reason:          reason,
		Payload:         extra,
		Now:             ev.CreatedAt,
	})
	return err
}

func parseTaskEventSignals(ev contracts.TaskEventScopeRow) (needsUser bool, runtimeHealth string, nextAction string, summary string) {
	toState := map[string]any(ev.ToStateJSON)
	payload := map[string]any(ev.PayloadJSON)

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
