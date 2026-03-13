package pm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestRecoverActiveTaskRuns_RepairsQueuedProjectionFromDeliverRun(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "recovery-active-run")
	worker, err := svc.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := p.DB.WithContext(ctx).Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}
	if err := p.DB.WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", worker.ID).Updates(map[string]any{
		"status":     contracts.WorkerStopped,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("set worker stopped failed: %v", err)
	}
	run := createWorkerTaskRun(t, p.DB, tk.ID, worker.ID, fmt.Sprintf("recovery-run-%d", now.UnixNano()))

	recovered, err := svc.RecoverActiveTaskRuns(ctx, "test", now, nil)
	if err != nil {
		t.Fatalf("RecoverActiveTaskRuns failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("expected exactly one repaired active run, got=%d", recovered)
	}

	var ticket contracts.Ticket
	if err := p.DB.WithContext(ctx).First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketActive {
		t.Fatalf("expected ticket repaired to active, got=%s", ticket.WorkflowStatus)
	}

	var afterWorker contracts.Worker
	if err := p.DB.WithContext(ctx).First(&afterWorker, worker.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if afterWorker.Status != contracts.WorkerRunning {
		t.Fatalf("expected worker marked running, got=%s", afterWorker.Status)
	}

	var repaired contracts.TicketLifecycleEvent
	if err := p.DB.WithContext(ctx).
		Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleRepaired).
		Order("id desc").
		First(&repaired).Error; err != nil {
		t.Fatalf("expected repaired lifecycle event: %v", err)
	}
	if repaired.TaskRunID == nil || *repaired.TaskRunID != run.ID {
		t.Fatalf("expected repaired lifecycle task_run_id=%d, got=%v", run.ID, repaired.TaskRunID)
	}
	if repaired.WorkerID == nil || *repaired.WorkerID != worker.ID {
		t.Fatalf("expected repaired lifecycle worker_id=%d, got=%v", worker.ID, repaired.WorkerID)
	}
	if got := fmt.Sprint(repaired.PayloadJSON["target_workflow"]); got != string(contracts.TicketActive) {
		t.Fatalf("expected repaired target_workflow=active, got=%q", got)
	}
}

func TestListActiveTaskRunIDs_ReturnsOnlyDeliverTicketRuns(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "recovery-active-run-index")
	worker, err := svc.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	deliverRun := createWorkerTaskRun(t, p.DB, tk.ID, worker.ID, fmt.Sprintf("deliver-run-%d", time.Now().UnixNano()))

	plannerRun := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "pm_planner_run",
		ProjectKey:         "test",
		SubjectType:        "pm",
		SubjectID:          "planner",
		RequestID:          fmt.Sprintf("planner-run-%d", time.Now().UnixNano()),
		OrchestrationState: contracts.TaskRunning,
	}
	if err := p.DB.WithContext(ctx).Create(&plannerRun).Error; err != nil {
		t.Fatalf("create planner run failed: %v", err)
	}

	legacyDispatchRun := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "dispatch_ticket",
		ProjectKey:         "test",
		TicketID:           tk.ID,
		WorkerID:           worker.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          fmt.Sprintf("dispatch-run-%d", time.Now().UnixNano()),
		OrchestrationState: contracts.TaskRunning,
	}
	if err := p.DB.WithContext(ctx).Create(&legacyDispatchRun).Error; err != nil {
		t.Fatalf("create legacy dispatch run failed: %v", err)
	}

	runIDs, err := svc.ListActiveTaskRunIDs(ctx)
	if err != nil {
		t.Fatalf("ListActiveTaskRunIDs failed: %v", err)
	}
	if len(runIDs) != 1 {
		t.Fatalf("expected only one deliver_ticket active run, got=%v", runIDs)
	}
	if runIDs[0] != deliverRun.ID {
		t.Fatalf("expected deliver run id=%d, got=%v", deliverRun.ID, runIDs)
	}
}

func TestRecoverActiveTaskRuns_CancelsLegacyDispatchRuns(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "recovery-legacy-dispatch")
	worker, err := svc.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	legacyRun := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "dispatch_ticket",
		ProjectKey:         "test",
		TicketID:           tk.ID,
		WorkerID:           worker.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          fmt.Sprintf("legacy-dispatch-%d", now.UnixNano()),
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	}
	if err := p.DB.WithContext(ctx).Create(&legacyRun).Error; err != nil {
		t.Fatalf("create legacy dispatch run failed: %v", err)
	}

	recovered, err := svc.RecoverActiveTaskRuns(ctx, "test", now, nil)
	if err != nil {
		t.Fatalf("RecoverActiveTaskRuns failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("expected one recovered legacy dispatch run, got=%d", recovered)
	}

	var after contracts.TaskRun
	if err := p.DB.WithContext(ctx).First(&after, legacyRun.ID).Error; err != nil {
		t.Fatalf("reload legacy dispatch run failed: %v", err)
	}
	if after.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected legacy dispatch run canceled, got=%s", after.OrchestrationState)
	}
	if after.ErrorCode != "legacy_dispatch_removed" {
		t.Fatalf("expected legacy dispatch error_code, got=%q", after.ErrorCode)
	}
}
