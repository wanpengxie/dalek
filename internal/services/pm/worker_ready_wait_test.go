package pm

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

func TestWaitWorkerReady_AlreadyRunning(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "wait-worker-running")
	w := &contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		TmuxSocket:   "dalek",
		TmuxSession:  "s-running-test",
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
	svc, _, _, _ := newServiceForTest(t)

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), 1, nil)
	if err == nil {
		t.Fatalf("expected error for nil worker")
	}
	if !strings.Contains(err.Error(), "尚未启动") {
		t.Fatalf("expected missing session error, got=%v", err)
	}
}

func TestWaitWorkerReady_EmptySession(t *testing.T) {
	svc, _, _, _ := newServiceForTest(t)
	w := &contracts.Worker{
		Status:      contracts.WorkerRunning,
		TmuxSession: "",
	}

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), 1, w)
	if err == nil {
		t.Fatalf("expected error for empty session")
	}
	if !strings.Contains(err.Error(), "尚未启动") {
		t.Fatalf("expected missing session error, got=%v", err)
	}
}

func TestWaitWorkerReady_StoppedWorkerRejects(t *testing.T) {
	svc, _, _, _ := newServiceForTest(t)
	w := &contracts.Worker{
		ID:          99,
		Status:      contracts.WorkerStopped,
		TmuxSocket:  "dalek",
		TmuxSession: "s-stopped-test",
	}

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), 1, w)
	if err == nil {
		t.Fatalf("expected error for stopped worker")
	}
	if !strings.Contains(err.Error(), "不在 running") {
		t.Fatalf("expected not running error, got=%v", err)
	}
}

func TestWaitWorkerReady_FailedWorkerRejects(t *testing.T) {
	svc, _, _, _ := newServiceForTest(t)
	w := &contracts.Worker{
		ID:          98,
		Status:      contracts.WorkerFailed,
		TmuxSocket:  "dalek",
		TmuxSession: "s-failed-test",
	}

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), 1, w)
	if err == nil {
		t.Fatalf("expected error for failed worker")
	}
	if !strings.Contains(err.Error(), "不在 running") {
		t.Fatalf("expected not running error, got=%v", err)
	}
}

func TestWaitWorkerReady_TimeoutWhileCreating(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	svc.workerReadyTimeout = 100 * time.Millisecond
	svc.workerReadyPollInterval = 10 * time.Millisecond

	tk := createTicket(t, p.DB, "wait-worker-timeout")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerCreating,
		WorktreePath: t.TempDir(),
		Branch:       "ts/wait-timeout",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-timeout-test",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	initial := &contracts.Worker{
		ID: w.ID, TicketID: tk.ID,
		Status: contracts.WorkerCreating, TmuxSocket: "dalek", TmuxSession: "s-timeout-test",
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
	svc, p, _, _ := newServiceForTest(t)
	svc.workerReadyTimeout = 500 * time.Millisecond
	svc.workerReadyPollInterval = 10 * time.Millisecond

	tk := createTicket(t, p.DB, "wait-worker-transition")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerCreating,
		WorktreePath: t.TempDir(),
		Branch:       "ts/wait-transition",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-transition-test",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// Transition worker to running after a short delay.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = p.DB.Model(&store.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"status":     contracts.WorkerRunning,
			"updated_at": time.Now(),
		}).Error
	}()

	initial := &contracts.Worker{
		ID: w.ID, TicketID: tk.ID,
		Status: contracts.WorkerCreating, TmuxSocket: "dalek", TmuxSession: "s-transition-test",
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
	svc, p, _, _ := newServiceForTest(t)
	svc.workerReadyTimeout = 2 * time.Second
	svc.workerReadyPollInterval = 10 * time.Millisecond

	tk := createTicket(t, p.DB, "wait-worker-ctx-cancel")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerCreating,
		WorktreePath: t.TempDir(),
		Branch:       "ts/wait-ctx-cancel",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-ctx-cancel-test",
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
		Status: contracts.WorkerCreating, TmuxSocket: "dalek", TmuxSession: "s-ctx-cancel-test",
	}

	_, err := svc.waitWorkerReadyForDispatch(ctx, tk.ID, initial)
	if err == nil {
		t.Fatalf("expected error on context cancel")
	}
}

func TestWaitWorkerReady_TransitionToStopped(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	svc.workerReadyTimeout = 500 * time.Millisecond
	svc.workerReadyPollInterval = 10 * time.Millisecond

	tk := createTicket(t, p.DB, "wait-worker-to-stopped")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerCreating,
		WorktreePath: t.TempDir(),
		Branch:       "ts/wait-to-stopped",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-to-stopped-test",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// Transition worker to stopped after a short delay.
	go func() {
		time.Sleep(30 * time.Millisecond)
		_ = p.DB.Model(&store.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"status":     contracts.WorkerStopped,
			"updated_at": time.Now(),
		}).Error
	}()

	initial := &contracts.Worker{
		ID: w.ID, TicketID: tk.ID,
		Status: contracts.WorkerCreating, TmuxSocket: "dalek", TmuxSession: "s-to-stopped-test",
	}

	_, err := svc.waitWorkerReadyForDispatch(context.Background(), tk.ID, initial)
	if err == nil {
		t.Fatalf("expected error when worker transitions to stopped")
	}
	if !strings.Contains(err.Error(), "不在 running") {
		t.Fatalf("expected not running error, got=%v", err)
	}
}
