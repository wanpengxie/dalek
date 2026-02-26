package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

type runPMDispatchJobOptions struct {
	EntryPromptOverride string
}

func (s *Service) runPMDispatchJob(ctx context.Context, jobID uint, runnerID string, opt runPMDispatchJobOptions) error {
	p, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		runnerID = newPMDispatchRunnerID()
	}

	cfg := p.Config.WithDefaults()
	leaseTTL := time.Duration(cfg.PMDispatchTimeoutMS)*time.Millisecond + dispatchLeaseTTLBuffer
	if leaseTTL < dispatchLeaseTTLMin {
		leaseTTL = dispatchLeaseTTLMin
	}
	job, claimed, err := s.claimPMDispatchJob(ctx, jobID, runnerID, leaseTTL)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	s.recordPMTaskRuntime(ctx, job.TaskRunID, contracts.TaskHealthBusy, false, "dispatch task claimed", "pm_dispatch", map[string]any{
		"runner_id": strings.TrimSpace(runnerID),
	})
	s.recordPMTaskSemantic(ctx, job.TaskRunID, contracts.TaskPhasePlanning, "task_claimed", "continue", "dispatch task claimed by runner", map[string]any{
		"runner_id": strings.TrimSpace(runnerID),
	})

	stopRenew := make(chan struct{})
	defer close(stopRenew)
	go func() {
		ticker := time.NewTicker(dispatchLeaseRenewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopRenew:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = s.renewPMDispatchJobLease(context.Background(), job.ID, runnerID, leaseTTL)
			}
		}
	}()

	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, job.TicketID).Error; err != nil {
		s.recordPMTaskFailure(ctx, job.TaskRunID, err)
		_ = s.completePMDispatchJobFailed(context.Background(), job.ID, runnerID, err.Error())
		return err
	}
	var w contracts.Worker
	if err := db.WithContext(ctx).First(&w, job.WorkerID).Error; err != nil {
		s.recordPMTaskFailure(ctx, job.TaskRunID, err)
		_ = s.completePMDispatchJobFailed(context.Background(), job.ID, runnerID, err.Error())
		return err
	}

	s.recordPMTaskSemantic(ctx, job.TaskRunID, contracts.TaskPhaseImplementing, "dispatch_pm_agent", "continue", "开始执行 PM dispatch agent", map[string]any{
		"request_id": strings.TrimSpace(job.RequestID),
	})

	result, err := s.executePMDispatchJob(ctx, job, t, w, opt)
	if err != nil {
		s.recordPMTaskFailure(ctx, job.TaskRunID, err)
		_ = s.completePMDispatchJobFailed(context.Background(), job.ID, runnerID, err.Error())
		return err
	}
	b, err := json.Marshal(result)
	if err != nil {
		s.recordPMTaskFailure(ctx, job.TaskRunID, err)
		_ = s.completePMDispatchJobFailed(context.Background(), job.ID, runnerID, err.Error())
		return err
	}
	s.recordPMTaskRuntime(ctx, job.TaskRunID, contracts.TaskHealthIdle, false, "dispatch task completed", "pm_dispatch", map[string]any{
		"request_id": strings.TrimSpace(job.RequestID),
	})
	s.recordPMTaskSemantic(ctx, job.TaskRunID, contracts.TaskPhaseDone, "dispatch_done", "done", "dispatch 成功完成", map[string]any{
		"request_id": strings.TrimSpace(job.RequestID),
	})
	if err := s.completePMDispatchJobSuccess(context.Background(), job.ID, runnerID, string(b)); err != nil {
		return err
	}
	return nil
}

func (s *Service) executePMDispatchJob(ctx context.Context, job store.PMDispatchJob, t contracts.Ticket, w contracts.Worker, opt runPMDispatchJobOptions) (contracts.PMDispatchJobResult, error) {
	_, _, err := s.require()
	if err != nil {
		return contracts.PMDispatchJobResult{}, err
	}
	now := time.Now()
	empty := contracts.PMDispatchJobResult{Schema: contracts.PMDispatchJobResultSchemaV1}

	if strings.TrimSpace(w.WorktreePath) == "" {
		err := fmt.Errorf("worker worktree_path 为空")
		s.recordDispatchErrorEvent(ctx, now, w, fmt.Sprintf("dispatch_precheck_failed: %v", err))
		return empty, err
	}
	promptMeta, err := s.executePMDispatchAgent(ctx, job.RequestID, t, w, strings.TrimSpace(opt.EntryPromptOverride))
	if err != nil {
		s.recordDispatchErrorEvent(ctx, now, w, fmt.Sprintf("pm_agent_failed: %v", err))
		return empty, err
	}
	entryPrompt := strings.TrimSpace(promptMeta.EntryPrompt)
	s.recordDispatchEvent(ctx, now, w, "dispatch_requested", fmt.Sprintf("template=%s request_id=%s", strings.TrimSpace(promptMeta.TemplatePath), strings.TrimSpace(job.RequestID)))

	out := contracts.PMDispatchJobResult{
		Schema: contracts.PMDispatchJobResultSchemaV1,
	}

	s.recordDispatchEvent(ctx, now, w, "worker_loop_start", "开始同步 worker loop")
	loopResult, lerr := s.executeWorkerLoop(ctx, t, w, entryPrompt)
	if lerr != nil {
		s.recordDispatchErrorEvent(ctx, now, w, fmt.Sprintf("worker_loop_failed: %v", lerr))
		return empty, lerr
	}
	out.InjectedCmd = strings.TrimSpace(loopResult.InjectedCmd)
	out.WorkerLoopStages = loopResult.Stages
	out.WorkerLoopNextAction = strings.TrimSpace(loopResult.LastNextAction)
	s.recordDispatchEvent(ctx, now, w, "worker_loop_done", fmt.Sprintf("worker loop 完成 stages=%d next_action=%s", loopResult.Stages, strings.TrimSpace(loopResult.LastNextAction)))
	return out, nil
}

func (s *Service) recordDispatchEvent(ctx context.Context, ts time.Time, w contracts.Worker, typ, note string) {
	_ = s.worker.AppendWorkerTaskEvent(ctx, w.ID, typ, note, map[string]any{
		"ticket_id": w.TicketID,
	}, ts)
}

func (s *Service) recordDispatchErrorEvent(ctx context.Context, ts time.Time, w contracts.Worker, note string) {
	_ = s.worker.AppendWorkerTaskEvent(ctx, w.ID, "dispatch_error", strings.TrimSpace(note), map[string]any{
		"ticket_id": w.TicketID,
	}, ts)
}
