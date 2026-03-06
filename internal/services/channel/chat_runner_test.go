package channel

import (
	"context"
	"errors"
	"sync"
	"testing"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/services/channel/agentcli"
)

func TestChatRunnerManager_ClaudeStatefulReuseByConversation(t *testing.T) {
	origFactory := createClaudeChatRunner
	t.Cleanup(func() { createClaudeChatRunner = origFactory })

	var (
		mu         sync.Mutex
		createCnt  int
		created    []*fakeChatRunner
		nextResult = ChatRunResult{Text: "ok", SessionID: "sess-1", OutputMode: agentcli.OutputJSON}
	)
	createClaudeChatRunner = func(ctx context.Context, req ChatRunRequest) (ChatRunner, error) {
		mu.Lock()
		defer mu.Unlock()
		createCnt++
		r := &fakeChatRunner{result: nextResult}
		created = append(created, r)
		return r, nil
	}

	manager := newDefaultChatRunnerManager(nil)
	req := ChatRunRequest{
		ConversationID: "conv-1",
		Provider:       "claude",
		Model:          "m1",
		Prompt:         "hello",
	}
	if _, err := manager.RunTurn(context.Background(), req, nil); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if _, err := manager.RunTurn(context.Background(), req, nil); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if createCnt != 1 {
		t.Fatalf("expected create count=1, got=%d", createCnt)
	}
	if created[0].turnCount != 2 {
		t.Fatalf("expected same runner reused for 2 turns, got=%d", created[0].turnCount)
	}

	req.Model = "m2"
	if _, err := manager.RunTurn(context.Background(), req, nil); err != nil {
		t.Fatalf("run3 failed: %v", err)
	}
	if createCnt != 2 {
		t.Fatalf("expected recreate after signature change, got=%d", createCnt)
	}
	if created[0].closedCount != 1 {
		t.Fatalf("old runner should be closed once, got=%d", created[0].closedCount)
	}

	manager.CloseConversation("conv-1")
	if created[1].closedCount != 1 {
		t.Fatalf("runner should be closed by CloseConversation, got=%d", created[1].closedCount)
	}
}

func TestChatRunnerManager_GeminiStatefulReuseByConversation(t *testing.T) {
	origFactory := createGeminiChatRunner
	t.Cleanup(func() { createGeminiChatRunner = origFactory })

	var (
		mu         sync.Mutex
		createCnt  int
		created    []*fakeChatRunner
		nextResult = ChatRunResult{Text: "ok", SessionID: "sess-1", OutputMode: agentcli.OutputJSON}
	)
	createGeminiChatRunner = func(ctx context.Context, req ChatRunRequest) (ChatRunner, error) {
		mu.Lock()
		defer mu.Unlock()
		createCnt++
		r := &fakeChatRunner{result: nextResult}
		created = append(created, r)
		return r, nil
	}

	manager := newDefaultChatRunnerManager(nil)
	req := ChatRunRequest{
		ConversationID: "conv-1",
		Provider:       "gemini",
		Model:          "gemini-2.5-pro",
		Prompt:         "hello",
	}
	if _, err := manager.RunTurn(context.Background(), req, nil); err != nil {
		t.Fatalf("run1 failed: %v", err)
	}
	if _, err := manager.RunTurn(context.Background(), req, nil); err != nil {
		t.Fatalf("run2 failed: %v", err)
	}
	if createCnt != 1 {
		t.Fatalf("expected create count=1, got=%d", createCnt)
	}
	if created[0].turnCount != 2 {
		t.Fatalf("expected same runner reused for 2 turns, got=%d", created[0].turnCount)
	}
}

func TestChatRunnerManager_StatelessUsesTaskRunner(t *testing.T) {
	taskRunner := &fakeTaskRunner{
		result: sdkrunner.Result{
			Provider:   "codex",
			OutputMode: sdkrunner.OutputModeJSONL,
			Text:       "final",
			SessionID:  "thread-1",
			Events: []sdkrunner.Event{
				{Type: "message.delta", Text: "thinking"},
				{Type: "message", Text: "final"},
			},
		},
	}
	manager := newDefaultChatRunnerManager(taskRunner)
	captured := make([]agentcli.Event, 0, 2)
	res, err := manager.RunTurn(context.Background(), ChatRunRequest{
		ConversationID: "conv-stateless",
		Provider:       "codex",
		Prompt:         "hello",
	}, func(ev agentcli.Event) {
		captured = append(captured, ev)
	})
	if err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}
	if taskRunner.callCount != 1 {
		t.Fatalf("task runner call count mismatch: %d", taskRunner.callCount)
	}
	if res.OutputMode != agentcli.OutputJSONL {
		t.Fatalf("unexpected output mode: %s", res.OutputMode)
	}
	if len(captured) != 2 || captured[0].Text != "thinking" {
		t.Fatalf("unexpected streamed events: %#v", captured)
	}
}

