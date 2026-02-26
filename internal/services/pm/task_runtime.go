package pm

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"strings"
	"time"

	"dalek/internal/services/core"

	"gorm.io/gorm"
)

func (s *Service) taskRuntime() (core.TaskRuntime, error) {
	p, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if p.TaskRuntime == nil {
		return nil, fmt.Errorf("task runtime factory 为空")
	}
	return p.TaskRuntime.ForDB(db), nil
}

func (s *Service) taskRuntimeForDB(db *gorm.DB) (core.TaskRuntime, error) {
	p, _, err := s.require()
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

func (s *Service) recordPMTaskSemantic(ctx context.Context, taskRunID uint, phase contracts.TaskSemanticPhase, milestone, nextAction, summary string, payload any) {
	if taskRunID == 0 {
		return
	}
	rt, err := s.taskRuntime()
	if err != nil {
		return
	}
	_ = rt.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  taskRunID,
		Phase:      phase,
		Milestone:  strings.TrimSpace(milestone),
		NextAction: strings.TrimSpace(nextAction),
		Summary:    strings.TrimSpace(summary),
		ReportedAt: time.Now(),
		Payload:    payload,
	})
}

func (s *Service) recordPMTaskRuntime(ctx context.Context, taskRunID uint, state contracts.TaskRuntimeHealthState, needsUser bool, summary string, source string, metrics any) {
	if taskRunID == 0 {
		return
	}
	rt, err := s.taskRuntime()
	if err != nil {
		return
	}
	_ = rt.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  taskRunID,
		State:      state,
		NeedsUser:  needsUser,
		Summary:    strings.TrimSpace(summary),
		Source:     strings.TrimSpace(source),
		ObservedAt: time.Now(),
		Metrics:    metrics,
	})
}

func (s *Service) recordPMTaskFailure(ctx context.Context, taskRunID uint, err error) {
	if taskRunID == 0 || err == nil {
		return
	}
	msg := strings.TrimSpace(err.Error())
	s.recordPMTaskRuntime(ctx, taskRunID, contracts.TaskHealthStalled, false, msg, "pm_dispatch", map[string]any{
		"error": msg,
	})
	s.recordPMTaskSemantic(ctx, taskRunID, contracts.TaskPhaseBlocked, "dispatch_failed", "wait_user", msg, map[string]any{
		"error": msg,
	})
}

func (s *Service) ensureWorkerTaskRunFromDispatch(ctx context.Context, job contracts.PMDispatchJob, t contracts.Ticket, w contracts.Worker, taskPath string, health contracts.TaskRuntimeHealthState, phase contracts.TaskSemanticPhase, nextAction, summary string, payload any) (contracts.TaskRun, error) {
	if strings.TrimSpace(job.RequestID) == "" {
		return contracts.TaskRun{}, fmt.Errorf("dispatch request_id 为空")
	}
	p, db, err := s.require()
	if err != nil {
		return contracts.TaskRun{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	reason := fmt.Sprintf("redispatch superseded by request=%s", strings.TrimSpace(job.RequestID))
	requestID := "wrk_" + strings.TrimSpace(job.RequestID)
	var run contracts.TaskRun
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		rt, err := s.taskRuntimeForDB(tx)
		if err != nil {
			return err
		}
		if err := rt.CancelActiveWorkerRuns(ctx, w.ID, reason, now); err != nil {
			return err
		}
		created, err := rt.CreateRun(ctx, contracts.TaskRunCreateInput{
			OwnerType:          contracts.TaskOwnerWorker,
			TaskType:           "deliver_ticket",
			ProjectKey:         strings.TrimSpace(p.Key),
			TicketID:           t.ID,
			WorkerID:           w.ID,
			SubjectType:        "ticket",
			SubjectID:          fmt.Sprintf("%d", t.ID),
			RequestID:          requestID,
			OrchestrationState: contracts.TaskRunning,
			StartedAt:          &now,
			RequestPayloadJSON: marshalJSON(map[string]any{
				"dispatch_request_id":  strings.TrimSpace(job.RequestID),
				"dispatch_task_run_id": job.TaskRunID,
				"task_path":            strings.TrimSpace(taskPath),
			}),
		})
		if err != nil {
			return err
		}
		if err := rt.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: created.ID,
			EventType: "task_started",
			ToState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
			},
			Note: "dispatch accepted by worker",
		}); err != nil {
			return err
		}
		if err := rt.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
			TaskRunID:  created.ID,
			State:      health,
			NeedsUser:  false,
			Summary:    strings.TrimSpace(summary),
			Source:     "pm_dispatch",
			ObservedAt: now,
			Metrics:    payload,
		}); err != nil {
			return err
		}
		if err := rt.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
			TaskRunID:  created.ID,
			Phase:      phase,
			Milestone:  "dispatch_ready",
			NextAction: strings.TrimSpace(nextAction),
			Summary:    strings.TrimSpace(summary),
			ReportedAt: now,
			Payload:    payload,
		}); err != nil {
			return err
		}
		run = created
		return nil
	})
	if err != nil {
		return contracts.TaskRun{}, err
	}
	return run, nil
}
