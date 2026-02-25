package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/agent/sdkrunner"
	tasksvc "dalek/internal/services/task"
	"dalek/internal/store"
)

func (p *Project) SubmitSubagentRun(ctx context.Context, opt SubagentSubmitOptions) (SubagentSubmission, error) {
	if p == nil || p.task == nil {
		return SubagentSubmission{}, fmt.Errorf("project task service 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	prompt := strings.TrimSpace(opt.Prompt)
	if prompt == "" {
		return SubagentSubmission{}, fmt.Errorf("prompt 不能为空")
	}
	settings, err := p.resolveSubagentAgentSettings(opt.Provider, opt.Model)
	if err != nil {
		return SubagentSubmission{}, err
	}

	requestID := strings.TrimSpace(opt.RequestID)
	if requestID == "" {
		requestID = newSubagentRequestID()
	}

	existingRun, err := p.task.FindRunByRequestID(ctx, requestID)
	if err != nil {
		return SubagentSubmission{}, err
	}
	if existingRun != nil {
		if existingRun.OwnerType != store.TaskOwnerSubagent || strings.TrimSpace(existingRun.TaskType) != store.TaskTypeSubagentRun {
			return SubagentSubmission{}, fmt.Errorf("request_id 已被其他任务占用: %s", requestID)
		}
		existingRec, serr := p.task.FindSubagentRunByTaskRunID(ctx, existingRun.ID)
		if serr != nil {
			return SubagentSubmission{}, serr
		}
		if existingRec != nil {
			return SubagentSubmission{
				Accepted:   true,
				TaskRunID:  existingRun.ID,
				RequestID:  requestID,
				Provider:   strings.TrimSpace(existingRec.Provider),
				Model:      strings.TrimSpace(existingRec.Model),
				RuntimeDir: strings.TrimSpace(existingRec.RuntimeDir),
			}, nil
		}
	}

	requestPayload, _ := json.Marshal(map[string]any{
		"provider": settings.Provider,
		"model":    settings.Model,
		"prompt":   prompt,
	})
	createdRun, err := p.task.CreateRun(ctx, tasksvc.CreateRunInput{
		OwnerType:          store.TaskOwnerSubagent,
		TaskType:           store.TaskTypeSubagentRun,
		ProjectKey:         strings.TrimSpace(p.Key()),
		SubjectType:        "project",
		SubjectID:          strings.TrimSpace(p.Key()),
		RequestID:          requestID,
		OrchestrationState: store.TaskPending,
		RequestPayloadJSON: strings.TrimSpace(string(requestPayload)),
	})
	if err != nil {
		return SubagentSubmission{}, err
	}
	if createdRun.OwnerType != store.TaskOwnerSubagent || strings.TrimSpace(createdRun.TaskType) != store.TaskTypeSubagentRun {
		return SubagentSubmission{}, fmt.Errorf("request_id 已被其他任务占用: %s", requestID)
	}

	runtimeDir := p.subagentRuntimeDir(createdRun.ID)
	rec, err := p.task.CreateSubagentRun(ctx, tasksvc.CreateSubagentRunInput{
		ProjectKey: strings.TrimSpace(p.Key()),
		TaskRunID:  createdRun.ID,
		RequestID:  requestID,
		Provider:   settings.Provider,
		Model:      settings.Model,
		Prompt:     prompt,
		CWD:        strings.TrimSpace(p.RepoRoot()),
		RuntimeDir: runtimeDir,
	})
	if err != nil {
		return SubagentSubmission{}, err
	}

	now := time.Now()
	_ = p.task.AppendEvent(ctx, tasksvc.TaskEventInput{
		TaskRunID: createdRun.ID,
		EventType: "task_enqueued",
		ToState: map[string]any{
			"orchestration_state": store.TaskPending,
		},
		Note:      "subagent run enqueued",
		CreatedAt: now,
	})
	_ = p.task.AppendRuntimeSample(ctx, tasksvc.RuntimeSampleInput{
		TaskRunID:  createdRun.ID,
		State:      store.TaskHealthIdle,
		NeedsUser:  false,
		Summary:    "subagent queued",
		Source:     "agent.subagent.submit",
		ObservedAt: now,
	})
	_ = p.task.AppendSemanticReport(ctx, tasksvc.SemanticReportInput{
		TaskRunID:  createdRun.ID,
		Phase:      store.TaskPhasePlanning,
		Milestone:  "subagent_enqueued",
		NextAction: "continue",
		Summary:    "subagent accepted",
		ReportedAt: now,
	})

	return SubagentSubmission{
		Accepted:   true,
		TaskRunID:  createdRun.ID,
		RequestID:  requestID,
		Provider:   strings.TrimSpace(rec.Provider),
		Model:      strings.TrimSpace(rec.Model),
		RuntimeDir: strings.TrimSpace(rec.RuntimeDir),
	}, nil
}

func (p *Project) RunSubagentJob(ctx context.Context, taskRunID uint, opt SubagentRunOptions) error {
	if p == nil || p.task == nil {
		return fmt.Errorf("project task service 为空")
	}
	if taskRunID == 0 {
		return fmt.Errorf("task_run_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runRec, err := p.task.FindRunByID(ctx, taskRunID)
	if err != nil {
		return err
	}
	if runRec == nil {
		return fmt.Errorf("task run 不存在: %d", taskRunID)
	}
	if runRec.OwnerType != store.TaskOwnerSubagent || strings.TrimSpace(runRec.TaskType) != store.TaskTypeSubagentRun {
		return fmt.Errorf("task run 不是 subagent 任务: %d", taskRunID)
	}
	subRec, err := p.task.FindSubagentRunByTaskRunID(ctx, taskRunID)
	if err != nil {
		return err
	}
	if subRec == nil {
		return fmt.Errorf("subagent run 不存在: task_run_id=%d", taskRunID)
	}

	runtimeDir := strings.TrimSpace(subRec.RuntimeDir)
	if runtimeDir == "" {
		runtimeDir = p.subagentRuntimeDir(taskRunID)
	}
	if err := os.MkdirAll(runtimeDir, 0o755); err != nil {
		return fmt.Errorf("创建 runtime 目录失败: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "prompt.txt"), []byte(strings.TrimSpace(subRec.Prompt)+"\n"), 0o644); err != nil {
		return fmt.Errorf("写入 prompt 失败: %w", err)
	}

	streamPath := filepath.Join(runtimeDir, "stream.log")
	sdkStreamPath := filepath.Join(runtimeDir, "sdk-stream.log")
	streamFile, err := os.OpenFile(streamPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开 stream.log 失败: %w", err)
	}
	defer func() { _ = streamFile.Close() }()
	sdkStreamFile, err := os.OpenFile(sdkStreamPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开 sdk-stream.log 失败: %w", err)
	}
	defer func() { _ = sdkStreamFile.Close() }()

	settings, err := p.resolveSubagentAgentSettings(subRec.Provider, subRec.Model)
	if err != nil {
		return err
	}
	workDir := strings.TrimSpace(subRec.CWD)
	if workDir == "" {
		workDir = strings.TrimSpace(p.RepoRoot())
	}
	runnerID := strings.TrimSpace(opt.RunnerID)
	if runnerID == "" {
		runnerID = "daemon_" + newSubagentRequestID()
	}

	now := time.Now()
	var lease *time.Time
	if deadline, ok := ctx.Deadline(); ok {
		deadlineCopy := deadline
		lease = &deadlineCopy
	}
	if err := p.task.MarkRunRunning(ctx, taskRunID, runnerID, lease, now, true); err != nil {
		return err
	}
	_ = p.task.AppendEvent(ctx, tasksvc.TaskEventInput{
		TaskRunID: taskRunID,
		EventType: "task_started",
		FromState: map[string]any{"orchestration_state": store.TaskPending},
		ToState: map[string]any{
			"orchestration_state": store.TaskRunning,
			"runner_id":           runnerID,
		},
		Note:      "subagent started",
		CreatedAt: now,
	})
	_ = p.task.AppendRuntimeSample(ctx, tasksvc.RuntimeSampleInput{
		TaskRunID:  taskRunID,
		State:      store.TaskHealthBusy,
		NeedsUser:  false,
		Summary:    "subagent running",
		Source:     "agent.subagent.run",
		ObservedAt: now,
	})
	_ = p.task.AppendSemanticReport(ctx, tasksvc.SemanticReportInput{
		TaskRunID:  taskRunID,
		Phase:      store.TaskPhaseImplementing,
		Milestone:  "subagent_started",
		NextAction: "continue",
		Summary:    "subagent started",
		ReportedAt: now,
	})

	_, _ = fmt.Fprintf(streamFile, "%s subagent start provider=%s model=%s\n", now.Local().Format(time.RFC3339), settings.Provider, settings.Model)
	_, _ = fmt.Fprintf(sdkStreamFile, "%s sdk start provider=%s model=%s\n", now.Local().Format(time.RFC3339), settings.Provider, settings.Model)

	onEvent := func(ev sdkrunner.Event) {
		ts := time.Now().Local().Format(time.RFC3339)
		note := strings.TrimSpace(ev.Text)
		if note == "" {
			note = strings.TrimSpace(ev.Type)
		}
		if note == "" {
			note = "(empty)"
		}
		_, _ = fmt.Fprintf(streamFile, "%s %s\n", ts, note)
		raw := strings.TrimSpace(ev.RawJSON)
		if raw == "" {
			rawBytes, _ := json.Marshal(map[string]any{
				"type":       strings.TrimSpace(ev.Type),
				"text":       strings.TrimSpace(ev.Text),
				"session_id": strings.TrimSpace(ev.SessionID),
			})
			raw = strings.TrimSpace(string(rawBytes))
		}
		_, _ = fmt.Fprintf(sdkStreamFile, "%s %s\n", ts, raw)
		_ = p.task.AppendEvent(context.Background(), tasksvc.TaskEventInput{
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

	res, runErr := sdkrunner.Run(ctx, sdkrunner.Request{
		Provider:        settings.Provider,
		Model:           settings.Model,
		ReasoningEffort: settings.ReasoningEffort,
		Command:         settings.Command,
		Prompt:          strings.TrimSpace(subRec.Prompt),
		WorkDir:         workDir,
		Env: map[string]string{
			"DALEK_PROJECT_KEY":          strings.TrimSpace(p.Key()),
			"DALEK_REPO_ROOT":            strings.TrimSpace(p.RepoRoot()),
			"DALEK_SUBAGENT_RUN_ID":      strconv.FormatUint(uint64(taskRunID), 10),
			"DALEK_SUBAGENT_REQUEST_ID":  strings.TrimSpace(subRec.RequestID),
			"DALEK_SUBAGENT_RUNTIME_DIR": runtimeDir,
		},
	}, onEvent)

	finishedAt := time.Now()
	status := "succeeded"
	errorCode := ""
	errorMsg := ""
	if runErr != nil {
		if isSubagentCanceled(runErr, ctx.Err()) {
			status = "canceled"
			errorCode = "agent_canceled"
			errorMsg = strings.TrimSpace(runErr.Error())
			if errorMsg == "" {
				errorMsg = "subagent canceled"
			}
			_ = p.task.MarkRunCanceled(context.Background(), taskRunID, errorCode, errorMsg, finishedAt)
			_ = p.task.AppendEvent(context.Background(), tasksvc.TaskEventInput{
				TaskRunID: taskRunID,
				EventType: "task_canceled",
				FromState: map[string]any{"orchestration_state": store.TaskRunning},
				ToState:   map[string]any{"orchestration_state": store.TaskCanceled},
				Note:      errorMsg,
				CreatedAt: finishedAt,
			})
			_ = p.task.AppendRuntimeSample(context.Background(), tasksvc.RuntimeSampleInput{
				TaskRunID:  taskRunID,
				State:      store.TaskHealthDead,
				NeedsUser:  true,
				Summary:    errorMsg,
				Source:     "agent.subagent.run",
				ObservedAt: finishedAt,
			})
			_ = p.task.AppendSemanticReport(context.Background(), tasksvc.SemanticReportInput{
				TaskRunID:  taskRunID,
				Phase:      store.TaskPhaseBlocked,
				Milestone:  "subagent_canceled",
				NextAction: "wait_user",
				Summary:    errorMsg,
				ReportedAt: finishedAt,
			})
		} else {
			status = "failed"
			errorCode = "agent_exit_failed"
			errorMsg = strings.TrimSpace(runErr.Error())
			if errorMsg == "" {
				errorMsg = "subagent execution failed"
			}
			_ = p.task.MarkRunFailed(context.Background(), taskRunID, errorCode, errorMsg, finishedAt)
			_ = p.task.AppendEvent(context.Background(), tasksvc.TaskEventInput{
				TaskRunID: taskRunID,
				EventType: "task_failed",
				FromState: map[string]any{"orchestration_state": store.TaskRunning},
				ToState: map[string]any{
					"orchestration_state": store.TaskFailed,
					"error_code":          errorCode,
				},
				Note:      errorMsg,
				CreatedAt: finishedAt,
			})
			_ = p.task.AppendRuntimeSample(context.Background(), tasksvc.RuntimeSampleInput{
				TaskRunID:  taskRunID,
				State:      store.TaskHealthStalled,
				NeedsUser:  true,
				Summary:    errorMsg,
				Source:     "agent.subagent.run",
				ObservedAt: finishedAt,
			})
			_ = p.task.AppendSemanticReport(context.Background(), tasksvc.SemanticReportInput{
				TaskRunID:  taskRunID,
				Phase:      store.TaskPhaseBlocked,
				Milestone:  "subagent_failed",
				NextAction: "wait_user",
				Summary:    errorMsg,
				ReportedAt: finishedAt,
			})
		}
	} else {
		resultJSON, _ := json.Marshal(map[string]any{
			"provider":      strings.TrimSpace(res.Provider),
			"output_mode":   strings.TrimSpace(res.OutputMode),
			"text":          strings.TrimSpace(res.Text),
			"session_id":    strings.TrimSpace(res.SessionID),
			"stdout":        strings.TrimSpace(res.Stdout),
			"stderr":        strings.TrimSpace(res.Stderr),
			"events":        res.Events,
			"runtime_dir":   runtimeDir,
			"request_id":    strings.TrimSpace(subRec.RequestID),
			"task_run_id":   taskRunID,
			"finished_at":   finishedAt.Format(time.RFC3339),
			"orchestration": store.TaskSucceeded,
		})
		_ = p.task.MarkRunSucceeded(context.Background(), taskRunID, strings.TrimSpace(string(resultJSON)), finishedAt)
		_ = p.task.AppendEvent(context.Background(), tasksvc.TaskEventInput{
			TaskRunID: taskRunID,
			EventType: "task_succeeded",
			FromState: map[string]any{"orchestration_state": store.TaskRunning},
			ToState:   map[string]any{"orchestration_state": store.TaskSucceeded},
			Note:      "subagent completed",
			CreatedAt: finishedAt,
		})
		_ = p.task.AppendRuntimeSample(context.Background(), tasksvc.RuntimeSampleInput{
			TaskRunID:  taskRunID,
			State:      store.TaskHealthIdle,
			NeedsUser:  false,
			Summary:    "subagent completed",
			Source:     "agent.subagent.run",
			ObservedAt: finishedAt,
		})
		_ = p.task.AppendSemanticReport(context.Background(), tasksvc.SemanticReportInput{
			TaskRunID:  taskRunID,
			Phase:      store.TaskPhaseDone,
			Milestone:  "subagent_completed",
			NextAction: "done",
			Summary:    "subagent completed",
			ReportedAt: finishedAt,
		})
	}

	resultPayload, _ := json.MarshalIndent(map[string]any{
		"schema":      "dalek.agent.subagent.result.v1",
		"status":      status,
		"task_run_id": taskRunID,
		"request_id":  strings.TrimSpace(subRec.RequestID),
		"provider":    settings.Provider,
		"model":       settings.Model,
		"runtime_dir": runtimeDir,
		"output": map[string]any{
			"provider":    strings.TrimSpace(res.Provider),
			"output_mode": strings.TrimSpace(res.OutputMode),
			"text":        strings.TrimSpace(res.Text),
			"session_id":  strings.TrimSpace(res.SessionID),
			"stdout":      strings.TrimSpace(res.Stdout),
			"stderr":      strings.TrimSpace(res.Stderr),
			"events":      res.Events,
		},
		"error_code":  errorCode,
		"error":       errorMsg,
		"finished_at": finishedAt.Format(time.RFC3339),
	}, "", "  ")
	_ = os.WriteFile(filepath.Join(runtimeDir, "result.json"), resultPayload, 0o644)
	if runErr != nil {
		_, _ = fmt.Fprintf(streamFile, "%s subagent %s: %s\n", finishedAt.Local().Format(time.RFC3339), status, errorMsg)
		_, _ = fmt.Fprintf(sdkStreamFile, "%s sdk %s: %s\n", finishedAt.Local().Format(time.RFC3339), status, errorMsg)
		return runErr
	}
	_, _ = fmt.Fprintf(streamFile, "%s subagent succeeded\n", finishedAt.Local().Format(time.RFC3339))
	_, _ = fmt.Fprintf(sdkStreamFile, "%s sdk succeeded\n", finishedAt.Local().Format(time.RFC3339))
	return nil
}

func (p *Project) resolveSubagentAgentSettings(providerRaw, modelRaw string) (agentprovider.AgentConfig, error) {
	if p == nil || p.core == nil {
		return agentprovider.AgentConfig{}, fmt.Errorf("project 为空")
	}
	cfg := p.core.Config.WithDefaults().WorkerAgent
	providerName := strings.TrimSpace(strings.ToLower(providerRaw))
	baseProvider := strings.TrimSpace(strings.ToLower(cfg.Provider))
	if providerName == "" {
		providerName = baseProvider
	}
	if providerName == "" {
		providerName = "codex"
	}
	model := strings.TrimSpace(modelRaw)
	if model == "" {
		if strings.TrimSpace(providerRaw) == "" || providerName == baseProvider {
			model = strings.TrimSpace(cfg.Model)
		}
	}
	if providerName == "codex" && model == "" {
		model = "gpt-5.3-codex"
	}
	reasoning := strings.TrimSpace(strings.ToLower(cfg.ReasoningEffort))
	if providerName == "claude" {
		reasoning = ""
	}
	resolved := agentprovider.AgentConfig{
		Provider:        providerName,
		Model:           model,
		ReasoningEffort: reasoning,
		ExtraFlags:      append([]string(nil), cfg.ExtraFlags...),
		Command:         strings.TrimSpace(cfg.Command),
	}
	if _, err := agentprovider.NewFromConfig(resolved); err != nil {
		return agentprovider.AgentConfig{}, err
	}
	return resolved.Normalize(), nil
}

func (p *Project) subagentRuntimeDir(taskRunID uint) string {
	if taskRunID == 0 {
		return ""
	}
	projectName := strings.TrimSpace(p.Name())
	if projectName == "" {
		projectName = strings.TrimSpace(p.Key())
	}
	homeDir := inferHomeRootFromWorktreesDir("")
	if p != nil && p.core != nil {
		homeDir = inferHomeRootFromWorktreesDir(strings.TrimSpace(p.core.WorktreesDir))
	}
	if homeDir == "" {
		homeDir = strings.TrimSpace(p.ProjectDir())
	}
	return filepath.Join(homeDir, "agents", projectName, strconv.FormatUint(uint64(taskRunID), 10))
}

func inferHomeRootFromWorktreesDir(worktreesDir string) string {
	cur := filepath.Clean(strings.TrimSpace(worktreesDir))
	if cur == "" || cur == "." {
		return ""
	}
	for {
		if strings.EqualFold(filepath.Base(cur), "worktrees") {
			return filepath.Dir(cur)
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return ""
}

func isSubagentCanceled(runErr error, ctxErr error) bool {
	return errors.Is(runErr, context.Canceled) ||
		errors.Is(runErr, context.DeadlineExceeded) ||
		errors.Is(ctxErr, context.Canceled) ||
		errors.Is(ctxErr, context.DeadlineExceeded)
}

func newSubagentRequestID() string {
	return fmt.Sprintf("sub_%d", time.Now().UnixNano())
}
