package pm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
)

type stubFocusLoopControl struct {
	mu              sync.Mutex
	cancelRunIDs    []uint
	cancelRunCauses []contracts.TaskCancelCause
	cancelTicketIDs []uint
}

func (s *stubFocusLoopControl) CancelTaskRun(ctx context.Context, runID uint, cause contracts.TaskCancelCause) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelRunIDs = append(s.cancelRunIDs, runID)
	s.cancelRunCauses = append(s.cancelRunCauses, cause)
	return nil
}

func (s *stubFocusLoopControl) CancelTicketLoop(ctx context.Context, ticketID uint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cancelTicketIDs = append(s.cancelTicketIDs, ticketID)
	return nil
}

func (s *stubFocusLoopControl) RunIDs() []uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint, len(s.cancelRunIDs))
	copy(out, s.cancelRunIDs)
	return out
}

func (s *stubFocusLoopControl) RunCauses() []contracts.TaskCancelCause {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]contracts.TaskCancelCause, len(s.cancelRunCauses))
	copy(out, s.cancelRunCauses)
	return out
}

func (s *stubFocusLoopControl) TicketIDs() []uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]uint, len(s.cancelTicketIDs))
	copy(out, s.cancelTicketIDs)
	return out
}

func TestAdvanceFocusController_AdoptsActiveTicket(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-adopt-active")
	tk2 := createTicket(t, p.DB, "focus-adopt-next")
	w1, err := svc.StartTicket(ctx, tk1.ID)
	if err != nil {
		t.Fatalf("StartTicket(t1) failed: %v", err)
	}
	run1 := createWorkerTaskRun(t, p.DB, tk1.ID, w1.ID, "focus-adopt-active-run")
	if _, err := svc.acceptWorkerRun(ctx, tk1.ID, w1, run1.ID, "test.focus.accept", contracts.TicketLifecycleActorSystem, map[string]any{
		"ticket_id": tk1.ID,
		"worker_id": w1.ID,
		"task_run":  run1.ID,
	}); err != nil {
		t.Fatalf("acceptWorkerRun failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}

	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	if view.ActiveItem == nil {
		t.Fatalf("expected active item")
	}
	if view.ActiveItem.TicketID != tk1.ID {
		t.Fatalf("expected active ticket t%d, got t%d", tk1.ID, view.ActiveItem.TicketID)
	}
	if view.ActiveItem.Status != contracts.FocusItemExecuting {
		t.Fatalf("expected active item executing, got=%s", view.ActiveItem.Status)
	}
	if view.ActiveItem.CurrentWorkerID == nil || *view.ActiveItem.CurrentWorkerID != w1.ID {
		t.Fatalf("expected adopted worker w%d, got=%v", w1.ID, view.ActiveItem.CurrentWorkerID)
	}
	if view.ActiveItem.CurrentTaskRunID == nil || *view.ActiveItem.CurrentTaskRunID != run1.ID {
		t.Fatalf("expected adopted task_run %d, got=%v", run1.ID, view.ActiveItem.CurrentTaskRunID)
	}
	if got := focusItemByTicketID(view.Items, tk2.ID); got == nil || got.Status != contracts.FocusItemPending {
		t.Fatalf("expected t%d stays pending, got=%+v", tk2.ID, got)
	}
}

func TestScheduleQueuedTickets_StrictSerialSkipsLaterFocusTicket(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-serial-first")
	tk2 := createTicket(t, p.DB, "focus-serial-second")
	if _, err := svc.StartTicket(ctx, tk2.ID); err != nil {
		t.Fatalf("StartTicket(t2) failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}

	submitter := &stubWorkerRunSubmitter{}
	svc.SetWorkerRunSubmitter(submitter)
	out := svc.scheduleQueuedTickets(ctx, p.DB, scheduleOptions{
		Capacity:         2,
		RunningTicketIDs: map[uint]bool{},
	})
	if len(out.Errors) != 0 {
		t.Fatalf("scheduleQueuedTickets returned errors: %v", out.Errors)
	}
	callIDs := submitter.CallIDs()
	if len(callIDs) != 1 || callIDs[0] != tk1.ID {
		t.Fatalf("expected only current focus item t%d activated, got=%v", tk1.ID, callIDs)
	}
	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	if got := focusItemByTicketID(view.Items, tk2.ID); got == nil || got.Status != contracts.FocusItemPending {
		t.Fatalf("expected later focus item t%d stays pending, got=%+v", tk2.ID, got)
	}
}

func TestAdvanceFocusController_CompletesMergedItemAndAdvances(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-merged-first")
	tk2 := createTicket(t, p.DB, "focus-next-after-merged")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk1.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationMerged,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare t1 merged failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}

	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	item1 := focusItemByTicketID(view.Items, tk1.ID)
	if item1 == nil || item1.Status != contracts.FocusItemCompleted {
		t.Fatalf("expected t%d completed, got=%+v", tk1.ID, item1)
	}
	item2 := focusItemByTicketID(view.Items, tk2.ID)
	if item2 == nil || item2.Status != contracts.FocusItemQueued {
		t.Fatalf("expected t%d queued after t1 completion, got=%+v", tk2.ID, item2)
	}
}

func TestAdvanceFocusController_CancelingStopsActiveTicket(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()
	ctrl := &stubFocusLoopControl{}
	svc.SetFocusLoopControl(ctrl)

	tk := createTicket(t, p.DB, "focus-cancel-active")
	w, err := svc.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	run := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "focus-cancel-run")
	if _, err := svc.acceptWorkerRun(ctx, tk.ID, w, run.ID, "test.focus.cancel", contracts.TicketLifecycleActorSystem, nil); err != nil {
		t.Fatalf("acceptWorkerRun failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController adopt failed: %v", err)
	}
	if err := svc.FocusCancel(ctx, res.FocusID, "cancel-focus-test"); err != nil {
		t.Fatalf("FocusCancel failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController cancel failed: %v", err)
	}

	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	if view.Run.Status != contracts.FocusRunning {
		t.Fatalf("expected focus still running until execution terminal, got=%s", view.Run.Status)
	}
	item := focusItemByTicketID(view.Items, tk.ID)
	if item == nil || item.Status != contracts.FocusItemExecuting {
		t.Fatalf("expected item still executing before execution terminal, got=%+v", item)
	}
	var afterRun contracts.TaskRun
	if err := p.DB.First(&afterRun, run.ID).Error; err != nil {
		t.Fatalf("load task run failed: %v", err)
	}
	if afterRun.OrchestrationState != contracts.TaskRunning {
		t.Fatalf("expected task run remain running before execution terminal, got=%s", afterRun.OrchestrationState)
	}
	if got := ctrl.RunIDs(); len(got) != 1 || got[0] != run.ID {
		t.Fatalf("expected cancel task request for run %d, got=%v", run.ID, got)
	}
	if got := ctrl.RunCauses(); len(got) != 1 || got[0] != contracts.TaskCancelCauseFocusCancel {
		t.Fatalf("expected cancel run cause focus_cancel, got=%v", got)
	}
	if got := ctrl.TicketIDs(); len(got) != 1 || got[0] != tk.ID {
		t.Fatalf("expected cancel ticket request for t%d, got=%v", tk.ID, got)
	}
	var afterTicket contracts.Ticket
	if err := p.DB.First(&afterTicket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if afterTicket.WorkflowStatus == contracts.TicketBacklog {
		t.Fatalf("ticket workflow should not be repaired to backlog before execution terminal")
	}
	if err := p.DB.Model(&contracts.TaskRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"orchestration_state": contracts.TaskCanceled,
		"finished_at":         time.Now(),
		"updated_at":          time.Now(),
	}).Error; err != nil {
		t.Fatalf("set task run canceled failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerStopped,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set worker stopped failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController terminalize failed: %v", err)
	}
	view, err = svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet terminalized failed: %v", err)
	}
	if view.Run.Status != contracts.FocusCanceled {
		t.Fatalf("expected focus canceled after execution terminal, got=%s", view.Run.Status)
	}
	item = focusItemByTicketID(view.Items, tk.ID)
	if item == nil || item.Status != contracts.FocusItemCanceled {
		t.Fatalf("expected item canceled after execution terminal, got=%+v", item)
	}
}

func TestAdvanceFocusController_UserStopStopsItemInsteadOfRestart(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-user-stop")
	w, err := svc.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	run := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "focus-user-stop-run")
	if _, err := svc.acceptWorkerRun(ctx, tk.ID, w, run.ID, "test.focus.user_stop", contracts.TicketLifecycleActorSystem, nil); err != nil {
		t.Fatalf("acceptWorkerRun failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController adopt failed: %v", err)
	}

	rt, err := svc.taskRuntime()
	if err != nil {
		t.Fatalf("taskRuntime failed: %v", err)
	}
	if err := p.DB.Model(&contracts.TaskRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"orchestration_state": contracts.TaskCanceled,
		"error_code":          contracts.TaskCancelCauseUserStop,
		"finished_at":         time.Now(),
		"updated_at":          time.Now(),
	}).Error; err != nil {
		t.Fatalf("set task run canceled failed: %v", err)
	}
	if err := rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "task_canceled",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskCanceled,
			"cancel_cause":        contracts.TaskCancelCauseUserStop,
			"error_code":          contracts.TaskCancelCauseUserStop,
		},
		Note: "用户主动停止 ticket",
		Payload: map[string]any{
			"cancel_cause": contracts.TaskCancelCauseUserStop,
		},
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("append task_canceled event failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerStopped,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set worker stopped failed: %v", err)
	}

	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController user-stop failed: %v", err)
	}

	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	item := focusItemByTicketID(view.Items, tk.ID)
	if item == nil || item.Status != contracts.FocusItemStopped {
		t.Fatalf("expected item stopped after user stop, got=%+v", item)
	}
	if view.Run.Status != contracts.FocusRunning {
		t.Fatalf("expected focus run keep running after single item stop, got=%s", view.Run.Status)
	}
}

