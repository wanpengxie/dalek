package daemon

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dalek/internal/contracts"
)

type testExecutionHostResolver struct {
	project       *testExecutionHostProject
	projects      map[string]*testExecutionHostProject
	projectOrder  []string
	listProjCalls atomic.Int64
}

func (r *testExecutionHostResolver) OpenProject(name string) (ExecutionHostProject, error) {
	projectName := strings.TrimSpace(name)
	if projectName == "" {
		return nil, fmt.Errorf("project name empty")
	}
	if r == nil {
		return nil, fmt.Errorf("project not found: %s", projectName)
	}
	if r.projects != nil {
		project := r.projects[projectName]
		if project == nil {
			return nil, fmt.Errorf("project not found: %s", projectName)
		}
		return project, nil
	}
	if r.project == nil {
		return nil, fmt.Errorf("project not found: %s", projectName)
	}
	return r.project, nil
}

func (r *testExecutionHostResolver) ListProjects() ([]string, error) {
	if r == nil {
		return nil, nil
	}
	r.listProjCalls.Add(1)
	if len(r.projectOrder) > 0 {
		projects := make([]string, len(r.projectOrder))
		copy(projects, r.projectOrder)
		return projects, nil
	}
	if len(r.projects) > 0 {
		projects := make([]string, 0, len(r.projects))
		for name := range r.projects {
			projects = append(projects, name)
		}
		sort.Strings(projects)
		return projects, nil
	}
	return []string{"demo"}, nil
}

func (r *testExecutionHostResolver) ListProjectsCount() int64 {
	if r == nil {
		return 0
	}
	return r.listProjCalls.Load()
}

type testExecutionHostProject struct {
	mu sync.Mutex

	dispatchCalls         int
	runDispatchCalls      int
	directDispatchCalls   int
	subagentCalls         int
	runSubagentCalls      int
	runPlannerCalls       int
	lastDispatchAutoStart *bool

	nextJobID   uint
	nextRunID   uint
	workerRunID uint

	dispatchByRequest map[string]DispatchSubmission
	subagentByRequest map[string]SubagentSubmission
	projectName       string
	statusByRun       map[uint]*RunStatus
	eventsByRun       map[uint][]RunEvent
	statusCalls       int
	eventCalls        int

	runDispatchDelay        time.Duration
	runDispatchStarted      chan struct{}
	runDispatchRelease      chan struct{}
	runDispatchIgnoreCancel bool

	directDispatchDelay        time.Duration
	directDispatchStarted      chan struct{}
	directDispatchRelease      chan struct{}
	directDispatchIgnoreCancel bool

	runSubagentDelay        time.Duration
	runSubagentStarted      chan struct{}
	runSubagentRelease      chan struct{}
	runSubagentIgnoreCancel bool

	runPlannerDelay        time.Duration
	runPlannerStarted      chan struct{}
	runPlannerRelease      chan struct{}
	runPlannerIgnoreCancel bool
}

func (p *testExecutionHostProject) SubmitDispatchTicket(ctx context.Context, ticketID uint, opt DispatchSubmitOptions) (DispatchSubmission, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.dispatchCalls++
	p.lastDispatchAutoStart = cloneBoolPtr(opt.AutoStart)

	if p.dispatchByRequest == nil {
		p.dispatchByRequest = make(map[string]DispatchSubmission)
	}
	if p.nextJobID == 0 {
		p.nextJobID = 100
	}
	if p.nextRunID == 0 {
		p.nextRunID = 200
	}

	requestID := strings.TrimSpace(opt.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("dsp-test-generated-%d", p.dispatchCalls)
	}
	if existing, ok := p.dispatchByRequest[requestID]; ok {
		return existing, nil
	}

	p.nextJobID++
	p.nextRunID++
	submission := DispatchSubmission{
		JobID:      p.nextJobID,
		TaskRunID:  p.nextRunID,
		RequestID:  requestID,
		TicketID:   ticketID,
		WorkerID:   301,
		JobStatus:  contracts.PMDispatchPending,
		Dispatched: false,
	}
	p.dispatchByRequest[requestID] = submission
	return submission, nil
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	b := *v
	return &b
}

