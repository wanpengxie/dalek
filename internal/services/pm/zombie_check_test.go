package pm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestCheckZombieWorkers_DeadWorker_Recovery(t *testing.T) {
	svc, p, fTmux, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-dead-recovery")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"process_pid": 0,
		"updated_at":  time.Now(),
	}).Error; err != nil {
		t.Fatalf("clear runtime pid failed: %v", err)
	}
	sentinel := "tmux-list-should-not-be-used"
	fTmux.ListErrBySocket[strings.TrimSpace(w.TmuxSocket)] = errors.New(sentinel)

	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Checked != 1 {
		t.Fatalf("expected checked=1, got=%d", out.Checked)
	}
	if out.Recovered != 1 {
		t.Fatalf("expected recovered=1, got=%d errors=%v", out.Recovered, out.Errors)
	}
	if out.Blocked != 0 {
		t.Fatalf("expected blocked=0, got=%d", out.Blocked)
	}
	for _, msg := range out.Errors {
		if strings.Contains(msg, sentinel) {
			t.Fatalf("zombie check should not call tmux list sessions anymore, errors=%v", out.Errors)
		}
	}

	var got contracts.Worker
	if err := p.DB.First(&got, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if got.RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got=%d", got.RetryCount)
	}
	if got.LastRetryAt == nil || got.LastRetryAt.IsZero() {
		t.Fatalf("expected last_retry_at set")
	}
	if strings.TrimSpace(got.LastErrorHash) == "" {
		t.Fatalf("expected last_error_hash set")
	}
}

func TestCheckZombieWorkers_StalledWorker_Recovery(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-stalled-recovery")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	rt, run := createWorkerRunForManagerTickTest(t, svc, p, tk.ID, w.ID, "zombie-stalled")
	stalledAt := time.Now().Add(-(defaultZombieStallThreshold + time.Minute))
	if err := rt.AppendRuntimeSample(context.Background(), contracts.TaskRuntimeSampleInput{
		TaskRunID:  run.ID,
		State:      contracts.TaskHealthIdle,
		NeedsUser:  false,
		Summary:    "still running",
		Source:     "test",
		ObservedAt: stalledAt,
	}); err != nil {
		t.Fatalf("append runtime sample failed: %v", err)
	}

	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Checked != 1 {
		t.Fatalf("expected checked=1, got=%d", out.Checked)
	}
	if out.Recovered != 1 {
		t.Fatalf("expected recovered=1, got=%d errors=%v", out.Recovered, out.Errors)
	}
	if out.Blocked != 0 {
		t.Fatalf("expected blocked=0, got=%d", out.Blocked)
	}
	var got contracts.Worker
	if err := p.DB.First(&got, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if got.RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got=%d", got.RetryCount)
	}
}

func TestCheckZombieWorkers_MaxRetries_BlockTicket(t *testing.T) {
	svc, p, fTmux, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-max-retries")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"retry_count": defaultZombieMaxRetries,
		"process_pid": 0,
		"updated_at":  time.Now(),
	}).Error; err != nil {
		t.Fatalf("set retry_count failed: %v", err)
	}
	delete(fTmux.Sessions, strings.TrimSpace(w.TmuxSession))

	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Blocked != 1 {
		t.Fatalf("expected blocked=1, got=%d errors=%v", out.Blocked, out.Errors)
	}
	if out.Recovered != 0 {
		t.Fatalf("expected recovered=0, got=%d", out.Recovered)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked, got=%s", ticket.WorkflowStatus)
	}

	var inbox contracts.InboxItem
	key := inboxKeyWorkerIncident(w.ID, "zombie_blocked")
	if err := p.DB.Where("key = ? AND status = ?", key, contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected zombie blocked inbox, err=%v", err)
	}
	if inbox.Severity != contracts.InboxBlocker {
		t.Fatalf("expected blocker inbox, got=%s", inbox.Severity)
	}
}

func TestCheckZombieWorkers_BackoffRespected(t *testing.T) {
	svc, p, fTmux, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-backoff")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	lastRetryAt := time.Now().Add(-time.Minute)
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"retry_count":   1,
		"last_retry_at": &lastRetryAt,
		"updated_at":    time.Now(),
	}).Error; err != nil {
		t.Fatalf("set backoff state failed: %v", err)
	}
	delete(fTmux.Sessions, strings.TrimSpace(w.TmuxSession))

	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Recovered != 0 || out.Blocked != 0 {
		t.Fatalf("expected no recovery/block under backoff, got recovered=%d blocked=%d errors=%v", out.Recovered, out.Blocked, out.Errors)
	}

	var got contracts.Worker
	if err := p.DB.First(&got, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if got.RetryCount != 1 {
		t.Fatalf("expected retry_count unchanged=1, got=%d", got.RetryCount)
	}
}

func TestManagerTick_ReportsZombieStats(t *testing.T) {
	svc, p, fTmux, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "manager-tick-zombie-stats")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"process_pid": 0,
		"updated_at":  time.Now(),
	}).Error; err != nil {
		t.Fatalf("clear runtime pid failed: %v", err)
	}
	delete(fTmux.Sessions, strings.TrimSpace(w.TmuxSession))

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{
		DryRun:            true,
		MaxRunningWorkers: 1,
	})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if res.ZombieRecovered != 1 {
		t.Fatalf("expected zombie_recovered=1, got=%d errors=%v", res.ZombieRecovered, res.Errors)
	}
	if res.ZombieBlocked != 0 {
		t.Fatalf("expected zombie_blocked=0, got=%d", res.ZombieBlocked)
	}
}
