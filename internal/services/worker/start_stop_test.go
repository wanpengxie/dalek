package worker

import (
	"context"
	"dalek/internal/contracts"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	tasksvc "dalek/internal/services/task"
)

func TestStartTicket_CreatesWorkerAndSession(t *testing.T) {
	svc, p, fTmux, fGit := newServiceForTest(t)

	tk := createTicket(t, p.DB, "ticket-start")
	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicketResources failed: %v", err)
	}
	if w.ID == 0 {
		t.Fatalf("expected worker ID")
	}
	if strings.TrimSpace(w.TmuxSession) == "" {
		t.Fatalf("expected tmux session")
	}
	if w.Status != contracts.WorkerCreating {
		t.Fatalf("unexpected worker status: %s", w.Status)
	}
	if w.ProcessPID <= 0 {
		t.Fatalf("expected runtime pid > 0, got %d", w.ProcessPID)
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
	if fTmux.NewSessionCalls != 0 {
		t.Fatalf("expected runtime-primary start without tmux new-session, got %d", fTmux.NewSessionCalls)
	}
	if fGit.AddCalls != 1 {
		t.Fatalf("expected one git worktree add call, got %d", fGit.AddCalls)
	}

	var ev contracts.WorkerStatusEvent
	if err := p.DB.Where("worker_id = ?", w.ID).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("query worker status event failed: %v", err)
	}
	if ev.FromStatus != contracts.WorkerStopped || ev.ToStatus != contracts.WorkerCreating {
		t.Fatalf("unexpected worker status event transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
}

func TestStartTicket_RuntimePrimaryWithoutTmuxClient(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	p.Tmux = nil

	tk := createTicket(t, p.DB, "ticket-start-no-tmux")
	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicketResources failed without tmux client: %v", err)
	}
	if w.ProcessPID <= 0 {
		t.Fatalf("expected runtime pid > 0, got %d", w.ProcessPID)
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
	svc, p, _, _ := newServiceForTest(t)

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
	svc, p, _, fGit := newServiceForTest(t)
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
	svc, p, _, fGit := newServiceForTest(t)
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
	svc, p, _, fGit := newServiceForTest(t)
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

func TestStartTicket_RuntimePrimaryIgnoresTmuxNewSessionFailure(t *testing.T) {
	svc, p, fTmux, fGit := newServiceForTest(t)
	fTmux.NewSessionErr = errors.New("tmux new-session failed")

	tk := createTicket(t, p.DB, "ticket-rollback-session")
	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicketResources should succeed via runtime path, got=%v", err)
	}
	if w.ProcessPID <= 0 {
		t.Fatalf("expected runtime pid > 0, got %d", w.ProcessPID)
	}
	if fGit.RemoveCalls != 0 {
		t.Fatalf("worktree should not be rolled back on successful runtime start, got=%d", fGit.RemoveCalls)
	}
}

func TestStartTicket_RuntimePrimarySkipsRollbackCleanupOnTmuxFailure(t *testing.T) {
	svc, p, fTmux, fGit := newServiceForTest(t)
	fTmux.NewSessionErr = errors.New("tmux new-session failed")
	fGit.RemoveErr = errors.New("remove worktree failed")

	tk := createTicket(t, p.DB, "ticket-rollback-best-effort")
	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("runtime-primary start should not fail due tmux fallback error, got=%v", err)
	}
	if w.ProcessPID <= 0 {
		t.Fatalf("expected runtime pid > 0, got %d", w.ProcessPID)
	}
	if fGit.RemoveCalls != 0 {
		t.Fatalf("unexpected rollback remove attempt, got=%d", fGit.RemoveCalls)
	}
}

