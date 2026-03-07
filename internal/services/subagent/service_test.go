package subagent

import (
	"context"
	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
	"dalek/internal/repo"
	"dalek/internal/services/core"
	tasksvc "dalek/internal/services/task"
	"dalek/internal/store"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeTaskRunner struct {
	runFn func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error)
}

func (f fakeTaskRunner) Run(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
	if f.runFn == nil {
		return sdkrunner.Result{}, nil
	}
	return f.runFn(ctx, req, onEvent)
}

func newSubagentServiceForTest(t *testing.T) (*Service, *tasksvc.Service, *core.Project) {
	t.Helper()

	homeRoot := t.TempDir()
	repoRoot := filepath.Join(homeRoot, "repo")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("mkdir repo failed: %v", err)
	}
	worktreesDir := filepath.Join(homeRoot, "worktrees", "demo", "tickets")
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatalf("mkdir worktrees failed: %v", err)
	}

	dbPath := filepath.Join(homeRoot, "dalek.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	project := &core.Project{
		Name:         "demo",
		Key:          "demo",
		RepoRoot:     repoRoot,
		Layout:       repo.NewLayout(repoRoot),
		WorktreesDir: worktreesDir,
		Config:       repo.Config{},
		DB:           db,
		Logger:       core.DiscardLogger(),
	}
	taskService := tasksvc.New(db)
	return New(project, taskService, nil), taskService, project
}

func TestService_Submit_Idempotent(t *testing.T) {
	svc, _, _ := newSubagentServiceForTest(t)

	first, err := svc.Submit(context.Background(), SubmitInput{
		RequestID: "subagent_req_1",
		Prompt:    "  实现一个测试  ",
	})
	if err != nil {
		t.Fatalf("Submit first failed: %v", err)
	}
	if first.TaskRunID == 0 {
		t.Fatalf("expected non-zero task run id")
	}
	if strings.TrimSpace(first.RuntimeDir) == "" {
		t.Fatalf("runtime dir should not be empty")
	}

	second, err := svc.Submit(context.Background(), SubmitInput{
		RequestID: "subagent_req_1",
		Prompt:    "实现一个测试",
	})
	if err != nil {
		t.Fatalf("Submit second failed: %v", err)
	}
	if second.TaskRunID != first.TaskRunID {
		t.Fatalf("idempotent submit should reuse task run id: first=%d second=%d", first.TaskRunID, second.TaskRunID)
	}
	if second.RequestID != first.RequestID {
		t.Fatalf("request id should remain unchanged: first=%s second=%s", first.RequestID, second.RequestID)
	}
}

func TestService_Run_SucceededWritesArtifacts(t *testing.T) {
	svc, taskService, _ := newSubagentServiceForTest(t)

	submission, err := svc.Submit(context.Background(), SubmitInput{
		RequestID: "subagent_run_success",
		Prompt:    "请输出 success",
	})
	if err != nil {
		t.Fatalf("Submit failed: %v", err)
	}

	svc.runner = fakeTaskRunner{runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
		if got := strings.TrimSpace(req.Env["DALEK_SUBAGENT_RUN_ID"]); got == "" {
			t.Fatalf("missing DALEK_SUBAGENT_RUN_ID")
		}
		if onEvent != nil {
			onEvent(sdkrunner.Event{Type: "message", Text: "hello", RawJSON: `{"type":"message","text":"hello"}`, SessionID: "sess-1"})
		}
		return sdkrunner.Result{
			Provider:   "codex",
			OutputMode: sdkrunner.OutputModeJSONL,
			Text:       "done",
			SessionID:  "sess-1",
			Stdout:     "ok",
			Events: []sdkrunner.Event{
				{Type: "message", Text: "hello", SessionID: "sess-1"},
			},
		}, nil
	}}

	if err := svc.Run(context.Background(), submission.TaskRunID, RunInput{RunnerID: "runner-1"}); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	status, err := taskService.GetStatusByRunID(context.Background(), submission.TaskRunID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.OrchestrationState != string(contracts.TaskSucceeded) {
		t.Fatalf("expected succeeded state, got=%+v", status)
	}

	promptBytes, err := os.ReadFile(filepath.Join(submission.RuntimeDir, "prompt.txt"))
	if err != nil {
		t.Fatalf("read prompt.txt failed: %v", err)
	}
	if !strings.Contains(string(promptBytes), "请输出 success") {
		t.Fatalf("prompt.txt missing prompt content")
	}

	streamBytes, err := os.ReadFile(filepath.Join(submission.RuntimeDir, "stream.log"))
	if err != nil {
		t.Fatalf("read stream.log failed: %v", err)
	}
	if !strings.Contains(string(streamBytes), "subagent start") {
		t.Fatalf("stream.log missing start line")
	}
	if !strings.Contains(string(streamBytes), "subagent succeeded") {
		t.Fatalf("stream.log missing success line")
	}

	resultBytes, err := os.ReadFile(filepath.Join(submission.RuntimeDir, "result.json"))
	if err != nil {
		t.Fatalf("read result.json failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(resultBytes, &result); err != nil {
		t.Fatalf("parse result.json failed: %v", err)
	}
	if got := strings.TrimSpace(result["status"].(string)); got != "succeeded" {
		t.Fatalf("unexpected result status: %s", got)
	}
}

func TestService_Run_ErrorBranches(t *testing.T) {
	tests := []struct {
		name          string
		runErr        error
		expectState   contracts.TaskOrchestrationState
		expectStatus  string
		expectErrCode string
	}{
		{
			name:          "failed",
			runErr:        errors.New("runner boom"),
			expectState:   contracts.TaskFailed,
			expectStatus:  "failed",
			expectErrCode: "agent_exit_failed",
		},
		{
			name:          "canceled",
			runErr:        context.Canceled,
			expectState:   contracts.TaskCanceled,
			expectStatus:  "canceled",
			expectErrCode: "agent_canceled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, taskService, _ := newSubagentServiceForTest(t)
			submission, err := svc.Submit(context.Background(), SubmitInput{
				RequestID: "subagent_run_" + tc.name,
				Prompt:    "run " + tc.name,
			})
			if err != nil {
				t.Fatalf("Submit failed: %v", err)
			}

			svc.runner = fakeTaskRunner{runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
				if onEvent != nil {
					onEvent(sdkrunner.Event{Type: "message", Text: tc.name})
				}
				return sdkrunner.Result{}, tc.runErr
			}}

			err = svc.Run(context.Background(), submission.TaskRunID, RunInput{})
			if !errors.Is(err, tc.runErr) {
				t.Fatalf("Run error mismatch: got=%v want=%v", err, tc.runErr)
			}

			status, serr := taskService.GetStatusByRunID(context.Background(), submission.TaskRunID)
			if serr != nil {
				t.Fatalf("GetStatusByRunID failed: %v", serr)
			}
			if status == nil || status.OrchestrationState != string(tc.expectState) {
				t.Fatalf("unexpected orchestration state: got=%+v want=%s", status, tc.expectState)
			}

			resultBytes, rerr := os.ReadFile(filepath.Join(submission.RuntimeDir, "result.json"))
			if rerr != nil {
				t.Fatalf("read result.json failed: %v", rerr)
			}
			var result map[string]any
			if jerr := json.Unmarshal(resultBytes, &result); jerr != nil {
				t.Fatalf("parse result.json failed: %v", jerr)
			}
			if got := strings.TrimSpace(result["status"].(string)); got != tc.expectStatus {
				t.Fatalf("unexpected result status: got=%s want=%s", got, tc.expectStatus)
			}
			if got := strings.TrimSpace(result["error_code"].(string)); got != tc.expectErrCode {
				t.Fatalf("unexpected error_code: got=%s want=%s", got, tc.expectErrCode)
			}
		})
	}
}

