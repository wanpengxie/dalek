package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"

	"gorm.io/gorm"
)

var (
	ErrInvalidWorkerReportTaskRun = errors.New("worker report task_run binding invalid")
	ErrDuplicateTerminalReport    = errors.New("duplicate terminal worker report")
)

func (s *Service) taskRuntime() (core.TaskRuntime, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	if p.TaskRuntime == nil {
		return nil, fmt.Errorf("task runtime factory 为空")
	}
	return p.TaskRuntime.ForDB(p.DB), nil
}

func (s *Service) taskRuntimeForDB(db *gorm.DB) (core.TaskRuntime, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	if p.TaskRuntime == nil {
		return nil, fmt.Errorf("task runtime factory 为空")
	}
	if db == nil {
		return nil, fmt.Errorf("task runtime db 为空")
	}
	return p.TaskRuntime.ForDB(db), nil
}

func (s *Service) appendWorkerTaskEvent(ctx context.Context, workerID uint, eventType string, note string, payload any, createdAt time.Time) error {
	rt, err := s.taskRuntime()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID == 0 {
		return fmt.Errorf("worker_id 不能为空")
	}
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	run, err := rt.LatestActiveWorkerRun(ctx, workerID)
	if err != nil || run == nil {
		return err
	}
	return rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: strings.TrimSpace(eventType),
		Note:      strings.TrimSpace(note),
		Payload:   payload,
		CreatedAt: createdAt,
	})
}

func (s *Service) AppendWorkerTaskEvent(ctx context.Context, workerID uint, eventType string, note string, payload any, createdAt time.Time) error {
	return s.appendWorkerTaskEvent(ctx, workerID, eventType, note, payload, createdAt)
}

func (s *Service) ensureActiveWorkerTaskRun(ctx context.Context, w contracts.Worker, reason string, now time.Time) (contracts.TaskRun, error) {
	rt, err := s.taskRuntime()
	if err != nil {
		return contracts.TaskRun{}, err
	}
	return s.ensureActiveWorkerTaskRunWithRuntime(ctx, rt, w, reason, now)
}

func (s *Service) ensureActiveWorkerTaskRunWithRuntime(ctx context.Context, rt core.TaskRuntime, w contracts.Worker, reason string, now time.Time) (contracts.TaskRun, error) {
	if rt == nil {
		return contracts.TaskRun{}, fmt.Errorf("task runtime service 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	run, err := rt.LatestActiveWorkerRun(ctx, w.ID)
	if err != nil {
		return contracts.TaskRun{}, err
	}
	if run != nil {
		return *run, nil
	}
	p, err := s.require()
	if err != nil {
		return contracts.TaskRun{}, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "worker report bootstrap"
	}
	requestID := fmt.Sprintf("wrk_w%d_%d", w.ID, now.UnixNano())
	created, err := rt.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
		ProjectKey:         strings.TrimSpace(p.Key),
		TicketID:           w.TicketID,
		WorkerID:           w.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", w.TicketID),
		RequestID:          requestID,
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
		RequestPayloadJSON: workerTaskJSON(map[string]any{
			"source": "worker_report",
			"reason": reason,
		}),
	})
	if err != nil {
		return contracts.TaskRun{}, err
	}
	_ = rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: created.ID,
		EventType: "task_started",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskRunning,
		},
		Note: reason,
	})
	return created, nil
}

func (s *Service) syncTaskRuntimeFromReport(ctx context.Context, w contracts.Worker, r contracts.WorkerReport, runtimeHealth contracts.TaskRuntimeHealthState, needsUser bool, summary string, source string, now time.Time) (bool, error) {
	rt, err := s.taskRuntime()
	if err != nil {
		return false, err
	}
	return s.syncTaskRuntimeFromReportWithRuntime(ctx, rt, nil, w, r, runtimeHealth, needsUser, summary, source, now)
}