func (p *testExecutionHostProject) RunDispatchJob(ctx context.Context, jobID uint, opt DispatchRunOptions) error {
	p.mu.Lock()
	p.runDispatchCalls++
	delay := p.runDispatchDelay
	started := p.runDispatchStarted
	release := p.runDispatchRelease
	ignoreCancel := p.runDispatchIgnoreCancel
	p.mu.Unlock()
	notifyExecutionStarted(started)
	return waitExecutionRelease(ctx, delay, release, ignoreCancel)
}

func (p *testExecutionHostProject) DirectDispatchWorker(ctx context.Context, ticketID uint, opt WorkerRunOptions) (WorkerRunResult, error) {
	p.mu.Lock()
	p.directDispatchCalls++
	delay := p.directDispatchDelay
	started := p.directDispatchStarted
	release := p.directDispatchRelease
	ignoreCancel := p.directDispatchIgnoreCancel
	runID := p.workerRunID
	p.mu.Unlock()
	notifyExecutionStarted(started)
	if err := waitExecutionRelease(ctx, delay, release, ignoreCancel); err != nil {
		return WorkerRunResult{}, err
	}
	if runID == 0 {
		runID = 501
	}
	return WorkerRunResult{
		TicketID: ticketID,
		WorkerID: 401,
		RunID:    runID,
	}, nil
}

func (p *testExecutionHostProject) SubmitSubagentRun(ctx context.Context, opt SubagentSubmitOptions) (SubagentSubmission, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.subagentCalls++
	if p.subagentByRequest == nil {
		p.subagentByRequest = map[string]SubagentSubmission{}
	}
	if p.nextRunID == 0 {
		p.nextRunID = 200
	}
	requestID := strings.TrimSpace(opt.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("sub-test-generated-%d", p.subagentCalls)
	}
	if existing, ok := p.subagentByRequest[requestID]; ok {
		return existing, nil
	}
	p.nextRunID++
	submission := SubagentSubmission{
		Accepted:   true,
		TaskRunID:  p.nextRunID,
		RequestID:  requestID,
		Provider:   strings.TrimSpace(opt.Provider),
		Model:      strings.TrimSpace(opt.Model),
		RuntimeDir: fmt.Sprintf("/tmp/subagent/%d", p.nextRunID),
	}
	p.subagentByRequest[requestID] = submission
	return submission, nil
}

func (p *testExecutionHostProject) RunSubagentJob(ctx context.Context, taskRunID uint, opt SubagentRunOptions) error {
	p.mu.Lock()
	p.runSubagentCalls++
	delay := p.runSubagentDelay
	started := p.runSubagentStarted
	release := p.runSubagentRelease
	ignoreCancel := p.runSubagentIgnoreCancel
	p.mu.Unlock()
	notifyExecutionStarted(started)
	return waitExecutionRelease(ctx, delay, release, ignoreCancel)
}

func (p *testExecutionHostProject) RunPlannerJob(ctx context.Context, taskRunID uint, opt PlannerRunOptions) error {
	p.mu.Lock()
	p.runPlannerCalls++
	delay := p.runPlannerDelay
	started := p.runPlannerStarted
	release := p.runPlannerRelease
	ignoreCancel := p.runPlannerIgnoreCancel
	p.mu.Unlock()
	notifyExecutionStarted(started)
	return waitExecutionRelease(ctx, delay, release, ignoreCancel)
}