func TestStartTicket_FreshWorkerCleansStaleSameNameSession(t *testing.T) {
	svc, p, fTmux, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "ticket-fresh-clean-stale-session")
	staleSession := fmt.Sprintf("ts-%s-t%d-w1", p.Key, tk.ID)
	fTmux.Sessions[staleSession] = true

	w, err := svc.StartTicketResources(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicketResources failed: %v", err)
	}
	if strings.TrimSpace(w.TmuxSession) != staleSession {
		t.Fatalf("unexpected session name: got=%q want=%q", strings.TrimSpace(w.TmuxSession), staleSession)
	}
	if fTmux.KillSessionCalls < 1 {
		t.Fatalf("expected stale session cleanup before start, killSessionCalls=%d", fTmux.KillSessionCalls)
	}
	if fTmux.Sessions[staleSession] {
		t.Fatalf("runtime-primary path should not recreate tmux session")
	}
}

func TestStartTicket_RestartReusesWorkerRecord(t *testing.T) {
	svc, p, fTmux, _ := newServiceForTest(t)

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
	if fTmux.NewSessionCalls != 0 {
		t.Fatalf("runtime-primary restart should not create tmux session, got %d", fTmux.NewSessionCalls)
	}
}

func TestStartTicket_RunningSessionDoesNotPromoteTicketStatus(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

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
	svc, p, fTmux, _ := newServiceForTest(t)

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
	if fTmux.KillSessionCalls != 0 {
		t.Fatalf("runtime-primary stop should not require kill-session, got %d", fTmux.KillSessionCalls)
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

func TestStopTicket_KillsOrphanTmuxSessionsWhenNoWorkerRecord(t *testing.T) {
	svc, _, fTmux, _ := newServiceForTest(t)

	tk := createTicket(t, svc.p.DB, "ticket-orphan-session")
	// 模拟“DB 被清空/缺记录，但 tmux socket 里还残留旧 session”的情况。
	// stop -ticket 应该能按命名约定清理掉这类 session。
	orphan := "ts-demo-t" + strconv.Itoa(int(tk.ID)) + "-w999"
	fTmux.Sessions[orphan] = true

	if err := svc.StopTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StopTicket failed: %v", err)
	}
	if fTmux.Sessions[orphan] {
		t.Fatalf("expected orphan session killed")
	}
	if fTmux.KillSessionCalls != 1 {
		t.Fatalf("expected one kill-session call, got %d", fTmux.KillSessionCalls)
	}
}

func TestReconcileRunningWorkersAfterKillAll_DoesNotChangeTicketWorkflowStatus(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	tkRunning := createTicket(t, p.DB, "reconcile-running")
	tkDone := createTicket(t, p.DB, "reconcile-done")
	tkBlocked := createTicket(t, p.DB, "reconcile-blocked")
	tkBacklog := createTicket(t, p.DB, "reconcile-backlog")

	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tkRunning.ID).Update("workflow_status", contracts.TicketActive).Error; err != nil {
		t.Fatalf("set active ticket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tkDone.ID).Update("workflow_status", contracts.TicketDone).Error; err != nil {
		t.Fatalf("set done ticket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tkBlocked.ID).Update("workflow_status", contracts.TicketBlocked).Error; err != nil {
		t.Fatalf("set blocked ticket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tkBacklog.ID).Update("workflow_status", contracts.TicketBacklog).Error; err != nil {
		t.Fatalf("set backlog ticket failed: %v", err)
	}

	workers := []contracts.Worker{
		{
			TicketID:     tkRunning.ID,
			Status:       contracts.WorkerRunning,
			WorktreePath: "/tmp/w-running",
			Branch:       "ts/demo-ticket-running",
			TmuxSocket:   "dalek-test",
			TmuxSession:  "s-running",
		},
		{
			TicketID:     tkDone.ID,
			Status:       contracts.WorkerRunning,
			WorktreePath: "/tmp/w-done",
			Branch:       "ts/demo-ticket-done",
			TmuxSocket:   "dalek-test",
			TmuxSession:  "s-done",
		},
		{
			TicketID:     tkBlocked.ID,
			Status:       contracts.WorkerRunning,
			WorktreePath: "/tmp/w-blocked",
			Branch:       "ts/demo-ticket-blocked",
			TmuxSocket:   "dalek-test",
			TmuxSession:  "s-blocked",
		},
		{
			TicketID:     tkBacklog.ID,
			Status:       contracts.WorkerRunning,
			WorktreePath: "/tmp/w-backlog",
			Branch:       "ts/demo-ticket-backlog",
			TmuxSocket:   "dalek-test",
			TmuxSession:  "s-backlog",
		},
	}
	if err := p.DB.Create(&workers).Error; err != nil {
		t.Fatalf("create workers failed: %v", err)
	}
	rt := tasksvc.New(p.DB)
	for i, w := range workers {
		now := time.Now().UTC().Add(time.Duration(i) * time.Second)
		run, err := rt.CreateRun(context.Background(), contracts.TaskRunCreateInput{
			OwnerType:          contracts.TaskOwnerWorker,
			TaskType:           "deliver_ticket",
			ProjectKey:         p.Key,
			TicketID:           w.TicketID,
			WorkerID:           w.ID,
			SubjectType:        "ticket",
			SubjectID:          fmt.Sprintf("%d", w.TicketID),
			RequestID:          fmt.Sprintf("reconcile-run-%d", w.ID),
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
	}

	rows, err := svc.ReconcileRunningWorkersAfterKillAll(context.Background(), "dalek-test")
	if err != nil {
		t.Fatalf("ReconcileRunningWorkersAfterKillAll failed: %v", err)
	}
	if rows != int64(len(workers)) {
		t.Fatalf("expected reconciled workers=%d, got=%d", len(workers), rows)
	}

	var tickets []contracts.Ticket
	if err := p.DB.Where("id IN ?", []uint{tkRunning.ID, tkDone.ID, tkBlocked.ID, tkBacklog.ID}).Order("id asc").Find(&tickets).Error; err != nil {
		t.Fatalf("query tickets failed: %v", err)
	}
	got := map[uint]contracts.TicketWorkflowStatus{}
	for _, tk := range tickets {
		got[tk.ID] = tk.WorkflowStatus
	}

	if got[tkRunning.ID] != contracts.TicketActive {
		t.Fatalf("active ticket should be preserved, got=%s", got[tkRunning.ID])
	}
	if got[tkDone.ID] != contracts.TicketDone {
		t.Fatalf("done ticket should be preserved, got=%s", got[tkDone.ID])
	}
	if got[tkBlocked.ID] != contracts.TicketBlocked {
		t.Fatalf("blocked ticket should be preserved, got=%s", got[tkBlocked.ID])
	}
	if got[tkBacklog.ID] != contracts.TicketBacklog {
		t.Fatalf("backlog ticket should stay backlog, got=%s", got[tkBacklog.ID])
	}

	statusRows, err := rt.ListStatus(context.Background(), contracts.TaskListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		IncludeTerminal: true,
		Limit:           20,
	})
	if err != nil {
		t.Fatalf("list task status failed: %v", err)
	}
	byWorker := map[uint]contracts.TaskStatusView{}
	for _, it := range statusRows {
		if it.WorkerID == 0 {
			continue
		}
		if _, ok := byWorker[it.WorkerID]; ok {
			continue
		}
		byWorker[it.WorkerID] = it
	}
	for _, w := range workers {
		st, ok := byWorker[w.ID]
		if !ok {
			t.Fatalf("missing task status for worker=%d", w.ID)
		}
		if st.OrchestrationState != string(contracts.TaskCanceled) {
			t.Fatalf("worker=%d expected canceled, got=%s", w.ID, st.OrchestrationState)
		}
		if st.RuntimeHealthState != string(contracts.TaskHealthDead) {
			t.Fatalf("worker=%d expected dead runtime, got=%s", w.ID, st.RuntimeHealthState)
		}
	}
}
