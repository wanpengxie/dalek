package pm

import (
	"context"
	"dalek/internal/contracts"
	"dalek/internal/services/agentexec"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stubWorkerRunSubmitter struct {
	mu    sync.Mutex
	calls []uint
	err   error
	runID uint
}

func (s *stubWorkerRunSubmitter) SubmitTicketWorkerRun(_ context.Context, ticketID uint) (WorkerRunSubmission, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, ticketID)
	if s.err != nil {
		return WorkerRunSubmission{}, s.err
	}
	runID := s.runID
	if runID == 0 {
		runID = 1
	}
	return WorkerRunSubmission{TaskRunID: runID}, nil
}

func (s *stubWorkerRunSubmitter) CallIDs() []uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint, len(s.calls))
	copy(out, s.calls)
	return out
}

func TestManagerTick_UsesWorkerRunSubmitterWhenConfigured(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	svc.SetAutopilotEnabled(context.Background(), true)
	tk := createTicket(t, p.DB, "manager-tick-submitter")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}

	submitter := &stubWorkerRunSubmitter{}
	svc.SetWorkerRunSubmitter(submitter)

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if !containsTicketID(res.StartedTickets, tk.ID) {
		t.Fatalf("expected started ticket contains t%d, got=%v", tk.ID, res.StartedTickets)
	}
	if !containsTicketID(res.ActivatedTickets, tk.ID) {
		t.Fatalf("expected activated ticket recorded in ActivatedTickets for t%d, got=%v", tk.ID, res.ActivatedTickets)
	}

	callIDs := submitter.CallIDs()
	if len(callIDs) != 1 || callIDs[0] != tk.ID {
		t.Fatalf("expected submitter called once with t%d, got=%v", tk.ID, callIDs)
	}
}

func TestManagerTick_RejectsActivationWithoutSubmitter(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	svc.SetAutopilotEnabled(context.Background(), true)
	tk := createTicket(t, p.DB, "manager-tick-fallback")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if !containsTicketID(res.StartedTickets, tk.ID) {
		t.Fatalf("expected started ticket contains t%d, got=%v", tk.ID, res.StartedTickets)
	}
	if containsTicketID(res.ActivatedTickets, tk.ID) {
		t.Fatalf("activation should be rejected without submitter, got=%v", res.ActivatedTickets)
	}
	joined := strings.Join(res.Errors, "\n")
	if !strings.Contains(joined, "worker run submitter 未配置") {
		t.Fatalf("expected submitter missing error, got=%v", res.Errors)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyTicketIncident(tk.ID, "worker_run_no_submitter"), contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected worker_run_no_submitter inbox, err=%v", err)
	}
	if inbox.Severity != contracts.InboxBlocker {
		t.Fatalf("expected blocker severity, got=%s", inbox.Severity)
	}
	if inbox.Reason != contracts.InboxIncident {
		t.Fatalf("expected incident reason, got=%s", inbox.Reason)
	}
}

func TestManagerTick_DryRunSkipsWorkerRunSubmitter(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "manager-tick-dry-run")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}

	submitter := &stubWorkerRunSubmitter{}
	svc.SetWorkerRunSubmitter(submitter)

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{DryRun: true})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if len(submitter.CallIDs()) != 0 {
		t.Fatalf("dry-run should not call worker run submitter")
	}
	if len(res.StartedTickets) != 0 || len(res.ActivatedTickets) != 0 {
		t.Fatalf("dry-run should not start/activate tickets, started=%v activated=%v", res.StartedTickets, res.ActivatedTickets)
	}
}

