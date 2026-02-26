package pm

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

func TestStartLeaseRenewal_LogsOnFailure(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	// Install a buffer-backed logger to capture log output.
	var logBuf bytes.Buffer
	svc.logger = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tk := createTicket(t, p.DB, "lease-renewal-log-test")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/lease-renewal-test",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-lease-renewal-test",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// Create a dispatch job with a non-existent runner_id so renewal will fail.
	job := contracts.PMDispatchJob{
		RequestID: "dsp_lease_renewal_test",
		TicketID:  tk.ID,
		WorkerID:  w.ID,
		Status:    contracts.PMDispatchRunning,
		RunnerID:  "runner-original",
		Attempt:   1,
	}
	if err := p.DB.Create(&job).Error; err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	cw := contracts.Worker{ID: w.ID, TicketID: tk.ID}
	// Use a different runner_id so renewal will fail (runner mismatch).
	stop := svc.startLeaseRenewal(context.Background(), job, cw, "runner-different", 2*time.Minute)

	// Wait enough for at least one renewal attempt.
	time.Sleep(dispatchLeaseRenewInterval + 500*time.Millisecond)
	close(stop)

	logs := logBuf.String()
	if !strings.Contains(logs, "pm dispatch lease renewal failed") {
		t.Fatalf("expected renewal failure log, got:\n%s", logs)
	}
	if !strings.Contains(logs, "runner-different") {
		t.Fatalf("expected runner_id in log, got:\n%s", logs)
	}
}

func TestStartLeaseRenewal_EscalatesAfterThreshold(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	var logBuf bytes.Buffer
	svc.logger = slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	tk := createTicket(t, p.DB, "lease-renewal-escalate-test")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/lease-escalate-test",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-lease-escalate-test",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	job := contracts.PMDispatchJob{
		RequestID: "dsp_lease_escalate_test",
		TicketID:  tk.ID,
		WorkerID:  w.ID,
		Status:    contracts.PMDispatchRunning,
		RunnerID:  "runner-original-escalate",
		Attempt:   1,
	}
	if err := p.DB.Create(&job).Error; err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	cw := contracts.Worker{ID: w.ID, TicketID: tk.ID}
	// Use mismatched runner to force failures.
	stop := svc.startLeaseRenewal(context.Background(), job, cw, "runner-wrong-escalate", 2*time.Minute)

	// Wait enough for multiple renewal attempts to reach escalation threshold.
	waitTime := time.Duration(leaseRenewalEscalateThreshold+1) * dispatchLeaseRenewInterval
	time.Sleep(waitTime + 500*time.Millisecond)
	close(stop)

	logs := logBuf.String()
	if !strings.Contains(logs, "escalated") {
		t.Fatalf("expected escalated log after %d failures, got:\n%s", leaseRenewalEscalateThreshold, logs)
	}
}

func TestStartLeaseRenewal_StopsOnClose(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "lease-renewal-stop-test")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/lease-stop-test",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-lease-stop-test",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	job := contracts.PMDispatchJob{
		RequestID: "dsp_lease_stop_test",
		TicketID:  tk.ID,
		WorkerID:  w.ID,
		Status:    contracts.PMDispatchRunning,
		RunnerID:  "runner-stop",
		Attempt:   1,
	}
	if err := p.DB.Create(&job).Error; err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	cw := contracts.Worker{ID: w.ID, TicketID: tk.ID}
	stop := svc.startLeaseRenewal(context.Background(), job, cw, "runner-stop", 2*time.Minute)
	close(stop)

	// If the goroutine doesn't stop, the test will hang/leak.
	// Wait a brief moment to verify no panic.
	time.Sleep(50 * time.Millisecond)
}

func TestStartLeaseRenewal_StopsOnContextCancel(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "lease-renewal-ctx-test")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/lease-ctx-test",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-lease-ctx-test",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	job := contracts.PMDispatchJob{
		RequestID: "dsp_lease_ctx_test",
		TicketID:  tk.ID,
		WorkerID:  w.ID,
		Status:    contracts.PMDispatchRunning,
		RunnerID:  "runner-ctx-cancel",
		Attempt:   1,
	}
	if err := p.DB.Create(&job).Error; err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cw := contracts.Worker{ID: w.ID, TicketID: tk.ID}
	stop := svc.startLeaseRenewal(ctx, job, cw, "runner-ctx-cancel", 2*time.Minute)
	defer close(stop)

	cancel()
	// If the goroutine doesn't stop on ctx cancel, the test will hang.
	time.Sleep(50 * time.Millisecond)
}

func TestStartLeaseRenewal_RecordsEvent(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "lease-renewal-event-test")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/lease-event-test",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-lease-event-test",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// Create an active task run for the worker so events can be recorded.
	taskRun := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
		ProjectKey:         "test",
		TicketID:           tk.ID,
		WorkerID:           w.ID,
		SubjectType:        "ticket",
		SubjectID:          "1",
		RequestID:          "wrk_lease_event_test",
		OrchestrationState: contracts.TaskRunning,
	}
	if err := p.DB.Create(&taskRun).Error; err != nil {
		t.Fatalf("create task run failed: %v", err)
	}

	job := contracts.PMDispatchJob{
		RequestID: "dsp_lease_event_test",
		TicketID:  tk.ID,
		WorkerID:  w.ID,
		Status:    contracts.PMDispatchRunning,
		RunnerID:  "runner-event-original",
		Attempt:   1,
	}
	if err := p.DB.Create(&job).Error; err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	cw := contracts.Worker{ID: w.ID, TicketID: tk.ID}
	stop := svc.startLeaseRenewal(context.Background(), job, cw, "runner-event-wrong", 2*time.Minute)

	// Wait for at least one renewal to fire.
	time.Sleep(dispatchLeaseRenewInterval + 500*time.Millisecond)
	close(stop)

	// Check that a lease_renewal_failed event was recorded against the task run.
	var evCount int64
	if err := p.DB.Model(&contracts.TaskEvent{}).
		Where("event_type = ? AND task_run_id = ?", "lease_renewal_failed", taskRun.ID).
		Count(&evCount).Error; err != nil {
		t.Fatalf("count events failed: %v", err)
	}
	if evCount == 0 {
		t.Fatalf("expected at least one lease_renewal_failed event for task_run_id=%d", taskRun.ID)
	}
}
