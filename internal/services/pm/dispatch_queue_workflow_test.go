package pm

import (
	"context"
	"dalek/internal/contracts"
	"testing"
	"time"
)

func TestClaimPMDispatchJob_KeepsTicketQueuedUntilTargetReady(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-claim-promote-active")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketQueued).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}
	w := createDispatchWorker(t, p.DB, tk.ID)
	job, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, "", newDispatchTaskRequestPayload(tk.ID, w.ID, true, ""))
	if err != nil {
		t.Fatalf("enqueue dispatch job failed: %v", err)
	}

	runnerID := "runner-claim-active"
	got, claimed, err := svc.claimPMDispatchJob(context.Background(), job.ID, runnerID, 2*time.Minute)
	if err != nil {
		t.Fatalf("claim dispatch job failed: %v", err)
	}
	if !claimed {
		t.Fatalf("expected claimed=true")
	}
	if got.Status != contracts.PMDispatchRunning {
		t.Fatalf("expected running job, got=%s", got.Status)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket remain queued after claim, got=%s", ticket.WorkflowStatus)
	}
}

func TestPromoteTicketActiveForDispatch_PromotesQueuedTicket(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-promote-active")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketQueued).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}
	w := createDispatchWorker(t, p.DB, tk.ID)
	job, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, "", newDispatchTaskRequestPayload(tk.ID, w.ID, true, ""))
	if err != nil {
		t.Fatalf("enqueue dispatch job failed: %v", err)
	}

	if err := svc.promoteTicketActiveForDispatch(context.Background(), job, "runner-promote-active"); err != nil {
		t.Fatalf("promoteTicketActiveForDispatch failed: %v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketActive {
		t.Fatalf("expected ticket active after dispatch promote, got=%s", ticket.WorkflowStatus)
	}

	var ev contracts.TicketWorkflowEvent
	if err := p.DB.Where("ticket_id = ? AND to_workflow_status = ?", tk.ID, contracts.TicketActive).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("query workflow event failed: %v", err)
	}
	if ev.FromStatus != contracts.TicketQueued || ev.ToStatus != contracts.TicketActive {
		t.Fatalf("unexpected workflow event transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.Source != "pm.dispatch" {
		t.Fatalf("unexpected workflow event source: %s", ev.Source)
	}
}

func TestCompletePMDispatchJobFailed_DemotesTicketWorkflowToBlocked(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-failed-demote-blocked")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketQueued).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}
	w := createDispatchWorker(t, p.DB, tk.ID)
	job, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, "", newDispatchTaskRequestPayload(tk.ID, w.ID, true, ""))
	if err != nil {
		t.Fatalf("enqueue dispatch job failed: %v", err)
	}

	runnerID := "runner-failed-blocked"
	if _, claimed, err := svc.claimPMDispatchJob(context.Background(), job.ID, runnerID, 2*time.Minute); err != nil {
		t.Fatalf("claim dispatch job failed: %v", err)
	} else if !claimed {
		t.Fatalf("expected claimed=true")
	}

	if err := svc.completePMDispatchJobFailed(context.Background(), job.ID, runnerID, "dispatch boom"); err != nil {
		t.Fatalf("complete dispatch failed state failed: %v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked after dispatch failed, got=%s", ticket.WorkflowStatus)
	}

	var ev contracts.TicketWorkflowEvent
	if err := p.DB.Where("ticket_id = ? AND to_workflow_status = ?", tk.ID, contracts.TicketBlocked).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("query workflow event failed: %v", err)
	}
	if ev.FromStatus != contracts.TicketQueued || ev.ToStatus != contracts.TicketBlocked {
		t.Fatalf("unexpected workflow event transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.Source != "pm.dispatch" {
		t.Fatalf("unexpected workflow event source: %s", ev.Source)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyWorkerIncident(w.ID, "dispatch_failed"), contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("dispatch failed should create incident inbox: %v", err)
	}
	if inbox.Reason != contracts.InboxIncident {
		t.Fatalf("unexpected inbox reason: %s", inbox.Reason)
	}
	if inbox.TicketID != tk.ID || inbox.WorkerID != w.ID {
		t.Fatalf("unexpected inbox refs: ticket=%d worker=%d", inbox.TicketID, inbox.WorkerID)
	}
}