func TestResolveAgentSettings_DefaultsToCodex(t *testing.T) {
	svc, _, _ := newSubagentServiceForTest(t)
	got, err := svc.resolveAgentSettings("", "")
	if err != nil {
		t.Fatalf("resolveAgentSettings failed: %v", err)
	}
	if got.Provider != agentprovider.ProviderCodex {
		t.Fatalf("unexpected provider: %q", got.Provider)
	}
	if got.Model != agentprovider.DefaultModel(agentprovider.ProviderCodex) {
		t.Fatalf("unexpected model: %q", got.Model)
	}
	if got.ReasoningEffort != agentprovider.DefaultReasoningEffort(agentprovider.ProviderCodex) {
		t.Fatalf("unexpected reasoning_effort: %q", got.ReasoningEffort)
	}
}

func TestResolveAgentSettings_SwitchToClaudeDoesNotInheritModel(t *testing.T) {
	svc, _, _ := newSubagentServiceForTest(t)
	got, err := svc.resolveAgentSettings("claude", "")
	if err != nil {
		t.Fatalf("resolveAgentSettings failed: %v", err)
	}
	if got.Provider != agentprovider.ProviderClaude {
		t.Fatalf("unexpected provider: %q", got.Provider)
	}
	if got.Model != "" {
		t.Fatalf("claude model should remain empty when only provider overrides, got=%q", got.Model)
	}
	if got.ReasoningEffort != "" {
		t.Fatalf("claude reasoning_effort should be empty, got=%q", got.ReasoningEffort)
	}
}

func TestResolveAgentSettings_DoesNotInheritElevatedPermissions(t *testing.T) {
	svc, _, project := newSubagentServiceForTest(t)
	project.Config.WorkerAgent = repo.AgentExecConfig{
		Provider:         "codex",
		Model:            "gpt-5.3-codex",
		ReasoningEffort:  "xhigh",
		DangerFullAccess: true,
	}

	got, err := svc.resolveAgentSettings("", "")
	if err != nil {
		t.Fatalf("resolveAgentSettings failed: %v", err)
	}
	if got.Provider != agentprovider.ProviderCodex {
		t.Fatalf("unexpected provider: %q", got.Provider)
	}
	if got.DangerFullAccess {
		t.Fatalf("subagent should not inherit danger_full_access")
	}

	project.Config.WorkerAgent = repo.AgentExecConfig{
		Provider:          "claude",
		Model:             "opus",
		BypassPermissions: true,
	}
	got, err = svc.resolveAgentSettings("", "")
	if err != nil {
		t.Fatalf("resolveAgentSettings failed: %v", err)
	}
	if got.Provider != agentprovider.ProviderClaude {
		t.Fatalf("unexpected provider after switch: %q", got.Provider)
	}
	if got.BypassPermissions {
		t.Fatalf("subagent should not inherit bypass_permissions")
	}
}
