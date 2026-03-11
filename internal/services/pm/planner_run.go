package pm

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"dalek/internal/agent/progresstimeout"
	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
	"dalek/internal/repo"

	"gorm.io/gorm"
)

const defaultPlannerRunPrompt = "你是 dalek 的 PM planner agent。请基于当前项目状态做调度决策，并通过 dalek CLI 执行必要动作（如创建 ticket、start ticket、处理 merge/inbox）。"

type PlannerRunOptions struct {
	RunnerID string
	Prompt   string
}

func (s *Service) RunPlannerJob(ctx context.Context, taskRunID uint, opt PlannerRunOptions) error {
	if _, _, err := s.require(); err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if taskRunID == 0 {
		return fmt.Errorf("task_run_id 不能为空")
	}

	runnerID := strings.TrimSpace(opt.RunnerID)
	if runnerID == "" {
		runnerID = fmt.Sprintf("pm_planner_%d", taskRunID)
	}
	prompt := strings.TrimSpace(opt.Prompt)
	if prompt == "" {
		prompt = defaultPlannerRunPrompt
	}
	if err := s.completePlannerRunSuccess(ctx, taskRunID, runnerID, prompt); err != nil {
		failMsg := strings.TrimSpace(err.Error())
		if failMsg == "" {
			failMsg = "planner run failed"
		}
		failCtx := context.WithoutCancel(ctx)
		var ferr error
		switch {
		case errors.Is(err, context.Canceled):
			ferr = s.completePlannerRunCanceled(failCtx, taskRunID, "planner_canceled", failMsg)
		case errors.Is(err, context.DeadlineExceeded):
			ferr = s.completePlannerRunFailed(failCtx, taskRunID, "planner_timeout", failMsg)
		default:
			ferr = s.completePlannerRunFailed(failCtx, taskRunID, "planner_failed", failMsg)
		}
		if ferr != nil {
			return fmt.Errorf("planner run failed: %w (mark failed also failed: %v)", err, ferr)
		}
		return err
	}
	return nil
}

