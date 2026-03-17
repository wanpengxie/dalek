package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
)

type remoteTaskProxyPayload struct {
	Role          string `json:"role"`
	RouteMode     string `json:"route_mode"`
	RemoteBaseURL string `json:"remote_base_url"`
	RemoteProject string `json:"remote_project"`
	RemoteRunID   uint   `json:"remote_run_id"`
	RequestID     string `json:"request_id"`
	LinkedRunID   uint   `json:"linked_run_id,omitempty"`
	LinkedSummary string `json:"linked_summary,omitempty"`
}

type remoteRoleRoute struct {
	role      string
	mode      string
	baseURL   string
	project   string
	available bool
}

type taskRequestDecision struct {
	role       TaskRequestRole
	roleSource string
	reason     string
}

func (p *Project) SubmitTaskRequest(ctx context.Context, opt SubmitTaskRequestOptions) (TaskRequestSubmission, error) {
	if p == nil {
		return TaskRequestSubmission{}, fmt.Errorf("project 为空")
	}
	decision, err := p.resolveTaskRequestDecision(opt)
	if err != nil {
		return TaskRequestSubmission{}, err
	}
	role := decision.role
	route := p.resolveRemoteRoleRoute(role, opt)
	finalize := func(res TaskRequestSubmission) (TaskRequestSubmission, error) {
		res.RoleSource = strings.TrimSpace(decision.roleSource)
		res.RouteReason = strings.TrimSpace(decision.reason)
		p.appendTaskRequestDecisionEvent(ctx, res)
		return res, nil
	}
	switch role {
	case TaskRequestRoleRun:
		if route.available {
			res, err := p.submitRemoteRunTask(ctx, route, opt)
			if err != nil {
				return TaskRequestSubmission{}, err
			}
			return finalize(res)
		}
		res, err := p.SubmitRun(ctx, SubmitRunOptions{
			TicketID:     opt.TicketID,
			RequestID:    strings.TrimSpace(opt.RequestID),
			VerifyTarget: strings.TrimSpace(opt.VerifyTarget),
		})
		if err != nil {
			return TaskRequestSubmission{}, err
		}
		return finalize(TaskRequestSubmission{
			Accepted:     res.Accepted,
			Role:         string(TaskRequestRoleRun),
			RouteMode:    "local",
			RouteTarget:  "local",
			TaskRunID:    res.TaskRunID,
			RemoteRunID:  res.RunID,
			RequestID:    res.RequestID,
			TicketID:     opt.TicketID,
			VerifyTarget: res.VerifyTarget,
		})
	case TaskRequestRoleDev:
		if !route.available {
			return TaskRequestSubmission{}, fmt.Errorf("dev role 未配置远程路由；请设置 multi_node.dev_base_url")
		}
		res, err := p.submitRemoteDevTask(ctx, route, opt)
		if err != nil {
			return TaskRequestSubmission{}, err
		}
		return finalize(res)
	default:
		return TaskRequestSubmission{}, fmt.Errorf("未知任务角色: %s", role)
	}
}

func (p *Project) resolveTaskRequestDecision(opt SubmitTaskRequestOptions) (taskRequestDecision, error) {
	if opt.ForceRole != "" {
		switch opt.ForceRole {
		case TaskRequestRoleDev, TaskRequestRoleRun:
			return taskRequestDecision{
				role:       opt.ForceRole,
				roleSource: "explicit",
				reason:     "explicit role override",
			}, nil
		default:
			return taskRequestDecision{}, fmt.Errorf("非法角色: %s", opt.ForceRole)
		}
	}
	if strings.TrimSpace(opt.VerifyTarget) != "" {
		return taskRequestDecision{
			role:       TaskRequestRoleRun,
			roleSource: "verify_target",
			reason:     "verify_target provided",
		}, nil
	}
	prompt := strings.TrimSpace(opt.Prompt)
	if prompt != "" {
		if p != nil {
			cfg := p.core.Config.WithDefaults().MultiNode
			if cfg.AutoRoute {
				if role, reason, ok := inferAutoTaskRequestRole(prompt); ok {
					return taskRequestDecision{
						role:       role,
						roleSource: "auto_route_prompt",
						reason:     reason,
					}, nil
				}
			}
		}
		return taskRequestDecision{
			role:       TaskRequestRoleDev,
			roleSource: "prompt_default",
			reason:     "prompt provided without verify target",
		}, nil
	}
	return taskRequestDecision{}, fmt.Errorf("prompt 或 verify_target 至少需要一个")
}