func notifyExecutionStarted(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

func waitExecutionRelease(ctx context.Context, delay time.Duration, release chan struct{}, ignoreCancel bool) error {
	if delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		if ignoreCancel {
			<-timer.C
			return nil
		}
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if release != nil {
		if ignoreCancel {
			<-release
			return nil
		}
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	timer := time.NewTimer(100 * time.Millisecond)
	defer timer.Stop()
	if ignoreCancel {
		<-timer.C
		return nil
	}
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *testExecutionHostProject) FindLatestWorkerRun(ctx context.Context, ticketID uint, afterRunID uint) (*RunStatus, error) {
	p.mu.Lock()
	runID := p.workerRunID
	p.mu.Unlock()
	if runID == 0 {
		runID = 501
	}
	if afterRunID >= runID {
		return nil, nil
	}
	return &RunStatus{
		RunID:     runID,
		TicketID:  ticketID,
		WorkerID:  401,
		Project:   "demo",
		UpdatedAt: time.Now(),
	}, nil
}

func (p *testExecutionHostProject) AddNote(ctx context.Context, rawText string) (NoteAddResult, error) {
	return NoteAddResult{}, nil
}

func (p *testExecutionHostProject) GetTaskStatus(ctx context.Context, runID uint) (*RunStatus, error) {
	p.mu.Lock()
	p.statusCalls++
	status := p.statusByRun[runID]
	projectName := strings.TrimSpace(p.projectName)
	p.mu.Unlock()
	if status == nil {
		return nil, nil
	}
	copied := *status
	if strings.TrimSpace(copied.Project) == "" {
		copied.Project = projectName
	}
	return &copied, nil
}

func (p *testExecutionHostProject) ListTaskEvents(ctx context.Context, runID uint, limit int) ([]RunEvent, error) {
	p.mu.Lock()
	p.eventCalls++
	events := p.eventsByRun[runID]
	p.mu.Unlock()
	if len(events) == 0 {
		return nil, nil
	}
	copied := make([]RunEvent, len(events))
	copy(copied, events)
	if limit <= 0 || limit >= len(copied) {
		return copied, nil
	}
	return copied[len(copied)-limit:], nil
}

func (p *testExecutionHostProject) DispatchSubmitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dispatchCalls
}

func (p *testExecutionHostProject) LastDispatchAutoStart() *bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return cloneBoolPtr(p.lastDispatchAutoStart)
}

func (p *testExecutionHostProject) RunDispatchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runDispatchCalls
}

func (p *testExecutionHostProject) DirectDispatchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.directDispatchCalls
}

func (p *testExecutionHostProject) SubagentSubmitCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.subagentCalls
}

func (p *testExecutionHostProject) RunSubagentCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runSubagentCalls
}

func (p *testExecutionHostProject) RunPlannerCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runPlannerCalls
}

func (p *testExecutionHostProject) GetTaskStatusCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.statusCalls
}

func (p *testExecutionHostProject) ListTaskEventsCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.eventCalls
}

