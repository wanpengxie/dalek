package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	claude "github.com/wanpengxie/go-claude-agent-sdk"
)

type fakeRecordedConnectClaudeClient struct {
	connectCtx context.Context
	sessionID  string
}

func (f *fakeRecordedConnectClaudeClient) Connect(ctx context.Context) error {
	f.connectCtx = ctx
	return nil
}

func (f *fakeRecordedConnectClaudeClient) QueryWithSession(ctx context.Context, prompt string, sessionID string) error {
	_ = ctx
	_ = prompt
	f.sessionID = sessionID
	return nil
}

func (f *fakeRecordedConnectClaudeClient) ReceiveResponseWithErrors(ctx context.Context) (<-chan claude.Message, <-chan error) {
	_ = ctx
	msgs := make(chan claude.Message, 1)
	errs := make(chan error, 1)
	msgs <- &claude.ResultMessage{
		Subtype:          "success",
		DurationMS:       1,
		DurationAPIMS:    1,
		IsError:          false,
		NumTurns:         1,
		SessionID:        f.sessionID,
		TotalCostUSD:     nil,
		Usage:            map[string]any{},
		Result:           "ok",
		StructuredOutput: nil,
	}
	close(msgs)
	close(errs)
	return msgs, errs
}

func (f *fakeRecordedConnectClaudeClient) Interrupt(ctx context.Context) error {
	_ = ctx
	return nil
}

func (f *fakeRecordedConnectClaudeClient) Close() error { return nil }

func TestClaudeChatRunner_ConnectUsesTurnContextAndReusesClient(t *testing.T) {
	origFactory := createClaudeClient
	t.Cleanup(func() { createClaudeClient = origFactory })

	fakeClient := &fakeRecordedConnectClaudeClient{}
	createClaudeClient = func(opts ...claude.Option) claudeClient {
		_ = opts
		return fakeClient
	}

	r := &claudeChatRunner{}

	firstCtx, firstCancel := context.WithCancel(context.Background())
	res1, err := r.RunTurn(firstCtx, ChatRunRequest{
		Prompt:    "hello-1",
		SessionID: "sess-1",
	}, nil)
	if err != nil {
		t.Fatalf("first turn failed: %v", err)
	}
	if res1.Text != "ok" {
		t.Fatalf("first turn text mismatch: %q", res1.Text)
	}
	if fakeClient.connectCtx != firstCtx {
		t.Fatalf("connect should use turn context")
	}
	firstCancel()

	time.Sleep(10 * time.Millisecond)

	secondCtx, secondCancel := context.WithCancel(context.Background())
	defer secondCancel()
	res2, err := r.RunTurn(secondCtx, ChatRunRequest{
		Prompt:    "hello-2",
		SessionID: "sess-1",
	}, nil)
	if err != nil {
		t.Fatalf("second turn failed: %v", err)
	}
	if res2.Text != "ok" {
		t.Fatalf("second turn should still produce text, got=%q", res2.Text)
	}
}

type fakeRetryClaudeClient struct {
	queryErr  error
	sessionID string
}

func (f *fakeRetryClaudeClient) Connect(ctx context.Context) error {
	_ = ctx
	return nil
}

func (f *fakeRetryClaudeClient) QueryWithSession(ctx context.Context, prompt string, sessionID string) error {
	_ = ctx
	_ = prompt
	f.sessionID = sessionID
	if f.queryErr != nil {
		return f.queryErr
	}
	return nil
}

func (f *fakeRetryClaudeClient) ReceiveResponseWithErrors(ctx context.Context) (<-chan claude.Message, <-chan error) {
	_ = ctx
	msgs := make(chan claude.Message, 1)
	errs := make(chan error, 1)
	msgs <- &claude.ResultMessage{
		Subtype:       "success",
		SessionID:     f.sessionID,
		Result:        "ok",
		Usage:         map[string]any{},
		DurationMS:    1,
		DurationAPIMS: 1,
		NumTurns:      1,
	}
	close(msgs)
	close(errs)
	return msgs, errs
}

func (f *fakeRetryClaudeClient) Interrupt(ctx context.Context) error {
	_ = ctx
	return nil
}

func (f *fakeRetryClaudeClient) Close() error { return nil }

func TestClaudeChatRunner_ReconnectAfterInactiveConnectionError(t *testing.T) {
	origFactory := createClaudeClient
	t.Cleanup(func() { createClaudeClient = origFactory })

	created := 0
	createClaudeClient = func(opts ...claude.Option) claudeClient {
		_ = opts
		created++
		if created == 1 {
			return &fakeRetryClaudeClient{queryErr: errors.New("Connection is no longer active")}
		}
		return &fakeRetryClaudeClient{}
	}

	r := &claudeChatRunner{}
	_, err := r.RunTurn(context.Background(), ChatRunRequest{
		Prompt:    "hello-1",
		SessionID: "sess-1",
	}, nil)
	if err == nil {
		t.Fatalf("first turn should fail on inactive connection")
	}

	res2, err := r.RunTurn(context.Background(), ChatRunRequest{
		Prompt:    "hello-2",
		SessionID: "sess-1",
	}, nil)
	if err != nil {
		t.Fatalf("second turn should reconnect and succeed: %v", err)
	}
	if res2.Text != "ok" {
		t.Fatalf("second turn text mismatch: %q", res2.Text)
	}
	if created != 2 {
		t.Fatalf("expected client recreation after failure, got created=%d", created)
	}
}