func (p *Project) resolveRemoteRoleRoute(role TaskRequestRole, opt SubmitTaskRequestOptions) remoteRoleRoute {
	cfg := p.core.Config.WithDefaults().MultiNode
	route := remoteRoleRoute{role: string(role), mode: "local"}
	baseURL := strings.TrimSpace(opt.RemoteBaseURL)
	projectName := strings.TrimSpace(opt.RemoteProject)
	switch role {
	case TaskRequestRoleDev:
		if baseURL == "" {
			baseURL = strings.TrimSpace(cfg.DevBaseURL)
		}
		if projectName == "" {
			projectName = strings.TrimSpace(cfg.DevProjectName)
		}
	case TaskRequestRoleRun:
		if baseURL == "" {
			baseURL = strings.TrimSpace(cfg.RunBaseURL)
		}
		if projectName == "" {
			projectName = strings.TrimSpace(cfg.RunProjectName)
		}
	}
	if projectName == "" {
		projectName = strings.TrimSpace(p.Name())
	}
	if baseURL != "" {
		route.mode = "remote"
		route.baseURL = baseURL
		route.project = projectName
		route.available = true
	}
	return route
}

func (p *Project) submitRemoteDevTask(ctx context.Context, route remoteRoleRoute, opt SubmitTaskRequestOptions) (TaskRequestSubmission, error) {
	remote, err := NewDaemonRemoteProjectFromBaseURL(route.baseURL, route.project)
	if err != nil {
		return TaskRequestSubmission{}, err
	}
	requestID := strings.TrimSpace(opt.RequestID)
	linkedRunID, linkedSummary := p.latestFailedRunContext(ctx, opt.TicketID)
	prompt := strings.TrimSpace(opt.Prompt)
	if linkedSummary != "" {
		prompt = strings.TrimSpace(prompt + "\n\n[Latest run failure context]\n" + linkedSummary)
	}
	receipt, err := remote.SubmitWorkerRun(ctx, DaemonWorkerRunSubmitRequest{
		TicketID:  opt.TicketID,
		RequestID: requestID,
		Prompt:    prompt,
	})
	if err != nil {
		return TaskRequestSubmission{}, err
	}
	if requestID == "" {
		requestID = strings.TrimSpace(receipt.RequestID)
	}
	now := time.Now()
	payload := remoteTaskProxyPayload{
		Role:          string(TaskRequestRoleDev),
		RouteMode:     route.mode,
		RemoteBaseURL: route.baseURL,
		RemoteProject: route.project,
		RemoteRunID:   receipt.TaskRunID,
		RequestID:     strings.TrimSpace(receipt.RequestID),
		LinkedRunID:   linkedRunID,
		LinkedSummary: linkedSummary,
	}
	rawPayload, _ := json.Marshal(payload)
	created, err := p.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeDevRemote,
		ProjectKey:         p.Key(),
		TicketID:           opt.TicketID,
		WorkerID:           receipt.WorkerID,
		SubjectType:        "role",
		SubjectID:          "dev",
		RequestID:          requestID,
		OrchestrationState: contracts.TaskPending,
		RequestPayloadJSON: string(rawPayload),
	})
	if err != nil {
		return TaskRequestSubmission{}, err
	}
	if err := p.task.MarkRunRunning(ctx, created.ID, "remote:"+route.role, nil, now, false); err != nil {
		return TaskRequestSubmission{}, err
	}
	_ = p.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: created.ID,
		EventType: "dev_remote_submitted",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskRunning,
			"role":                "dev",
			"route_mode":          route.mode,
		},
		Note:      fmt.Sprintf("remote dev task submitted to %s", route.baseURL),
		CreatedAt: now,
		Payload:   payload,
	})
	_ = p.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  created.ID,
		State:      contracts.TaskHealthBusy,
		Summary:    "remote dev task accepted",
		Source:     "multi_node.dev",
		ObservedAt: now,
		Metrics: map[string]any{
			"remote_run_id": receipt.TaskRunID,
			"route_target":  route.baseURL,
		},
	})
	_ = p.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  created.ID,
		Phase:      contracts.TaskPhaseImplementing,
		Milestone:  "dev_remote_submitted",
		NextAction: string(contracts.NextContinue),
		Summary:    "remote dev task accepted",
		ReportedAt: now,
		Payload:    payload,
	})
	if linkedSummary != "" {
		_ = p.task.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: created.ID,
			EventType: "run_failure_context_linked",
			Note:      linkedSummary,
			CreatedAt: now,
			Payload: map[string]any{
				"linked_run_id": linkedRunID,
			},
		})
	}
	return TaskRequestSubmission{
		Accepted:      true,
		Role:          "dev",
		RouteMode:     route.mode,
		RouteTarget:   route.baseURL,
		TaskRunID:     created.ID,
		RemoteRunID:   receipt.TaskRunID,
		RequestID:     created.RequestID,
		TicketID:      opt.TicketID,
		WorkerID:      receipt.WorkerID,
		LinkedRunID:   linkedRunID,
		LinkedSummary: linkedSummary,
	}, nil
}