func TestExecutionHost_OnRunSettled_Dispatch(t *testing.T) {
	resolver := &testExecutionHostResolver{project: &testExecutionHostProject{}}
	notifyCh := make(chan string, 1)
	host, err := NewExecutionHost(resolver, ExecutionHostOptions{
		OnRunSettled: func(project string) {
			notifyCh <- strings.TrimSpace(project)
		},
	})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	_, err = host.SubmitDispatch(context.Background(), DispatchSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "dispatch-notify-test",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("SubmitDispatch failed: %v", err)
	}

	select {
	case got := <-notifyCh:
		if got != "demo" {
			t.Fatalf("unexpected project notify: got=%q want=%q", got, "demo")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected OnRunSettled callback for dispatch")
	}
}

func TestExecutionHost_OnRunSettled_WorkerRun(t *testing.T) {
	resolver := &testExecutionHostResolver{project: &testExecutionHostProject{}}
	notifyCh := make(chan string, 1)
	host, err := NewExecutionHost(resolver, ExecutionHostOptions{
		OnRunSettled: func(project string) {
			notifyCh <- strings.TrimSpace(project)
		},
	})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	_, err = host.SubmitWorkerRun(context.Background(), WorkerRunSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "worker-notify-test",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("SubmitWorkerRun failed: %v", err)
	}

	select {
	case got := <-notifyCh:
		if got != "demo" {
			t.Fatalf("unexpected project notify: got=%q want=%q", got, "demo")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected OnRunSettled callback for worker run")
	}
}

func TestExecutionHost_SubmitWorkerRun_UsesRunIDFromDirectResult(t *testing.T) {
	project := &testExecutionHostProject{workerRunID: 8801}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	receipt, err := host.SubmitWorkerRun(context.Background(), WorkerRunSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "worker-runid-from-result",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("SubmitWorkerRun failed: %v", err)
	}
	if receipt.TaskRunID != 8801 {
		t.Fatalf("expected task_run_id from direct result, got=%d", receipt.TaskRunID)
	}
	if receipt.WorkerID != 401 {
		t.Fatalf("expected worker_id=401, got=%d", receipt.WorkerID)
	}
}

func TestExecutionHost_OnRunSettled_SubagentRun(t *testing.T) {
	resolver := &testExecutionHostResolver{project: &testExecutionHostProject{}}
	notifyCh := make(chan string, 1)
	host, err := NewExecutionHost(resolver, ExecutionHostOptions{
		OnRunSettled: func(project string) {
			notifyCh <- strings.TrimSpace(project)
		},
	})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	_, err = host.SubmitSubagentRun(context.Background(), SubagentSubmitRequest{
		Project:   "demo",
		RequestID: "subagent-notify-test",
		Provider:  "codex",
		Model:     "gpt-5.3-codex",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("SubmitSubagentRun failed: %v", err)
	}

	select {
	case got := <-notifyCh:
		if got != "demo" {
			t.Fatalf("unexpected project notify: got=%q want=%q", got, "demo")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected OnRunSettled callback for subagent run")
	}
}

func TestExecutionHost_OnRunSettled_PlannerRun(t *testing.T) {
	resolver := &testExecutionHostResolver{project: &testExecutionHostProject{}}
	notifyCh := make(chan string, 1)
	host, err := NewExecutionHost(resolver, ExecutionHostOptions{
		OnRunSettled: func(project string) {
			notifyCh <- strings.TrimSpace(project)
		},
	})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	_, err = host.SubmitPlannerRun(context.Background(), PlannerSubmitRequest{
		Project:   "demo",
		RequestID: "planner-notify-test",
		TaskRunID: 9001,
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("SubmitPlannerRun failed: %v", err)
	}

	select {
	case got := <-notifyCh:
		if got != "demo" {
			t.Fatalf("unexpected project notify: got=%q want=%q", got, "demo")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("expected OnRunSettled callback for planner run")
	}
}

func TestExecutionHost_SubmitDispatch_IdempotentByRequestID(t *testing.T) {
	project := &testExecutionHostProject{}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	req := DispatchSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "dispatch-idempotent-single",
		Prompt:    "继续执行任务",
	}
	first, err := host.SubmitDispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("first SubmitDispatch failed: %v", err)
	}
	second, err := host.SubmitDispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("second SubmitDispatch failed: %v", err)
	}

	if first.TaskRunID != second.TaskRunID {
		t.Fatalf("expected same run id for duplicate request_id: first=%d second=%d", first.TaskRunID, second.TaskRunID)
	}
	if first.RequestID != second.RequestID {
		t.Fatalf("expected same request_id in receipt: first=%q second=%q", first.RequestID, second.RequestID)
	}
	if got := project.DispatchSubmitCount(); got != 1 {
		t.Fatalf("expected only one SubmitDispatchTicket call, got=%d", got)
	}
}

func TestExecutionHost_SubmitDispatch_ForwardsAutoStartOption(t *testing.T) {
	project := &testExecutionHostProject{}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	autoStart := false
	_, err = host.SubmitDispatch(context.Background(), DispatchSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "dispatch-forward-auto-start",
		Prompt:    "继续执行任务",
		AutoStart: &autoStart,
	})
	if err != nil {
		t.Fatalf("SubmitDispatch failed: %v", err)
	}

	got := project.LastDispatchAutoStart()
	if got == nil || *got {
		t.Fatalf("expected forwarded auto_start=false, got=%v", got)
	}
}

func TestExecutionHost_SubmitDispatch_IdempotentByRequestIDConcurrent(t *testing.T) {
	project := &testExecutionHostProject{}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	const workers = 8
	results := make(chan DispatchSubmitReceipt, workers)
	errs := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			receipt, submitErr := host.SubmitDispatch(context.Background(), DispatchSubmitRequest{
				Project:   "demo",
				TicketID:  1,
				RequestID: "dispatch-idempotent-concurrent",
				Prompt:    "继续执行任务",
			})
			if submitErr != nil {
				errs <- submitErr
				return
			}
			results <- receipt
		}()
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent SubmitDispatch failed: %v", err)
		}
	}

	var baseRunID uint
	for receipt := range results {
		if baseRunID == 0 {
			baseRunID = receipt.TaskRunID
			continue
		}
		if receipt.TaskRunID != baseRunID {
			t.Fatalf("expected same run id under concurrent duplicate request_id: base=%d got=%d", baseRunID, receipt.TaskRunID)
		}
	}
	if baseRunID == 0 {
		t.Fatalf("expected at least one concurrent receipt")
	}
}