func (s *Service) completePlannerRunSuccess(ctx context.Context, taskRunID uint, runnerID string, prompt string) error {
	p, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := s.plannerRunTimeout()
	runCtx, watchdog := progresstimeout.New(ctx, timeout)
	defer watchdog.Stop()

	startedAt := time.Now()
	lease := watchdog.CurrentDeadline()

	var requestID string
	if err := db.WithContext(runCtx).Transaction(func(tx *gorm.DB) error {
		taskRuntime, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		run, rerr := taskRuntime.FindRunByID(runCtx, taskRunID)
		if rerr != nil {
			return rerr
		}
		if run == nil {
			return fmt.Errorf("planner task run 不存在: run_id=%d", taskRunID)
		}
		if run.OwnerType != contracts.TaskOwnerPM || run.TaskType != contracts.TaskTypePMPlannerRun {
			return fmt.Errorf("task run 类型不匹配: run_id=%d owner=%s type=%s", taskRunID, run.OwnerType, run.TaskType)
		}
		requestID = strings.TrimSpace(run.RequestID)
		if err := taskRuntime.MarkRunRunning(runCtx, taskRunID, runnerID, lease, startedAt, true); err != nil {
			return err
		}
		return taskRuntime.AppendEvent(runCtx, contracts.TaskEventInput{
			TaskRunID: taskRunID,
			EventType: "task_started",
			FromState: map[string]any{
				"orchestration_state": contracts.TaskPending,
			},
			ToState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
				"runner_id":           runnerID,
			},
			Note:      "pm planner run started",
			CreatedAt: startedAt,
		})
	}); err != nil {
		return err
	}

	cfg := p.Config.WithDefaults()
	agentCfg := repo.AgentConfigFromExecConfig(cfg.PMAgent)
	if _, err := agentprovider.NewFromConfig(agentCfg); err != nil {
		return fmt.Errorf("pm_agent 配置非法: %w", err)
	}
	taskRuntime, err := s.taskRuntime()
	if err != nil {
		return err
	}
	workDir := strings.TrimSpace(p.RepoRoot)
	if workDir == "" {
		return fmt.Errorf("planner repo_root 为空")
	}
	env := map[string]string{
		envProjectKey:       strings.TrimSpace(p.Key),
		envRepoRoot:         strings.TrimSpace(p.RepoRoot),
		envDBPath:           strings.TrimSpace(p.DBPath()),
		envPlannerRunID:     strconv.FormatUint(uint64(taskRunID), 10),
		envPlannerRunnerID:  strings.TrimSpace(runnerID),
		envPlannerPromptTpl: plannerPromptID,
		dispatchDepthEnvKey: "0",
	}
	if requestID != "" {
		env[envPlannerRequest] = requestID
	}
	touchPlannerProgress := func() {
		lease := watchdog.Touch()
		if lease == nil {
			return
		}
		leaseCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = taskRuntime.RenewLease(leaseCtx, taskRunID, runnerID, lease)
	}

	onEvent := func(ev sdkrunner.Event) {
		touchPlannerProgress()
		note := strings.TrimSpace(ev.Text)
		if note == "" {
			note = strings.TrimSpace(ev.Type)
		}
		if note == "" {
			note = "(empty)"
		}
		_ = taskRuntime.AppendEvent(context.Background(), contracts.TaskEventInput{
			TaskRunID: taskRunID,
			EventType: "task_stream",
			Note:      note,
			Payload: map[string]any{
				"type":       strings.TrimSpace(ev.Type),
				"text":       strings.TrimSpace(ev.Text),
				"raw_json":   strings.TrimSpace(ev.RawJSON),
				"session_id": strings.TrimSpace(ev.SessionID),
			},
			CreatedAt: time.Now(),
		})
	}

	touchPlannerProgress()
	res, runErr := s.taskRunner().Run(runCtx, sdkrunner.Request{
		AgentConfig: agentCfg,
		Prompt:      strings.TrimSpace(prompt),
		WorkDir:     workDir,
		Env:         env,
	}, onEvent)
	if runErr != nil {
		if watchdog.TimedOut() {
			return watchdog.TimeoutError("planner run")
		}
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("planner run timed out after %s: %w", timeout, context.DeadlineExceeded)
		}
		if errors.Is(runErr, context.Canceled) || errors.Is(runCtx.Err(), context.Canceled) {
			return fmt.Errorf("planner run canceled: %w", context.Canceled)
		}
		return runErr
	}

	finishedAt := time.Now()
	parsedOps, parseErr := parsePlannerPMOpsFromResult(res, taskRunID, requestID)
	if parseErr != nil {
		s.slog().Warn("planner pmops parse failed; fallback to empty ops",
			"task_run_id", taskRunID,
			"error", parseErr,
		)
		parsedOps = nil
	}
	pmopsSummary, pmopsErr := s.executePlannerPMOps(context.WithoutCancel(ctx), taskRunID, parsedOps, finishedAt)
	if pmopsErr != nil {
		return pmopsErr
	}
	pmopsPayload := map[string]any{
		"parsed":           pmopsSummary.Parsed,
		"planned":          pmopsSummary.Planned,
		"succeeded":        pmopsSummary.Succeeded,
		"failed":           pmopsSummary.Failed,
		"superseded":       pmopsSummary.Superseded,
		"completed_ops":    pmopsSummary.CompletedOps,
		"remaining_ops":    pmopsSummary.RemainingOps,
		"failure_context":  pmopsSummary.FailureContext,
		"checkpoint_id":    pmopsSummary.CheckpointID,
		"checkpoint_rev":   pmopsSummary.CheckpointRev,
		"checkpoint_runid": taskRunID,
	}
	if parseErr != nil {
		pmopsPayload["parse_error"] = strings.TrimSpace(parseErr.Error())
	}
	resultPayload := marshalJSON(map[string]any{
		"runner_id":   strings.TrimSpace(runnerID),
		"request_id":  requestID,
		"mode":        "agent",
		"provider":    strings.TrimSpace(res.Provider),
		"output_mode": strings.TrimSpace(res.OutputMode),
		"text":        strings.TrimSpace(res.Text),
		"session_id":  strings.TrimSpace(res.SessionID),
		"stdout":      strings.TrimSpace(res.Stdout),
		"stderr":      strings.TrimSpace(res.Stderr),
		"events":      res.Events,
		"pmops":       pmopsPayload,
	})
	finishCtx := context.WithoutCancel(ctx)
	return db.WithContext(finishCtx).Transaction(func(tx *gorm.DB) error {
		taskRuntime, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		pmState, serr := s.loadPMStateForUpdateTx(finishCtx, tx)
		if serr != nil {
			return serr
		}
		if plannerRunMatchesState(pmState, taskRunID) {
			s.clearPlannerRun(pmState, finishedAt)
			if err := s.persistPlannerStateTx(finishCtx, tx, pmState, finishedAt); err != nil {
				return err
			}
		}
		if err := taskRuntime.MarkRunSucceeded(finishCtx, taskRunID, resultPayload, finishedAt); err != nil {
			return err
		}
		return taskRuntime.AppendEvent(finishCtx, contracts.TaskEventInput{
			TaskRunID: taskRunID,
			EventType: "task_succeeded",
			FromState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
			},
			ToState: map[string]any{
				"orchestration_state": contracts.TaskSucceeded,
			},
			Note:      "pm planner run completed",
			CreatedAt: finishedAt,
			Payload: map[string]any{
				"provider":   strings.TrimSpace(res.Provider),
				"session_id": strings.TrimSpace(res.SessionID),
			},
		})
	})
}

