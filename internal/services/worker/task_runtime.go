package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"

	"gorm.io/gorm"
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
	return rt.AppendEvent(ctx, core.TaskRuntimeEventInput{
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
	requestID := fmt.Sprintf("wrk_legacy_w%d_%d", w.ID, now.UnixNano())
	created, err := rt.CreateRun(ctx, core.TaskRuntimeCreateRunInput{
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
	_ = rt.AppendEvent(ctx, core.TaskRuntimeEventInput{
		TaskRunID: created.ID,
		EventType: "task_started",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskRunning,
		},
		Note: reason,
	})
	return created, nil
}

func (s *Service) syncTaskRuntimeFromReport(ctx context.Context, w contracts.Worker, r contracts.WorkerReport, runtimeHealth contracts.TaskRuntimeHealthState, needsUser bool, summary string, source string, now time.Time) error {
	rt, err := s.taskRuntime()
	if err != nil {
		return err
	}
	return s.syncTaskRuntimeFromReportWithRuntime(ctx, rt, w, r, runtimeHealth, needsUser, summary, source, now)
}

func (s *Service) syncTaskRuntimeFromReportWithRuntime(ctx context.Context, rt core.TaskRuntime, w contracts.Worker, r contracts.WorkerReport, runtimeHealth contracts.TaskRuntimeHealthState, needsUser bool, summary string, source string, now time.Time) error {
	if rt == nil {
		return fmt.Errorf("task runtime service 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	run, err := s.ensureActiveWorkerTaskRunWithRuntime(ctx, rt, w, "worker report created missing active task", now)
	if err != nil {
		return err
	}
	if err := rt.AppendRuntimeSample(ctx, core.TaskRuntimeRuntimeSampleInput{
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
		return err
	}
	phase := core.NextActionToSemanticPhase(r.NextAction)
	if err := rt.AppendSemanticReport(ctx, core.TaskRuntimeSemanticReportInput{
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
		return err
	}
	_ = rt.AppendEvent(ctx, core.TaskRuntimeEventInput{
		TaskRunID: run.ID,
		EventType: "semantic_reported",
		ToState: map[string]any{
			"semantic_phase": phase,
			"next_action":    strings.TrimSpace(r.NextAction),
		},
		Note: fmt.Sprintf("report source=%s", strings.TrimSpace(source)),
	})

	next := strings.TrimSpace(strings.ToLower(r.NextAction))
	if next == string(contracts.NextDone) {
		if err := rt.MarkRunSucceeded(ctx, run.ID, workerTaskJSON(r), now); err != nil {
			return err
		}
		if err := rt.AppendEvent(ctx, core.TaskRuntimeEventInput{
			TaskRunID: run.ID,
			EventType: "task_succeeded",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState:   map[string]any{"orchestration_state": contracts.TaskSucceeded},
			Note:      "worker report next_action=done",
		}); err != nil {
			return err
		}
	}
	return nil
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
	if err := rt.AppendRuntimeSample(ctx, core.TaskRuntimeRuntimeSampleInput{
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
	return rt.AppendEvent(ctx, core.TaskRuntimeEventInput{
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