func TestExecutionHost_SubmitDispatch_EmptyRequestIDCompatible(t *testing.T) {
	project := &testExecutionHostProject{}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	first, err := host.SubmitDispatch(context.Background(), DispatchSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("first SubmitDispatch failed: %v", err)
	}
	second, err := host.SubmitDispatch(context.Background(), DispatchSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("second SubmitDispatch failed: %v", err)
	}

	if first.TaskRunID == second.TaskRunID {
		t.Fatalf("expected different run id when request_id is empty: first=%d second=%d", first.TaskRunID, second.TaskRunID)
	}
	if got := project.DispatchSubmitCount(); got != 2 {
		t.Fatalf("expected two SubmitDispatchTicket calls for empty request_id, got=%d", got)
	}
}

func TestExecutionHost_SubmitDispatch_DifferentRequestIDCreatesDifferentRuns(t *testing.T) {
	project := &testExecutionHostProject{}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	first, err := host.SubmitDispatch(context.Background(), DispatchSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "dispatch-id-a",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("first SubmitDispatch failed: %v", err)
	}
	second, err := host.SubmitDispatch(context.Background(), DispatchSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "dispatch-id-b",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("second SubmitDispatch failed: %v", err)
	}

	if first.TaskRunID == second.TaskRunID {
		t.Fatalf("expected different run id for different request_id: first=%d second=%d", first.TaskRunID, second.TaskRunID)
	}
	if got := project.DispatchSubmitCount(); got != 2 {
		t.Fatalf("expected two SubmitDispatchTicket calls for different request_id, got=%d", got)
	}
}

func TestExecutionHost_SubmitSubagent_IdempotentByRequestID(t *testing.T) {
	project := &testExecutionHostProject{}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	req := SubagentSubmitRequest{
		Project:   "demo",
		RequestID: "subagent-idempotent-single",
		Provider:  "claude",
		Model:     "sonnet",
		Prompt:    "继续执行任务",
	}
	first, err := host.SubmitSubagentRun(context.Background(), req)
	if err != nil {
		t.Fatalf("first SubmitSubagentRun failed: %v", err)
	}
	second, err := host.SubmitSubagentRun(context.Background(), req)
	if err != nil {
		t.Fatalf("second SubmitSubagentRun failed: %v", err)
	}
	if first.TaskRunID != second.TaskRunID {
		t.Fatalf("expected same run id for duplicate request_id: first=%d second=%d", first.TaskRunID, second.TaskRunID)
	}
	if first.RequestID != second.RequestID {
		t.Fatalf("expected same request_id in receipt: first=%q second=%q", first.RequestID, second.RequestID)
	}
	if got := project.SubagentSubmitCount(); got != 1 {
		t.Fatalf("expected only one SubmitSubagentRun call, got=%d", got)
	}
}

