package pm

import (
	"context"
	"testing"
	"time"

	"dalek/internal/contracts"
)

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
	if view.Run.Status != contracts.FocusCanceled {
		t.Fatalf("expected focus canceled, got=%s", view.Run.Status)
	}
	item := focusItemByTicketID(view.Items, tk.ID)
	if item == nil || item.Status != contracts.FocusItemCanceled {
		t.Fatalf("expected item canceled, got=%+v", item)
	}

	var afterRun contracts.TaskRun
	if err := p.DB.First(&afterRun, run.ID).Error; err != nil {
		t.Fatalf("load task run failed: %v", err)
	}
	if afterRun.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected task run canceled, got=%s", afterRun.OrchestrationState)
	}
	var afterTicket contracts.Ticket
	if err := p.DB.First(&afterTicket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if afterTicket.WorkflowStatus != contracts.TicketBacklog {
		t.Fatalf("expected ticket workflow backlog after cancel stop, got=%s", afterTicket.WorkflowStatus)
	}
}

func TestAdvanceFocusController_BlocksAfterRestartExhausted(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "focus-restart-exhausted")
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
		t.Fatalf("AdvanceFocusController restart exhausted failed: %v", err)
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
	if item.BlockedReason != "restart_exhausted" {
		t.Fatalf("expected blocked_reason restart_exhausted, got=%s", item.BlockedReason)
	}
	if item.CurrentAttempt != 2 {
		t.Fatalf("expected current_attempt=2 after second accept, got=%d", item.CurrentAttempt)
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
