package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/repo"

	"gorm.io/gorm"
)

type DispatchResult struct {
	TicketID  uint
	WorkerID  uint
	TaskRunID uint

	TmuxSocket  string
	TmuxSession string

	WorkerCommand string
	InjectedCmd   string
}

type DispatchOptions struct {
	// EntryPrompt 非空时，覆盖本轮 dispatch prompt 的 entry_prompt（用于补充本轮任务意图）。
	EntryPrompt string
	// AutoStart=nil/true 时，dispatch 发现 worker 未就绪会先自动 start。
	AutoStart *bool
}

type DispatchSubmitOptions struct {
	RequestID string
	// AutoStart=nil/true 时，dispatch 发现 worker 未就绪会先自动 start。
	AutoStart *bool
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
	t, w, err := s.resolveDispatchTarget(ctx, ticketID, dispatchAutoStartEnabled(opt.AutoStart))
	if err != nil {
		return DispatchSubmission{}, err
	}
	job, err := s.enqueuePMDispatchJob(ctx, t.ID, w.ID, strings.TrimSpace(opt.RequestID))
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
	p, db, err := s.require()
	if err != nil {
		return DispatchResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	submission, err := s.SubmitDispatchTicket(ctx, ticketID, DispatchSubmitOptions{
		AutoStart: opt.AutoStart,
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

	payload := contracts.PMDispatchJobResult{}
	if strings.TrimSpace(finalJob.ResultJSON) != "" {
		_ = json.Unmarshal([]byte(finalJob.ResultJSON), &payload)
	}

	updatedWorker, err := s.worker.WorkerByID(ctx, finalJob.WorkerID)
	if err != nil {
		return DispatchResult{}, err
	}

	// dispatch 成功意味着进入 active（仅 PM reducer 写 workflow）。
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur contracts.Ticket
		if err := tx.WithContext(ctx).First(&cur, ticketID).Error; err != nil {
			return err
		}
		from := normalizeTicketWorkflowStatus(cur.WorkflowStatus)
		if !fsm.ShouldPromoteOnDispatchClaim(from) {
			return nil
		}
		now := time.Now()
		if err := tx.WithContext(ctx).Model(&contracts.Ticket{}).
			Where("id = ?", ticketID).
			Updates(map[string]any{
				"workflow_status": contracts.TicketActive,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		return s.appendTicketWorkflowEventTx(ctx, tx, ticketID, from, contracts.TicketActive, "pm.dispatch", "dispatch 成功推进到 active", map[string]any{
			"worker_id":   finalJob.WorkerID,
			"request_id":  strings.TrimSpace(finalJob.RequestID),
			"dispatch_id": finalJob.ID,
		}, now)
	}); err != nil {
		return DispatchResult{}, fmt.Errorf("更新 ticket workflow 失败（t%d）：%w", ticketID, err)
	}
	cfg := p.Config.WithDefaults()

	return DispatchResult{
		TicketID:      finalJob.TicketID,
		WorkerID:      finalJob.WorkerID,
		TaskRunID:     finalJob.TaskRunID,
		TmuxSocket:    strings.TrimSpace(updatedWorker.TmuxSocket),
		TmuxSession:   strings.TrimSpace(updatedWorker.TmuxSession),
		WorkerCommand: workerCommandHint(cfg),
		InjectedCmd:   strings.TrimSpace(payload.InjectedCmd),
	}, nil
}

func (s *Service) resolveDispatchTarget(ctx context.Context, ticketID uint, autoStart bool) (contracts.Ticket, *contracts.Worker, error) {
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
	if !fsm.CanDispatchTicket(t.WorkflowStatus) {
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
	if autoStart && (w == nil || strings.TrimSpace(w.TmuxSession) == "") {
		w, err = s.ensureDispatchWorkerStarted(ctx, ticketID)
		if err != nil {
			return contracts.Ticket{}, nil, err
		}
	}
	if w == nil || strings.TrimSpace(w.TmuxSession) == "" {
		return contracts.Ticket{}, nil, s.workerMissingSessionError()
	}
	if w.Status == contracts.WorkerCreating {
		ready, waitErr := s.waitWorkerReadyForDispatch(ctx, ticketID, w)
		if waitErr != nil {
			return contracts.Ticket{}, nil, waitErr
		}
		w = ready
	}
	if autoStart && w.Status != contracts.WorkerRunning {
		w, err = s.ensureDispatchWorkerStarted(ctx, ticketID)
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
	if w.Status != contracts.WorkerRunning {
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

func (s *Service) ensureDispatchWorkerStarted(ctx context.Context, ticketID uint) (*contracts.Worker, error) {
	w, err := s.StartTicketWithOptions(ctx, ticketID, StartOptions{})
	if err != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", err)
	}
	if w != nil && strings.TrimSpace(w.TmuxSession) != "" {
		return w, nil
	}
	w, err = s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("auto-start 失败: %w", err)
	}
	if w == nil || strings.TrimSpace(w.TmuxSession) == "" {
		return nil, fmt.Errorf("auto-start 失败: 启动后仍无可用 worker/session（t%d）", ticketID)
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
