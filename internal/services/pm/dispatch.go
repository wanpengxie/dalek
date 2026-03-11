package pm

import (
	"context"
	"fmt"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/repo"
)

type DispatchResult struct {
	TicketID  uint
	WorkerID  uint
	TaskRunID uint

	WorkerCommand string
	InjectedCmd   string
}

type DispatchOptions struct {
	// EntryPrompt 非空时，覆盖本轮 dispatch prompt 的 entry_prompt（用于补充本轮任务意图）。
	EntryPrompt string
	// AutoStart=nil/true 时，dispatch 发现 worker 未就绪会先自动 start。
	AutoStart *bool
	// BaseBranch 非空时，auto-start worker 会优先从该基线创建/修复 worktree。
	BaseBranch string
}

type DispatchSubmitOptions struct {
	RequestID string
	// AutoStart=nil/true 时，dispatch 发现 worker 未就绪会先自动 start。
	AutoStart *bool
	// BaseBranch 非空时，auto-start worker 会优先从该基线创建/修复 worktree。
	BaseBranch string
}

type DispatchSubmission struct {
	JobID      uint
	TaskRunID  uint
	RequestID  string
	TicketID   uint
	WorkerID   uint
	JobStatus  contracts.PMDispatchJobStatus
	Dispatched bool
}

type DispatchRunOptions struct {
	RunnerID    string
	EntryPrompt string
}

// DispatchTicket 是 PM 视角的 dispatch：执行一次 PM dispatch agent，并记录结果。
func (s *Service) DispatchTicket(ctx context.Context, ticketID uint) (DispatchResult, error) {
	return s.DispatchTicketWithOptions(ctx, ticketID, DispatchOptions{})
}

func (s *Service) SubmitDispatchTicket(ctx context.Context, ticketID uint, opt DispatchSubmitOptions) (DispatchSubmission, error) {
	autoStart := dispatchAutoStartEnabled(opt.AutoStart)
	t, w, err := s.prepareDispatchSubmission(ctx, ticketID, autoStart)
	if err != nil {
		return DispatchSubmission{}, err
	}
	workerID := uint(0)
	if w != nil {
		workerID = w.ID
	}
	job, err := s.enqueuePMDispatchJob(ctx, t.ID, workerID, strings.TrimSpace(opt.RequestID), newDispatchTaskRequestPayload(t.ID, workerID, autoStart, strings.TrimSpace(opt.BaseBranch)))
	if err != nil {
		return DispatchSubmission{}, err
	}
	out := DispatchSubmission{
		JobID:      job.ID,
		TaskRunID:  job.TaskRunID,
		RequestID:  strings.TrimSpace(job.RequestID),
		TicketID:   job.TicketID,
		WorkerID:   job.WorkerID,
		JobStatus:  job.Status,
		Dispatched: job.Status == contracts.PMDispatchPending || job.Status == contracts.PMDispatchRunning,
	}
	if out.TaskRunID == 0 && strings.TrimSpace(out.RequestID) != "" {
		if rt, terr := s.taskRuntime(); terr == nil {
			if run, rerr := rt.FindRunByRequestID(ctx, out.RequestID); rerr == nil && run != nil {
				out.TaskRunID = run.ID
			}
		}
	}
	return out, nil
}

func (s *Service) RunDispatchJob(ctx context.Context, jobID uint, opt DispatchRunOptions) error {
	return s.runPMDispatchJob(ctx, jobID, strings.TrimSpace(opt.RunnerID), runPMDispatchJobOptions{
		EntryPromptOverride: strings.TrimSpace(opt.EntryPrompt),
	})
}

// DispatchTicketWithOptions 是带可选行为参数的 dispatch 入口。
func (s *Service) DispatchTicketWithOptions(ctx context.Context, ticketID uint, opt DispatchOptions) (DispatchResult, error) {
	p, _, err := s.require()
	if err != nil {
		return DispatchResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	submission, err := s.SubmitDispatchTicket(ctx, ticketID, DispatchSubmitOptions{
		AutoStart:  opt.AutoStart,
		BaseBranch: strings.TrimSpace(opt.BaseBranch),
	})
	if err != nil {
		return DispatchResult{}, err
	}

	runnerID := newPMDispatchRunnerID()
	runErr := s.RunDispatchJob(ctx, submission.JobID, DispatchRunOptions{
		RunnerID:    runnerID,
		EntryPrompt: strings.TrimSpace(opt.EntryPrompt),
	})

	// worker loop 同步执行可能运行数小时，不设内部超时。
	// 调用方若需要超时控制，应通过 ctx 传入。
	finalJob, err := s.waitPMDispatchJob(ctx, submission.JobID, defaultDispatchPollInterval)
	if err != nil {
		if runErr != nil {
			return DispatchResult{}, fmt.Errorf("dispatch runner 失败: %v（wait: %w）", runErr, err)
		}
		return DispatchResult{}, err
	}
	if finalJob.Status != contracts.PMDispatchSucceeded {
		if strings.TrimSpace(finalJob.Error) != "" {
			return DispatchResult{}, fmt.Errorf("dispatch 失败: %s", strings.TrimSpace(finalJob.Error))
		}
		if runErr != nil {
			return DispatchResult{}, runErr
		}
		return DispatchResult{}, fmt.Errorf("dispatch job 失败: status=%s", finalJob.Status)
	}

	payload := finalJob.ResultJSON
	cfg := p.Config.WithDefaults()

	return DispatchResult{
		TicketID:      finalJob.TicketID,
		WorkerID:      finalJob.WorkerID,
		TaskRunID:     finalJob.TaskRunID,
		WorkerCommand: workerCommandHint(cfg),
		InjectedCmd:   strings.TrimSpace(payload.InjectedCmd),
	}, nil
}

func (s *Service) prepareDispatchSubmission(ctx context.Context, ticketID uint, autoStart bool) (contracts.Ticket, *contracts.Worker, error) {
	t, w, err := s.inspectDispatchTarget(ctx, ticketID)
	if err != nil {
		return contracts.Ticket{}, nil, err
	}
	if autoStart {
		return t, w, nil
	}
	if w == nil {
		return contracts.Ticket{}, nil, s.workerMissingSessionError()
	}
	ready, rerr := s.workerDispatchReady(ctx, w)
	if rerr != nil {
		return contracts.Ticket{}, nil, rerr
	}
	if !ready {
		return contracts.Ticket{}, nil, s.workerMissingSessionError()
	}
	if w.Status != contracts.WorkerRunning {
		return contracts.Ticket{}, nil, s.workerNotRunningError(w)
	}
	return t, w, nil
}

func (s *Service) inspectDispatchTarget(ctx context.Context, ticketID uint) (contracts.Ticket, *contracts.Worker, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.Ticket{}, nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var t contracts.Ticket
	if err := db.WithContext(ctx).First(&t, ticketID).Error; err != nil {
		return contracts.Ticket{}, nil, err
	}
	if !fsm.CanQueueRunTicket(t.WorkflowStatus) {
		switch contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus) {
		case contracts.TicketArchived:
			return contracts.Ticket{}, nil, fmt.Errorf("ticket 已归档，不能派发（dispatch）：t%d", ticketID)
		default:
			return contracts.Ticket{}, nil, fmt.Errorf("ticket 已完成，不能派发（dispatch）：t%d", ticketID)
		}
	}
	w, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return contracts.Ticket{}, nil, err
	}
	return t, w, nil
}

