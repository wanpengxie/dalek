package agentexec

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

type runningCall struct {
	runID  uint
	runner string
	lease  *time.Time
}

type failedCall struct {
	runID uint
	code  string
	msg   string
}

type fakeLifecycleRuntime struct {
	nextID uint

	createdInputs []contracts.TaskRunCreateInput
	runningCalls  []runningCall
	succeeded     []uint
	failed        []failedCall
	canceled      []failedCall
	events        []contracts.TaskEventInput

	findRunFn func(ctx context.Context, runID uint) (*contracts.TaskRun, error)
}

func (f *fakeLifecycleRuntime) FindRunByID(ctx context.Context, runID uint) (*contracts.TaskRun, error) {
	if f.findRunFn != nil {
		return f.findRunFn(ctx, runID)
	}
	return nil, nil
}

func (f *fakeLifecycleRuntime) FindRunByRequestID(ctx context.Context, requestID string) (*contracts.TaskRun, error) {
	return nil, nil
}

func (f *fakeLifecycleRuntime) LatestActiveWorkerRun(ctx context.Context, workerID uint) (*contracts.TaskRun, error) {
	return nil, nil
}

func (f *fakeLifecycleRuntime) CreateRun(ctx context.Context, in contracts.TaskRunCreateInput) (contracts.TaskRun, error) {
	f.createdInputs = append(f.createdInputs, in)
	if f.nextID == 0 {
		f.nextID = 1
	}
	id := f.nextID
	f.nextID++
	return contracts.TaskRun{ID: id}, nil
}

func (f *fakeLifecycleRuntime) CancelActiveWorkerRuns(ctx context.Context, workerID uint, reason string, now time.Time) error {
	return nil
}

func (f *fakeLifecycleRuntime) MarkRunRunning(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time, now time.Time, bumpAttempt bool) error {
	f.runningCalls = append(f.runningCalls, runningCall{runID: runID, runner: runnerID, lease: leaseExpiresAt})
	return nil
}

func (f *fakeLifecycleRuntime) RenewLease(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time) error {
	return nil
}

func (f *fakeLifecycleRuntime) MarkRunSucceeded(ctx context.Context, runID uint, resultPayloadJSON string, now time.Time) error {
	f.succeeded = append(f.succeeded, runID)
	return nil
}