func TestFocusStop_PreservesRunRequestIDAndTerminalizesPendingItems(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, svc.p.DB, "focus-stop-pending-1")
	tk2 := createTicket(t, svc.p.DB, "focus-stop-pending-2")
	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
		RequestID:      "focus-start-req",
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.FocusStop(ctx, res.FocusID, "focus-stop-req"); err != nil {
		t.Fatalf("FocusStop failed: %v", err)
	}
	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	if view.Run.RequestID != "focus-start-req" {
		t.Fatalf("expected start request_id preserved, got=%q", view.Run.RequestID)
	}
	if view.Run.DesiredState != contracts.FocusDesiredStopping {
		t.Fatalf("expected desired_state=stopping, got=%s", view.Run.DesiredState)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}
	view, err = svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet after stop failed: %v", err)
	}
	if view.Run.Status != contracts.FocusStopped {
		t.Fatalf("expected focus stopped, got=%s", view.Run.Status)
	}
	for _, ticketID := range []uint{tk1.ID, tk2.ID} {
		item := focusItemByTicketID(view.Items, ticketID)
		if item == nil || item.Status != contracts.FocusItemStopped {
			t.Fatalf("expected t%d stopped, got=%+v", ticketID, item)
		}
	}
}

func TestAdvanceFocusController_BlocksAfterFocusBudgetExhausted(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-budget-exhausted")
	w, err := svc.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	run1 := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "focus-restart-run-1")
	if _, err := svc.acceptWorkerRun(ctx, tk.ID, w, run1.ID, "test.focus.restart1", contracts.TicketLifecycleActorSystem, nil); err != nil {
		t.Fatalf("acceptWorkerRun(1) failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
		AgentBudget:    2,
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController adopt failed: %v", err)
	}

	if err := p.DB.Model(&contracts.TaskRun{}).Where("id = ?", run1.ID).Updates(map[string]any{
		"orchestration_state": contracts.TaskCanceled,
		"finished_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("mark run1 canceled failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("requeue ticket after run1 failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController first requeue failed: %v", err)
	}
	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet after first requeue failed: %v", err)
	}
	if view.Run.AgentBudget != 1 {
		t.Fatalf("expected remaining budget=1 after first restart, got=%d", view.Run.AgentBudget)
	}

	run2 := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "focus-restart-run-2")
	if _, err := svc.acceptWorkerRun(ctx, tk.ID, w, run2.ID, "test.focus.restart2", contracts.TicketLifecycleActorSystem, nil); err != nil {
		t.Fatalf("acceptWorkerRun(2) failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController second accept failed: %v", err)
	}

	if err := p.DB.Model(&contracts.TaskRun{}).Where("id = ?", run2.ID).Updates(map[string]any{
		"orchestration_state": contracts.TaskCanceled,
		"finished_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("mark run2 canceled failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("requeue ticket after run2 failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController second requeue failed: %v", err)
	}

	view, err = svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet after second requeue failed: %v", err)
	}
	if view.Run.AgentBudget != 0 {
		t.Fatalf("expected remaining budget=0 after second restart, got=%d", view.Run.AgentBudget)
	}

	run3 := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "focus-restart-run-3")
	if _, err := svc.acceptWorkerRun(ctx, tk.ID, w, run3.ID, "test.focus.restart3", contracts.TicketLifecycleActorSystem, nil); err != nil {
		t.Fatalf("acceptWorkerRun(3) failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController third accept failed: %v", err)
	}

	if err := p.DB.Model(&contracts.TaskRun{}).Where("id = ?", run3.ID).Updates(map[string]any{
		"orchestration_state": contracts.TaskCanceled,
		"finished_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("mark run3 canceled failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("requeue ticket after run3 failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController budget exhausted failed: %v", err)
	}

	view, err = svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	if view.Run.Status != contracts.FocusBlocked {
		t.Fatalf("expected focus blocked, got=%s", view.Run.Status)
	}
	item := focusItemByTicketID(view.Items, tk.ID)
	if item == nil || item.Status != contracts.FocusItemBlocked {
		t.Fatalf("expected item blocked, got=%+v", item)
	}
	if item.BlockedReason != "budget_exhausted" {
		t.Fatalf("expected blocked_reason budget_exhausted, got=%s", item.BlockedReason)
	}
	if item.CurrentAttempt != 3 {
		t.Fatalf("expected current_attempt=3 after budget-exhausted block, got=%d", item.CurrentAttempt)
	}
}

