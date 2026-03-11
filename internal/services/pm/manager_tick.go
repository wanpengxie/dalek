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

	AutopilotEnabled bool
	MaxRunning       int
	Running          int
	RunningBlocked   int
	ZombieRecovered  int
	ZombieBlocked    int
	ZombieIllegal    int
	ZombieUndefined  int
	Capacity         int

	EventsConsumed int
	InboxUpserts   int

	PlannerRunScheduled bool

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
	if err := s.reconcilePlannerActiveRun(ctx, taskRuntime, st, now); err != nil {
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

	// merge 观测不受 autopilot 门控：只是被动检测 git 事实，不产生 start/activation 副作用。
	mergeResult := s.freezeMergesForDoneTickets(ctx, db, st, opt.DryRun)
	res.applyMergeFrozenResult(mergeResult)

	if !st.AutopilotEnabled {
		finalizeCtx, finalizeCancel := managerTickFinalizeContext(ctx)
		defer finalizeCancel()
		if err := s.saveManagerTickState(finalizeCtx, db, st, now, lastEventID, maxRunning, opt); err != nil {
			res.Errors = append(res.Errors, err.Error())
		}
		return res, nil
	}

	scheduleResult := s.scheduleQueuedTickets(ctx, db, scheduleOptions{
		Capacity:         capacity,
		RunningTicketIDs: scanResult.RunningTicketIDs,
		DryRun:           opt.DryRun,
		SyncWorkerRun:    opt.SyncWorkerRun,
		WorkerRunTimeout: opt.WorkerRunTimeout,
		PMState:          st,
	})
	res.applyScheduleResult(scheduleResult)
	res.SurfaceConflicts = uniqueSurfaceConflicts(res.SurfaceConflicts)

	finalizeCtx, finalizeCancel := managerTickFinalizeContext(ctx)
	defer finalizeCancel()

	scheduled, planErr := s.maybeSchedulePlannerRun(finalizeCtx, db, st, now)
	if planErr != nil {
		res.Errors = append(res.Errors, planErr.Error())
	}
	res.PlannerRunScheduled = scheduled

	if err := s.saveManagerTickState(finalizeCtx, db, st, now, lastEventID, maxRunning, opt); err != nil {
		res.Errors = append(res.Errors, err.Error())
	}

	return res, nil
}

func (s *Service) reconcilePlannerActiveRun(ctx context.Context, taskRuntime core.TaskRuntime, st *contracts.PMState, now time.Time) error {
	if st == nil || taskRuntime == nil || st.PlannerActiveTaskRunID == nil || *st.PlannerActiveTaskRunID == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runID := *st.PlannerActiveTaskRunID
	run, err := taskRuntime.FindRunByID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		s.failPlannerRun(st, now, fmt.Sprintf("planner active task run missing: run_id=%d", runID))
		s.slog().Warn("pm planner active run missing; clearing stale state",
			"task_run_id", runID,
		)
		return nil
	}
	if run.OwnerType != contracts.TaskOwnerPM || run.TaskType != contracts.TaskTypePMPlannerRun {
		s.failPlannerRun(st, plannerRunTerminalTime(run, now), fmt.Sprintf("planner active task run type mismatch: run_id=%d owner=%s type=%s", runID, run.OwnerType, run.TaskType))
		s.slog().Warn("pm planner active run type mismatch; clearing stale state",
			"task_run_id", runID,
			"owner_type", run.OwnerType,
			"task_type", run.TaskType,
		)
		return nil
	}

	switch run.OrchestrationState {
	case contracts.TaskSucceeded:
		if recovered, rerr := s.RecoverPlannerOpsForRun(ctx, runID, now); rerr != nil {
			s.slog().Warn("pm planner reconcile recover ops failed",
				"task_run_id", runID,
				"error", rerr,
			)
		} else if recovered > 0 {
			s.slog().Info("pm planner reconcile recovered running ops",
				"task_run_id", runID,
				"recovered_ops", recovered,
			)
		}
		finishedAt := plannerRunTerminalTime(run, now)
		s.clearPlannerRun(st, finishedAt)
		s.slog().Info("pm planner reconciled succeeded terminal run",
			"task_run_id", runID,
			"finished_at", finishedAt,
		)
	case contracts.TaskFailed, contracts.TaskCanceled:
		if recovered, rerr := s.RecoverPlannerOpsForRun(ctx, runID, now); rerr != nil {
			s.slog().Warn("pm planner reconcile recover ops failed",
				"task_run_id", runID,
				"error", rerr,
			)
		} else if recovered > 0 {
			s.slog().Info("pm planner reconcile recovered running ops",
				"task_run_id", runID,
				"recovered_ops", recovered,
			)
		}
		finishedAt := plannerRunTerminalTime(run, now)
		msg := strings.TrimSpace(run.ErrorMessage)
		if msg == "" {
			msg = fmt.Sprintf("planner run ended with state=%s", run.OrchestrationState)
		}
		s.failPlannerRun(st, finishedAt, msg)
		s.slog().Warn("pm planner reconciled failed terminal run",
			"task_run_id", runID,
			"state", run.OrchestrationState,
			"finished_at", finishedAt,
			"error", msg,
		)
	}
	return nil
}

