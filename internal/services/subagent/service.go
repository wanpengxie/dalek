package subagent

import (
	"context"
	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
	"dalek/internal/services/core"
	tasksvc "dalek/internal/services/task"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

type Service struct {
	p      *core.Project
	task   *tasksvc.Service
	logger *slog.Logger
	runner sdkrunner.TaskRunner
	now    func() time.Time
}

func New(p *core.Project, task *tasksvc.Service, logger *slog.Logger) *Service {
	if logger == nil && p != nil {
		logger = p.Logger
	}
	return &Service{
		p:      p,
		task:   task,
		logger: core.EnsureLogger(logger).With("service", "subagent"),
		runner: sdkrunner.DefaultTaskRunner{},
		now:    time.Now,
	}
}

func (s *Service) Submit(ctx context.Context, in SubmitInput) (SubmitResult, error) {
	if s == nil || s.task == nil {
		return SubmitResult{}, fmt.Errorf("project task service 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	prompt := strings.TrimSpace(in.Prompt)
	if prompt == "" {
		return SubmitResult{}, fmt.Errorf("prompt 不能为空")
	}
	settings, err := s.resolveAgentSettings(in.Provider, in.Model)
	if err != nil {
		return SubmitResult{}, err
	}

	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		requestID = newSubagentRequestID()
	}

	existingRun, err := s.task.FindRunByRequestID(ctx, requestID)
	if err != nil {
		return SubmitResult{}, err
	}
	if existingRun != nil {
		if existingRun.OwnerType != contracts.TaskOwnerSubagent || strings.TrimSpace(existingRun.TaskType) != contracts.TaskTypeSubagentRun {
			return SubmitResult{}, fmt.Errorf("request_id 已被其他任务占用: %s", requestID)
		}
		existingRec, serr := s.task.FindSubagentRunByTaskRunID(ctx, existingRun.ID)
		if serr != nil {
			return SubmitResult{}, serr
		}
		if existingRec != nil {
			return SubmitResult{
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
	createdRun, err := s.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerSubagent,
		TaskType:           contracts.TaskTypeSubagentRun,
		ProjectKey:         strings.TrimSpace(s.projectKey()),
		SubjectType:        "project",
		SubjectID:          strings.TrimSpace(s.projectKey()),
		RequestID:          requestID,
		OrchestrationState: contracts.TaskPending,
		RequestPayloadJSON: strings.TrimSpace(string(requestPayload)),
	})
	if err != nil {
		return SubmitResult{}, err
	}
	if createdRun.OwnerType != contracts.TaskOwnerSubagent || strings.TrimSpace(createdRun.TaskType) != contracts.TaskTypeSubagentRun {
		return SubmitResult{}, fmt.Errorf("request_id 已被其他任务占用: %s", requestID)
	}

	runtimeDir := s.runtimeDir(createdRun.ID)
	rec, err := s.task.CreateSubagentRun(ctx, tasksvc.CreateSubagentRunInput{
		ProjectKey: strings.TrimSpace(s.projectKey()),
		TaskRunID:  createdRun.ID,
		RequestID:  requestID,
		Provider:   settings.Provider,
		Model:      settings.Model,
		Prompt:     prompt,
		CWD:        strings.TrimSpace(s.repoRoot()),
		RuntimeDir: runtimeDir,
	})
	if err != nil {
		return SubmitResult{}, err
	}

	now := s.nowTime()
	_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: createdRun.ID,
		EventType: "task_enqueued",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskPending,
		},
		Note:      "subagent run enqueued",
		CreatedAt: now,
	})
	_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  createdRun.ID,
		State:      contracts.TaskHealthIdle,
		NeedsUser:  false,
		Summary:    "subagent queued",
		Source:     "agent.subagent.submit",
		ObservedAt: now,
	})
	_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  createdRun.ID,
		Phase:      contracts.TaskPhasePlanning,
		Milestone:  "subagent_enqueued",
		NextAction: "continue",
		Summary:    "subagent accepted",
		ReportedAt: now,
	})

	return SubmitResult{
		Accepted:   true,
		TaskRunID:  createdRun.ID,
		RequestID:  requestID,
		Provider:   strings.TrimSpace(rec.Provider),
		Model:      strings.TrimSpace(rec.Model),
		RuntimeDir: strings.TrimSpace(rec.RuntimeDir),
	}, nil
}