func TestAdvanceFocusController_CreatesIntegrationTicketAndBlocksHandoffOnMergeConflict(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-merge-conflict")
	tk2 := createTicket(t, p.DB, "focus-merge-conflict-next")
	targetBranch := prepareFocusMergeConflictTicket(t, svc, p, tk1, "")

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController select failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController merge failed: %v", err)
	}

	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	if view.Run.Status != contracts.FocusBlocked {
		t.Fatalf("expected focus blocked, got=%s", view.Run.Status)
	}
	item1 := focusItemByTicketID(view.Items, tk1.ID)
	if item1 == nil || item1.Status != contracts.FocusItemBlocked {
		t.Fatalf("expected t%d blocked, got=%+v", tk1.ID, item1)
	}
	if item1.BlockedReason != focusBlockedReasonHandoffWaitingMerge {
		t.Fatalf("expected blocked_reason=%s, got=%s", focusBlockedReasonHandoffWaitingMerge, item1.BlockedReason)
	}
	if item1.HandoffTicketID == nil || *item1.HandoffTicketID == 0 {
		t.Fatalf("expected handoff ticket created, got=%v", item1.HandoffTicketID)
	}
	if got := focusItemByTicketID(view.Items, tk2.ID); got == nil || got.Status != contracts.FocusItemPending {
		t.Fatalf("expected t%d stays pending while t%d is blocked, got=%+v", tk2.ID, tk1.ID, got)
	}

	var ticketCount int64
	if err := p.DB.Model(&contracts.Ticket{}).Count(&ticketCount).Error; err != nil {
		t.Fatalf("count tickets failed: %v", err)
	}
	if ticketCount != 3 {
		t.Fatalf("expected replacement integration ticket, got count=%d", ticketCount)
	}

	var replacement contracts.Ticket
	if err := p.DB.First(&replacement, *item1.HandoffTicketID).Error; err != nil {
		t.Fatalf("load replacement ticket failed: %v", err)
	}
	if replacement.Label != "integration" {
		t.Fatalf("expected replacement label=integration, got=%q", replacement.Label)
	}
	if replacement.Priority != contracts.TicketPriorityHigh {
		t.Fatalf("expected replacement priority=%d, got=%d", contracts.TicketPriorityHigh, replacement.Priority)
	}
	if replacement.TargetBranch != "refs/heads/"+targetBranch {
		t.Fatalf("expected replacement target_ref=refs/heads/%s, got=%q", targetBranch, replacement.TargetBranch)
	}
	if replacement.Title != fmt.Sprintf("集成 t%d 到 %s", tk1.ID, targetBranch) {
		t.Fatalf("unexpected replacement title: %q", replacement.Title)
	}
	if !strings.Contains(replacement.Description, fmt.Sprintf("source_tickets: t%d", tk1.ID)) {
		t.Fatalf("replacement description should include source_tickets, got:\n%s", replacement.Description)
	}
	if !strings.Contains(replacement.Description, "target_ref: refs/heads/"+targetBranch) {
		t.Fatalf("replacement description should include target_ref, got:\n%s", replacement.Description)
	}
	if !strings.Contains(replacement.Description, "conflict_files:") || !strings.Contains(replacement.Description, "conflict.txt") {
		t.Fatalf("replacement description should include conflict files, got:\n%s", replacement.Description)
	}
	if !strings.Contains(replacement.Description, "merge stderr/log:") {
		t.Fatalf("replacement description should include merge summary, got:\n%s", replacement.Description)
	}
	if !strings.Contains(replacement.Description, "docs/architecture/focus-run-batch-v1-lean-spec.md") ||
		!strings.Contains(replacement.Description, "docs/architecture/focus-run-batch-v1-remediation-spec.md") {
		t.Fatalf("replacement description should include evidence refs, got:\n%s", replacement.Description)
	}

	var createdEvent contracts.FocusEvent
	if err := p.DB.Where("focus_run_id = ? AND focus_item_id = ? AND kind = ?", res.FocusID, item1.ID, contracts.FocusEventIntegrationCreated).First(&createdEvent).Error; err != nil {
		t.Fatalf("expected integration created event, got err=%v", err)
	}
	if !strings.Contains(createdEvent.PayloadJSON, "source_anchor_shas") ||
		!strings.Contains(createdEvent.PayloadJSON, "evidence_refs") ||
		!strings.Contains(createdEvent.PayloadJSON, "docs/architecture/focus-run-batch-v1-remediation-spec.md") {
		t.Fatalf("integration created payload should include full evidence, got=%s", createdEvent.PayloadJSON)
	}

	if svc.gitHasConflicts(ctx) {
		t.Fatalf("expected no unresolved conflicts after merge abort on %s", targetBranch)
	}
	if _, err := os.Stat(filepath.Join(p.RepoRoot, ".git", "MERGE_HEAD")); err == nil {
		t.Fatalf("expected MERGE_HEAD removed after merge abort on %s", targetBranch)
	}
}

