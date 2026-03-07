package pm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
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

	stopRenew := s.startLeaseRenewal(ctx, job, contracts.Worker{ID: job.WorkerID}, runnerID, leaseTTL)
	defer close(stopRenew)

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

func (s *Service) executePMDispatchJob(ctx context.Context, job contracts.PMDispatchJob, t contracts.Ticket, w contracts.Worker, opt runPMDispatchJobOptions) (contracts.PMDispatchJobResult, error) {
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
	promptMeta, err := s.runPMDispatchAgent(ctx, job.RequestID, t, w, strings.TrimSpace(opt.EntryPromptOverride))
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
	out.InjectedCmd = strings.TrimSpace(loopResult.InjectedCmd)
	out.WorkerLoopStages = loopResult.Stages
	if lerr != nil {
		var missingErr *workerLoopMissingReportError
		if errors.As(lerr, &missingErr) {
			handledAt := time.Now()
			s.recordDispatchErrorEvent(ctx, handledAt, w, fmt.Sprintf("worker_loop_missing_report: %v", missingErr))
			if err := s.applyMissingWorkerReportWaitUser(ctx, t.ID, w, loopResult, "pm.dispatch_runner.missing_report"); err != nil {
				return empty, fmt.Errorf("worker loop 缺少 report，自动 blocked 失败: %w", err)
			}
			out.WorkerLoopNextAction = string(contracts.NextWaitUser)
			s.recordDispatchEvent(ctx, handledAt, w, "worker_loop_missing_report_blocked", missingErr.Error())
			s.recordDispatchEvent(ctx, handledAt, w, "worker_loop_done", fmt.Sprintf("worker loop 完成 stages=%d next_action=%s", loopResult.Stages, out.WorkerLoopNextAction))
			return out, nil
		}
		s.recordDispatchErrorEvent(ctx, now, w, fmt.Sprintf("worker_loop_failed: %v", lerr))
		return empty, lerr
	}
	out.WorkerLoopNextAction = strings.TrimSpace(loopResult.LastNextAction)
	s.recordDispatchEvent(ctx, now, w, "worker_loop_done", fmt.Sprintf("worker loop 完成 stages=%d next_action=%s", loopResult.Stages, strings.TrimSpace(loopResult.LastNextAction)))
	return out, nil
}

// startLeaseRenewal 启动后台 goroutine 定期续租 dispatch job 的 lease。
// 返回 stop channel，调用方 close(stop) 即可停止续租。
// 续租失败时会记录日志和事件；连续失败达到阈值后升级为 Error 级别。
func (s *Service) startLeaseRenewal(ctx context.Context, job contracts.PMDispatchJob, w contracts.Worker, runnerID string, leaseTTL time.Duration) chan struct{} {
	stopRenew := make(chan struct{})
	go func() {
		ticker := time.NewTicker(dispatchLeaseRenewInterval)
		defer ticker.Stop()
		var consecutiveFailures uint
		for {
			select {
			case <-stopRenew:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				err := s.renewPMDispatchJobLease(context.Background(), job.ID, runnerID, leaseTTL)
				if err == nil {
					if consecutiveFailures > 0 {
						s.slog().Info("pm dispatch lease renewal recovered",
							"job_id", job.ID,
							"runner_id", runnerID,
							"previous_failures", consecutiveFailures,
						)
					}
					consecutiveFailures = 0
					continue
				}
				consecutiveFailures++
				if consecutiveFailures >= leaseRenewalEscalateThreshold {
					s.slog().Error("pm dispatch lease renewal failed (escalated)",
						"job_id", job.ID,
						"runner_id", runnerID,
						"consecutive_failures", consecutiveFailures,
						"error", err,
					)
				} else {
					s.slog().Warn("pm dispatch lease renewal failed",
						"job_id", job.ID,
						"runner_id", runnerID,
						"consecutive_failures", consecutiveFailures,
						"error", err,
					)
				}
				_ = s.worker.AppendWorkerTaskEvent(context.Background(), w.ID,
					"lease_renewal_failed",
					fmt.Sprintf("dispatch job %d lease renewal failed (attempt %d): %v", job.ID, consecutiveFailures, err),
					map[string]any{
						"job_id":               job.ID,
						"runner_id":            runnerID,
						"consecutive_failures": consecutiveFailures,
						"ticket_id":            job.TicketID,
					},
					time.Now(),
				)
			}
		}
	}()
	return stopRenew
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
