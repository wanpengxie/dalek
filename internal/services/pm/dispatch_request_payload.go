package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

type dispatchTaskRequestPayload struct {
	TicketID       uint   `json:"ticket_id"`
	WorkerID       uint   `json:"worker_id,omitempty"`
	AutoStart      bool   `json:"auto_start,omitempty"`
	BaseBranch     string `json:"base_branch,omitempty"`
	Orchestrator   string `json:"orchestrator,omitempty"`
	OrchestrationV string `json:"orchestration_v,omitempty"`
}

func newDispatchTaskRequestPayload(ticketID, workerID uint, autoStart bool, baseBranch string) dispatchTaskRequestPayload {
	return dispatchTaskRequestPayload{
		TicketID:       ticketID,
		WorkerID:       workerID,
		AutoStart:      autoStart,
		BaseBranch:     strings.TrimSpace(baseBranch),
		Orchestrator:   "pm_dispatch",
		OrchestrationV: "v1",
	}
}

func (p dispatchTaskRequestPayload) JSON() string {
	return marshalJSON(p)
}

func decodeDispatchTaskRequestPayload(raw string) (dispatchTaskRequestPayload, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return dispatchTaskRequestPayload{}, nil
	}
	var payload dispatchTaskRequestPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return dispatchTaskRequestPayload{}, fmt.Errorf("解析 dispatch request payload 失败: %w", err)
	}
	payload.BaseBranch = strings.TrimSpace(payload.BaseBranch)
	payload.Orchestrator = strings.TrimSpace(payload.Orchestrator)
	payload.OrchestrationV = strings.TrimSpace(payload.OrchestrationV)
	return payload, nil
}

func (s *Service) loadDispatchTaskRequestPayload(ctx context.Context, taskRunID uint) (dispatchTaskRequestPayload, error) {
	rt, err := s.taskRuntime()
	if err != nil {
		return dispatchTaskRequestPayload{}, err
	}
	return s.loadDispatchTaskRequestPayloadWithRuntime(ctx, rt, taskRunID)
}

func (s *Service) loadDispatchTaskRequestPayloadWithRuntime(ctx context.Context, rt interface {
	FindRunByID(ctx context.Context, runID uint) (*contracts.TaskRun, error)
}, taskRunID uint) (dispatchTaskRequestPayload, error) {
	if taskRunID == 0 {
		return dispatchTaskRequestPayload{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := rt.FindRunByID(ctx, taskRunID)
	if err != nil {
		return dispatchTaskRequestPayload{}, err
	}
	if run == nil {
		return dispatchTaskRequestPayload{}, fmt.Errorf("dispatch task_run 不存在: run_id=%d", taskRunID)
	}
	return decodeDispatchTaskRequestPayload(run.RequestPayloadJSON)
}

func (s *Service) bindDispatchJobWorker(ctx context.Context, job contracts.PMDispatchJob, worker contracts.Worker, payload dispatchTaskRequestPayload) (contracts.PMDispatchJob, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.PMDispatchJob{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if job.ID == 0 || worker.ID == 0 {
		return job, nil
	}
	payload.WorkerID = worker.ID
	now := time.Now()
	var out contracts.PMDispatchJob
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.PMDispatchJob{}).
			Where("id = ?", job.ID).
			Updates(map[string]any{
				"worker_id":  worker.ID,
				"updated_at": now,
			}).Error; err != nil {
			return err
		}
		if job.TaskRunID != 0 {
			if err := tx.WithContext(ctx).Model(&contracts.TaskRun{}).
				Where("id = ?", job.TaskRunID).
				Updates(map[string]any{
					"worker_id":            worker.ID,
					"request_payload_json": payload.JSON(),
					"updated_at":           now,
				}).Error; err != nil {
				return err
			}
			taskRuntime, terr := s.taskRuntimeForDB(tx)
			if terr != nil {
				return terr
			}
			if err := taskRuntime.AppendEvent(ctx, contracts.TaskEventInput{
				TaskRunID: job.TaskRunID,
				EventType: "dispatch_target_resolved",
				ToState: map[string]any{
					"worker_id": worker.ID,
				},
				Note: fmt.Sprintf("dispatch target resolved to worker w%d", worker.ID),
				Payload: map[string]any{
					"ticket_id":   job.TicketID,
					"worker_id":   worker.ID,
					"base_branch": strings.TrimSpace(payload.BaseBranch),
				},
				CreatedAt: now,
			}); err != nil {
				return err
			}
		}
		if err := tx.WithContext(ctx).First(&out, job.ID).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return contracts.PMDispatchJob{}, err
	}
	return out, nil
}
