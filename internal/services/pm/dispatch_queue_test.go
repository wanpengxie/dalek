package pm

import (
	"context"
	"dalek/internal/contracts"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestEnqueuePMDispatchJob_RejectsActiveJobOnDifferentWorker(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-enqueue-worker-mismatch")
	active := contracts.PMDispatchJob{
		RequestID:       "dsp_active_other_worker",
		TicketID:        tk.ID,
		WorkerID:        101,
		TaskRunID:       0,
		ActiveTicketKey: func(v uint) *uint { return &v }(tk.ID),
		Status:          contracts.PMDispatchPending,
	}
	if err := p.DB.Create(&active).Error; err != nil {
		t.Fatalf("create active job failed: %v", err)
	}

	_, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, 202, "", newDispatchTaskRequestPayload(tk.ID, 202, true, ""))
	if err == nil {
		t.Fatalf("expected enqueue fail on active job worker mismatch")
	}
	if !strings.Contains(err.Error(), "绑定其他 worker") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnqueuePMDispatchJob_IdempotentByRequestID(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-enqueue-idempotent-request-id")
	w := createDispatchWorker(t, p.DB, tk.ID)

	reqID := "req_dispatch_idempotent_001"
	first, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, reqID, newDispatchTaskRequestPayload(tk.ID, w.ID, true, ""))
	if err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}
	second, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, reqID, newDispatchTaskRequestPayload(tk.ID, w.ID, true, ""))
	if err != nil {
		t.Fatalf("second enqueue failed: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected idempotent enqueue returns same job, first=%d second=%d", first.ID, second.ID)
	}
	if strings.TrimSpace(first.RequestID) != reqID || strings.TrimSpace(second.RequestID) != reqID {
		t.Fatalf("unexpected request_id: first=%q second=%q", first.RequestID, second.RequestID)
	}
	if first.TaskRunID == 0 || second.TaskRunID == 0 {
		t.Fatalf("task_run_id should be populated, first=%d second=%d", first.TaskRunID, second.TaskRunID)
	}

	var cnt int64
	if err := p.DB.Model(&contracts.PMDispatchJob{}).Where("request_id = ?", reqID).Count(&cnt).Error; err != nil {
		t.Fatalf("count request_id failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected only one job for same request_id, got=%d", cnt)
	}
}

func createDispatchWorker(t *testing.T, db *gorm.DB, ticketID uint) contracts.Worker {
	t.Helper()
	w := contracts.Worker{
		TicketID:     ticketID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/test-dispatch-worker",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("create dispatch worker failed: %v", err)
	}
	return w
}