func TestExecutionHost_SubmitPlannerRun_IdempotentByRequestID(t *testing.T) {
	project := &testExecutionHostProject{}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	req := PlannerSubmitRequest{
		Project:   "demo",
		RequestID: "planner-idempotent-single",
		TaskRunID: 3001,
		Prompt:    "继续执行任务",
	}
	first, err := host.SubmitPlannerRun(context.Background(), req)
	if err != nil {
		t.Fatalf("first SubmitPlannerRun failed: %v", err)
	}
	second, err := host.SubmitPlannerRun(context.Background(), req)
	if err != nil {
		t.Fatalf("second SubmitPlannerRun failed: %v", err)
	}
	if first.TaskRunID != second.TaskRunID {
		t.Fatalf("expected same run id for duplicate request_id: first=%d second=%d", first.TaskRunID, second.TaskRunID)
	}
	if first.RequestID != second.RequestID {
		t.Fatalf("expected same request_id in receipt: first=%q second=%q", first.RequestID, second.RequestID)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := project.RunPlannerCount(); got == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected only one RunPlannerJob call, got=%d", project.RunPlannerCount())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestExecutionHost_Stop_WaitsForDispatchExit(t *testing.T) {
	releaseCh := make(chan struct{})
	project := &testExecutionHostProject{
		runDispatchStarted:      make(chan struct{}, 1),
		runDispatchRelease:      releaseCh,
		runDispatchIgnoreCancel: true,
	}
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	_, err = host.SubmitDispatch(context.Background(), DispatchSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "dispatch-stop-wait",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("SubmitDispatch failed: %v", err)
	}

	select {
	case <-project.runDispatchStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("dispatch goroutine not started")
	}

	stopDone := make(chan error, 1)
	go func() {
		stopDone <- host.Stop(context.Background())
	}()

	select {
	case err := <-stopDone:
		t.Fatalf("Stop returned before dispatch released: err=%v", err)
	case <-time.After(120 * time.Millisecond):
	}

	close(releaseCh)

	select {
	case err := <-stopDone:
		if err != nil {
			t.Fatalf("Stop failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Stop should return after dispatch exits")
	}
}

func TestExecutionHost_Stop_TimeoutReportsPendingWorkerRun(t *testing.T) {
	releaseCh := make(chan struct{})
	project := &testExecutionHostProject{
		directDispatchStarted:      make(chan struct{}, 1),
		directDispatchRelease:      releaseCh,
		directDispatchIgnoreCancel: true,
	}
	var buf bytes.Buffer
	host, err := NewExecutionHost(&testExecutionHostResolver{project: project}, ExecutionHostOptions{
		Logger: slog.New(slog.NewTextHandler(&buf, nil)),
	})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	_, err = host.SubmitWorkerRun(context.Background(), WorkerRunSubmitRequest{
		Project:   "demo",
		TicketID:  1,
		RequestID: "worker-stop-timeout",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("SubmitWorkerRun failed: %v", err)
	}

	select {
	case <-project.directDispatchStarted:
	case <-time.After(2 * time.Second):
		t.Fatalf("worker-run goroutine not started")
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	err = host.Stop(stopCtx)
	if err == nil {
		t.Fatalf("expected Stop timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got=%v", err)
	}
	var timeoutErr *StopTimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected StopTimeoutError, got=%T", err)
	}
	if timeoutErr.PendingCount <= 0 {
		t.Fatalf("expected pending_count>0, got=%d", timeoutErr.PendingCount)
	}
	if !strings.Contains(strings.Join(timeoutErr.PendingSummary, ","), "worker-stop-timeout") {
		t.Fatalf("expected pending summary contains request id, got=%v", timeoutErr.PendingSummary)
	}

	logs := buf.String()
	if !strings.Contains(logs, "execution host stop timeout") {
		t.Fatalf("expected timeout log, got=%q", logs)
	}
	if !strings.Contains(logs, "worker-stop-timeout") {
		t.Fatalf("expected timeout log contains request summary, got=%q", logs)
	}

	close(releaseCh)
	if err := host.Stop(context.Background()); err != nil {
		t.Fatalf("cleanup Stop failed: %v", err)
	}
}

func TestExecutionHost_Stop_NoRunsReturnsImmediately(t *testing.T) {
	host, err := NewExecutionHost(&testExecutionHostResolver{project: &testExecutionHostProject{}}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	start := time.Now()
	if err := host.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("expected immediate Stop for empty host, elapsed=%s", elapsed)
	}
}

func TestExecutionHost_WarmupRunProjectIndex_FillsAndDedups(t *testing.T) {
	host, err := NewExecutionHost(&testExecutionHostResolver{project: &testExecutionHostProject{}}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	first := host.WarmupRunProjectIndex("alpha", []uint{11, 12, 0, 11})
	if first != 2 {
		t.Fatalf("expected first warmup indexed 2 runs, got=%d", first)
	}
	if !containsProject(host.lookupRunProject(11), "alpha") {
		t.Fatalf("expected run 11 indexed to alpha")
	}
	if !containsProject(host.lookupRunProject(12), "alpha") {
		t.Fatalf("expected run 12 indexed to alpha")
	}

	second := host.WarmupRunProjectIndex("alpha", []uint{11, 12})
	if second != 0 {
		t.Fatalf("expected duplicate warmup adds zero entries, got=%d", second)
	}

	third := host.WarmupRunProjectIndex("beta", []uint{11})
	if third != 1 {
		t.Fatalf("expected warmup on new project adds one entry, got=%d", third)
	}
	projects := host.lookupRunProject(11)
	if !containsProject(projects, "alpha") || !containsProject(projects, "beta") {
		t.Fatalf("expected run 11 indexed for both projects, got=%v", projects)
	}
}

func TestExecutionHost_FinalizeHandle_RemovesRunProjectIndex(t *testing.T) {
	host, err := NewExecutionHost(&testExecutionHostResolver{project: &testExecutionHostProject{}}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	handle := &executionRunHandle{
		project:   "demo",
		requestID: "req-finalize-index",
		runID:     7001,
		ready:     make(chan struct{}),
		done:      make(chan struct{}),
	}
	host.mu.Lock()
	host.runs[handle.runID] = handle
	host.requests[handle.requestID] = handle
	host.addRunProjectIndexLocked(handle.runID, handle.project)
	host.mu.Unlock()

	host.finalizeHandle(handle)

	if projects := host.lookupRunProject(handle.runID); len(projects) != 0 {
		t.Fatalf("expected runProjectIndex entry removed after finalize, got=%v", projects)
	}
}

func TestExecutionHost_Stop_ClearsRunProjectIndex(t *testing.T) {
	host, err := NewExecutionHost(&testExecutionHostResolver{project: &testExecutionHostProject{}}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	host.addRunProjectIndex(8080, "demo")
	if got := host.lookupRunProject(8080); len(got) == 0 {
		t.Fatalf("expected warm index entry before Stop")
	}

	if err := host.Stop(context.Background()); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
	if got := host.lookupRunProject(8080); len(got) != 0 {
		t.Fatalf("expected runProjectIndex cleared after Stop, got=%v", got)
	}
}

func TestExecutionHost_GetRunStatus_UsesRunProjectIndex(t *testing.T) {
	runID := uint(9001)
	alpha := &testExecutionHostProject{
		projectName: "alpha",
		statusByRun: map[uint]*RunStatus{
			runID: {
				RunID:     runID,
				TicketID:  11,
				WorkerID:  22,
				UpdatedAt: time.Now(),
			},
		},
	}
	resolver := &testExecutionHostResolver{
		projects: map[string]*testExecutionHostProject{
			"alpha": alpha,
			"beta":  {projectName: "beta"},
		},
		projectOrder: []string{"alpha", "beta"},
	}
	host, err := NewExecutionHost(resolver, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	host.addRunProjectIndex(runID, "alpha")

	status, err := host.GetRunStatus(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRunStatus failed: %v", err)
	}
	if status == nil {
		t.Fatalf("expected non-nil status")
	}
	if got := strings.TrimSpace(status.Project); got != "alpha" {
		t.Fatalf("expected project from index path: got=%q want=%q", got, "alpha")
	}
	if got := resolver.ListProjectsCount(); got != 0 {
		t.Fatalf("expected no full scan when index hits, list_projects_calls=%d", got)
	}
	if got := host.scanFallbackCount.Load(); got != 0 {
		t.Fatalf("expected no scan fallback, got=%d", got)
	}
}

func TestExecutionHost_GetRunStatus_ScanFallbackSelfHealsIndex(t *testing.T) {
	runID := uint(42)
	alpha := &testExecutionHostProject{projectName: "alpha"}
	beta := &testExecutionHostProject{
		projectName: "beta",
		statusByRun: map[uint]*RunStatus{
			runID: {
				RunID:     runID,
				TicketID:  7,
				WorkerID:  8,
				UpdatedAt: time.Now(),
			},
		},
	}
	resolver := &testExecutionHostResolver{
		projects: map[string]*testExecutionHostProject{
			"alpha": alpha,
			"beta":  beta,
		},
		projectOrder: []string{"alpha", "beta"},
	}
	host, err := NewExecutionHost(resolver, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}

	first, err := host.GetRunStatus(context.Background(), runID)
	if err != nil {
		t.Fatalf("first GetRunStatus failed: %v", err)
	}
	if first == nil {
		t.Fatalf("expected first status non-nil")
	}
	if got := strings.TrimSpace(first.Project); got != "beta" {
		t.Fatalf("expected run belongs to beta, got=%q", got)
	}
	if got := resolver.ListProjectsCount(); got != 1 {
		t.Fatalf("expected one scan on first lookup, got=%d", got)
	}
	if got := host.scanFallbackCount.Load(); got != 1 {
		t.Fatalf("expected one fallback count after first lookup, got=%d", got)
	}
	indexed := host.lookupRunProject(runID)
	if !containsProject(indexed, "beta") {
		t.Fatalf("expected scan self-heal writes index for beta, indexed=%v", indexed)
	}

	second, err := host.GetRunStatus(context.Background(), runID)
	if err != nil {
		t.Fatalf("second GetRunStatus failed: %v", err)
	}
	if second == nil {
		t.Fatalf("expected second status non-nil")
	}
	if got := resolver.ListProjectsCount(); got != 1 {
		t.Fatalf("expected second lookup to avoid scan, list_projects_calls=%d", got)
	}
	if got := host.scanFallbackCount.Load(); got != 1 {
		t.Fatalf("expected fallback count unchanged on second lookup, got=%d", got)
	}
}

func TestExecutionHost_GetRunStatus_RunIDCollisionUsesIndexedCandidates(t *testing.T) {
	runID := uint(77)
	alpha := &testExecutionHostProject{projectName: "alpha"}
	beta := &testExecutionHostProject{
		projectName: "beta",
		statusByRun: map[uint]*RunStatus{
			runID: {
				RunID:     runID,
				TicketID:  101,
				UpdatedAt: time.Now(),
			},
		},
	}
	resolver := &testExecutionHostResolver{
		projects: map[string]*testExecutionHostProject{
			"alpha": alpha,
			"beta":  beta,
		},
		projectOrder: []string{"alpha", "beta"},
	}
	host, err := NewExecutionHost(resolver, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	host.addRunProjectIndex(runID, "alpha")
	host.addRunProjectIndex(runID, "beta")

	status, err := host.GetRunStatus(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRunStatus failed: %v", err)
	}
	if status == nil {
		t.Fatalf("expected non-nil status")
	}
	if got := strings.TrimSpace(status.Project); got != "beta" {
		t.Fatalf("expected collision lookup falls through to beta, got=%q", got)
	}
	if got := resolver.ListProjectsCount(); got != 0 {
		t.Fatalf("expected collision lookup resolved by index candidates without scan, got=%d", got)
	}
}

func TestExecutionHost_ListRunEvents_UsesRunProjectIndex(t *testing.T) {
	runID := uint(303)
	beta := &testExecutionHostProject{
		projectName: "beta",
		statusByRun: map[uint]*RunStatus{
			runID: {
				RunID:     runID,
				TicketID:  13,
				UpdatedAt: time.Now(),
			},
		},
		eventsByRun: map[uint][]RunEvent{
			runID: {
				{ID: 1, TaskRunID: runID, EventType: "created"},
				{ID: 2, TaskRunID: runID, EventType: "finished"},
			},
		},
	}
	resolver := &testExecutionHostResolver{
		projects: map[string]*testExecutionHostProject{
			"beta": beta,
		},
		projectOrder: []string{"beta"},
	}
	host, err := NewExecutionHost(resolver, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	host.addRunProjectIndex(runID, "beta")

	events, err := host.ListRunEvents(context.Background(), runID, 100)
	if err != nil {
		t.Fatalf("ListRunEvents failed: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got=%d", len(events))
	}
	if got := resolver.ListProjectsCount(); got != 0 {
		t.Fatalf("expected events lookup avoid scan when index hits, got=%d", got)
	}
	if got := host.scanFallbackCount.Load(); got != 0 {
		t.Fatalf("expected no fallback scan for indexed events lookup, got=%d", got)
	}
}

func TestExecutionHost_CancelRun_UsesRunProjectIndex(t *testing.T) {
	runID := uint(808)
	beta := &testExecutionHostProject{
		projectName: "beta",
		statusByRun: map[uint]*RunStatus{
			runID: {
				RunID:     runID,
				TicketID:  21,
				UpdatedAt: time.Now(),
			},
		},
	}
	resolver := &testExecutionHostResolver{
		projects: map[string]*testExecutionHostProject{
			"beta": beta,
		},
		projectOrder: []string{"beta"},
	}
	host, err := NewExecutionHost(resolver, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	host.addRunProjectIndex(runID, "beta")

	res, err := host.CancelRun(runID)
	if err != nil {
		t.Fatalf("CancelRun failed: %v", err)
	}
	if !res.Found || res.Canceled {
		t.Fatalf("expected found=true and canceled=false for historical run, got=%+v", res)
	}
	if got := strings.TrimSpace(res.Project); got != "beta" {
		t.Fatalf("expected cancel result carries indexed project beta, got=%q", got)
	}
	if got := resolver.ListProjectsCount(); got != 0 {
		t.Fatalf("expected cancel lookup avoid scan when index hits, got=%d", got)
	}
	if got := host.scanFallbackCount.Load(); got != 0 {
		t.Fatalf("expected no fallback scan for indexed cancel lookup, got=%d", got)
	}
}

func containsProject(projects []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, project := range projects {
		if strings.TrimSpace(project) == target {
			return true
		}
	}
	return false
}