func TestAdvanceFocusController_BlocksIntegrationTicketMergeConflictAsHandoffRequiresUser(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-integration-merge-conflict")
	prepareFocusMergeConflictTicket(t, svc, p, tk, "integration")

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController select failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController merge failed: %v", err)
	}

	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	if view.Run.Status != contracts.FocusBlocked {
		t.Fatalf("expected focus blocked, got=%s", view.Run.Status)
	}
	item := focusItemByTicketID(view.Items, tk.ID)
	if item == nil || item.Status != contracts.FocusItemBlocked {
		t.Fatalf("expected ticket blocked, got=%+v", item)
	}
	if item.BlockedReason != focusBlockedReasonHandoffRecursionRequiresUser {
		t.Fatalf("expected blocked_reason=%s, got=%s", focusBlockedReasonHandoffRecursionRequiresUser, item.BlockedReason)
	}
	if item.HandoffTicketID != nil {
		t.Fatalf("expected no recursive handoff ticket created, got=%v", item.HandoffTicketID)
	}
	var ticketCount int64
	if err := p.DB.Model(&contracts.Ticket{}).Count(&ticketCount).Error; err != nil {
		t.Fatalf("count tickets failed: %v", err)
	}
	if ticketCount != 1 {
		t.Fatalf("expected no extra integration ticket, got count=%d", ticketCount)
	}
}

func TestAdvanceFocusController_BlocksIntegrationTicketWithoutTargetRef(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-integration-missing-target")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"label":           "integration",
		"target_branch":   "",
		"workflow_status": contracts.TicketBacklog,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare integration ticket failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}

	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	if view.Run.Status != contracts.FocusBlocked {
		t.Fatalf("expected focus blocked, got=%s", view.Run.Status)
	}
	item := focusItemByTicketID(view.Items, tk.ID)
	if item == nil || item.Status != contracts.FocusItemBlocked {
		t.Fatalf("expected item blocked, got=%+v", item)
	}
	if item.BlockedReason != focusBlockedReasonStartFailed {
		t.Fatalf("expected blocked_reason=%s, got=%s", focusBlockedReasonStartFailed, item.BlockedReason)
	}
	if !strings.Contains(item.LastError, "target_ref") {
		t.Fatalf("expected last_error mention target_ref, got=%q", item.LastError)
	}
}

func TestAdvanceFocusController_BlocksMergeSuccessWhenRepoDirty(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-merge-dirty-root")
	targetBranch := prepareFocusMergeSuccessTicket(t, svc, p, tk)
	dirtyPath := filepath.Join(p.RepoRoot, "dirty.txt")
	if err := os.WriteFile(dirtyPath, []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write dirty file failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController select failed: %v", err)
	}
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController merge failed: %v", err)
	}

	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	if view.Run.Status != contracts.FocusBlocked {
		t.Fatalf("expected focus blocked, got=%s", view.Run.Status)
	}
	item := focusItemByTicketID(view.Items, tk.ID)
	if item == nil || item.Status != contracts.FocusItemBlocked {
		t.Fatalf("expected item blocked, got=%+v", item)
	}
	if item.BlockedReason != focusBlockedReasonMergeFailed {
		t.Fatalf("expected blocked_reason=%s, got=%s", focusBlockedReasonMergeFailed, item.BlockedReason)
	}
	if item.Status == contracts.FocusItemAwaitingMergeObservation {
		t.Fatalf("dirty merge must not enter awaiting_merge_observation")
	}
	if !strings.Contains(item.LastError, "clean gate") {
		t.Fatalf("expected last_error mention clean gate, got=%q", item.LastError)
	}
	if branch := strings.TrimSpace(mustRunGit(t, p.RepoRoot, "branch", "--show-current")); branch != targetBranch {
		t.Fatalf("expected repo stay on target branch %s, got=%s", targetBranch, branch)
	}
}

func TestAdvanceFocusController_ResolvesHandoffBlockedItemAndAdvances(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	source := createTicket(t, p.DB, "focus-handoff-source")
	replacement := createTicket(t, p.DB, "focus-handoff-replacement")
	next := createTicket(t, p.DB, "focus-handoff-next")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", source.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare source ticket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", replacement.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationMerged,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare replacement ticket failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{source.ID, next.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	item := focusItemByTicketID(view.Items, source.ID)
	if item == nil {
		t.Fatalf("expected focus item for t%d", source.ID)
	}
	if err := p.DB.Model(&contracts.FocusRun{}).Where("id = ?", res.FocusID).Updates(map[string]any{
		"status":     contracts.FocusBlocked,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set focus blocked failed: %v", err)
	}
	if err := p.DB.Model(&contracts.FocusRunItem{}).Where("id = ?", item.ID).Updates(map[string]any{
		"status":            contracts.FocusItemBlocked,
		"blocked_reason":    focusBlockedReasonHandoffWaitingMerge,
		"handoff_ticket_id": replacement.ID,
		"updated_at":        time.Now(),
	}).Error; err != nil {
		t.Fatalf("set item handoff blocked failed: %v", err)
	}

	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}

	after, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet after blocked tick failed: %v", err)
	}
	afterItem := focusItemByTicketID(after.Items, source.ID)
	if after.Run.Status != contracts.FocusRunning {
		t.Fatalf("expected focus returns running, got=%s", after.Run.Status)
	}
	if afterItem == nil || afterItem.Status != contracts.FocusItemCompleted {
		t.Fatalf("expected item completed after handoff resolve, got=%+v", afterItem)
	}
	if afterItem.BlockedReason != "" {
		t.Fatalf("expected blocked_reason cleared, got=%s", afterItem.BlockedReason)
	}
	nextItem := focusItemByTicketID(after.Items, next.ID)
	if nextItem == nil || nextItem.Status != contracts.FocusItemQueued {
		t.Fatalf("expected next item queued after handoff resolve, got=%+v", nextItem)
	}

	var sourceAfter contracts.Ticket
	if err := p.DB.First(&sourceAfter, source.ID).Error; err != nil {
		t.Fatalf("reload source ticket failed: %v", err)
	}
	if sourceAfter.SupersededByTicketID == nil || *sourceAfter.SupersededByTicketID != replacement.ID {
		t.Fatalf("expected source ticket superseded by t%d, got=%v", replacement.ID, sourceAfter.SupersededByTicketID)
	}
	if contracts.CanonicalIntegrationStatus(sourceAfter.IntegrationStatus) != contracts.IntegrationAbandoned {
		t.Fatalf("expected source integration_status abandoned, got=%s", sourceAfter.IntegrationStatus)
	}
	if !strings.Contains(sourceAfter.AbandonedReason, fmt.Sprintf("t%d", replacement.ID)) {
		t.Fatalf("expected abandoned_reason mention replacement ticket, got=%q", sourceAfter.AbandonedReason)
	}

	var resolvedEvent contracts.FocusEvent
	if err := p.DB.Where("focus_run_id = ? AND focus_item_id = ? AND kind = ?", res.FocusID, afterItem.ID, contracts.FocusEventHandoffResolved).First(&resolvedEvent).Error; err != nil {
		t.Fatalf("expected handoff resolved event, got err=%v", err)
	}
}