func (p *Project) submitRemoteRunTask(ctx context.Context, route remoteRoleRoute, opt SubmitTaskRequestOptions) (TaskRequestSubmission, error) {
	remote, err := NewDaemonRemoteProjectFromBaseURL(route.baseURL, route.project)
	if err != nil {
		return TaskRequestSubmission{}, err
	}
	receipt, err := remote.SubmitRun(ctx, DaemonRunSubmitRequest{
		TicketID:            opt.TicketID,
		RequestID:           strings.TrimSpace(opt.RequestID),
		VerifyTarget:        strings.TrimSpace(opt.VerifyTarget),
		SnapshotID:          "",
		BaseCommit:          "",
		WorkspaceGeneration: "",
	})
	if err != nil {
		return TaskRequestSubmission{}, err
	}
	now := time.Now()
	payload := remoteTaskProxyPayload{
		Role:          string(TaskRequestRoleRun),
		RouteMode:     route.mode,
		RemoteBaseURL: route.baseURL,
		RemoteProject: route.project,
		RemoteRunID:   receipt.RunID,
		RequestID:     strings.TrimSpace(receipt.RequestID),
	}
	rawPayload, _ := json.Marshal(payload)
	created, err := p.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeRunVerify,
		ProjectKey:         p.Key(),
		TicketID:           opt.TicketID,
		SubjectType:        "run",
		SubjectID:          strings.TrimSpace(opt.VerifyTarget),
		RequestID:          strings.TrimSpace(receipt.RequestID),
		OrchestrationState: contracts.TaskPending,
		RequestPayloadJSON: string(rawPayload),
	})
	if err != nil {
		return TaskRequestSubmission{}, err
	}
	if err := p.core.DB.WithContext(ctx).Create(&contracts.RunView{
		RunID:        created.ID,
		TaskRunID:    created.ID,
		ProjectKey:   p.Key(),
		RequestID:    strings.TrimSpace(receipt.RequestID),
		TicketID:     opt.TicketID,
		RunStatus:    contracts.RunStatus(strings.TrimSpace(receipt.RunStatus)),
		VerifyTarget: strings.TrimSpace(receipt.VerifyTarget),
		SnapshotID:   strings.TrimSpace(receipt.SnapshotID),
		BaseCommit:   strings.TrimSpace(receipt.BaseCommit),
	}).Error; err != nil {
		return TaskRequestSubmission{}, err
	}
	if err := p.task.MarkRunRunning(ctx, created.ID, "remote:"+route.role, nil, now, false); err != nil {
		return TaskRequestSubmission{}, err
	}
	_ = p.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: created.ID,
		EventType: "run_remote_submitted",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskRunning,
			"run_status":          strings.TrimSpace(receipt.RunStatus),
			"role":                "run",
			"route_mode":          route.mode,
		},
		Note:      fmt.Sprintf("remote run submitted to %s", route.baseURL),
		CreatedAt: now,
		Payload:   payload,
	})
	_ = p.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  created.ID,
		State:      contracts.TaskHealthBusy,
		Summary:    "remote run accepted",
		Source:     "multi_node.run",
		ObservedAt: now,
		Metrics: map[string]any{
			"remote_run_id": receipt.RunID,
			"route_target":  route.baseURL,
		},
	})
	_ = p.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  created.ID,
		Phase:      contracts.TaskPhaseTesting,
		Milestone:  "run_remote_submitted",
		NextAction: string(contracts.NextContinue),
		Summary:    "remote run accepted",
		ReportedAt: now,
		Payload:    payload,
	})
	return TaskRequestSubmission{
		Accepted:     true,
		Role:         "run",
		RouteMode:    route.mode,
		RouteTarget:  firstNonEmpty(route.baseURL, "local"),
		TaskRunID:    created.ID,
		RemoteRunID:  receipt.RunID,
		RequestID:    strings.TrimSpace(receipt.RequestID),
		TicketID:     opt.TicketID,
		VerifyTarget: strings.TrimSpace(receipt.VerifyTarget),
	}, nil
}