func (s *Service) syncTaskRuntimeFromReportWithRuntime(ctx context.Context, rt core.TaskRuntime, db *gorm.DB, w contracts.Worker, r contracts.WorkerReport, runtimeHealth contracts.TaskRuntimeHealthState, needsUser bool, summary string, source string, now time.Time) (bool, error) {
	if rt == nil {
		return false, fmt.Errorf("task runtime service 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	if db == nil {
		p, rerr := s.require()
		if rerr != nil {
			return false, rerr
		}
		db = p.DB
	}

	run, duplicateTerminal, err := s.resolveBoundWorkerReportRun(ctx, rt, w, r, source, now)
	if err != nil {
		return false, err
	}
	if duplicateTerminal {
		return true, nil
	}
	next := strings.TrimSpace(strings.ToLower(r.NextAction))
	if err := rt.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  run.ID,
		State:      runtimeHealth,
		NeedsUser:  needsUser,
		Summary:    strings.TrimSpace(summary),
		Source:     strings.TrimSpace(source),
		ObservedAt: now,
		Metrics: map[string]any{
			"runtime_health_state": strings.TrimSpace(string(runtimeHealth)),
			"head_sha":             strings.TrimSpace(r.HeadSHA),
			"dirty":                r.Dirty,
		},
	}); err != nil {
		return false, err
	}
	phase := contracts.NextActionToSemanticPhase(r.NextAction)
	if err := rt.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  run.ID,
		Phase:      phase,
		Milestone:  "agent_report",
		NextAction: strings.TrimSpace(r.NextAction),
		Summary:    strings.TrimSpace(summary),
		ReportedAt: now,
		Payload: map[string]any{
			"source":      strings.TrimSpace(source),
			"next_action": strings.TrimSpace(r.NextAction),
			"needs_user":  needsUser,
			"head_sha":    strings.TrimSpace(r.HeadSHA),
			"dirty":       r.Dirty,
			"blockers":    r.Blockers,
		},
	}); err != nil {
		return false, err
	}
	_ = rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "semantic_reported",
		ToState: map[string]any{
			"semantic_phase": phase,
			"next_action":    strings.TrimSpace(r.NextAction),
		},
		Note: fmt.Sprintf("report source=%s", strings.TrimSpace(source)),
	})

	if next == string(contracts.NextDone) {
		currentRun, err := rt.FindRunByID(ctx, run.ID)
		if err != nil {
			return false, err
		}
		if currentRun == nil {
			return false, fmt.Errorf("task_run 不存在: run_id=%d", run.ID)
		}
		if isTerminalTaskState(currentRun.OrchestrationState) {
			if err := appendDuplicateTerminalReport(ctx, rt, currentRun.ID, currentRun.OrchestrationState, now, "worker report next_action=done ignored: run already terminal", source, r); err != nil {
				return false, err
			}
			return true, nil
		}
		if err := rt.MarkRunSucceeded(ctx, run.ID, workerTaskJSON(r), now); err != nil {
			return false, err
		}
		hasSucceededEvent, err := hasTaskEvent(ctx, db, run.ID, "task_succeeded")
		if err != nil {
			return false, err
		}
		if hasSucceededEvent {
			afterRun, aerr := rt.FindRunByID(ctx, run.ID)
			if aerr != nil {
				return false, aerr
			}
			state := contracts.TaskSucceeded
			if afterRun != nil {
				state = afterRun.OrchestrationState
			}
			if err := appendDuplicateTerminalReport(ctx, rt, run.ID, state, now, "worker report next_action=done ignored: task_succeeded already recorded", source, r); err != nil {
				return false, err
			}
			return true, nil
		}
		if err := rt.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: run.ID,
			EventType: "task_succeeded",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState:   map[string]any{"orchestration_state": contracts.TaskSucceeded},
			Note:      "worker report next_action=done",
		}); err != nil {
			return false, err
		}
	}
	return false, nil
}

