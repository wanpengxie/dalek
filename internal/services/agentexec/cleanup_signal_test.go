package agentexec

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

// TestLifecycleFinish_WithCanceledParentCtx 验证当调用方 context 已被 cancel 时
// （模拟 sh -c SIGHUP 场景），Finish 仍然能通过独立超时 context 完成 DB 写入。
func TestLifecycleFinish_WithCanceledParentCtx(t *testing.T) {
	rt := &fakeLifecycleRuntime{}
	tracker := NewRunLifecycleTracker(BaseConfig{
		Runtime:     rt,
		OwnerType:   contracts.TaskOwnerWorker,
		TaskType:    contracts.TaskTypeDeliverTicket,
		ProjectKey:  "test",
		TicketID:    99,
		WorkerID:    88,
		SubjectType: "ticket",
		SubjectID:   "99",
	})

	if _, err := tracker.Start(context.Background(), `{"provider":"sdk"}`, "sdk:test", nil, "started"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 模拟 parent context 已被 cancel（如 SIGHUP 导致）
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// Finish 应该仍然能完成写入
	tracker.Finish(canceledCtx, AgentRunResult{ExitCode: 0}, nil, "success despite canceled ctx")

	if len(rt.succeeded) != 1 {
		t.Fatalf("expected 1 succeeded call even with canceled parent ctx, got=%d", len(rt.succeeded))
	}
}

// TestLifecycleFinish_CanceledError_WithCanceledParentCtx 验证 cancel 错误+已 cancel 的 ctx 场景。
func TestLifecycleFinish_CanceledError_WithCanceledParentCtx(t *testing.T) {
	rt := &fakeLifecycleRuntime{}
	tracker := NewRunLifecycleTracker(BaseConfig{
		Runtime:     rt,
		OwnerType:   contracts.TaskOwnerWorker,
		TaskType:    contracts.TaskTypeDeliverTicket,
		ProjectKey:  "test",
		TicketID:    99,
		WorkerID:    88,
		SubjectType: "ticket",
		SubjectID:   "99",
	})

	if _, err := tracker.Start(context.Background(), `{"provider":"sdk"}`, "sdk:test", nil, "started"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// 模拟 agent 因 context canceled 而退出
	tracker.Finish(canceledCtx, AgentRunResult{ExitCode: 1, Stderr: "signal: hangup"}, context.Canceled, "")

	if len(rt.canceled) != 1 {
		t.Fatalf("expected 1 canceled call, got=%d", len(rt.canceled))
	}
	if rt.canceled[0].code != "agent_canceled" {
		t.Fatalf("unexpected canceled code: %q", rt.canceled[0].code)
	}
}

// TestLifecycleFinish_FailedRun_WithCanceledParentCtx 验证当 parent ctx 被 cancel 且 agent 退出码非零时，
// 应标记为 canceled（SIGHUP 场景下，context cancel 优先于 exit code）。
func TestLifecycleFinish_FailedRun_WithCanceledParentCtx(t *testing.T) {
	rt := &fakeLifecycleRuntime{}
	tracker := NewRunLifecycleTracker(BaseConfig{
		Runtime:     rt,
		OwnerType:   contracts.TaskOwnerWorker,
		TaskType:    contracts.TaskTypeDeliverTicket,
		ProjectKey:  "test",
		TicketID:    99,
		WorkerID:    88,
		SubjectType: "ticket",
		SubjectID:   "99",
	})

	if _, err := tracker.Start(context.Background(), `{"provider":"sdk"}`, "sdk:test", nil, "started"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	// 当 parent ctx 被 cancel 但 execErr 不是 cancel 错误时，
	// Finish 仍会检测到 ctx.Err() 是 Canceled 并路由到 canceled 分支。
	// 这是正确行为：SIGHUP 导致的退出应被视为 cancel，而非 failure。
	tracker.Finish(canceledCtx, AgentRunResult{ExitCode: 1, Stderr: "panic: nil pointer"}, nil, "")

	if len(rt.canceled) != 1 {
		t.Fatalf("expected 1 canceled call (ctx cancel takes priority over exit code), got canceled=%d failed=%d",
			len(rt.canceled), len(rt.failed))
	}
	if !strings.Contains(rt.canceled[0].msg, "panic: nil pointer") {
		t.Fatalf("expected canceled message to contain stderr, got=%q", rt.canceled[0].msg)
	}
}

// TestLifecycleFinish_FailedRun_WithActiveCtx 验证正常 context 下 agent 失败会正确标记为 failed。
func TestLifecycleFinish_FailedRun_WithActiveCtx(t *testing.T) {
	rt := &fakeLifecycleRuntime{}
	tracker := NewRunLifecycleTracker(BaseConfig{
		Runtime:     rt,
		OwnerType:   contracts.TaskOwnerWorker,
		TaskType:    contracts.TaskTypeDeliverTicket,
		ProjectKey:  "test",
		TicketID:    99,
		WorkerID:    88,
		SubjectType: "ticket",
		SubjectID:   "99",
	})

	if _, err := tracker.Start(context.Background(), `{"provider":"sdk"}`, "sdk:test", nil, "started"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// 使用活跃的 context（非 canceled），模拟正常 agent 失败
	tracker.Finish(context.Background(), AgentRunResult{ExitCode: 1, Stderr: "panic: nil pointer"}, nil, "")

	if len(rt.failed) != 1 {
		t.Fatalf("expected 1 failed call, got=%d", len(rt.failed))
	}
	if !strings.Contains(rt.failed[0].msg, "panic: nil pointer") {
		t.Fatalf("expected failure message to contain stderr, got=%q", rt.failed[0].msg)
	}
}

// TestProcessHandle_Wait_SignalCancel 验证 processHandle.Wait 在 context cancel 时能快速退出并取消进程。
func TestProcessHandle_Wait_SignalCancel(t *testing.T) {
	h := &processHandle{
		runID:  42,
		doneCh: make(chan struct{}), // 永不关闭，模拟进程永不结束
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := h.Wait(ctx)
	elapsed := time.Since(started)

	if err == nil {
		t.Fatal("expected error from Wait when context is canceled")
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("Wait should exit quickly on context cancel, took=%s", elapsed)
	}
}
