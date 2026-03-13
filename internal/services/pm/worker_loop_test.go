package pm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/agentexec"
)

// fakeAgentRunHandle implements agentexec.AgentRunHandle for testing.
type fakeAgentRunHandle struct {
	runID      uint
	result     agentexec.AgentRunResult
	err        error
	waitFunc   func(ctx context.Context) (agentexec.AgentRunResult, error)
	cancelFunc func() error
}

func (h *fakeAgentRunHandle) RunID() uint { return h.runID }
func (h *fakeAgentRunHandle) Wait(ctx context.Context) (agentexec.AgentRunResult, error) {
	if h.waitFunc != nil {
		return h.waitFunc(ctx)
	}
	return h.result, h.err
}
func (h *fakeAgentRunHandle) Cancel() error {
	if h.cancelFunc != nil {
		return h.cancelFunc()
	}
	return nil
}

// makeSemanticReport inserts a TaskSemanticReport row so that
// readWorkerNextActionFromRun can pick up the next_action.
func makeSemanticReport(t *testing.T, svc *Service, runID uint, nextAction string) {
	t.Helper()
	_, db, err := svc.require()
	if err != nil {
		t.Fatalf("require failed: %v", err)
	}
	payload := contracts.JSONMap{
		"source": "test",
	}
	switch strings.TrimSpace(strings.ToLower(nextAction)) {
	case string(contracts.NextDone):
		payload["head_sha"] = testWorkerDoneHeadSHA
		payload["dirty"] = false
	case string(contracts.NextWaitUser):
		payload["blockers"] = []string{"test blocker"}
		payload["needs_user"] = true
	}
	if err := db.Create(&contracts.TaskSemanticReport{
		TaskRunID:         runID,
		Phase:             contracts.TaskPhaseImplementing,
		Milestone:         "agent_report",
		NextAction:        nextAction,
		Summary:           "test",
		ReportPayloadJSON: payload,
		ReportedAt:        time.Now(),
	}).Error; err != nil {
		t.Fatalf("create semantic report failed: %v", err)
	}
}

// createWorkerLoopTestFixture sets up ticket + worker + task run + semantic report for worker_loop tests.
func createWorkerLoopTestFixture(t *testing.T, svc *Service, nextAction string) (contracts.Ticket, contracts.Worker, uint) {
	t.Helper()
	_, db, err := svc.require()
	if err != nil {
		t.Fatalf("require failed: %v", err)
	}

	tk := createTicket(t, db, "worker-loop-test")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/worker-loop-test",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	taskRun := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "test",
		TicketID:           tk.ID,
		WorkerID:           w.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          fmt.Sprintf("wrk_loop_test_%d", tk.ID),
		OrchestrationState: contracts.TaskRunning,
	}
	if err := db.Create(&taskRun).Error; err != nil {
		t.Fatalf("create task run failed: %v", err)
	}

	if nextAction != "" {
		makeSemanticReport(t, svc, taskRun.ID, nextAction)
	}
	switch strings.TrimSpace(strings.ToLower(nextAction)) {
	case string(contracts.NextDone):
		writeWorkerLoopStateForTest(t, w.WorktreePath, nextAction, "test", nil, true, testWorkerDoneHeadSHA, "clean")
	case string(contracts.NextWaitUser):
		writeWorkerLoopStateForTest(t, w.WorktreePath, nextAction, "test", []string{"test blocker"}, false, testWorkerDoneHeadSHA, "clean")
	default:
		writeWorkerLoopStateForTest(t, w.WorktreePath, nextAction, "test", nil, false, testWorkerDoneHeadSHA, "clean")
	}

	return tk, w, taskRun.ID
}