func (p *Project) latestFailedRunContext(ctx context.Context, ticketID uint) (uint, string) {
	if p == nil || p.task == nil || ticketID == 0 {
		return 0, ""
	}
	items, err := p.ListTaskStatus(ctx, ListTaskOptions{
		TaskType:        contracts.TaskTypeRunVerify,
		TicketID:        ticketID,
		IncludeTerminal: true,
		Limit:           20,
	})
	if err != nil {
		return 0, ""
	}
	for _, item := range items {
		runStatus := DeriveRunStatus(item.OrchestrationState, item.RuntimeHealthState, item.RuntimeNeedsUser)
		if runStatus != "failed" && runStatus != "dead" && runStatus != "waiting_user" {
			continue
		}
		summary := strings.TrimSpace(item.ErrorMessage)
		if summary == "" {
			summary = strings.TrimSpace(item.LastEventNote)
		}
		if summary == "" {
			summary = strings.TrimSpace(item.SemanticSummary)
		}
		if summary == "" {
			summary = strings.TrimSpace(item.RuntimeSummary)
		}
		if summary == "" {
			summary = strings.TrimSpace(item.ErrorMessage)
		}
		return item.RunID, summary
	}
	return 0, ""
}

func inferAutoTaskRequestRole(prompt string) (TaskRequestRole, string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(prompt))
	if normalized == "" {
		return "", "", false
	}
	runKeywords := []string{
		"verify", "verification", "test", "tests", "testing",
		"validate", "validation", "smoke", "regression", "benchmark",
		"验证", "测试", "回归", "冒烟", "跑一下", "跑测试", "检查失败", "复现",
	}
	devKeywords := []string{
		"develop", "development", "implement", "fix", "code", "refactor",
		"开发", "继续开发", "修复", "实现", "改代码", "编码",
	}
	runHit := containsAnyKeyword(normalized, runKeywords)
	devHit := containsAnyKeyword(normalized, devKeywords)
	switch {
	case runHit && !devHit:
		return TaskRequestRoleRun, "prompt matched verify/test keywords", true
	case devHit && !runHit:
		return TaskRequestRoleDev, "prompt matched development keywords", true
	default:
		return "", "", false
	}
}

func containsAnyKeyword(text string, keywords []string) bool {
	for _, kw := range keywords {
		kw = strings.TrimSpace(strings.ToLower(kw))
		if kw != "" && strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func (p *Project) appendTaskRequestDecisionEvent(ctx context.Context, res TaskRequestSubmission) {
	if p == nil || p.task == nil || res.TaskRunID == 0 {
		return
	}
	_ = p.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: res.TaskRunID,
		EventType: "task_request_routed",
		Note:      strings.TrimSpace(firstNonEmpty(res.RouteReason, res.RoleSource, "task request routed")),
		CreatedAt: time.Now(),
		Payload: map[string]any{
			"role":          strings.TrimSpace(res.Role),
			"role_source":   strings.TrimSpace(res.RoleSource),
			"route_reason":  strings.TrimSpace(res.RouteReason),
			"route_mode":    strings.TrimSpace(res.RouteMode),
			"route_target":  strings.TrimSpace(res.RouteTarget),
			"remote_run_id": res.RemoteRunID,
			"request_id":    strings.TrimSpace(res.RequestID),
		},
	})
}

