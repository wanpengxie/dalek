package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
	daemonsvc "dalek/internal/services/daemon"
	pmsvc "dalek/internal/services/pm"
)

type integrationTaskRunnerFunc func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error)

func (f integrationTaskRunnerFunc) Run(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
	return f(ctx, req, onEvent)
}

type singleProjectExecutionHostResolver struct {
	projectName string
	project     *Project
}

func (r singleProjectExecutionHostResolver) OpenProject(name string) (daemonsvc.ExecutionHostProject, error) {
	if r.project == nil {
		return nil, fmt.Errorf("project 为空")
	}
	if strings.TrimSpace(name) != strings.TrimSpace(r.projectName) {
		return nil, fmt.Errorf("unknown project: %s", strings.TrimSpace(name))
	}
	return &daemonProjectAdapter{project: r.project}, nil
}

func (r singleProjectExecutionHostResolver) ListProjects() ([]string, error) {
	if strings.TrimSpace(r.projectName) == "" {
		return nil, nil
	}
	return []string{strings.TrimSpace(r.projectName)}, nil
}

func requiredEnvUint(t *testing.T, env map[string]string, key string) uint {
	t.Helper()
	raw := strings.TrimSpace(env[key])
	if raw == "" {
		t.Fatalf("missing env %s", key)
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 {
		t.Fatalf("invalid env %s=%q err=%v", key, raw, err)
	}
	return uint(n)
}

func gitHeadSHAForIntegration(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse failed: %v\n%s", err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func writeWorkerLoopStateForIntegration(
	t *testing.T,
	ticketID uint,
	workerID uint,
	worktreePath string,
	nextAction string,
	summary string,
	blockers []string,
	allDone bool,
	headSHA string,
	workingTree string,
) {
	t.Helper()
	statePath := filepath.Join(worktreePath, ".dalek", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir failed: %v", err)
	}
	items := []string{
		`{"id":"phase-understanding","status":"done"}`,
		`{"id":"phase-implementation","status":"done"}`,
		`{"id":"phase-validation","status":"done"}`,
		`{"id":"phase-handoff","status":"done"}`,
	}
	currentStatus := "done"
	if !allDone {
		items = []string{
			`{"id":"phase-understanding","status":"done"}`,
			`{"id":"phase-implementation","status":"in_progress"}`,
			`{"id":"phase-validation","status":"pending"}`,
			`{"id":"phase-handoff","status":"pending"}`,
		}
		currentStatus = "running"
	}
	blockersJSON := "[]"
	if len(blockers) > 0 {
		quoted := make([]string, 0, len(blockers))
		for _, blocker := range blockers {
			blocker = strings.TrimSpace(blocker)
			if blocker == "" {
				continue
			}
			quoted = append(quoted, fmt.Sprintf("%q", blocker))
		}
		blockersJSON = "[" + strings.Join(quoted, ",") + "]"
	}
	raw := fmt.Sprintf(`{
  "ticket": {
    "id": %d,
    "worker_id": %d
  },
  "phases": {
    "current_id": "phase-handoff",
    "current_status": %q,
    "next_action": %q,
    "summary": %q,
    "items": [%s]
  },
  "blockers": %s,
  "code": {
    "head_sha": %q,
    "working_tree": %q,
    "last_commit_subject": "integration test commit"
  },
  "updated_at": %q
}
`, ticketID, workerID, currentStatus, strings.TrimSpace(nextAction), strings.TrimSpace(summary), strings.Join(items, ","), blockersJSON, strings.TrimSpace(headSHA), strings.TrimSpace(workingTree), time.Now().Format(time.RFC3339))
	if err := os.WriteFile(statePath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write state failed: %v", err)
	}
}

func applyWorkerReportForIntegration(
	t *testing.T,
	p *Project,
	req sdkrunner.Request,
	nextAction string,
	summary string,
	blockers []string,
	needsUser bool,
	dirty bool,
	headSHA string,
) {
	t.Helper()
	workerID := requiredEnvUint(t, req.Env, "DALEK_WORKER_ID")
	ticketID := requiredEnvUint(t, req.Env, "DALEK_TICKET_ID")
	runID := requiredEnvUint(t, req.Env, "DALEK_TASK_RUN_ID")
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ReportedAt: time.Now().Format(time.RFC3339),
		ProjectKey: strings.TrimSpace(p.Key()),
		WorkerID:   workerID,
		TicketID:   ticketID,
		TaskRunID:  runID,
		HeadSHA:    strings.TrimSpace(headSHA),
		Summary:    strings.TrimSpace(summary),
		Blockers:   append([]string(nil), blockers...),
		NeedsUser:  needsUser,
		Dirty:      dirty,
		NextAction: strings.TrimSpace(nextAction),
	}
	if err := p.ApplyWorkerReport(context.Background(), report, "integration.real_worker_loop"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}
}

func reserveLoopbackAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback addr failed: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitUntil(t *testing.T, timeout time.Duration, check func() bool, desc string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", desc)
}

func TestIntegration_FreshRepo_WorkerLoopCleanDone(t *testing.T) {
	_, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "fresh repo clean done", "clean done should promote ticket")
	if err != nil {
		t.Fatalf("CreateTicketWithDescription failed: %v", err)
	}

	p.pm.SetTaskRunner(integrationTaskRunnerFunc(func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
		worktreePath := strings.TrimSpace(req.WorkDir)
		workerID := requiredEnvUint(t, req.Env, "DALEK_WORKER_ID")
		ticketID := requiredEnvUint(t, req.Env, "DALEK_TICKET_ID")
		headSHA := gitHeadSHAForIntegration(t, worktreePath)
		writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "done", "真实 fresh repo clean done", nil, true, headSHA, "clean")
		applyWorkerReportForIntegration(t, p, req, "done", "真实 fresh repo clean done", nil, false, false, headSHA)
		if onEvent != nil {
			onEvent(sdkrunner.Event{Type: "agent_message", Text: "clean done"})
		}
		return sdkrunner.Result{Provider: "test", OutputMode: sdkrunner.OutputModeJSONL, Text: "clean done"}, nil
	}))

	result, err := p.RunTicketWorker(ctx, tk.ID, pmsvc.WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if err != nil {
		t.Fatalf("RunTicketWorker failed: %v", err)
	}
	if result.Stages != 1 {
		t.Fatalf("expected 1 stage, got=%d", result.Stages)
	}
	if result.LastNextAction != string(contracts.NextDone) {
		t.Fatalf("expected next_action done, got=%q", result.LastNextAction)
	}

	view, err := p.GetTicketViewByID(ctx, tk.ID)
	if err != nil {
		t.Fatalf("GetTicketViewByID failed: %v", err)
	}
	if view == nil {
		t.Fatalf("ticket view should not be nil")
	}
	if view.Ticket.WorkflowStatus != contracts.TicketDone {
		t.Fatalf("expected ticket done, got=%s", view.Ticket.WorkflowStatus)
	}
	if got := contracts.CanonicalIntegrationStatus(view.Ticket.IntegrationStatus); got != contracts.IntegrationNeedsMerge {
		t.Fatalf("expected integration needs_merge, got=%s", got)
	}
}

func TestIntegration_FreshRepo_WorkerLoopDirtyDoneFallsBackToWaitUser(t *testing.T) {
	_, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "fresh repo dirty done", "dirty done must not promote ticket")
	if err != nil {
		t.Fatalf("CreateTicketWithDescription failed: %v", err)
	}

	p.pm.SetTaskRunner(integrationTaskRunnerFunc(func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
		worktreePath := strings.TrimSpace(req.WorkDir)
		workerID := requiredEnvUint(t, req.Env, "DALEK_WORKER_ID")
		ticketID := requiredEnvUint(t, req.Env, "DALEK_TICKET_ID")
		headSHA := gitHeadSHAForIntegration(t, worktreePath)
		dirtyPath := filepath.Join(worktreePath, "tmp", "dirty.txt")
		if err := os.MkdirAll(filepath.Dir(dirtyPath), 0o755); err != nil {
			t.Fatalf("mkdir dirty dir failed: %v", err)
		}
		if err := os.WriteFile(dirtyPath, []byte("dirty\n"), 0o644); err != nil {
			t.Fatalf("write dirty file failed: %v", err)
		}
		writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "done", "worktree 仍然 dirty", nil, true, headSHA, "dirty")
		applyWorkerReportForIntegration(t, p, req, "done", "worktree 仍然 dirty", nil, false, true, headSHA)
		if onEvent != nil {
			onEvent(sdkrunner.Event{Type: "agent_message", Text: "dirty done"})
		}
		return sdkrunner.Result{Provider: "test", OutputMode: sdkrunner.OutputModeJSONL, Text: "dirty done"}, nil
	}))

	result, err := p.RunTicketWorker(ctx, tk.ID, pmsvc.WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if err != nil {
		t.Fatalf("RunTicketWorker failed: %v", err)
	}
	if result.LastNextAction != string(contracts.NextWaitUser) {
		t.Fatalf("expected next_action wait_user, got=%q", result.LastNextAction)
	}

	view, err := p.GetTicketViewByID(ctx, tk.ID)
	if err != nil {
		t.Fatalf("GetTicketViewByID failed: %v", err)
	}
	if view == nil {
		t.Fatalf("ticket view should not be nil")
	}
	if view.Ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked, got=%s", view.Ticket.WorkflowStatus)
	}
	if got := contracts.CanonicalIntegrationStatus(view.Ticket.IntegrationStatus); got == contracts.IntegrationNeedsMerge {
		t.Fatalf("dirty done should not freeze integration, got=%s", got)
	}

	db := mustProjectDB(t, p)
	var inbox contracts.InboxItem
	if err := db.Where("ticket_id = ? AND status = ?", tk.ID, contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("load open inbox failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(inbox.Body), "dirty") {
		t.Fatalf("expected inbox mention dirty closure, got=%q", inbox.Body)
	}
}