func TestExecuteWorkerLoop_StopsOnDone(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	tk, w, runID := createWorkerLoopTestFixture(t, svc, "done")

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{runID: runID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	result, err := svc.executeWorkerLoop(context.Background(), tk, w, "test prompt")
	if err != nil {
		t.Fatalf("executeWorkerLoop failed: %v", err)
	}
	if result.Stages != 1 {
		t.Fatalf("expected 1 stage, got=%d", result.Stages)
	}
	if result.LastNextAction != "done" {
		t.Fatalf("expected last_next_action=done, got=%q", result.LastNextAction)
	}
}

func TestExecuteWorkerLoop_StopsOnWaitUser(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	tk, w, runID := createWorkerLoopTestFixture(t, svc, "wait_user")

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{runID: runID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	result, err := svc.executeWorkerLoop(context.Background(), tk, w, "test prompt")
	if err != nil {
		t.Fatalf("executeWorkerLoop failed: %v", err)
	}
	if result.Stages != 1 {
		t.Fatalf("expected 1 stage, got=%d", result.Stages)
	}
	if result.LastNextAction != "wait_user" {
		t.Fatalf("expected last_next_action=wait_user, got=%q", result.LastNextAction)
	}
}

func TestExecuteWorkerLoop_EmptyNextAction_RetrySucceeds(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	_, db, err := svc.require()
	if err != nil {
		t.Fatalf("require failed: %v", err)
	}

	tk := createTicket(t, db, "worker-loop-empty-retry-success")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/worker-loop-empty-retry-success",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	run1 := createWorkerTaskRun(t, db, tk.ID, w.ID, "wrk_empty_retry_success_1")
	run2 := createWorkerTaskRun(t, db, tk.ID, w.ID, "wrk_empty_retry_success_2")
	makeSemanticReport(t, svc, run2.ID, "done")
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "test", nil, true, testWorkerDoneHeadSHA, "clean")

	var prompts []string
	var callCount atomic.Int32
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		prompts = append(prompts, prompt)
		if callCount.Add(1) == 1 {
			return &fakeAgentRunHandle{runID: run1.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
		}
		return &fakeAgentRunHandle{runID: run2.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	result, err := svc.executeWorkerLoop(context.Background(), tk, w, "test prompt")
	if err != nil {
		t.Fatalf("executeWorkerLoop failed: %v", err)
	}
	if result.Stages != 1 {
		t.Fatalf("expected 1 stage after in-stage repair, got=%d", result.Stages)
	}
	if result.LastNextAction != "done" {
		t.Fatalf("expected last_next_action=done, got=%q", result.LastNextAction)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got=%d", len(prompts))
	}
	if prompts[0] != "test prompt" {
		t.Fatalf("unexpected first prompt: %q", prompts[0])
	}
	if !strings.Contains(prompts[1], "当前 stage 尚未闭合") {
		t.Fatalf("unexpected retry prompt: %q", prompts[1])
	}

	var after contracts.Worker
	if err := db.First(&after, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if after.Status != contracts.WorkerStopped {
		t.Fatalf("expected worker stopped after successful retry, got=%s", after.Status)
	}
}

func TestExecuteWorkerLoop_DoneClosureRepairSucceedsWithinSameStage(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	_, db, err := svc.require()
	if err != nil {
		t.Fatalf("require failed: %v", err)
	}

	tk := createTicket(t, db, "worker-loop-done-closure-repair")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/worker-loop-done-closure-repair",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	run1 := createWorkerTaskRun(t, db, tk.ID, w.ID, "wrk_done_closure_repair_1")
	run2 := createWorkerTaskRun(t, db, tk.ID, w.ID, "wrk_done_closure_repair_2")
	makeSemanticReport(t, svc, run1.ID, "done")
	makeSemanticReport(t, svc, run2.ID, "done")
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "test", nil, false, testWorkerDoneHeadSHA, "clean")

	var prompts []string
	var callCount atomic.Int32
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		prompts = append(prompts, prompt)
		if callCount.Add(1) == 1 {
			return &fakeAgentRunHandle{runID: run1.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
		}
		writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "test", nil, true, testWorkerDoneHeadSHA, "clean")
		return &fakeAgentRunHandle{runID: run2.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	result, err := svc.executeWorkerLoop(context.Background(), tk, w, "test prompt")
	if err != nil {
		t.Fatalf("executeWorkerLoop failed: %v", err)
	}
	if result.Stages != 1 {
		t.Fatalf("expected 1 stage after closure repair, got=%d", result.Stages)
	}
	if result.LastNextAction != "done" {
		t.Fatalf("expected last_next_action=done, got=%q", result.LastNextAction)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got=%d", len(prompts))
	}
	if !strings.Contains(prompts[1], "当前 stage 尚未闭合") {
		t.Fatalf("unexpected repair prompt: %q", prompts[1])
	}
}

func TestExecuteWorkerLoop_EmptyNextAction_RetryExhaustedReturnsClosureFallback(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	_, db, err := svc.require()
	if err != nil {
		t.Fatalf("require failed: %v", err)
	}

	tk := createTicket(t, db, "worker-loop-empty-retry-failed")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/worker-loop-empty-retry-failed",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	run1 := createWorkerTaskRun(t, db, tk.ID, w.ID, "wrk_empty_retry_failed_1")
	run2 := createWorkerTaskRun(t, db, tk.ID, w.ID, "wrk_empty_retry_failed_2")

	var prompts []string
	var callCount atomic.Int32
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		prompts = append(prompts, prompt)
		if callCount.Add(1) == 1 {
			return &fakeAgentRunHandle{runID: run1.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
		}
		return &fakeAgentRunHandle{runID: run2.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	result, err := svc.executeWorkerLoop(context.Background(), tk, w, "test prompt")
	var closureErr *workerLoopClosureExhaustedError
	if !errors.As(err, &closureErr) {
		t.Fatalf("expected workerLoopClosureExhaustedError, got=%v", err)
	}
	if result.Stages != 1 {
		t.Fatalf("expected 1 stage after repair exhaustion, got=%d", result.Stages)
	}
	if result.LastNextAction != "" {
		t.Fatalf("expected empty last_next_action, got=%q", result.LastNextAction)
	}
	if closureErr.LastRunID != run2.ID {
		t.Fatalf("expected closure exhausted last_run_id=%d, got=%d", run2.ID, closureErr.LastRunID)
	}
	if len(prompts) != 2 {
		t.Fatalf("expected 2 prompts, got=%d", len(prompts))
	}
	if !strings.Contains(prompts[1], "当前 stage 尚未闭合") {
		t.Fatalf("unexpected retry prompt: %q", prompts[1])
	}

	var after contracts.Worker
	if err := db.First(&after, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if after.Status != contracts.WorkerStopped {
		t.Fatalf("expected worker stopped after retry exhausted, got=%s", after.Status)
	}
	if strings.TrimSpace(after.LastError) != "" {
		t.Fatalf("expected closure exhaustion not mark worker failed, got last_error=%q", after.LastError)
	}
}

func TestExecuteWorkerLoop_ContinuesThenStops(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	_, db, err := svc.require()
	if err != nil {
		t.Fatalf("require failed: %v", err)
	}

	tk := createTicket(t, db, "worker-loop-multi")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/worker-loop-multi",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// Create two task runs: first returns "continue", second returns "done".
	run1 := contracts.TaskRun{
		OwnerType: contracts.TaskOwnerWorker, TaskType: contracts.TaskTypeDeliverTicket,
		ProjectKey: "test", TicketID: tk.ID, WorkerID: w.ID,
		SubjectType: "ticket", SubjectID: fmt.Sprintf("%d", tk.ID),
		RequestID: "wrk_multi_1", OrchestrationState: contracts.TaskRunning,
	}
	if err := db.Create(&run1).Error; err != nil {
		t.Fatalf("create run1 failed: %v", err)
	}
	makeSemanticReport(t, svc, run1.ID, "continue")

	run2 := contracts.TaskRun{
		OwnerType: contracts.TaskOwnerWorker, TaskType: contracts.TaskTypeDeliverTicket,
		ProjectKey: "test", TicketID: tk.ID, WorkerID: w.ID,
		SubjectType: "ticket", SubjectID: fmt.Sprintf("%d", tk.ID),
		RequestID: "wrk_multi_2", OrchestrationState: contracts.TaskRunning,
	}
	if err := db.Create(&run2).Error; err != nil {
		t.Fatalf("create run2 failed: %v", err)
	}
	makeSemanticReport(t, svc, run2.ID, "done")
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "test", nil, true, testWorkerDoneHeadSHA, "clean")

	var callCount atomic.Int32
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		n := callCount.Add(1)
		if n == 1 {
			return &fakeAgentRunHandle{runID: run1.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
		}
		return &fakeAgentRunHandle{runID: run2.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	result, err := svc.executeWorkerLoop(context.Background(), tk, w, "initial prompt")
	if err != nil {
		t.Fatalf("executeWorkerLoop failed: %v", err)
	}
	if result.Stages != 2 {
		t.Fatalf("expected 2 stages, got=%d", result.Stages)
	}
	if result.LastNextAction != "done" {
		t.Fatalf("expected last_next_action=done, got=%q", result.LastNextAction)
	}
	if result.LastRunID != run2.ID {
		t.Fatalf("expected last_run_id=%d, got=%d", run2.ID, result.LastRunID)
	}
}

func TestExecuteWorkerLoop_LaunchError(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	tk, w, _ := createWorkerLoopTestFixture(t, svc, "done")

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return nil, fmt.Errorf("sdk provider unavailable")
	}

	result, err := svc.executeWorkerLoop(context.Background(), tk, w, "test prompt")
	if err == nil {
		t.Fatalf("expected error on launch failure")
	}
	if !strings.Contains(err.Error(), "launch 失败") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Stages != 1 {
		t.Fatalf("expected 1 stage (failed), got=%d", result.Stages)
	}
}

func TestExecuteWorkerLoop_WaitError(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	tk, w, runID := createWorkerLoopTestFixture(t, svc, "done")

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{
			runID:  runID,
			result: agentexec.AgentRunResult{},
			err:    fmt.Errorf("agent process crashed"),
		}, nil
	}

	result, err := svc.executeWorkerLoop(context.Background(), tk, w, "test prompt")
	if err != nil {
		t.Fatalf("expected closure to accept valid done report after wait error, got=%v", err)
	}
	if result.Stages != 1 {
		t.Fatalf("expected 1 stage, got=%d", result.Stages)
	}
	if result.LastNextAction != "done" {
		t.Fatalf("expected last_next_action=done, got=%q", result.LastNextAction)
	}
}

func TestExecuteWorkerLoop_DefaultPromptOnEmpty(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	tk, w, runID := createWorkerLoopTestFixture(t, svc, "done")

	var receivedPrompt string
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		receivedPrompt = prompt
		return &fakeAgentRunHandle{runID: runID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	_, err := svc.executeWorkerLoop(context.Background(), tk, w, "")
	if err != nil {
		t.Fatalf("executeWorkerLoop failed: %v", err)
	}
	if receivedPrompt != defaultContinuePrompt {
		t.Fatalf("expected default prompt %q when empty, got=%q", defaultContinuePrompt, receivedPrompt)
	}
}

func TestExecuteWorkerLoop_ContextCancellation(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	tk, w, _ := createWorkerLoopTestFixture(t, svc, "continue")

	ctx, cancel := context.WithCancel(context.Background())
	svc.sdkHandleLauncher = func(launchCtx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		cancel() // cancel parent context
		// AfterFunc propagation is async; wait briefly for it to take effect.
		time.Sleep(10 * time.Millisecond)
		if launchCtx.Err() != nil {
			return nil, launchCtx.Err()
		}
		// Even if cancel hasn't propagated, return an error to simulate the effect.
		return nil, context.Canceled
	}

	_, err := svc.executeWorkerLoop(ctx, tk, w, "test prompt")
	if err == nil {
		t.Fatalf("expected error on context cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got=%v", err)
	}

	_, db, derr := svc.require()
	if derr != nil {
		t.Fatalf("require failed: %v", derr)
	}
	var after contracts.Worker
	if err := db.First(&after, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if after.Status != contracts.WorkerStopped {
		t.Fatalf("expected canceled launch to stop worker, got=%s", after.Status)
	}
	if strings.TrimSpace(after.LastError) != "" {
		t.Fatalf("expected canceled launch not mark last_error, got=%q", after.LastError)
	}
}

func TestExecuteWorkerLoop_WaitCancellationStopsWorkerWithoutFailure(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	tk, w, runID := createWorkerLoopTestFixture(t, svc, "continue")

	ctx, cancel := context.WithCancel(context.Background())
	svc.sdkHandleLauncher = func(launchCtx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{
			runID: runID,
			waitFunc: func(waitCtx context.Context) (agentexec.AgentRunResult, error) {
				cancel()
				time.Sleep(10 * time.Millisecond)
				if waitCtx.Err() != nil {
					return agentexec.AgentRunResult{}, waitCtx.Err()
				}
				return agentexec.AgentRunResult{}, context.Canceled
			},
		}, nil
	}

	_, err := svc.executeWorkerLoop(ctx, tk, w, "test prompt")
	if err == nil {
		t.Fatalf("expected error on wait cancellation")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got=%v", err)
	}

	_, db, derr := svc.require()
	if derr != nil {
		t.Fatalf("require failed: %v", derr)
	}
	var after contracts.Worker
	if err := db.First(&after, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if after.Status != contracts.WorkerStopped {
		t.Fatalf("expected canceled wait to stop worker, got=%s", after.Status)
	}
	if strings.TrimSpace(after.LastError) != "" {
		t.Fatalf("expected canceled wait not mark last_error, got=%q", after.LastError)
	}
}