func (s *Service) resolveBoundWorkerReportRun(ctx context.Context, rt core.TaskRuntime, w contracts.Worker, r contracts.WorkerReport, source string, now time.Time) (contracts.TaskRun, bool, error) {
	if rt == nil {
		return contracts.TaskRun{}, false, fmt.Errorf("task runtime service 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if r.TaskRunID == 0 {
		return contracts.TaskRun{}, false, fmt.Errorf("%w: task_run_id 不能为空", ErrInvalidWorkerReportTaskRun)
	}
	run, err := rt.FindRunByID(ctx, r.TaskRunID)
	if err != nil {
		return contracts.TaskRun{}, false, err
	}
	if run == nil {
		return contracts.TaskRun{}, false, fmt.Errorf("%w: task_run_id=%d 不存在", ErrInvalidWorkerReportTaskRun, r.TaskRunID)
	}
	if run.OwnerType != contracts.TaskOwnerWorker ||
		strings.TrimSpace(run.TaskType) != contracts.TaskTypeDeliverTicket ||
		run.WorkerID != w.ID ||
		run.TicketID != w.TicketID {
		return contracts.TaskRun{}, false, fmt.Errorf("%w: run_id=%d 不属于当前 worker/ticket", ErrInvalidWorkerReportTaskRun, r.TaskRunID)
	}

	activeRun, err := rt.LatestActiveWorkerRun(ctx, w.ID)
	if err != nil {
		return contracts.TaskRun{}, false, err
	}
	if activeRun != nil && activeRun.ID != run.ID {
		return contracts.TaskRun{}, false, fmt.Errorf("%w: run_id=%d 不是当前 active deliver run（active=%d）", ErrInvalidWorkerReportTaskRun, run.ID, activeRun.ID)
	}
	if activeRun != nil {
		return *activeRun, false, nil
	}
	if !isTerminalTaskState(run.OrchestrationState) {
		return *run, false, nil
	}

	next := strings.TrimSpace(strings.ToLower(r.NextAction))
	if next == string(contracts.NextDone) {
		if err := appendDuplicateTerminalReport(ctx, rt, run.ID, run.OrchestrationState, now, "worker report next_action=done ignored: run already terminal", source, r); err != nil {
			return contracts.TaskRun{}, false, err
		}
		return *run, true, nil
	}
	if next == string(contracts.NextWaitUser) && strings.HasPrefix(strings.TrimSpace(source), "pm.worker_run.missing_report") {
		return *run, false, nil
	}
	return contracts.TaskRun{}, false, fmt.Errorf("%w: run_id=%d 已终态 state=%s", ErrInvalidWorkerReportTaskRun, run.ID, run.OrchestrationState)
}

func latestWorkerTaskRun(ctx context.Context, db *gorm.DB, workerID uint) (*contracts.TaskRun, error) {
	if db == nil {
		return nil, fmt.Errorf("task runtime db 为空")
	}
	if workerID == 0 {
		return nil, fmt.Errorf("worker_id 不能为空")
	}
	var run contracts.TaskRun
	if err := db.WithContext(ctx).
		Where("owner_type = ? AND worker_id = ?", contracts.TaskOwnerWorker, workerID).
		Order("id desc").
		First(&run).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func hasTaskEvent(ctx context.Context, db *gorm.DB, runID uint, eventType string) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("task runtime db 为空")
	}
	if runID == 0 {
		return false, fmt.Errorf("run_id 不能为空")
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return false, fmt.Errorf("event_type 不能为空")
	}
	var count int64
	if err := db.WithContext(ctx).Model(&contracts.TaskEvent{}).
		Where("task_run_id = ? AND event_type = ?", runID, eventType).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func appendDuplicateTerminalReport(ctx context.Context, rt core.TaskRuntime, runID uint, state contracts.TaskOrchestrationState, now time.Time, note string, source string, report contracts.WorkerReport) error {
	if rt == nil {
		return fmt.Errorf("task runtime service 为空")
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}
	return rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "duplicate_terminal_report",
		FromState: map[string]any{
			"orchestration_state": state,
		},
		ToState: map[string]any{
			"orchestration_state": state,
		},
		Note: strings.TrimSpace(note),
		Payload: map[string]any{
			"source":          strings.TrimSpace(source),
			"next_action":     strings.TrimSpace(report.NextAction),
			"runtime_health":  reportToRuntimeHealth(report),
			"needs_user":      report.NeedsUser,
			"orchestration":   state,
			"duplicate_guard": "done_terminal_guard",
		},
		CreatedAt: now,
	})
}

func isTerminalTaskState(state contracts.TaskOrchestrationState) bool {
	switch state {
	case contracts.TaskSucceeded, contracts.TaskFailed, contracts.TaskCanceled:
		return true
	default:
		return false
	}
}

func (s *Service) finalizeWorkerTaskRunOnStopWithRuntime(ctx context.Context, rt core.TaskRuntime, w contracts.Worker, reason string, source string, now time.Time) error {
	if rt == nil {
		return fmt.Errorf("task runtime service 为空")
	}
	if w.ID == 0 {
		return fmt.Errorf("worker_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = fmt.Sprintf("worker stopped: w%d", w.ID)
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "worker_stop"
	}
	run, err := rt.LatestActiveWorkerRun(ctx, w.ID)
	if err != nil {
		return err
	}
	if run == nil {
		return nil
	}
	from := strings.TrimSpace(string(run.OrchestrationState))
	if from == "" {
		from = string(contracts.TaskRunning)
	}
	if err := rt.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  run.ID,
		State:      contracts.TaskHealthDead,
		NeedsUser:  false,
		Summary:    reason,
		Source:     source,
		ObservedAt: now,
		Metrics: map[string]any{
			"worker_id": w.ID,
			"ticket_id": w.TicketID,
			"source":    source,
			"reason":    reason,
		},
	}); err != nil {
		return err
	}
	if err := rt.MarkRunCanceled(ctx, run.ID, "worker_stopped", reason, now); err != nil {
		return err
	}
	return rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "task_canceled",
		FromState: map[string]any{
			"orchestration_state": from,
		},
		ToState: map[string]any{
			"orchestration_state":  contracts.TaskCanceled,
			"runtime_health_state": contracts.TaskHealthDead,
		},
		Note: reason,
		Payload: map[string]any{
			"source":    source,
			"worker_id": w.ID,
			"ticket_id": w.TicketID,
		},
		CreatedAt: now,
	})
}

func workerTaskJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