func TestManagerTick_SyncWorkerRunBypassesSubmitter(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	svc.SetAutopilotEnabled(context.Background(), true)
	tk := createTicket(t, p.DB, "manager-tick-sync-worker-run")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}

	submitter := &stubWorkerRunSubmitter{}
	svc.SetWorkerRunSubmitter(submitter)
	var launchCount atomic.Int32
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		run := createWorkerTaskRun(t, p.DB, ticket.ID, worker.ID, fmt.Sprintf("sync-activation-%d-%d", ticket.ID, launchCount.Add(1)))
		makeSemanticReport(t, svc, run.ID, "done")
		writeWorkerLoopStateForTest(t, worker.WorktreePath, "done", "sync worker run done", nil, true, testWorkerDoneHeadSHA, "clean")
		return &fakeAgentRunHandle{runID: run.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{SyncWorkerRun: true})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if !containsTicketID(res.StartedTickets, tk.ID) {
		t.Fatalf("expected started ticket contains t%d, got=%v", tk.ID, res.StartedTickets)
	}
	if !containsTicketID(res.ActivatedTickets, tk.ID) {
		t.Fatalf("expected activated ticket recorded in ActivatedTickets for t%d, got=%v", tk.ID, res.ActivatedTickets)
	}
	if len(submitter.CallIDs()) != 0 {
		t.Fatalf("sync activation should bypass worker run submitter, got=%v", submitter.CallIDs())
	}
}

func TestManagerTick_SyncWorkerRunHonorsTimeout(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	svc.SetAutopilotEnabled(context.Background(), true)
	tk := createTicket(t, p.DB, "manager-tick-sync-timeout")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		run := createWorkerTaskRun(t, p.DB, ticket.ID, worker.ID, fmt.Sprintf("sync-timeout-%d", ticket.ID))
		return blockingAgentRunHandle{runID: run.ID}, nil
	}

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{
		SyncWorkerRun:    true,
		WorkerRunTimeout: time.Nanosecond,
	})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if containsTicketID(res.ActivatedTickets, tk.ID) {
		t.Fatalf("activation should timeout when worker-run-timeout is tiny, got=%v", res.ActivatedTickets)
	}
	if len(res.Errors) == 0 {
		t.Fatalf("expected sync activation timeout errors")
	}
	joined := strings.Join(res.Errors, "\n")
	if !strings.Contains(joined, "sync activation 失败") {
		t.Fatalf("expected sync activation failure message, got=%v", res.Errors)
	}
}

func TestManagerTick_DemotesBlockedWhenActivationReportsWorkerReadyTimeout(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	svc.SetAutopilotEnabled(context.Background(), true)
	tk := createTicket(t, p.DB, "manager-tick-worker-ready-timeout")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}

	submitter := &stubWorkerRunSubmitter{
		err: &workerReadyTimeoutError{
			TicketID:   tk.ID,
			LastStatus: contracts.WorkerCreating,
			Waited:     5 * time.Second,
		},
	}
	svc.SetWorkerRunSubmitter(submitter)

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if containsTicketID(res.ActivatedTickets, tk.ID) {
		t.Fatalf("worker ready timeout should not mark activated, got=%v", res.ActivatedTickets)
	}
	joined := strings.Join(res.Errors, "\n")
	if !strings.Contains(joined, "submit worker run 失败") {
		t.Fatalf("expected submit worker run failure in errors, got=%v", res.Errors)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked after worker ready timeout, got=%s", ticket.WorkflowStatus)
	}

	var w contracts.Worker
	if err := p.DB.Where("ticket_id = ?", tk.ID).Order("id desc").First(&w).Error; err != nil {
		t.Fatalf("load latest worker failed: %v", err)
	}
	var inbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyWorkerIncident(w.ID, "worker_not_ready"), contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected worker_not_ready inbox, err=%v", err)
	}

	var ev contracts.TicketWorkflowEvent
	if err := p.DB.Where("ticket_id = ? AND to_workflow_status = ?", tk.ID, contracts.TicketBlocked).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("expected workflow event blocked, err=%v", err)
	}
	if ev.Source != "pm.manager_tick" {
		t.Fatalf("expected workflow event source pm.manager_tick, got=%s", ev.Source)
	}
}

func containsTicketID(ids []uint, want uint) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

type blockingAgentRunHandle struct {
	runID uint
}

func (h blockingAgentRunHandle) RunID() uint { return h.runID }

func (h blockingAgentRunHandle) Wait(ctx context.Context) (agentexec.AgentRunResult, error) {
	<-ctx.Done()
	return agentexec.AgentRunResult{}, ctx.Err()
}

func (h blockingAgentRunHandle) Cancel() error { return nil }