func TestAdvanceFocusController_DoesNotAutoSupersedeNonHandoffBlockedItem(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	source := createTicket(t, p.DB, "focus-blocked-source")
	replacement := createTicket(t, p.DB, "focus-blocked-replacement")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", source.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare source ticket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", replacement.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationMerged,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare replacement ticket failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{source.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	item := focusItemByTicketID(view.Items, source.ID)
	if item == nil {
		t.Fatalf("expected focus item for t%d", source.ID)
	}
	if err := p.DB.Model(&contracts.FocusRun{}).Where("id = ?", res.FocusID).Updates(map[string]any{
		"status":     contracts.FocusBlocked,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set focus blocked failed: %v", err)
	}
	if err := p.DB.Model(&contracts.FocusRunItem{}).Where("id = ?", item.ID).Updates(map[string]any{
		"status":            contracts.FocusItemBlocked,
		"blocked_reason":    focusBlockedReasonNeedsUser,
		"handoff_ticket_id": replacement.ID,
		"updated_at":        time.Now(),
	}).Error; err != nil {
		t.Fatalf("set item blocked failed: %v", err)
	}

	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}

	after, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet after blocked tick failed: %v", err)
	}
	afterItem := focusItemByTicketID(after.Items, source.ID)
	if after.Run.Status != contracts.FocusRunning {
		t.Fatalf("expected focus converges back to running, got=%s", after.Run.Status)
	}
	if afterItem == nil || afterItem.Status != contracts.FocusItemMerging {
		t.Fatalf("expected item converges to merging by source ticket truth, got=%+v", afterItem)
	}
	if afterItem.BlockedReason != "" {
		t.Fatalf("expected blocked_reason cleared after converge, got=%s", afterItem.BlockedReason)
	}

	var sourceAfter contracts.Ticket
	if err := p.DB.First(&sourceAfter, source.ID).Error; err != nil {
		t.Fatalf("reload source ticket failed: %v", err)
	}
	if sourceAfter.SupersededByTicketID != nil {
		t.Fatalf("expected non-handoff blocked item not auto-superseded, got=%v", *sourceAfter.SupersededByTicketID)
	}
}

func TestAdvanceFocusController_ConvergesBlockedNeedsUserItemToMergingByTicketTruth(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-blocked-needs-merge")
	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	item := focusItemByTicketID(view.Items, tk.ID)
	if item == nil {
		t.Fatalf("expected focus item for t%d", tk.ID)
	}
	if err := p.DB.Model(&contracts.FocusRun{}).Where("id = ?", res.FocusID).Updates(map[string]any{
		"status":     contracts.FocusBlocked,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set focus blocked failed: %v", err)
	}
	if err := p.DB.Model(&contracts.FocusRunItem{}).Where("id = ?", item.ID).Updates(map[string]any{
		"status":         contracts.FocusItemBlocked,
		"blocked_reason": focusBlockedReasonNeedsUser,
		"last_error":     "waiting for inbox reply",
		"updated_at":     time.Now(),
	}).Error; err != nil {
		t.Fatalf("set focus item blocked failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket done+needs_merge failed: %v", err)
	}

	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}

	after, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet after converge failed: %v", err)
	}
	afterItem := focusItemByTicketID(after.Items, tk.ID)
	if after.Run.Status != contracts.FocusRunning {
		t.Fatalf("expected focus running after canonical converge, got=%s", after.Run.Status)
	}
	if afterItem == nil || afterItem.Status != contracts.FocusItemMerging {
		t.Fatalf("expected item merging after canonical converge, got=%+v", afterItem)
	}
	if afterItem.BlockedReason != "" {
		t.Fatalf("expected blocked_reason cleared, got=%s", afterItem.BlockedReason)
	}
	if afterItem.LastError != "" {
		t.Fatalf("expected last_error cleared, got=%s", afterItem.LastError)
	}
}

func TestAdvanceFocusController_IgnoresStaleNeedsUserInboxProjectionWhenTicketAlreadyNeedsMerge(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-stale-inbox-source")
	tk2 := createTicket(t, p.DB, "focus-stale-inbox-next")
	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	item1 := focusItemByTicketID(view.Items, tk1.ID)
	item2 := focusItemByTicketID(view.Items, tk2.ID)
	if item1 == nil {
		t.Fatalf("expected focus item for t%d", tk1.ID)
	}
	if item2 == nil {
		t.Fatalf("expected focus item for t%d", tk2.ID)
	}
	now := time.Now()
	if err := p.DB.Model(&contracts.FocusRun{}).Where("id = ?", res.FocusID).Updates(map[string]any{
		"status":     contracts.FocusBlocked,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("set focus blocked failed: %v", err)
	}
	if err := p.DB.Model(&contracts.FocusRunItem{}).Where("id = ?", item1.ID).Updates(map[string]any{
		"status":         contracts.FocusItemBlocked,
		"blocked_reason": focusBlockedReasonNeedsUser,
		"last_error":     "waiting for inbox reply",
		"updated_at":     now,
	}).Error; err != nil {
		t.Fatalf("set focus item blocked failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk1.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"updated_at":         now,
	}).Error; err != nil {
		t.Fatalf("set ticket done+needs_merge failed: %v", err)
	}

	staleInbox := contracts.InboxItem{
		Key:              fmt.Sprintf("manual-needs-user:t%d", tk1.ID),
		Status:           contracts.InboxOpen,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "人工伪造的 needs_user inbox",
		Body:             "这是一条手工注入的错误投影，不应再影响 focus controller。",
		TicketID:         tk1.ID,
		OriginTaskRunID:  901,
		CurrentTaskRunID: 901,
		WaitRoundCount:   1,
	}
	if err := p.DB.Create(&staleInbox).Error; err != nil {
		t.Fatalf("create stale needs_user inbox failed: %v", err)
	}

	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}

	after, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet after converge failed: %v", err)
	}
	afterItem1 := focusItemByTicketID(after.Items, tk1.ID)
	afterItem2 := focusItemByTicketID(after.Items, tk2.ID)
	if after.Run.Status != contracts.FocusRunning {
		t.Fatalf("expected focus running after converge, got=%s", after.Run.Status)
	}
	if afterItem1 == nil || afterItem1.Status != contracts.FocusItemMerging {
		t.Fatalf("expected stale inbox projection ignored and item enters merging, got=%+v", afterItem1)
	}
	if afterItem1.BlockedReason != "" {
		t.Fatalf("expected blocked_reason cleared, got=%s", afterItem1.BlockedReason)
	}
	if afterItem1.LastError != "" {
		t.Fatalf("expected last_error cleared, got=%s", afterItem1.LastError)
	}
	if afterItem2 == nil || afterItem2.Status != contracts.FocusItemPending {
		t.Fatalf("expected downstream item remains pending while merge is unresolved, got=%+v", afterItem2)
	}

	var inboxAfter contracts.InboxItem
	if err := p.DB.First(&inboxAfter, staleInbox.ID).Error; err != nil {
		t.Fatalf("reload stale inbox failed: %v", err)
	}
	if inboxAfter.Status != contracts.InboxOpen {
		t.Fatalf("expected manually injected stale inbox stay open and not affect controller, got=%s", inboxAfter.Status)
	}
}