func (f *fakeLifecycleRuntime) MarkRunFailed(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error {
	f.failed = append(f.failed, failedCall{runID: runID, code: strings.TrimSpace(errorCode), msg: strings.TrimSpace(errorMessage)})
	return nil
}

func (f *fakeLifecycleRuntime) MarkRunCanceled(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error {
	f.canceled = append(f.canceled, failedCall{runID: runID, code: strings.TrimSpace(errorCode), msg: strings.TrimSpace(errorMessage)})
	return nil
}

func (f *fakeLifecycleRuntime) AppendEvent(ctx context.Context, in contracts.TaskEventInput) error {
	f.events = append(f.events, in)
	return nil
}

func (f *fakeLifecycleRuntime) AppendRuntimeSample(ctx context.Context, in contracts.TaskRuntimeSampleInput) error {
	return nil
}

func (f *fakeLifecycleRuntime) AppendSemanticReport(ctx context.Context, in contracts.TaskSemanticReportInput) error {
	return nil
}

func (f *fakeLifecycleRuntime) ListStatus(ctx context.Context, opt contracts.TaskListStatusOptions) ([]contracts.TaskStatusView, error) {
	return nil, nil
}

func (f *fakeLifecycleRuntime) ListEventsAfterID(ctx context.Context, afterID uint, limit int) ([]contracts.TaskEventScopeRow, error) {
	return nil, nil
}

func TestRunLifecycleTracker_StartAndFinishSuccess(t *testing.T) {
	rt := &fakeLifecycleRuntime{}
	tracker := NewRunLifecycleTracker(BaseConfig{
		Runtime:     rt,
		OwnerType:   contracts.TaskOwnerWorker,
		TaskType:    "deliver_ticket",
		ProjectKey:  "demo",
		TicketID:    12,
		WorkerID:    34,
		SubjectType: "ticket",
		SubjectID:   "12",
	})

	lease := time.Now().Add(2 * time.Minute)
	runID, err := tracker.Start(context.Background(), `{"provider":"sdk"}`, "sdk:codex", &lease, "sdk executor started")
	if err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	if runID == 0 {
		t.Fatalf("expected non-zero run id")
	}
	if len(rt.createdInputs) != 1 {
		t.Fatalf("expected 1 create input, got=%d", len(rt.createdInputs))
	}
	if len(rt.runningCalls) != 1 {
		t.Fatalf("expected 1 running call, got=%d", len(rt.runningCalls))
	}
	if rt.runningCalls[0].runner != "sdk:codex" {
		t.Fatalf("unexpected runner id: %q", rt.runningCalls[0].runner)
	}
	if len(rt.events) == 0 || rt.events[0].EventType != "task_started" {
		t.Fatalf("expected first event task_started, got=%v", rt.events)
	}

	tracker.Finish(context.Background(), AgentRunResult{ExitCode: 0, Stdout: "ok"}, nil, "sdk executor finished")
	if len(rt.succeeded) != 1 || rt.succeeded[0] != runID {
		t.Fatalf("expected mark succeeded once for run=%d, got=%v", runID, rt.succeeded)
	}
	if len(rt.failed) != 0 || len(rt.canceled) != 0 {
		t.Fatalf("expected no failed/canceled calls, failed=%v canceled=%v", rt.failed, rt.canceled)
	}
	if got := rt.events[len(rt.events)-1].EventType; got != "task_succeeded" {
		t.Fatalf("expected terminal event task_succeeded, got=%q", got)
	}
}

func TestRunLifecycleTracker_CreatePendingThenMarkRunning(t *testing.T) {
	rt := &fakeLifecycleRuntime{}
	tracker := NewRunLifecycleTracker(BaseConfig{Runtime: rt})

	runID, err := tracker.CreatePending(context.Background(), `{"provider":"process"}`)
	if err != nil {
		t.Fatalf("CreatePending failed: %v", err)
	}
	if runID == 0 {
		t.Fatalf("expected run id")
	}
	if len(rt.runningCalls) != 0 {
		t.Fatalf("expected no running call before MarkRunning")
	}
	if err := tracker.MarkRunning(context.Background(), "pid:1234", nil, "process executor started"); err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}
	if len(rt.runningCalls) != 1 {
		t.Fatalf("expected 1 running call, got=%d", len(rt.runningCalls))
	}
	if len(rt.events) == 0 || rt.events[0].EventType != "task_started" {
		t.Fatalf("expected task_started event, got=%v", rt.events)
	}
}

func TestRunLifecycleTracker_FinishCanceled(t *testing.T) {
	rt := &fakeLifecycleRuntime{}
	tracker := NewRunLifecycleTracker(BaseConfig{Runtime: rt})
	if _, err := tracker.Start(context.Background(), `{"provider":"sdk"}`, "sdk:codex", nil, "started"); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tracker.Finish(ctx, AgentRunResult{ExitCode: 1, Stderr: "interrupted"}, context.Canceled, "ignored")

	if len(rt.canceled) != 1 {
		t.Fatalf("expected 1 canceled call, got=%d", len(rt.canceled))
	}
	if rt.canceled[0].code != "agent_canceled" {
		t.Fatalf("unexpected canceled code: %q", rt.canceled[0].code)
	}
	if !strings.Contains(rt.canceled[0].msg, "context canceled") {
		t.Fatalf("expected canceled message contains context canceled, got=%q", rt.canceled[0].msg)
	}
	if got := rt.events[len(rt.events)-1].EventType; got != "task_canceled" {
		t.Fatalf("expected task_canceled event, got=%q", got)
	}
}
