package worker

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tasksvc "dalek/internal/services/task"
)

func TestStartTicket_CreatesWorkerAndRuntimeAnchor(t *testing.T) {
	svc, p, fGit := newServiceForTest(t)

	tk := createTicket(t, p.DB, "ticket-start")
	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicketResources failed: %v", err)
	}
	if w.ID == 0 {
		t.Fatalf("expected worker ID")
	}
	if w.Status != contracts.WorkerStopped {
		t.Fatalf("unexpected worker status: %s", w.Status)
	}
	if strings.TrimSpace(w.LogPath) == "" {
		t.Fatalf("expected runtime log path")
	}
	var cnt int64
	if err := p.DB.Model(&contracts.Worker{}).Where("ticket_id = ?", tk.ID).Count(&cnt).Error; err != nil {
		t.Fatalf("count workers failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected 1 worker, got %d", cnt)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBacklog {
		t.Fatalf("expected ticket backlog, got %s", ticket.WorkflowStatus)
	}
	if fGit.AddCalls != 1 {
		t.Fatalf("expected one git worktree add call, got %d", fGit.AddCalls)
	}

	var ev contracts.WorkerStatusEvent
	if err := p.DB.Where("worker_id = ?", w.ID).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("query worker status event failed: %v", err)
	}
	if ev.FromStatus != contracts.WorkerCreating || ev.ToStatus != contracts.WorkerStopped {
		t.Fatalf("unexpected worker status event transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
}

func TestStartTicket_RuntimePrimaryWithoutTmuxClient(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "ticket-start-no-tmux")
	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicketResources failed without tmux client: %v", err)
	}
	if strings.TrimSpace(w.LogPath) == "" {
		t.Fatalf("expected runtime log path")
	}

	attachCmd, err := svc.AttachCmd(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("AttachCmd should prefer runtime attach without tmux client: %v", err)
	}
	if attachCmd == nil {
		t.Fatalf("expected non-nil attach command")
	}

	if err := svc.StopTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StopTicket failed without tmux client: %v", err)
	}
}

func TestStartTicket_NewWorkerUsesRunScopedBranchAndWorktree(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "Worker PM Dispatch")
	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicketResources failed: %v", err)
	}

	wantBranchPrefix := fmt.Sprintf("ts/%s/t%d-", p.Key, tk.ID)
	if !strings.HasPrefix(strings.TrimSpace(w.Branch), wantBranchPrefix) {
		t.Fatalf("unexpected branch naming: got=%q want_prefix=%q", w.Branch, wantBranchPrefix)
	}
	if strings.TrimSpace(w.Branch) == fmt.Sprintf("ts/%s/t%d", p.Key, tk.ID) {
		t.Fatalf("branch should be run-scoped with nonce, got=%q", w.Branch)
	}

	base := filepath.Base(strings.TrimSpace(w.WorktreePath))
	wantWorktreePrefix := fmt.Sprintf("ticket-t%d-", tk.ID)
	if !strings.HasPrefix(base, wantWorktreePrefix) {
		t.Fatalf("unexpected worktree path naming: got=%q want_prefix=%q", base, wantWorktreePrefix)
	}
	if base == fmt.Sprintf("ticket-%d", tk.ID) {
		t.Fatalf("worktree path should not reuse static ticket-%d naming", tk.ID)
	}
}

func TestStartTicket_DefaultBaseUsesCurrentBranch(t *testing.T) {
	svc, p, fGit := newServiceForTest(t)
	fGit.CurrentBranchValue = "feature/current"

	tk := createTicket(t, p.DB, "base-current-branch")
	if _, err := svc.StartTicketResources(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicketResources failed: %v", err)
	}
	if got := strings.TrimSpace(fGit.LastBaseBranch); got != "feature/current" {
		t.Fatalf("expected base branch from current branch, got=%q", got)
	}
}

func TestStartTicket_BaseOverrideTakesPrecedence(t *testing.T) {
	svc, p, fGit := newServiceForTest(t)
	fGit.CurrentBranchValue = "feature/current"

	tk := createTicket(t, p.DB, "base-override")
	if _, err := svc.StartTicketResourcesWithOptions(context.Background(), tk.ID, StartOptions{
		BaseBranch: "release/v1",
	}); err != nil {
		t.Fatalf("StartTicketResourcesWithOptions failed: %v", err)
	}
	if got := strings.TrimSpace(fGit.LastBaseBranch); got != "release/v1" {
		t.Fatalf("expected base override release/v1, got=%q", got)
	}
}