func prepareFocusMergeConflictTicket(t *testing.T, svc *Service, p *core.Project, tk contracts.Ticket, label string) string {
	t.Helper()

	targetBranch, _ := initGitRepoForIntegrationObserveTest(t, p.RepoRoot)
	workerRef, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	workerBranch := workerRef.Branch
	conflictFile := filepath.Join(p.RepoRoot, "conflict.txt")

	mustRunGit(t, p.RepoRoot, "checkout", "-b", workerBranch)
	if err := os.WriteFile(conflictFile, []byte("worker change\n"), 0o644); err != nil {
		t.Fatalf("write worker conflict file failed: %v", err)
	}
	mustRunGit(t, p.RepoRoot, "add", "conflict.txt")
	mustRunGit(t, p.RepoRoot, "commit", "-m", "worker conflict change")
	workerAnchor := mustRunGit(t, p.RepoRoot, "rev-parse", "HEAD")

	mustRunGit(t, p.RepoRoot, "checkout", targetBranch)
	if err := os.WriteFile(conflictFile, []byte("target change\n"), 0o644); err != nil {
		t.Fatalf("write target conflict file failed: %v", err)
	}
	mustRunGit(t, p.RepoRoot, "add", "conflict.txt")
	mustRunGit(t, p.RepoRoot, "commit", "-m", "target conflict change")

	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"label":              label,
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"merge_anchor_sha":   workerAnchor,
		"target_branch":      "refs/heads/" + targetBranch,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare merge conflict ticket failed: %v", err)
	}
	return targetBranch
}

func prepareFocusMergeSuccessTicket(t *testing.T, svc *Service, p *core.Project, tk contracts.Ticket) string {
	t.Helper()

	targetBranch, _ := initGitRepoForIntegrationObserveTest(t, p.RepoRoot)
	workerRef, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	workerBranch := workerRef.Branch
	featureFile := filepath.Join(p.RepoRoot, "feature.txt")

	mustRunGit(t, p.RepoRoot, "checkout", "-b", workerBranch)
	if err := os.WriteFile(featureFile, []byte("worker change\n"), 0o644); err != nil {
		t.Fatalf("write worker feature file failed: %v", err)
	}
	mustRunGit(t, p.RepoRoot, "add", "feature.txt")
	mustRunGit(t, p.RepoRoot, "commit", "-m", "worker feature change")
	workerAnchor := mustRunGit(t, p.RepoRoot, "rev-parse", "HEAD")
	mustRunGit(t, p.RepoRoot, "checkout", targetBranch)

	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"merge_anchor_sha":   workerAnchor,
		"target_branch":      "refs/heads/" + targetBranch,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare merge success ticket failed: %v", err)
	}
	return targetBranch
}

func TestFocusAddTickets_HotInsertNewTickets(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-original-1")
	tk2 := createTicket(t, p.DB, "focus-original-2")
	tk3 := createTicket(t, p.DB, "focus-new-3")
	tk4 := createTicket(t, p.DB, "focus-new-4")

	// 创建初始 focus（scope = tk1, tk2）
	startRes, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	if len(startRes.View.Items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(startRes.View.Items))
	}

	// 热插入 tk3, tk4
	addRes, err := svc.FocusAddTickets(ctx, contracts.FocusAddTicketsInput{
		TicketIDs: []uint{tk3.ID, tk4.ID},
	})
	if err != nil {
		t.Fatalf("FocusAddTickets failed: %v", err)
	}
	if addRes.FocusID != startRes.FocusID {
		t.Fatalf("expected same focus_id=%d, got=%d", startRes.FocusID, addRes.FocusID)
	}
	if addRes.AddedCount != 2 {
		t.Fatalf("expected added_count=2, got=%d", addRes.AddedCount)
	}
	if addRes.SkippedCount != 0 {
		t.Fatalf("expected skipped_count=0, got=%d", addRes.SkippedCount)
	}

	// 验证 view 包含 4 个 items
	view := addRes.View
	if len(view.Items) != 4 {
		t.Fatalf("expected 4 items after add, got=%d", len(view.Items))
	}

	// 验证新增 items 状态为 pending，且 seq 正确
	item3 := focusItemByTicketID(view.Items, tk3.ID)
	if item3 == nil {
		t.Fatalf("expected tk3 in items")
	}
	if item3.Status != contracts.FocusItemPending {
		t.Fatalf("expected tk3 pending, got=%s", item3.Status)
	}
	if item3.Seq != 3 {
		t.Fatalf("expected tk3 seq=3, got=%d", item3.Seq)
	}
	item4 := focusItemByTicketID(view.Items, tk4.ID)
	if item4 == nil {
		t.Fatalf("expected tk4 in items")
	}
	if item4.Seq != 4 {
		t.Fatalf("expected tk4 seq=4, got=%d", item4.Seq)
	}
}