func plannerRunTerminalTime(run *contracts.TaskRun, fallback time.Time) time.Time {
	if run != nil {
		if run.FinishedAt != nil && !run.FinishedAt.IsZero() {
			return *run.FinishedAt
		}
		if !run.UpdatedAt.IsZero() {
			return run.UpdatedAt
		}
	}
	if fallback.IsZero() {
		return time.Now()
	}
	return fallback
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
		"last_tick_at":               st.LastTickAt,
		"last_event_id":              st.LastEventID,
		"max_running_workers":        st.MaxRunningWorkers,
		"planner_dirty":              st.PlannerDirty,
		"planner_wake_version":       st.PlannerWakeVersion,
		"planner_active_task_run_id": st.PlannerActiveTaskRunID,
		"planner_cooldown_until":     st.PlannerCooldownUntil,
		"planner_last_error":         strings.TrimSpace(st.PlannerLastError),
		"planner_last_run_at":        st.PlannerLastRunAt,
		"updated_at":                 now,
	}
	res := db.WithContext(ctx).Model(&contracts.PMState{}).
		Where("id = ?", st.ID).
		Updates(updates)
	if res.Error != nil {
		s.slog().Warn("pm manager tick save state failed",
			"pm_state_id", st.ID,
			"planner_dirty", st.PlannerDirty,
			"planner_wake_version", st.PlannerWakeVersion,
			"error", res.Error,
		)
		return res.Error
	}
	if res.RowsAffected == 0 {
		err := fmt.Errorf("pm state 保存失败：id=%d 未命中", st.ID)
		s.slog().Warn("pm manager tick save state missed row",
			"pm_state_id", st.ID,
			"planner_dirty", st.PlannerDirty,
			"planner_wake_version", st.PlannerWakeVersion,
		)
		return err
	}
	s.slog().Debug("pm manager tick state persisted",
		"pm_state_id", st.ID,
		"planner_dirty", st.PlannerDirty,
		"planner_wake_version", st.PlannerWakeVersion,
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
				s.markPlannerDirty(st)
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
					s.markPlannerDirty(st)
				}
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
				s.markPlannerDirty(st)
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
				s.markPlannerDirty(st)
				continue
			}
			if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				freeze, err := s.resolveDoneIntegrationFreezeTx(ctx, tx, t.ID, 0)
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
			s.markPlannerDirty(st)
			continue
		case contracts.IntegrationNeedsMerge:
			anchor := strings.TrimSpace(t.MergeAnchorSHA)
			target := strings.TrimSpace(t.TargetBranch)
			if target == "" {
				target = s.defaultIntegrationTargetBranch(ctx)
			}
			if !fsm.CanObserveTicketMerged(t.WorkflowStatus, status, anchor, target) {
				s.markPlannerDirty(st)
				continue
			}
			merged, merr := s.isAnchorMergedIntoTarget(ctx, anchor, target)
			if merr != nil {
				out.Errors = append(out.Errors, merr.Error())
				continue
			}
			if !merged {
				s.markPlannerDirty(st)
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

func (s *Service) maybeSchedulePlannerRun(ctx context.Context, db *gorm.DB, st *contracts.PMState, now time.Time) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if st == nil {
		return false, nil
	}
	skipAutopilot := !st.AutopilotEnabled
	skipDirty := !st.PlannerDirty
	skipActiveRun := st.PlannerActiveTaskRunID != nil
	skipCooldown := st.PlannerCooldownUntil != nil && !now.After(*st.PlannerCooldownUntil)
	if skipAutopilot || skipDirty || skipActiveRun || skipCooldown {
		s.slog().Debug("pm planner schedule skipped",
			"skip_autopilot", skipAutopilot,
			"skip_dirty", skipDirty,
			"skip_active_run", skipActiveRun,
			"skip_cooldown", skipCooldown,
			"autopilot_enabled", st.AutopilotEnabled,
			"planner_dirty", st.PlannerDirty,
			"planner_active_task_run_id", st.PlannerActiveTaskRunID,
			"planner_cooldown_until", st.PlannerCooldownUntil,
		)
		return false, nil
	}

	taskRuntime, err := s.taskRuntimeForDB(db)
	if err != nil {
		return false, err
	}
	requestID := newPMRequestID("pln")
	taskRun, err := taskRuntime.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypePMPlannerRun,
		ProjectKey:         strings.TrimSpace(s.p.Key),
		SubjectType:        "pm",
		SubjectID:          "planner",
		RequestID:          requestID,
		OrchestrationState: contracts.TaskPending,
		RequestPayloadJSON: marshalJSON(map[string]any{
			"wake_version": st.PlannerWakeVersion,
		}),
	})
	if err != nil {
		s.slog().Warn("pm planner schedule create run failed",
			"planner_wake_version", st.PlannerWakeVersion,
			"error", err,
		)
		return false, err
	}
	runID := taskRun.ID
	st.PlannerActiveTaskRunID = &runID
	st.PlannerDirty = false
	s.slog().Debug("pm planner schedule created run",
		"task_run_id", runID,
		"planner_wake_version", st.PlannerWakeVersion,
		"planner_dirty_after", st.PlannerDirty,
	)
	return true, nil
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
		if hasSurfaceIndex {
			strategy, conflicts := evaluateSurfaceConflictStrategyForTicket(t.ID, runningTicketIDs, surfaceIndex)
			if len(conflicts) > 0 {
				out.SurfaceConflicts = append(out.SurfaceConflicts, conflicts...)
			}
			if strategy == SurfaceConflictIntegration {
				created, _ := s.upsertOpenInbox(ctx, contracts.InboxItem{
					Key:      inboxKeyTicketIncident(t.ID, "surface_conflict_integration"),
					Status:   contracts.InboxOpen,
					Severity: contracts.InboxWarn,
					Reason:   contracts.InboxIncident,
					Title:    fmt.Sprintf("建议 integration 策略：t%d", t.ID),
					Body:     renderSurfaceConflictSummary(conflicts),
					TicketID: t.ID,
				})
				if created && opt.PMState != nil {
					s.markPlannerDirty(opt.PMState)
				}
			}
			if strategy == SurfaceConflictSerial {
				out.SerialDeferred = append(out.SerialDeferred, t.ID)
				created, _ := s.upsertOpenInbox(ctx, contracts.InboxItem{
					Key:      inboxKeyTicketIncident(t.ID, "surface_conflict_serial"),
					Status:   contracts.InboxOpen,
					Severity: contracts.InboxWarn,
					Reason:   contracts.InboxIncident,
					Title:    fmt.Sprintf("串行策略触发：t%d", t.ID),
					Body:     renderSurfaceConflictSummary(conflicts),
					TicketID: t.ID,
				})
				if created && opt.PMState != nil {
					s.markPlannerDirty(opt.PMState)
				}
				continue
			}
		}

		if workerID, remaining, err := s.queuedRetryBackoffRemaining(ctx, t.ID, time.Now()); err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("读取重试退避失败：t%d: %v", t.ID, err))
			continue
		} else if remaining > 0 {
			s.slog().Debug("pm manager tick defer queued retry by backoff",
				"ticket_id", t.ID,
				"worker_id", workerID,
				"retry_after", remaining.String(),
			)
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
		if w == nil || (w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped) {
			workerID := uint(0)
			workerStatus := contracts.WorkerStatus("")
			if w != nil {
				workerID = w.ID
				workerStatus = w.Status
			}
			msg := fmt.Sprintf("start 返回后 worker 未处于可调度状态（t%d w%d status=%s）", t.ID, workerID, workerStatus)
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

		dispatched, errs := s.submitScheduledWorkerRun(ctx, t.ID, w.ID, opt)
		out.Errors = append(out.Errors, errs...)
		if dispatched {
			out.ActivatedTickets = append(out.ActivatedTickets, t.ID)
		}
		capacity--
	}
	out.SurfaceConflicts = uniqueSurfaceConflicts(out.SurfaceConflicts)
	return out
}