func (s *Service) resolveDispatchTarget(ctx context.Context, ticketID uint, autoStart bool, baseBranch string) (contracts.Ticket, *contracts.Worker, error) {
	t, w, err := s.inspectDispatchTarget(ctx, ticketID)
	if err != nil {
		return contracts.Ticket{}, nil, err
	}
	ready, rerr := s.workerDispatchReady(ctx, w)
	if rerr != nil {
		return contracts.Ticket{}, nil, rerr
	}
	if autoStart && (w == nil || !ready) {
		w, err = s.ensureDispatchWorkerStarted(ctx, ticketID, baseBranch)
		if err != nil {
			return contracts.Ticket{}, nil, err
		}
	}
	if w == nil {
		return contracts.Ticket{}, nil, s.workerMissingSessionError()
	}
	if w.Status == contracts.WorkerCreating {
		ready, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
		if waitErr != nil {
			return contracts.Ticket{}, nil, waitErr
		}
		w = ready
	}
	if autoStart && w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped {
		w, err = s.ensureDispatchWorkerStarted(ctx, ticketID, baseBranch)
		if err != nil {
			return contracts.Ticket{}, nil, err
		}
		if w != nil && w.Status == contracts.WorkerCreating {
			ready, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
			if waitErr != nil {
				return contracts.Ticket{}, nil, waitErr
			}
			w = ready
		}
	}
	ready, rerr = s.workerDispatchReady(ctx, w)
	if rerr != nil {
		return contracts.Ticket{}, nil, rerr
	}
	if !ready {
		if autoStart {
			w, err = s.ensureDispatchWorkerStarted(ctx, ticketID, baseBranch)
			if err != nil {
				return contracts.Ticket{}, nil, err
			}
			if w != nil && w.Status == contracts.WorkerCreating {
				readyWorker, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
				if waitErr != nil {
					return contracts.Ticket{}, nil, waitErr
				}
				w = readyWorker
			}
			ready, rerr = s.workerDispatchReady(ctx, w)
			if rerr != nil {
				return contracts.Ticket{}, nil, rerr
			}
			if !ready {
				return contracts.Ticket{}, nil, s.workerMissingSessionError()
			}
		} else {
			return contracts.Ticket{}, nil, s.workerMissingSessionError()
		}
	}
	if w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped {
		return contracts.Ticket{}, nil, s.workerNotRunningError(w)
	}
	return t, w, nil
}

func dispatchAutoStartEnabled(v *bool) bool {
	if v == nil {
		return true
	}
	return *v
}

func (s *Service) ensureDispatchWorkerStarted(ctx context.Context, ticketID uint, baseBranch string) (*contracts.Worker, error) {
	w, err := s.StartTicketWithOptions(ctx, ticketID, StartOptions{BaseBranch: strings.TrimSpace(baseBranch)})
	if err != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", err)
	}
	if ready, rerr := s.workerDispatchReady(ctx, w); rerr != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", rerr)
	} else if ready {
		return w, nil
	}
	w, err = s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", err)
	}
	if ready, rerr := s.workerDispatchReady(ctx, w); rerr != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", rerr)
	} else if !ready {
		if w == nil {
			return nil, fmt.Errorf("auto-start 失败: 启动后仍无可用 worker/runtime（t%d）", ticketID)
		}
		return nil, fmt.Errorf("auto-start 失败: 启动后仍无可用 worker/runtime（t%d w%d status=%s）", ticketID, w.ID, w.Status)
	}
	return w, nil
}

func workerCommandHint(cfg repo.Config) string {
	agent := cfg.WorkerAgent
	if strings.TrimSpace(agent.Provider) == "" {
		return ""
	}
	model := strings.TrimSpace(agent.Model)
	if model == "" {
		return strings.TrimSpace(agent.Provider)
	}
	return strings.TrimSpace(agent.Provider) + " (" + model + ")"
}