func TestFocusAddTickets_IdempotentSkipExisting(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-existing-1")
	tk2 := createTicket(t, p.DB, "focus-new-2")

	_, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	// 添加 tk1（已存在）+ tk2（新）
	addRes, err := svc.FocusAddTickets(ctx, contracts.FocusAddTicketsInput{
		TicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusAddTickets failed: %v", err)
	}
	if addRes.AddedCount != 1 {
		t.Fatalf("expected added_count=1, got=%d", addRes.AddedCount)
	}
	if addRes.SkippedCount != 1 {
		t.Fatalf("expected skipped_count=1, got=%d", addRes.SkippedCount)
	}
	if len(addRes.SkippedIDs) != 1 || addRes.SkippedIDs[0] != tk1.ID {
		t.Fatalf("expected skipped_ids=[%d], got=%v", tk1.ID, addRes.SkippedIDs)
	}
	if len(addRes.View.Items) != 2 {
		t.Fatalf("expected 2 items, got=%d", len(addRes.View.Items))
	}
}

func TestFocusAddTickets_NoActiveFocusReturnsError(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	ctx := context.Background()

	_, err := svc.FocusAddTickets(ctx, contracts.FocusAddTicketsInput{
		TicketIDs: []uint{999},
	})
	if err == nil {
		t.Fatalf("expected error when no active focus")
	}
	if !strings.Contains(err.Error(), "当前无 active focus") {
		t.Fatalf("expected 'no active focus' error, got: %v", err)
	}
}

func TestFocusAddTickets_RejectsDoneTicket(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-scope")
	tkDone := createTicket(t, p.DB, "focus-done-ticket")
	p.DB.Model(&contracts.Ticket{}).Where("id = ?", tkDone.ID).Update("workflow_status", contracts.TicketDone)

	_, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	_, err = svc.FocusAddTickets(ctx, contracts.FocusAddTicketsInput{
		TicketIDs: []uint{tkDone.ID},
	})
	if err == nil {
		t.Fatalf("expected error for done ticket")
	}
	if !strings.Contains(err.Error(), "不可接入 focus") {
		t.Fatalf("expected rejection error, got: %v", err)
	}
}

func TestFocusAddTickets_EmptyTicketIDs(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	ctx := context.Background()

	_, err := svc.FocusAddTickets(ctx, contracts.FocusAddTicketsInput{
		TicketIDs: []uint{},
	})
	if err == nil {
		t.Fatalf("expected error for empty ticket IDs")
	}
	if !strings.Contains(err.Error(), "不能为空") {
		t.Fatalf("expected 'empty' error, got: %v", err)
	}
}

func TestFocusAddTickets_AllSkippedNoEvent(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-all-skipped")

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	// 全部幂等跳过
	addRes, err := svc.FocusAddTickets(ctx, contracts.FocusAddTicketsInput{
		TicketIDs: []uint{tk1.ID},
	})
	if err != nil {
		t.Fatalf("FocusAddTickets failed: %v", err)
	}
	if addRes.AddedCount != 0 {
		t.Fatalf("expected added_count=0, got=%d", addRes.AddedCount)
	}
	if addRes.SkippedCount != 1 {
		t.Fatalf("expected skipped_count=1, got=%d", addRes.SkippedCount)
	}

	// 确认没有追加 scope.tickets_added 事件
	var events []contracts.FocusEvent
	p.DB.Where("focus_run_id = ? AND kind = ?", res.FocusID, contracts.FocusEventScopeTicketsAdded).Find(&events)
	if len(events) != 0 {
		t.Fatalf("expected no scope.tickets_added event for all-skipped case, got=%d", len(events))
	}
}

// ---------------------------------------------------------------------------
// Regression: queued item must not emit repeated item.selected events (t137)
// ---------------------------------------------------------------------------

func TestFocusTick_QueuedItemNoRepeatedSelectedEvents(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-queued-no-repeat")

	// Start focus and advance once — item transitions pending → queued.
	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("first tick failed: %v", err)
	}

	// Count item.selected events after first tick.
	var firstCount int64
	p.DB.Model(&contracts.FocusEvent{}).
		Where("focus_run_id = ? AND kind = ?", res.FocusID, contracts.FocusEventItemSelected).
		Count(&firstCount)
	if firstCount != 1 {
		t.Fatalf("expected exactly 1 item.selected after first tick, got=%d", firstCount)
	}

	// Tick multiple more times — item is already queued, no new events.
	for i := 0; i < 5; i++ {
		if err := svc.AdvanceFocusController(ctx); err != nil {
			t.Fatalf("tick %d failed: %v", i+2, err)
		}
	}

	var afterCount int64
	p.DB.Model(&contracts.FocusEvent{}).
		Where("focus_run_id = ? AND kind = ?", res.FocusID, contracts.FocusEventItemSelected).
		Count(&afterCount)
	if afterCount != 1 {
		t.Fatalf("expected item.selected count to stay 1 after repeated ticks, got=%d", afterCount)
	}
}

func TestFocusTick_QueuedItemNoProjectWakeLoop(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-queued-no-wake-loop")

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	// First tick: pending → queued.
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("first tick failed: %v", err)
	}

	// Track projectWake calls during subsequent ticks.
	wakeCalls := 0
	svc.SetProjectWakeHook(func() {
		wakeCalls++
	})

	// Subsequent ticks on queued item should NOT trigger projectWake.
	for i := 0; i < 5; i++ {
		if err := svc.AdvanceFocusController(ctx); err != nil {
			t.Fatalf("tick %d failed: %v", i+2, err)
		}
	}

	if wakeCalls > 0 {
		t.Fatalf("expected 0 projectWake calls for already-queued item, got=%d", wakeCalls)
	}

	// Verify item is still queued and run is still active.
	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	item := focusItemByTicketID(view.Items, tk.ID)
	if item == nil || item.Status != contracts.FocusItemQueued {
		t.Fatalf("expected item queued, got=%+v", item)
	}
}