func TestChatRunnerManager_EvictStatefulOnConnectionError(t *testing.T) {
	origFactory := createClaudeChatRunner
	t.Cleanup(func() { createClaudeChatRunner = origFactory })

	var createCnt int
	createClaudeChatRunner = func(ctx context.Context, req ChatRunRequest) (ChatRunner, error) {
		createCnt++
		if createCnt == 1 {
			return &fakeChatRunner{runErr: errors.New("broken pipe")}, nil
		}
		return &fakeChatRunner{result: ChatRunResult{Text: "ok"}}, nil
	}
	manager := newDefaultChatRunnerManager(nil)
	req := ChatRunRequest{
		ConversationID: "conv-err",
		Provider:       "claude",
		Prompt:         "hello",
	}
	if _, err := manager.RunTurn(context.Background(), req, nil); err == nil {
		t.Fatalf("first turn should fail")
	}
	if _, err := manager.RunTurn(context.Background(), req, nil); err != nil {
		t.Fatalf("second turn should recover, err=%v", err)
	}
	if createCnt != 2 {
		t.Fatalf("runner should be recreated after connection error, got=%d", createCnt)
	}
}

func TestChatRunnerManager_InterruptConversation_StatefulRunner(t *testing.T) {
	origFactory := createClaudeChatRunner
	t.Cleanup(func() { createClaudeChatRunner = origFactory })

	runner := &fakeChatRunner{
		result:      ChatRunResult{Text: "ok"},
		interruptOK: true,
	}
	createClaudeChatRunner = func(ctx context.Context, req ChatRunRequest) (ChatRunner, error) {
		return runner, nil
	}

	manager := newDefaultChatRunnerManager(nil)
	if _, err := manager.RunTurn(context.Background(), ChatRunRequest{
		ConversationID: "conv-interrupt",
		Provider:       "claude",
		Prompt:         "hello",
	}, nil); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	interrupted, err := manager.InterruptConversation(context.Background(), "conv-interrupt")
	if err != nil {
		t.Fatalf("InterruptConversation failed: %v", err)
	}
	if !interrupted {
		t.Fatalf("expected interrupted=true")
	}
	if runner.interruptCount != 1 {
		t.Fatalf("expected interrupt count=1, got=%d", runner.interruptCount)
	}
}

func TestChatRunnerManager_ForceCloseConversation_StatefulRunner(t *testing.T) {
	origFactory := createClaudeChatRunner
	t.Cleanup(func() { createClaudeChatRunner = origFactory })

	runner := &fakeChatRunner{
		result:       ChatRunResult{Text: "ok"},
		forceCloseOK: true,
	}
	createClaudeChatRunner = func(ctx context.Context, req ChatRunRequest) (ChatRunner, error) {
		return runner, nil
	}

	manager := newDefaultChatRunnerManager(nil)
	if _, err := manager.RunTurn(context.Background(), ChatRunRequest{
		ConversationID: "conv-force-close",
		Provider:       "claude",
		Prompt:         "hello",
	}, nil); err != nil {
		t.Fatalf("RunTurn failed: %v", err)
	}

	if err := manager.ForceCloseConversation("conv-force-close"); err != nil {
		t.Fatalf("ForceCloseConversation failed: %v", err)
	}
	if runner.forceCloseCount != 1 {
		t.Fatalf("expected force close count=1, got=%d", runner.forceCloseCount)
	}
	if runner.closedCount != 0 {
		t.Fatalf("force close path should not call Close, got=%d", runner.closedCount)
	}
}

type fakeChatRunner struct {
	runErr      error
	result      ChatRunResult
	turnCount   int
	closedCount int

	interruptOK    bool
	interruptErr   error
	interruptCount int

	forceCloseOK    bool
	forceCloseErr   error
	forceCloseCount int
}

func (f *fakeChatRunner) RunTurn(ctx context.Context, req ChatRunRequest, onEvent ChatEventHandler) (ChatRunResult, error) {
	f.turnCount++
	return f.result, f.runErr
}

func (f *fakeChatRunner) Close() error {
	f.closedCount++
	return nil
}

func (f *fakeChatRunner) Interrupt(ctx context.Context) (bool, error) {
	_ = ctx
	f.interruptCount++
	if f.interruptErr != nil {
		return false, f.interruptErr
	}
	return f.interruptOK, nil
}

func (f *fakeChatRunner) ForceClose() error {
	f.forceCloseCount++
	if f.forceCloseErr != nil {
		return f.forceCloseErr
	}
	if f.forceCloseOK {
		return nil
	}
	return nil
}

type fakeTaskRunner struct {
	result    sdkrunner.Result
	runErr    error
	callCount int
}

func (f *fakeTaskRunner) Run(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
	f.callCount++
	for _, ev := range f.result.Events {
		if onEvent != nil {
			onEvent(ev)
		}
	}
	return f.result, f.runErr
}