func (p *Project) reconcileRemoteRunIfNeeded(ctx context.Context, runID uint) error {
	if p == nil || p.task == nil || p.run == nil || runID == 0 {
		return nil
	}
	view, err := p.run.Get(ctx, runID)
	if err != nil || view == nil {
		return err
	}
	record, err := p.task.FindRunByID(ctx, runID)
	if err != nil || record == nil || strings.TrimSpace(record.TaskType) != contracts.TaskTypeRunVerify {
		return err
	}
	payload, ok := parseRemoteTaskProxyPayload(record.RequestPayloadJSON)
	if !ok || payload.Role != string(TaskRequestRoleRun) || payload.RemoteRunID == 0 || strings.TrimSpace(payload.RemoteBaseURL) == "" {
		return nil
	}
	remote, err := NewDaemonRemoteProjectFromBaseURL(payload.RemoteBaseURL, payload.RemoteProject)
	if err != nil {
		return err
	}
	status, err := remote.GetRun(ctx, payload.RemoteRunID)
	if err != nil || status == nil {
		return err
	}
	now := time.Now()
	nextRunStatus := deriveRemoteRunStatus(status)
	updates := map[string]any{}
	if nextRunStatus != "" && view.RunStatus != nextRunStatus {
		updates["run_status"] = nextRunStatus
		view.RunStatus = nextRunStatus
	}
	if view.SnapshotID == "" && strings.TrimSpace(status.Project) != "" {
		updates["project_key"] = p.Key()
	}
	if len(updates) > 0 {
		if err := p.core.DB.WithContext(ctx).Model(&contracts.RunView{}).Where("run_id = ?", runID).Updates(updates).Error; err != nil {
			return err
		}
	}
	switch strings.ToLower(strings.TrimSpace(status.OrchestrationState)) {
	case string(contracts.TaskSucceeded):
		if record.OrchestrationState != contracts.TaskSucceeded {
			if err := p.task.MarkRunSucceeded(ctx, runID, "", now); err != nil {
				return err
			}
		}
	case string(contracts.TaskFailed):
		if record.OrchestrationState != contracts.TaskFailed {
			if err := p.task.MarkRunFailed(ctx, runID, strings.TrimSpace(status.ErrorCode), strings.TrimSpace(status.ErrorMessage), now); err != nil {
				return err
			}
		}
	case string(contracts.TaskCanceled):
		if record.OrchestrationState != contracts.TaskCanceled {
			if err := p.task.MarkRunCanceled(ctx, runID, "remote_canceled", "remote run canceled", now); err != nil {
				return err
			}
		}
	default:
		if record.OrchestrationState == contracts.TaskPending {
			if err := p.task.MarkRunRunning(ctx, runID, "remote:run", nil, now, false); err != nil {
				return err
			}
		}
	}
	_ = p.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  runID,
		State:      contracts.TaskRuntimeHealthState(status.RuntimeHealthState),
		NeedsUser:  status.RuntimeNeedsUser,
		Summary:    strings.TrimSpace(status.RuntimeSummary),
		Source:     "multi_node.run.remote",
		ObservedAt: now,
	})
	_ = p.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  runID,
		Phase:      contracts.NextActionToSemanticPhase(status.SemanticNextAction),
		Milestone:  "run_remote_reconciled",
		NextAction: strings.TrimSpace(status.SemanticNextAction),
		Summary:    strings.TrimSpace(status.SemanticSummary),
		ReportedAt: now,
		Payload: map[string]any{
			"remote_run_id": payload.RemoteRunID,
			"remote_status": strings.TrimSpace(status.OrchestrationState),
		},
	})
	_ = p.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "run_remote_reconciled",
		Note:      strings.TrimSpace(status.RuntimeSummary),
		CreatedAt: now,
		Payload: map[string]any{
			"remote_run_id":       payload.RemoteRunID,
			"remote_project":      payload.RemoteProject,
			"remote_base_url":     payload.RemoteBaseURL,
			"orchestration_state": strings.TrimSpace(status.OrchestrationState),
			"error_code":          strings.TrimSpace(status.ErrorCode),
			"error_message":       strings.TrimSpace(status.ErrorMessage),
		},
	})
	return nil
}