func TestIntegration_FreshRepo_WorkerLoopClosureRepairWithinSameStage(t *testing.T) {
	_, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "fresh repo closure repair", "same stage repair should self-heal closure")
	if err != nil {
		t.Fatalf("CreateTicketWithDescription failed: %v", err)
	}

	var calls atomic.Int32
	p.pm.SetTaskRunner(integrationTaskRunnerFunc(func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
		worktreePath := strings.TrimSpace(req.WorkDir)
		workerID := requiredEnvUint(t, req.Env, "DALEK_WORKER_ID")
		ticketID := requiredEnvUint(t, req.Env, "DALEK_TICKET_ID")
		headSHA := gitHeadSHAForIntegration(t, worktreePath)
		call := calls.Add(1)
		if call == 1 {
			writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "done", "第一次报 done 但 state 未收口", nil, false, headSHA, "clean")
			applyWorkerReportForIntegration(t, p, req, "done", "第一次报 done 但 state 未收口", nil, false, false, headSHA)
		} else {
			if !strings.Contains(req.Prompt, "当前 stage 尚未闭合") {
				t.Fatalf("repair prompt missing closure hint: %q", req.Prompt)
			}
			writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "done", "repair 后 state 已收口", nil, true, headSHA, "clean")
			applyWorkerReportForIntegration(t, p, req, "done", "repair 后 state 已收口", nil, false, false, headSHA)
		}
		if onEvent != nil {
			onEvent(sdkrunner.Event{Type: "agent_message", Text: fmt.Sprintf("call=%d", call)})
		}
		return sdkrunner.Result{Provider: "test", OutputMode: sdkrunner.OutputModeJSONL, Text: fmt.Sprintf("call=%d", call)}, nil
	}))

	result, err := p.RunTicketWorker(ctx, tk.ID, pmsvc.WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if err != nil {
		t.Fatalf("RunTicketWorker failed: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("expected 2 runner calls, got=%d", got)
	}
	if result.Stages != 1 {
		t.Fatalf("expected same-stage repair to keep stage=1, got=%d", result.Stages)
	}
	if result.LastNextAction != string(contracts.NextDone) {
		t.Fatalf("expected next_action done after repair, got=%q", result.LastNextAction)
	}

	view, err := p.GetTicketViewByID(ctx, tk.ID)
	if err != nil {
		t.Fatalf("GetTicketViewByID failed: %v", err)
	}
	if view == nil || view.Ticket.WorkflowStatus != contracts.TicketDone {
		t.Fatalf("expected ticket done after repair, got=%+v", view)
	}
}

func TestIntegration_FreshRepo_DaemonTicketLoopContract(t *testing.T) {
	_, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "fresh repo daemon ticket loop", "ticket-loop submit/probe/cancel should work on fresh repo")
	if err != nil {
		t.Fatalf("CreateTicketWithDescription failed: %v", err)
	}

	cancelObserved := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseClosed atomic.Bool
	releaseLoop := func() {
		if releaseClosed.CompareAndSwap(false, true) {
			close(release)
		}
	}
	p.pm.SetTaskRunner(integrationTaskRunnerFunc(func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
		if onEvent != nil {
			onEvent(sdkrunner.Event{Type: "agent_message", Text: "ticket loop running"})
		}
		<-ctx.Done()
		select {
		case cancelObserved <- struct{}{}:
		default:
		}
		<-release
		return sdkrunner.Result{Provider: "test", OutputMode: sdkrunner.OutputModeJSONL, Text: "canceled"}, ctx.Err()
	}))

	resolver := singleProjectExecutionHostResolver{projectName: "demo", project: p}
	host, err := daemonsvc.NewExecutionHost(resolver, daemonsvc.ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	addr := reserveLoopbackAddr(t)
	api, err := daemonsvc.NewInternalAPI(host, daemonsvc.InternalAPIConfig{ListenAddr: addr}, daemonsvc.InternalAPIOptions{})
	if err != nil {
		t.Fatalf("NewInternalAPI failed: %v", err)
	}
	if err := api.Start(ctx); err != nil {
		t.Fatalf("InternalAPI Start failed: %v", err)
	}
	t.Cleanup(func() {
		releaseLoop()
		_ = api.Stop(context.Background())
		_ = host.Stop(context.Background())
	})

	client, err := NewDaemonAPIClient(DaemonAPIClientConfig{BaseURL: "http://" + addr})
	if err != nil {
		t.Fatalf("NewDaemonAPIClient failed: %v", err)
	}

	resp, err := http.Post("http://"+addr+"/api/worker-run/submit", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST legacy worker-run route failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected legacy worker-run route 404, got=%d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	resp, err = http.Post("http://"+addr+"/api/runs/1/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("POST legacy runs route failed: %v", err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected legacy runs route 404, got=%d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	receipt, err := client.SubmitTicketLoop(ctx, DaemonTicketLoopSubmitRequest{
		Project:   "demo",
		TicketID:  tk.ID,
		RequestID: "fresh-repo-ticket-loop",
		Prompt:    "继续执行任务",
	})
	if err != nil {
		t.Fatalf("SubmitTicketLoop failed: %v", err)
	}
	if receipt.TaskRunID == 0 {
		t.Fatalf("expected task_run_id on submit receipt")
	}

	waitUntil(t, 5*time.Second, func() bool {
		probe, probeErr := client.ProbeTicketLoop(ctx, "demo", tk.ID)
		return probeErr == nil && probe.Found
	}, "ticket-loop probe found")

	probe, err := client.ProbeTicketLoop(ctx, "demo", tk.ID)
	if err != nil {
		t.Fatalf("ProbeTicketLoop failed: %v", err)
	}
	if !probe.Found || !probe.OwnedByCurrentDaemon {
		t.Fatalf("unexpected probe result: %+v", probe)
	}
	if probe.RunID != receipt.TaskRunID {
		t.Fatalf("probe run_id mismatch: got=%d want=%d", probe.RunID, receipt.TaskRunID)
	}
	if strings.TrimSpace(probe.Phase) == "" {
		t.Fatalf("expected non-empty probe phase")
	}

	cancelRes, err := client.CancelTicketLoop(ctx, "demo", tk.ID)
	if err != nil {
		t.Fatalf("CancelTicketLoop failed: %v", err)
	}
	if !cancelRes.Found || !cancelRes.Canceled {
		t.Fatalf("unexpected cancel result: %+v", cancelRes)
	}

	select {
	case <-cancelObserved:
	case <-time.After(5 * time.Second):
		t.Fatalf("runner did not observe cancel")
	}

	probe, err = client.ProbeTicketLoop(ctx, "demo", tk.ID)
	if err != nil {
		t.Fatalf("ProbeTicketLoop after cancel failed: %v", err)
	}
	if probe.Found && probe.Phase != "canceling" {
		t.Fatalf("expected canceling-or-gone probe snapshot, got=%+v", probe)
	}

	releaseLoop()
	waitUntil(t, 5*time.Second, func() bool {
		probe, probeErr := client.ProbeTicketLoop(ctx, "demo", tk.ID)
		return probeErr == nil && !probe.Found
	}, "ticket-loop settle after cancel")
}
