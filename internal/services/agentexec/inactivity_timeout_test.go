package agentexec

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/agent/sdkrunner"
)

type fakeSDKRunner struct {
	runFn func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error)
}

func (f fakeSDKRunner) Run(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
	if f.runFn == nil {
		return sdkrunner.Result{}, nil
	}
	return f.runFn(ctx, req, onEvent)
}

type shellProvider struct {
	script string
}

func (p shellProvider) Name() string {
	return "shell"
}

func (p shellProvider) BuildCommand(prompt string) (string, []string) {
	_ = prompt
	return "bash", []string{"-lc", p.script}
}

func (p shellProvider) ParseOutput(stdout string) provider.ParsedOutput {
	return provider.ParsedOutput{Text: strings.TrimSpace(stdout)}
}

func TestSDKExecutor_TimeoutWithoutProgress(t *testing.T) {
	executor := NewSDKExecutor(SDKConfig{
		AgentConfig: provider.AgentConfig{Provider: "codex"},
		Runner: fakeSDKRunner{
			runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
				<-ctx.Done()
				return sdkrunner.Result{}, ctx.Err()
			},
		},
		Timeout: 20 * time.Millisecond,
	})

	handle, err := executor.Execute(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	_, err = handle.Wait(context.Background())
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got=%v", err)
	}
	if !strings.Contains(err.Error(), "without progress") {
		t.Fatalf("expected idle timeout message, got=%q", err.Error())
	}
}

func TestSDKExecutor_ProgressResetsTimeout(t *testing.T) {
	executor := NewSDKExecutor(SDKConfig{
		AgentConfig: provider.AgentConfig{Provider: "codex"},
		Runner: fakeSDKRunner{
			runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
				for i := 0; i < 3; i++ {
					if onEvent != nil {
						onEvent(sdkrunner.Event{Type: "message", Text: "tick"})
					}
					time.Sleep(15 * time.Millisecond)
				}
				return sdkrunner.Result{Provider: "codex", Text: "ok"}, nil
			},
		},
		Timeout: 20 * time.Millisecond,
	})

	handle, err := executor.Execute(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	res, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if strings.TrimSpace(res.Parsed.Text) != "ok" {
		t.Fatalf("unexpected parsed text: %q", res.Parsed.Text)
	}
}

func TestProcessExecutor_TimeoutWithoutOutput(t *testing.T) {
	executor := NewProcessExecutor(ProcessConfig{
		Provider: shellProvider{script: "sleep 0.06"},
		Timeout:  20 * time.Millisecond,
	})

	handle, err := executor.Execute(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	_, err = handle.Wait(context.Background())
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got=%v", err)
	}
	if !strings.Contains(err.Error(), "without progress") {
		t.Fatalf("expected idle timeout message, got=%q", err.Error())
	}
}

func TestProcessExecutor_ProgressResetsTimeout(t *testing.T) {
	executor := NewProcessExecutor(ProcessConfig{
		Provider: shellProvider{script: "for i in 1 2 3; do echo tick; sleep 0.02; done"},
		Timeout:  30 * time.Millisecond,
	})

	handle, err := executor.Execute(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	res, err := handle.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if !strings.Contains(res.Stdout, "tick") {
		t.Fatalf("expected stdout contains progress output, got=%q", res.Stdout)
	}
}