func (p *Project) reconcileRemoteTaskIfNeeded(ctx context.Context, runID uint) error {
	if p == nil || p.task == nil || runID == 0 {
		return nil
	}
	record, err := p.task.FindRunByID(ctx, runID)
	if err != nil || record == nil || strings.TrimSpace(record.TaskType) != contracts.TaskTypeDevRemote {
		return err
	}
	payload, ok := parseRemoteTaskProxyPayload(record.RequestPayloadJSON)
	if !ok || payload.RemoteRunID == 0 || strings.TrimSpace(payload.RemoteBaseURL) == "" {
		return nil
	}
	client, err := NewDaemonAPIClient(DaemonAPIClientConfig{BaseURL: payload.RemoteBaseURL})
	if err != nil {
		return err
	}
	status, err := client.GetRun(ctx, payload.RemoteRunID)
	if err != nil || status == nil {
		return err
	}
	localStatus, err := p.task.GetStatusByRunID(ctx, runID)
	if err != nil {
		return err
	}
	now := time.Now()
	switch strings.ToLower(strings.TrimSpace(status.OrchestrationState)) {
	case string(contracts.TaskSucceeded):
		if record.OrchestrationState != contracts.TaskSucceeded {
			if err := p.task.MarkRunSucceeded(ctx, runID, "", now); err != nil {
				return err
			}
		}
	case string(contracts.TaskFailed):
		if record.OrchestrationState != contracts.TaskFailed {
			if err := p.task.MarkRunFailed(ctx, runID, strings.TrimSpace(status.ErrorCode), strings.TrimSpace(status.ErrorMessage), now); err != nil {
				return err
			}
		}
	case string(contracts.TaskCanceled):
		if record.OrchestrationState != contracts.TaskCanceled {
			if err := p.task.MarkRunCanceled(ctx, runID, "remote_canceled", "remote task canceled", now); err != nil {
				return err
			}
		}
	default:
		if record.OrchestrationState == contracts.TaskPending {
			if err := p.task.MarkRunRunning(ctx, runID, "remote:"+payload.Role, nil, now, false); err != nil {
				return err
			}
		}
	}
	_ = p.task.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  runID,
		State:      contracts.TaskRuntimeHealthState(status.RuntimeHealthState),
		NeedsUser:  status.RuntimeNeedsUser,
		Summary:    strings.TrimSpace(status.RuntimeSummary),
		Source:     "multi_node.remote",
		ObservedAt: now,
	})
	_ = p.task.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  runID,
		Phase:      contracts.NextActionToSemanticPhase(status.SemanticNextAction),
		Milestone:  strings.TrimSpace(status.TaskType),
		NextAction: strings.TrimSpace(status.SemanticNextAction),
		Summary:    strings.TrimSpace(status.SemanticSummary),
		ReportedAt: now,
		Payload: map[string]any{
			"remote_run_id": payload.RemoteRunID,
			"route_target":  payload.RemoteBaseURL,
		},
	})
	if localStatus == nil || localStatus.LastEventType != "remote_task_reconciled" || localStatus.LastEventNote != strings.TrimSpace(status.RuntimeSummary) {
		_ = p.task.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: runID,
			EventType: "remote_task_reconciled",
			Note:      strings.TrimSpace(status.RuntimeSummary),
			CreatedAt: now,
			Payload: map[string]any{
				"remote_run_id":       payload.RemoteRunID,
				"remote_project":      payload.RemoteProject,
				"remote_base_url":     payload.RemoteBaseURL,
				"orchestration_state": strings.TrimSpace(status.OrchestrationState),
				"runtime_health":      strings.TrimSpace(status.RuntimeHealthState),
				"semantic_next":       strings.TrimSpace(status.SemanticNextAction),
				"error_code":          strings.TrimSpace(status.ErrorCode),
				"error_message":       strings.TrimSpace(status.ErrorMessage),
			},
		})
	}
	return nil
}

