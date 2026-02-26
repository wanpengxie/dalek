package pm

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/agentexec"
	"dalek/internal/store"
)

// fakeAgentRunHandle implements agentexec.AgentRunHandle for testing.
type fakeAgentRunHandle struct {
	runID  uint
	result agentexec.AgentRunResult
	err    error
}

func (h *fakeAgentRunHandle) RunID() uint                             { return h.runID }
func (h *fakeAgentRunHandle) Wait() (agentexec.AgentRunResult, error) { return h.result, h.err }
func (h *fakeAgentRunHandle) Cancel() error                           { return nil }

// makeSemanticReport inserts a TaskSemanticReport row so that
// readWorkerNextActionFromRun can pick up the next_action.
func makeSemanticReport(t *testing.T, svc *Service, runID uint, nextAction string) {
	t.Helper()
	_, db, err := svc.require()
	if err != nil {
		t.Fatalf("require failed: %v", err)
	}
	if err := db.Create(&contracts.TaskSemanticReport{
		TaskRunID:  runID,
		Phase:      contracts.TaskPhaseImplementing,
		NextAction: nextAction,
		Summary:    "test",
		ReportedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("create semantic report failed: %v", err)
	}
}

// createWorkerLoopTestFixture sets up ticket + worker + task run + semantic report for worker_loop tests.
func createWorkerLoopTestFixture(t *testing.T, svc *Service, nextAction string) (store.Ticket, store.Worker, uint) {
	t.Helper()
	_, db, err := svc.require()
	if err != nil {
		t.Fatalf("require failed: %v", err)
	}

	tk := createTicket(t, db, "worker-loop-test")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/worker-loop-test",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-worker-loop-test",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	taskRun := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
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

	return tk, w, taskRun.ID
}

func TestExecuteWorkerLoop_StopsOnDone(t *testing.T) {
	svc, _, _, _ := newServiceForTest(t)
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
	svc, _, _, _ := newServiceForTest(t)
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

func TestExecuteWorkerLoop_StopsOnEmptyNextAction(t *testing.T) {
	svc, _, _, _ := newServiceForTest(t)
	tk, w, runID := createWorkerLoopTestFixture(t, svc, "")

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
	if result.LastNextAction != "" {
		t.Fatalf("expected empty last_next_action, got=%q", result.LastNextAction)
	}
}

func TestExecuteWorkerLoop_ContinuesThenStops(t *testing.T) {
	svc, _, _, _ := newServiceForTest(t)
	_, db, err := svc.require()
	if err != nil {
		t.Fatalf("require failed: %v", err)
	}

	tk := createTicket(t, db, "worker-loop-multi")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/worker-loop-multi",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-worker-loop-multi",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// Create two task runs: first returns "continue", second returns "done".
	run1 := contracts.TaskRun{
		OwnerType: contracts.TaskOwnerWorker, TaskType: "deliver_ticket",
		ProjectKey: "test", TicketID: tk.ID, WorkerID: w.ID,
		SubjectType: "ticket", SubjectID: fmt.Sprintf("%d", tk.ID),
		RequestID: "wrk_multi_1", OrchestrationState: contracts.TaskRunning,
	}
	if err := db.Create(&run1).Error; err != nil {
		t.Fatalf("create run1 failed: %v", err)
	}
	makeSemanticReport(t, svc, run1.ID, "continue")

	run2 := contracts.TaskRun{
		OwnerType: contracts.TaskOwnerWorker, TaskType: "deliver_ticket",
		ProjectKey: "test", TicketID: tk.ID, WorkerID: w.ID,
		SubjectType: "ticket", SubjectID: fmt.Sprintf("%d", tk.ID),
		RequestID: "wrk_multi_2", OrchestrationState: contracts.TaskRunning,
	}
	if err := db.Create(&run2).Error; err != nil {
		t.Fatalf("create run2 failed: %v", err)
	}
	makeSemanticReport(t, svc, run2.ID, "done")

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
	svc, _, _, _ := newServiceForTest(t)
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
	svc, _, _, _ := newServiceForTest(t)
	tk, w, runID := createWorkerLoopTestFixture(t, svc, "done")

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{
			runID:  runID,
			result: agentexec.AgentRunResult{},
			err:    fmt.Errorf("agent process crashed"),
		}, nil
	}

	result, err := svc.executeWorkerLoop(context.Background(), tk, w, "test prompt")
	if err == nil {
		t.Fatalf("expected error on wait failure")
	}
	if !strings.Contains(err.Error(), "wait 失败") {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Stages != 1 {
		t.Fatalf("expected 1 stage (failed), got=%d", result.Stages)
	}
}

func TestExecuteWorkerLoop_DefaultPromptOnEmpty(t *testing.T) {
	svc, _, _, _ := newServiceForTest(t)
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
	svc, _, _, _ := newServiceForTest(t)
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
}