func (s *Service) completePlannerRunFailed(ctx context.Context, taskRunID uint, errorCode string, errMsg string) error {
	return s.completePlannerRunTerminal(ctx, taskRunID, plannerTerminalFailure{
		state:     contracts.TaskFailed,
		errorCode: errorCode,
		errorMsg:  errMsg,
	})
}

func (s *Service) completePlannerRunCanceled(ctx context.Context, taskRunID uint, errorCode string, errMsg string) error {
	return s.completePlannerRunTerminal(ctx, taskRunID, plannerTerminalFailure{
		state:     contracts.TaskCanceled,
		errorCode: errorCode,
		errorMsg:  errMsg,
	})
}

type plannerTerminalFailure struct {
	state     contracts.TaskOrchestrationState
	errorCode string
	errorMsg  string
}

func (s *Service) completePlannerRunTerminal(ctx context.Context, taskRunID uint, terminal plannerTerminalFailure) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	terminal.errorCode = strings.TrimSpace(terminal.errorCode)
	if terminal.errorCode == "" {
		switch terminal.state {
		case contracts.TaskCanceled:
			terminal.errorCode = "planner_canceled"
		default:
			terminal.errorCode = "planner_failed"
		}
	}
	terminal.errorMsg = strings.TrimSpace(terminal.errorMsg)
	if terminal.errorMsg == "" {
		switch terminal.state {
		case contracts.TaskCanceled:
			terminal.errorMsg = "planner run canceled"
		default:
			terminal.errorMsg = "planner run failed"
		}
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		taskRuntime, terr := s.taskRuntimeForDB(tx)
		if terr != nil {
			return terr
		}
		pmState, serr := s.loadPMStateForUpdateTx(ctx, tx)
		if serr != nil {
			return serr
		}
		if plannerRunMatchesState(pmState, taskRunID) {
			s.failPlannerRun(pmState, now, terminal.errorMsg)
			if err := s.persistPlannerStateTx(ctx, tx, pmState, now); err != nil {
				return err
			}
		}
		eventType := "task_failed"
		note := "pm planner run failed"
		switch terminal.state {
		case contracts.TaskCanceled:
			if err := taskRuntime.MarkRunCanceled(ctx, taskRunID, terminal.errorCode, terminal.errorMsg, now); err != nil {
				return err
			}
			eventType = "task_canceled"
			note = "pm planner run canceled"
		default:
			if err := taskRuntime.MarkRunFailed(ctx, taskRunID, terminal.errorCode, terminal.errorMsg, now); err != nil {
				return err
			}
		}
		return taskRuntime.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: taskRunID,
			EventType: eventType,
			FromState: map[string]any{
				"orchestration_state": contracts.TaskRunning,
			},
			ToState: map[string]any{
				"orchestration_state": terminal.state,
				"error_code":          terminal.errorCode,
				"error_message":       terminal.errorMsg,
			},
			Note:      note,
			CreatedAt: now,
		})
	})
}

func plannerRunMatchesState(st *contracts.PMState, taskRunID uint) bool {
	if st == nil || taskRunID == 0 {
		return false
	}
	return st.PlannerActiveTaskRunID != nil && *st.PlannerActiveTaskRunID == taskRunID
}

func (s *Service) loadPMStateForUpdateTx(ctx context.Context, tx *gorm.DB) (*contracts.PMState, error) {
	if tx == nil {
		return nil, fmt.Errorf("db 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var st contracts.PMState
	err := tx.WithContext(ctx).Order("id asc").First(&st).Error
	if err == nil {
		return &st, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	st = contracts.PMState{
		AutopilotEnabled:       false,
		MaxRunningWorkers:      3,
		PlannerDirty:           false,
		PlannerWakeVersion:     0,
		PlannerActiveTaskRunID: nil,
		PlannerCooldownUntil:   nil,
		PlannerLastError:       "",
		PlannerLastRunAt:       nil,
	}
	if err := tx.WithContext(ctx).Create(&st).Error; err != nil {
		return nil, err
	}
	return &st, nil
}

func (s *Service) persistPlannerStateTx(ctx context.Context, tx *gorm.DB, st *contracts.PMState, now time.Time) error {
	if tx == nil {
		return fmt.Errorf("db 不能为空")
	}
	if st == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return tx.WithContext(ctx).Model(&contracts.PMState{}).
		Where("id = ?", st.ID).
		Updates(map[string]any{
			"planner_dirty":              st.PlannerDirty,
			"planner_active_task_run_id": st.PlannerActiveTaskRunID,
			"planner_cooldown_until":     st.PlannerCooldownUntil,
			"planner_last_error":         strings.TrimSpace(st.PlannerLastError),
			"planner_last_run_at":        st.PlannerLastRunAt,
			"updated_at":                 now,
		}).Error
}