func (s *Service) submitScheduledWorkerRun(ctx context.Context, ticketID, workerID uint, opt scheduleOptions) (bool, []string) {
	if opt.SyncWorkerRun {
		activationCtx := ctx
		cancelActivation := func() {}
		if opt.WorkerRunTimeout > 0 {
			activationCtx, cancelActivation = context.WithTimeout(ctx, opt.WorkerRunTimeout)
		}
		_, derr := s.RunTicketWorker(activationCtx, ticketID, WorkerRunOptions{})
		cancelActivation()
		if derr != nil {
			return false, s.handleActivationFailure(ctx, ticketID, workerID, derr, "sync activation 失败")
		}
		return true, nil
	}

	if submitter := s.getWorkerRunSubmitter(); submitter != nil {
		derr := submitter.SubmitTicketWorkerRun(context.WithoutCancel(ctx), ticketID)
		if derr != nil {
			return false, s.handleActivationFailure(ctx, ticketID, workerID, derr, "submit worker run 失败")
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

func (s *Service) handleActivationFailure(ctx context.Context, ticketID, workerID uint, activationErr error, prefix string) []string {
	if activationErr == nil {
		return nil
	}
	errs := []string{}

	_, _ = s.upsertOpenInbox(ctx, contracts.InboxItem{
		Key:      inboxKeyWorkerIncident(workerID, "activation_failed"),
		Status:   contracts.InboxOpen,
		Severity: contracts.InboxWarn,
		Reason:   contracts.InboxIncident,
		Title:    fmt.Sprintf("激活失败：t%d w%d", ticketID, workerID),
		Body:     activationErr.Error(),
		TicketID: ticketID,
		WorkerID: workerID,
	})

	if isWorkerReadyTimeout(activationErr) {
		if berr := s.demoteTicketBlockedOnWorkerNotReady(ctx, ticketID, workerID, activationErr.Error(), time.Now()); berr != nil {
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
