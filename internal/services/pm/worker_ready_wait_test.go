package pm

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
)

func TestWaitWorkerReady_AlreadyRunning(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "wait-worker-running")
	w := &contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		LogPath:      repo.WorkerStreamLogPath(p.WorkersDir, 1),
	}

	got, err := svc.waitWorkerReadyForDispatch(context.Background(), tk.ID, w)
	if err != nil {
		t.Fatalf("expected no error for running worker, got=%v", err)
	}
	if got != w {
		t.Fatalf("expected same worker pointer returned")
	}
}

func TestWaitWorkerReady_NilWorker(t *testing.T) {
	svc, _, _ := newServiceForTest(t)

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), 1, nil)
	if err == nil {
		t.Fatalf("expected error for nil worker")
	}
	if !strings.Contains(err.Error(), "尚未启动") {
		t.Fatalf("expected missing worker/runtime error, got=%v", err)
	}
}

func TestWaitWorkerReady_NoRuntimeHandle(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	w := &contracts.Worker{
		Status: contracts.WorkerRunning,
	}

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), 1, w)
	if err == nil {
		t.Fatalf("expected error for missing runtime handle")
	}
	if !strings.Contains(err.Error(), "尚未启动") {
		t.Fatalf("expected missing worker/runtime error, got=%v", err)
	}
}

func TestWaitWorkerReady_StoppedWorkerWithoutRuntimeHandleRejects(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	w := &contracts.Worker{
		ID:     99,
		Status: contracts.WorkerStopped,
	}

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), 1, w)
	if err == nil {
		t.Fatalf("expected error for stopped worker")
	}
	if !strings.Contains(err.Error(), "尚未启动") {
		t.Fatalf("expected missing worker/runtime error, got=%v", err)
	}
}

func TestWaitWorkerReady_FailedWorkerRejects(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	w := &contracts.Worker{
		ID:     98,
		Status: contracts.WorkerFailed,
	}

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), 1, w)
	if err == nil {
		t.Fatalf("expected error for failed worker")
	}
	if !strings.Contains(err.Error(), "不在可调度状态") {
		t.Fatalf("expected not ready error, got=%v", err)
	}
}

func TestWaitWorkerReady_TimeoutWhileCreating(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	svc.workerReadyTimeout = 100 * time.Millisecond
	svc.workerReadyPollInterval = 10 * time.Millisecond

	tk := createTicket(t, p.DB, "wait-worker-timeout")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerCreating,
		WorktreePath: t.TempDir(),
		Branch:       "ts/wait-timeout",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	initial := &contracts.Worker{
		ID: w.ID, TicketID: tk.ID,
		Status: contracts.WorkerCreating,
	}

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), tk.ID, initial)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !isWorkerReadyTimeout(err) {
		t.Fatalf("expected workerReadyTimeoutError, got=%v", err)
	}
	if !strings.Contains(err.Error(), "等待 worker 就绪超时") {
		t.Fatalf("unexpected timeout message: %v", err)
	}
}

func TestWaitWorkerReady_TransitionCreatingToRunning(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	svc.workerReadyTimeout = 500 * time.Millisecond
	svc.workerReadyPollInterval = 10 * time.Millisecond

	tk := createTicket(t, p.DB, "wait-worker-transition")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerCreating,
		WorktreePath: t.TempDir(),
		Branch:       "ts/wait-transition",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// Transition worker to running after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"status":     contracts.WorkerRunning,
			"log_path":   repo.WorkerStreamLogPath(p.WorkersDir, w.ID),
			"updated_at": time.Now(),
		}).Error
	}()

	initial := &contracts.Worker{
		ID: w.ID, TicketID: tk.ID,
		Status: contracts.WorkerCreating,
	}

	got, err := svc.waitWorkerReadyForDispatch(context.Background(), tk.ID, initial)
	if err != nil {
		t.Fatalf("expected successful transition, got err=%v", err)
	}
	if got == nil || got.Status != contracts.WorkerRunning {
		t.Fatalf("expected running worker after transition, got=%v", got)
	}
}

func TestWaitWorkerReady_ParentContextCancel(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	svc.workerReadyTimeout = 2 * time.Second
	svc.workerReadyPollInterval = 10 * time.Millisecond

	tk := createTicket(t, p.DB, "wait-worker-ctx-cancel")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerCreating,
		WorktreePath: t.TempDir(),
		Branch:       "ts/wait-ctx-cancel",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	initial := &contracts.Worker{
		ID: w.ID, TicketID: tk.ID,
		Status: contracts.WorkerCreating,
	}

	_, err := svc.waitWorkerReadyForDispatch(ctx, tk.ID, initial)
	if err == nil {
		t.Fatalf("expected error on context cancel")
	}
}

func TestWaitWorkerReady_TransitionToStoppedWithRuntimeHandle(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	svc.workerReadyTimeout = 500 * time.Millisecond
	svc.workerReadyPollInterval = 10 * time.Millisecond

	tk := createTicket(t, p.DB, "wait-worker-to-stopped")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerCreating,
		WorktreePath: t.TempDir(),
		Branch:       "ts/wait-to-stopped",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// Transition worker to stopped after a short delay.
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"status":     contracts.WorkerStopped,
			"log_path":   repo.WorkerStreamLogPath(p.WorkersDir, w.ID),
			"updated_at": time.Now(),
		}).Error
	}()

	initial := &contracts.Worker{
		ID: w.ID, TicketID: tk.ID,
		Status: contracts.WorkerCreating,
	}

	got, err := svc.waitWorkerReadyForDispatch(context.Background(), tk.ID, initial)
	if err != nil {
		t.Fatalf("expected stopped worker with runtime handle to be ready, got=%v", err)
	}
	if got == nil || got.Status != contracts.WorkerStopped {
		t.Fatalf("expected stopped worker after transition, got=%v", got)
	}
}