func TestFocusTick_NormalTransitionNotBroken(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "focus-normal-transition-1")
	tk2 := createTicket(t, p.DB, "focus-normal-transition-2")

	// Pre-mark tk1 as done+merged so it completes immediately.
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk1.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationMerged,
	}).Error; err != nil {
		t.Fatalf("prep tk1 failed: %v", err)
	}

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	// First tick: tk1 completes, tk2 should be picked up.
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("first tick failed: %v", err)
	}

	view, err := svc.FocusGet(ctx, res.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}

	item1 := focusItemByTicketID(view.Items, tk1.ID)
	if item1 == nil || item1.Status != contracts.FocusItemCompleted {
		t.Fatalf("expected tk1 completed, got=%+v", item1)
	}

	item2 := focusItemByTicketID(view.Items, tk2.ID)
	if item2 == nil || item2.Status != contracts.FocusItemQueued {
		t.Fatalf("expected tk2 queued after tk1 completion, got=%+v", item2)
	}

	// Verify item.selected was emitted for both items (one each).
	var selectedCount int64
	p.DB.Model(&contracts.FocusEvent{}).
		Where("focus_run_id = ? AND kind = ?", res.FocusID, contracts.FocusEventItemSelected).
		Count(&selectedCount)
	if selectedCount != 2 {
		t.Fatalf("expected 2 item.selected events (one per item), got=%d", selectedCount)
	}
}

func TestConvergentTick_QueuedItemNoRepeatedSelectedEvents(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "convergent-queued-no-repeat")

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeConvergent,
		ScopeTicketIDs: []uint{tk.ID},
		MaxPMRuns:      3,
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	// First tick: starts the round and transitions to batch phase.
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("first tick failed: %v", err)
	}

	// Second tick: processes the pending item → queued.
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("second tick failed: %v", err)
	}

	var firstCount int64
	p.DB.Model(&contracts.FocusEvent{}).
		Where("focus_run_id = ? AND kind = ?", res.FocusID, contracts.FocusEventItemSelected).
		Count(&firstCount)
	if firstCount != 1 {
		t.Fatalf("expected 1 item.selected after initial selection, got=%d", firstCount)
	}

	// Multiple further ticks — queued item should not re-emit.
	for i := 0; i < 5; i++ {
		if err := svc.AdvanceFocusController(ctx); err != nil {
			t.Fatalf("tick %d failed: %v", i+3, err)
		}
	}

	var afterCount int64
	p.DB.Model(&contracts.FocusEvent{}).
		Where("focus_run_id = ? AND kind = ?", res.FocusID, contracts.FocusEventItemSelected).
		Count(&afterCount)
	if afterCount != 1 {
		t.Fatalf("expected item.selected count to stay 1 in convergent mode, got=%d", afterCount)
	}
}

// TestFocusTick_PendingItemNoRepeatedSelectedOnRetick verifies that even when
// an item stays in "pending" status across multiple ticks (e.g. due to a
// transient failure after event emission), item.selected is only emitted once.
// This exercises the secondary DB-level dedup guard.
func TestFocusTick_PendingItemNoRepeatedSelectedOnRetick(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-pending-no-repeat")

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	// First tick: item transitions pending → queued, item.selected emitted.
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("first tick failed: %v", err)
	}

	// Force item back to pending (simulates a scenario where the status
	// transition was lost/reverted, leaving the item in pending state).
	if err := p.DB.Model(&contracts.FocusRunItem{}).
		Where("focus_run_id = ?", res.FocusID).
		Update("status", contracts.FocusItemPending).Error; err != nil {
		t.Fatalf("force pending failed: %v", err)
	}

	// Tick again — the secondary DB dedup guard should prevent re-emission.
	for i := 0; i < 5; i++ {
		if err := svc.AdvanceFocusController(ctx); err != nil {
			t.Fatalf("tick %d failed: %v", i+2, err)
		}
	}

	var count int64
	p.DB.Model(&contracts.FocusEvent{}).
		Where("focus_run_id = ? AND kind = ?", res.FocusID, contracts.FocusEventItemSelected).
		Count(&count)
	if count != 1 {
		t.Fatalf("expected exactly 1 item.selected event with DB-level dedup, got=%d", count)
	}
}

// TestFocusTick_SelectedEventSilent_NoProjectWake verifies that the
// item.selected event emission does NOT trigger projectWake, breaking the
// item.selected → projectWake → re-tick → item.selected feedback loop.
func TestFocusTick_SelectedEventSilent_NoProjectWake(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-selected-silent")

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	// Track projectWake calls during the first tick (which emits item.selected).
	wakeCalls := 0
	svc.SetProjectWakeHook(func() {
		wakeCalls++
	})

	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("first tick failed: %v", err)
	}

	// Verify item.selected was emitted.
	var selectedCount int64
	p.DB.Model(&contracts.FocusEvent{}).
		Where("focus_run_id = ? AND kind = ?", res.FocusID, contracts.FocusEventItemSelected).
		Count(&selectedCount)
	if selectedCount != 1 {
		t.Fatalf("expected 1 item.selected, got=%d", selectedCount)
	}

	// The item.selected event should NOT cause a projectWake.
	// Only the subsequent state transition (pending→queued) should call
	// projectWake. So we expect at most 1 wake call (from the state transition),
	// not 2 (one from item.selected + one from state transition).
	if wakeCalls > 1 {
		t.Fatalf("expected at most 1 projectWake call (from state transition only), got=%d", wakeCalls)
	}
}

// TestFocusTick_ItemSelectedAtomicUnderConcurrency verifies that concurrent
// ticks on a pending item only emit exactly one item.selected event, exercising
// the transactional check+write guard introduced in t148.
func TestFocusTick_ItemSelectedAtomicUnderConcurrency(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-atomic-selected")

	res, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	// Simulate concurrent ticks by running multiple goroutines.
	const concurrency = 10
	var wg sync.WaitGroup
	errs := make([]error, concurrency)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			errs[idx] = svc.AdvanceFocusController(ctx)
		}(i)
	}
	wg.Wait()

	// At least some should succeed (SQLite serializes writes, some may conflict).
	successCount := 0
	for _, e := range errs {
		if e == nil {
			successCount++
		}
	}
	if successCount == 0 {
		t.Fatalf("expected at least one tick to succeed, all failed")
	}

	// Key assertion: exactly 1 item.selected event, regardless of concurrency.
	var selectedCount int64
	p.DB.Model(&contracts.FocusEvent{}).
		Where("focus_run_id = ? AND kind = ?", res.FocusID, contracts.FocusEventItemSelected).
		Count(&selectedCount)
	if selectedCount != 1 {
		t.Fatalf("expected exactly 1 item.selected event under concurrency, got=%d", selectedCount)
	}
}

func focusItemByTicketID(items []contracts.FocusRunItem, ticketID uint) *contracts.FocusRunItem {
	for i := range items {
		if items[i].TicketID == ticketID {
			item := items[i]
			return &item
		}
	}
	return nil
}