func TestClaudeChatRunner_CanUseTool_AutoAllowsLowRiskWithoutApprovalCallback(t *testing.T) {
	r := &claudeChatRunner{}
	result, err := r.canUseTool(context.Background(), "Bash", map[string]any{
		"command": "dalek ticket ls",
	}, claude.ToolPermissionContext{})
	if err != nil {
		t.Fatalf("canUseTool should not return error, got=%v", err)
	}
	if _, ok := result.(*claude.PermissionResultAllow); !ok {
		t.Fatalf("low-risk command should be auto allowed, got=%T", result)
	}
}

func TestClaudeChatRunner_CanUseTool_LowRiskBypassesManualApproval(t *testing.T) {
	r := &claudeChatRunner{}
	called := false
	r.setToolApproval(func(ctx context.Context, toolName string, input map[string]any) (bool, error) {
		called = true
		return false, nil
	})
	defer r.setToolApproval(nil)

	result, err := r.canUseTool(context.Background(), "Bash", map[string]any{
		"command": "dalek ticket ls && dalek inbox ls",
	}, claude.ToolPermissionContext{})
	if err != nil {
		t.Fatalf("canUseTool should not return error, got=%v", err)
	}
	if _, ok := result.(*claude.PermissionResultAllow); !ok {
		t.Fatalf("low-risk command should be auto allowed, got=%T", result)
	}
	if called {
		t.Fatalf("low-risk command should not invoke manual approval callback")
	}
}

func TestDecorateClaudeRunnerError_AppendsStderr(t *testing.T) {
	err := decorateClaudeRunnerError(errors.New("exit code 1"), "line-1\nline-2")
	if err == nil {
		t.Fatalf("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "exit code 1") {
		t.Fatalf("missing original error: %q", msg)
	}
	if !strings.Contains(msg, "stderr: line-1\nline-2") {
		t.Fatalf("missing stderr detail: %q", msg)
	}
}

func TestClaudeChatRunner_CanUseTool_HighRiskRequiresManualApproval(t *testing.T) {
	r := &claudeChatRunner{}
	called := false
	r.setToolApproval(func(ctx context.Context, toolName string, input map[string]any) (bool, error) {
		called = true
		return false, nil
	})
	defer r.setToolApproval(nil)

	result, err := r.canUseTool(context.Background(), "Bash", map[string]any{
		"command": "docker ps",
	}, claude.ToolPermissionContext{})
	if err != nil {
		t.Fatalf("canUseTool should not return error, got=%v", err)
	}
	if _, ok := result.(*claude.PermissionResultDeny); !ok {
		t.Fatalf("high-risk command should be denied when approval rejects, got=%T", result)
	}
	if !called {
		t.Fatalf("high-risk command should invoke manual approval callback")
	}
}

func TestChatRunnerClaudeSettingsJSON_DeniesGitPushAndRM(t *testing.T) {
	rules := func(raw any) []string {
		arr, _ := raw.([]any)
		out := make([]string, 0, len(arr))
		for _, item := range arr {
			s := strings.TrimSpace(fmt.Sprint(item))
			if s != "" && s != "<nil>" {
				out = append(out, s)
			}
		}
		return out
	}

	raw := chatRunnerClaudeSettingsJSON()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatalf("chat settings should be valid json: %v", err)
	}
	perms, _ := parsed["permissions"].(map[string]any)
	if perms == nil {
		t.Fatalf("permissions missing")
	}
	deny := rules(perms["deny"])
	ask := rules(perms["ask"])

	denySet := map[string]bool{}
	for _, item := range deny {
		denySet[strings.ToLower(strings.TrimSpace(item))] = true
	}
	if !denySet["bash(git push*)"] {
		t.Fatalf("deny should include git push, got=%v", deny)
	}
	if !denySet["bash(rm *)"] {
		t.Fatalf("deny should include rm, got=%v", deny)
	}
	for _, item := range ask {
		low := strings.ToLower(strings.TrimSpace(item))
		if strings.HasPrefix(low, "bash(git push") {
			t.Fatalf("ask should not include git push rule, got=%v", ask)
		}
		if strings.HasPrefix(low, "bash(rm") {
			t.Fatalf("ask should not include rm rule, got=%v", ask)
		}
	}
}

func TestClaudeChatRunner_CanUseTool_AskSuggestionRequiresManualApproval(t *testing.T) {
	r := &claudeChatRunner{}
	called := false
	r.setToolApproval(func(ctx context.Context, toolName string, input map[string]any) (bool, error) {
		called = true
		return false, nil
	})
	defer r.setToolApproval(nil)

	result, err := r.canUseTool(context.Background(), "Bash", map[string]any{
		"command": "dalek ticket ls",
	}, claude.ToolPermissionContext{
		Suggestions: []claude.PermissionUpdate{
			{
				Type:     claude.PermissionUpdateAddRules,
				Behavior: claude.PermissionBehaviorAsk,
			},
		},
	})
	if err != nil {
		t.Fatalf("canUseTool should not return error, got=%v", err)
	}
	if _, ok := result.(*claude.PermissionResultDeny); !ok {
		t.Fatalf("ask suggestion should force callback path, got=%T", result)
	}
	if !called {
		t.Fatalf("ask suggestion should invoke manual approval callback")
	}
}