func (p *Project) cancelRemoteTaskIfNeeded(ctx context.Context, runID uint) error {
	if p == nil || p.task == nil || runID == 0 {
		return nil
	}
	record, err := p.task.FindRunByID(ctx, runID)
	if err != nil || record == nil || strings.TrimSpace(record.TaskType) != contracts.TaskTypeDevRemote {
		return err
	}
	payload, ok := parseRemoteTaskProxyPayload(record.RequestPayloadJSON)
	if !ok || payload.RemoteRunID == 0 || strings.TrimSpace(payload.RemoteBaseURL) == "" {
		return nil
	}
	client, err := NewDaemonAPIClient(DaemonAPIClientConfig{BaseURL: payload.RemoteBaseURL})
	if err != nil {
		return err
	}
	_, err = client.CancelRun(ctx, payload.RemoteRunID)
	return err
}

func (p *Project) cancelRemoteRunIfNeeded(ctx context.Context, runID uint) error {
	if p == nil || p.task == nil || runID == 0 {
		return nil
	}
	record, err := p.task.FindRunByID(ctx, runID)
	if err != nil || record == nil || strings.TrimSpace(record.TaskType) != contracts.TaskTypeRunVerify {
		return err
	}
	payload, ok := parseRemoteTaskProxyPayload(record.RequestPayloadJSON)
	if !ok || payload.Role != string(TaskRequestRoleRun) || payload.RemoteRunID == 0 || strings.TrimSpace(payload.RemoteBaseURL) == "" {
		return nil
	}
	client, err := NewDaemonAPIClient(DaemonAPIClientConfig{BaseURL: payload.RemoteBaseURL})
	if err != nil {
		return err
	}
	_, err = client.CancelRun(ctx, payload.RemoteRunID)
	return err
}

func parseRemoteTaskProxyPayload(raw string) (remoteTaskProxyPayload, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return remoteTaskProxyPayload{}, false
	}
	var payload remoteTaskProxyPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return remoteTaskProxyPayload{}, false
	}
	return payload, true
}

func deriveRemoteRunStatus(status *DaemonRunStatus) contracts.RunStatus {
	if status == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(status.OrchestrationState)) {
	case string(contracts.TaskPending):
		return contracts.RunQueued
	case string(contracts.TaskRunning):
		return contracts.RunRunning
	case string(contracts.TaskSucceeded):
		return contracts.RunSucceeded
	case string(contracts.TaskFailed):
		return contracts.RunFailed
	case string(contracts.TaskCanceled):
		return contracts.RunCanceled
	default:
		return ""
	}
}

func (p *Project) GetTaskRouteInfo(ctx context.Context, runID uint) (TaskRouteInfo, bool, error) {
	if p == nil || p.task == nil || runID == 0 {
		return TaskRouteInfo{}, false, nil
	}
	record, err := p.task.FindRunByID(ctx, runID)
	if err != nil {
		return TaskRouteInfo{}, false, err
	}
	if record == nil {
		return TaskRouteInfo{}, false, nil
	}
	payload, ok := parseRemoteTaskProxyPayload(record.RequestPayloadJSON)
	if !ok {
		return TaskRouteInfo{}, false, nil
	}
	info := TaskRouteInfo{
		Role:        strings.TrimSpace(payload.Role),
		RouteMode:   firstNonEmpty(payload.RouteMode, "remote"),
		RouteTarget: strings.TrimSpace(payload.RemoteBaseURL),
		RemoteRunID: payload.RemoteRunID,
		RequestID:   strings.TrimSpace(payload.RequestID),
	}
	if events, err := p.task.ListEvents(ctx, runID, 20); err == nil {
		for i := len(events) - 1; i >= 0; i-- {
			ev := events[i]
			if strings.TrimSpace(ev.EventType) != "task_request_routed" {
				continue
			}
			payloadMap := map[string]any(ev.PayloadJSON)
			info.RoleSource = strings.TrimSpace(fmt.Sprint(payloadMap["role_source"]))
			info.RouteReason = strings.TrimSpace(fmt.Sprint(payloadMap["route_reason"]))
			break
		}
	}
	if info.Role == "" {
		return TaskRouteInfo{}, false, nil
	}
	return info, true, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