func TestStartTicket_RollbackWorktreeWhenEnsureContractFails(t *testing.T) {
	svc, p, fGit := newServiceForTest(t)
	fGit.AfterAdd = func(path string) error {
		return os.WriteFile(filepath.Join(path, ".dalek"), []byte("conflict"), 0o644)
	}

	tk := createTicket(t, p.DB, "ticket-rollback-worktree")
	_, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err == nil {
		t.Fatalf("StartTicketResources should fail when EnsureWorktreeContract fails")
	}
	if fGit.AddCalls != 1 {
		t.Fatalf("expected one worktree add call, got=%d", fGit.AddCalls)
	}
	if fGit.RemoveCalls != 1 {
		t.Fatalf("expected rollback remove worktree once, got=%d", fGit.RemoveCalls)
	}
}

func TestStartTicket_RestartReusesWorkerRecord(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "ticket-restart")

	first, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("first StartTicketResources failed: %v", err)
	}
	if first.ID == 0 {
		t.Fatalf("expected first worker ID")
	}

	if err := svc.StopTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StopTicket failed: %v", err)
	}

	second, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("second StartTicketResources failed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected restart reuse same worker record id=%d, got %d", first.ID, second.ID)
	}

	var cnt int64
	if err := p.DB.Model(&contracts.Worker{}).Where("ticket_id = ?", tk.ID).Count(&cnt).Error; err != nil {
		t.Fatalf("count workers failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected single worker record after restart, got %d", cnt)
	}
}

func TestStartTicket_RunningSessionDoesNotPromoteTicketStatus(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "ticket-running-no-promote")
	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("first StartTicketResources failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Update("status", contracts.WorkerRunning).Error; err != nil {
		t.Fatalf("set worker running failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketBacklog).Error; err != nil {
		t.Fatalf("reset ticket backlog failed: %v", err)
	}

	if _, err := svc.StartTicketResources(context.Background(), tk.ID); err != nil {
		t.Fatalf("second StartTicketResources failed: %v", err)
	}

	var got contracts.Ticket
	if err := p.DB.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if got.WorkflowStatus != contracts.TicketBacklog {
		t.Fatalf("running session restart should not promote ticket workflow status, got=%s", got.WorkflowStatus)
	}
}

func TestStopWorker_UpdatesWorkerAndTicket(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "ticket-stop")
	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicketResources failed: %v", err)
	}
	rt := tasksvc.New(p.DB)
	now := time.Now().UTC().Truncate(time.Second)
	run, err := rt.CreateRun(context.Background(), contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
		ProjectKey:         p.Key,
		TicketID:           tk.ID,
		WorkerID:           w.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          "stop-worker-run-1",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("create task run failed: %v", err)
	}
	if err := rt.AppendRuntimeSample(context.Background(), contracts.TaskRuntimeSampleInput{
		TaskRunID:  run.ID,
		State:      contracts.TaskHealthBusy,
		NeedsUser:  false,
		Summary:    "running",
		Source:     "test",
		ObservedAt: now,
	}); err != nil {
		t.Fatalf("seed runtime sample failed: %v", err)
	}

	if err := svc.StopWorker(context.Background(), w.ID); err != nil {
		t.Fatalf("StopWorker failed: %v", err)
	}

	var got contracts.Worker
	if err := p.DB.First(&got, w.ID).Error; err != nil {
		t.Fatalf("query worker failed: %v", err)
	}
	if got.Status != contracts.WorkerStopped {
		t.Fatalf("expected stopped worker, got %s", got.Status)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBacklog {
		t.Fatalf("expected ticket workflow backlog, got %s", ticket.WorkflowStatus)
	}
	statusRows, err := rt.ListStatus(context.Background(), contracts.TaskListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		WorkerID:        w.ID,
		IncludeTerminal: true,
		Limit:           5,
	})
	if err != nil || len(statusRows) == 0 {
		t.Fatalf("load task status failed: %v", err)
	}
	latest := statusRows[0]
	if latest.OrchestrationState != string(contracts.TaskCanceled) {
		t.Fatalf("expected task canceled after stop, got=%s", latest.OrchestrationState)
	}
	if latest.RuntimeHealthState != string(contracts.TaskHealthDead) {
		t.Fatalf("expected task runtime dead after stop, got=%s", latest.RuntimeHealthState)
	}
	if strings.TrimSpace(latest.LastEventType) != "task_canceled" {
		t.Fatalf("expected last event task_canceled, got=%s", latest.LastEventType)
	}
}