func (s *Service) Run(ctx context.Context, taskRunID uint, in RunInput) error {
	if s == nil || s.task == nil {
		return fmt.Errorf("project task service 为空")
	}
	if taskRunID == 0 {
		return fmt.Errorf("task_run_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	runRec, err := s.task.FindRunByID(ctx, taskRunID)
	if err != nil {
		return err
	}
	if runRec == nil {
		return fmt.Errorf("task run 不存在: %d", taskRunID)
	}
	if runRec.OwnerType != contracts.TaskOwnerSubagent || strings.TrimSpace(runRec.TaskType) != contracts.TaskTypeSubagentRun {
		return fmt.Errorf("task run 不是 subagent 任务: %d", taskRunID)
	}
	subRec, err := s.task.FindSubagentRunByTaskRunID(ctx, taskRunID)
	if err != nil {
		return err
	}
	if subRec == nil {
		return fmt.Errorf("subagent run 不存在: task_run_id=%d", taskRunID)
	}

	runtimeDir := strings.TrimSpace(subRec.RuntimeDir)
	if runtimeDir == "" {
		runtimeDir = s.runtimeDir(taskRunID)
	}
	streamFile, sdkStreamFile, err := prepareSubagentRuntime(runtimeDir, subRec.Prompt)
	if err != nil {
		return err
	}
	defer func() { _ = streamFile.Close() }()
	defer func() { _ = sdkStreamFile.Close() }()

	settings, err := s.resolveAgentSettings(subRec.Provider, subRec.Model)
	if err != nil {
		return err
	}
	workDir := strings.TrimSpace(subRec.CWD)
	if workDir == "" {
		workDir = strings.TrimSpace(s.repoRoot())
	}
	runnerID := strings.TrimSpace(in.RunnerID)
	if runnerID == "" {
		runnerID = "daemon_" + newSubagentRequestID()
	}

	now := s.nowTime()
	var lease *time.Time
	if deadline, ok := ctx.Deadline(); ok {
		deadlineCopy := deadline
		lease = &deadlineCopy
	}
	if err := s.task.MarkRunRunning(ctx, taskRunID, runnerID, lease, now, true); err != nil {
		return err
	}
	_ = s.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: taskRunID,
		EventType: "task_started",
		FromState: map[string]any{"orchestration_state": contracts.TaskPending},
		ToState: map[string]any{
			"orchestration_state": contracts.TaskRunning,
			"runner_id":           runnerID,
		},
		Note:      "subagent started",
		CreatedAt: now,
	})
	_ = s.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  taskRunID,
		State:      contracts.TaskHealthBusy,
		NeedsUser:  false,
		Summary:    "subagent running",
		Source:     "agent.subagent.run",
		ObservedAt: now,
	})
	_ = s.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  taskRunID,
		Phase:      contracts.TaskPhaseImplementing,
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
		_ = s.task.AppendEvent(context.Background(), contracts.TaskEventInput{
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

	res, runErr := s.taskRunner().Run(ctx, sdkrunner.Request{
		AgentConfig: settings,
		Prompt:      strings.TrimSpace(subRec.Prompt),
		WorkDir:     workDir,
		Env: map[string]string{
			"DALEK_PROJECT_KEY":          strings.TrimSpace(s.projectKey()),
			"DALEK_REPO_ROOT":            strings.TrimSpace(s.repoRoot()),
			"DALEK_SUBAGENT_RUN_ID":      strconv.FormatUint(uint64(taskRunID), 10),
			"DALEK_SUBAGENT_REQUEST_ID":  strings.TrimSpace(subRec.RequestID),
			"DALEK_SUBAGENT_RUNTIME_DIR": runtimeDir,
		},
	}, onEvent)

	finishedAt := s.nowTime()
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
			_ = s.task.MarkRunCanceled(context.Background(), taskRunID, errorCode, errorMsg, finishedAt)
			_ = s.task.AppendEvent(context.Background(), contracts.TaskEventInput{
				TaskRunID: taskRunID,
				EventType: "task_canceled",
				FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
				ToState:   map[string]any{"orchestration_state": contracts.TaskCanceled},
				Note:      errorMsg,
				CreatedAt: finishedAt,
			})
			_ = s.task.AppendRuntimeSample(context.Background(), contracts.TaskRuntimeSampleInput{
				TaskRunID:  taskRunID,
				State:      contracts.TaskHealthDead,
				NeedsUser:  true,
				Summary:    errorMsg,
				Source:     "agent.subagent.run",
				ObservedAt: finishedAt,
			})
			_ = s.task.AppendSemanticReport(context.Background(), contracts.TaskSemanticReportInput{
				TaskRunID:  taskRunID,
				Phase:      contracts.TaskPhaseBlocked,
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
			_ = s.task.MarkRunFailed(context.Background(), taskRunID, errorCode, errorMsg, finishedAt)
			_ = s.task.AppendEvent(context.Background(), contracts.TaskEventInput{
				TaskRunID: taskRunID,
				EventType: "task_failed",
				FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
				ToState: map[string]any{
					"orchestration_state": contracts.TaskFailed,
					"error_code":          errorCode,
				},
				Note:      errorMsg,
				CreatedAt: finishedAt,
			})
			_ = s.task.AppendRuntimeSample(context.Background(), contracts.TaskRuntimeSampleInput{
				TaskRunID:  taskRunID,
				State:      contracts.TaskHealthStalled,
				NeedsUser:  true,
				Summary:    errorMsg,
				Source:     "agent.subagent.run",
				ObservedAt: finishedAt,
			})
			_ = s.task.AppendSemanticReport(context.Background(), contracts.TaskSemanticReportInput{
				TaskRunID:  taskRunID,
				Phase:      contracts.TaskPhaseBlocked,
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
			"orchestration": contracts.TaskSucceeded,
		})
		_ = s.task.MarkRunSucceeded(context.Background(), taskRunID, strings.TrimSpace(string(resultJSON)), finishedAt)
		_ = s.task.AppendEvent(context.Background(), contracts.TaskEventInput{
			TaskRunID: taskRunID,
			EventType: "task_succeeded",
			FromState: map[string]any{"orchestration_state": contracts.TaskRunning},
			ToState:   map[string]any{"orchestration_state": contracts.TaskSucceeded},
			Note:      "subagent completed",
			CreatedAt: finishedAt,
		})
		_ = s.task.AppendRuntimeSample(context.Background(), contracts.TaskRuntimeSampleInput{
			TaskRunID:  taskRunID,
			State:      contracts.TaskHealthIdle,
			NeedsUser:  false,
			Summary:    "subagent completed",
			Source:     "agent.subagent.run",
			ObservedAt: finishedAt,
		})
		_ = s.task.AppendSemanticReport(context.Background(), contracts.TaskSemanticReportInput{
			TaskRunID:  taskRunID,
			Phase:      contracts.TaskPhaseDone,
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
	writeSubagentResult(runtimeDir, resultPayload)

	if runErr != nil {
		_, _ = fmt.Fprintf(streamFile, "%s subagent %s: %s\n", finishedAt.Local().Format(time.RFC3339), status, errorMsg)
		_, _ = fmt.Fprintf(sdkStreamFile, "%s sdk %s: %s\n", finishedAt.Local().Format(time.RFC3339), status, errorMsg)
		return runErr
	}
	_, _ = fmt.Fprintf(streamFile, "%s subagent succeeded\n", finishedAt.Local().Format(time.RFC3339))
	_, _ = fmt.Fprintf(sdkStreamFile, "%s sdk succeeded\n", finishedAt.Local().Format(time.RFC3339))
	return nil
}

func (s *Service) taskRunner() sdkrunner.TaskRunner {
	if s != nil && s.runner != nil {
		return s.runner
	}
	return sdkrunner.DefaultTaskRunner{}
}

func (s *Service) nowTime() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) projectKey() string {
	if s == nil || s.p == nil {
		return ""
	}
	return strings.TrimSpace(s.p.Key)
}

func (s *Service) projectName() string {
	if s == nil || s.p == nil {
		return ""
	}
	return strings.TrimSpace(s.p.Name)
}

func (s *Service) repoRoot() string {
	if s == nil || s.p == nil {
		return ""
	}
	return strings.TrimSpace(s.p.RepoRoot)
}

func (s *Service) projectDir() string {
	if s == nil || s.p == nil {
		return ""
	}
	return strings.TrimSpace(s.p.ProjectDir())
}
