package pm

import (
	"context"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestStopTicket_CancelsActiveWorkerRun(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "pm-stop-ticket-active-run")
	w, err := svc.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	run := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "pm-stop-ticket-run")
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("mark worker running failed: %v", err)
	}

	if err := svc.StopTicket(ctx, tk.ID); err != nil {
		t.Fatalf("StopTicket failed: %v", err)
	}

	var afterWorker contracts.Worker
	if err := p.DB.First(&afterWorker, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if afterWorker.Status != contracts.WorkerStopped {
		t.Fatalf("expected worker stopped after stop, got=%s", afterWorker.Status)
	}

	var afterRun contracts.TaskRun
	if err := p.DB.First(&afterRun, run.ID).Error; err != nil {
		t.Fatalf("load task run failed: %v", err)
	}
	if afterRun.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected task run canceled after stop, got=%s", afterRun.OrchestrationState)
	}
}

func TestStopTicket_NoWorkerReturnsError(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "pm-stop-ticket-no-worker")
	if err := svc.StopTicket(context.Background(), tk.ID); err == nil {
		t.Fatalf("expected stop without worker to fail")
	}
}
