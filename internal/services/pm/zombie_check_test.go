package pm

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestCheckZombieWorkers_DeadWorker_Recovery(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-dead-recovery")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"log_path":   "",
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("clear runtime log path failed: %v", err)
	}
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
	if out.Illegal != 0 {
		t.Fatalf("expected illegal=0, got=%d", out.Illegal)
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
	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket queued after recovery, got=%s", ticket.WorkflowStatus)
	}
	var lifecycle contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleRequeued).Order("sequence desc").First(&lifecycle).Error; err != nil {
		t.Fatalf("expected requeued lifecycle event: %v", err)
	}
	var lost contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleExecutionLost).Order("sequence desc").First(&lost).Error; err != nil {
		t.Fatalf("expected execution_lost lifecycle event: %v", err)
	}
	if got := strings.TrimSpace(lost.PayloadJSON.String()); !strings.Contains(got, `"observation_kind":"host_loss"`) || !strings.Contains(got, `"failure_code":"runtime_anchor_missing"`) {
		t.Fatalf("unexpected execution_lost payload: %s", got)
	}
}

func TestCheckZombieWorkers_StalledWorker_Recovery(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-stalled-recovery")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("mark worker running failed: %v", err)
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
	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket queued after stalled recovery, got=%s", ticket.WorkflowStatus)
	}
	var lost contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleExecutionLost).Order("sequence desc").First(&lost).Error; err != nil {
		t.Fatalf("expected execution_lost lifecycle event: %v", err)
	}
	if got := strings.TrimSpace(lost.PayloadJSON.String()); !strings.Contains(got, `"observation_kind":"visibility_timeout"`) || !strings.Contains(got, `"last_seen_at"`) {
		t.Fatalf("unexpected execution_lost payload: %s", got)
	}
}

func TestCheckZombieWorkers_MaxRetries_BlockTicket(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-max-retries")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":      contracts.WorkerRunning,
		"retry_count": defaultZombieMaxRetries,
		"log_path":    "",
		"updated_at":  time.Now(),
	}).Error; err != nil {
		t.Fatalf("set retry_count failed: %v", err)
	}
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
	key := inboxKeyWorkerIncident(w.ID, "execution_escalated")
	if err := p.DB.Where("key = ? AND status = ?", key, contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected execution escalated inbox, err=%v", err)
	}
	if inbox.Severity != contracts.InboxBlocker {
		t.Fatalf("expected blocker inbox, got=%s", inbox.Severity)
	}
}

func TestCheckZombieWorkers_UserStopDoesNotConvergeExecutionLost(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-user-stop")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerStopped,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set worker stopped failed: %v", err)
	}
	rt, run := createWorkerRunForManagerTickTest(t, svc, p, tk.ID, w.ID, "zombie-user-stop")
	if err := rt.AppendEvent(context.Background(), contracts.TaskEventInput{
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

	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Blocked != 1 {
		t.Fatalf("expected blocked=1, got=%d errors=%v", out.Blocked, out.Errors)
	}
	if out.Illegal != 0 {
		t.Fatalf("expected illegal=0, got=%d", out.Illegal)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked, got=%s", ticket.WorkflowStatus)
	}

	snapshot, err := svc.RebuildTicketLifecycleSnapshot(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("RebuildTicketLifecycleSnapshot failed: %v", err)
	}
	if snapshot.Explanation == nil || snapshot.Explanation.BlockedReason != ticketBlockedReasonUserStopped {
		t.Fatalf("expected blocked_reason=%s, got=%+v", ticketBlockedReasonUserStopped, snapshot.Explanation)
	}

	var lostCount int64
	if err := p.DB.Model(&contracts.TicketLifecycleEvent{}).
		Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleExecutionLost).
		Count(&lostCount).Error; err != nil {
		t.Fatalf("count execution_lost lifecycle failed: %v", err)
	}
	if lostCount != 0 {
		t.Fatalf("expected no execution_lost lifecycle, got=%d", lostCount)
	}
}

func TestManagerTick_SkipsQueuedRetryDuringBackoff(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-backoff")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	lastRetryAt := time.Now()
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerStopped,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("unexpected worker update failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"retry_count":   1,
		"last_retry_at": &lastRetryAt,
		"updated_at":    time.Now(),
	}).Error; err != nil {
		t.Fatalf("set backoff state failed: %v", err)
	}

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if len(res.StartedTickets) != 0 || len(res.ActivatedTickets) != 0 {
		t.Fatalf("expected queued retry deferred by backoff, started=%v activated=%v", res.StartedTickets, res.ActivatedTickets)
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
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "manager-tick-zombie-stats")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"log_path":   "",
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("clear runtime log path failed: %v", err)
	}
	createWorkerRunForManagerTickTest(t, svc, p, tk.ID, w.ID, "zombie-stats")
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
	if res.ZombieIllegal != 0 {
		t.Fatalf("expected zombie_illegal=0, got=%d", res.ZombieIllegal)
	}
	if res.ZombieUndefined != 0 {
		t.Fatalf("expected zombie_undefined=0, got=%d", res.ZombieUndefined)
	}
}

func TestCheckZombieWorkers_ActiveWithStoppedWorker_Requeues(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-illegal-active-stopped")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now()
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerStopped,
		"stopped_at": &now,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("mark worker stopped failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}

	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Illegal != 1 {
		t.Fatalf("expected illegal=1, got=%d errors=%v", out.Illegal, out.Errors)
	}
	if out.Blocked != 0 {
		t.Fatalf("expected blocked=0, got=%d errors=%v", out.Blocked, out.Errors)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket queued, got=%s", ticket.WorkflowStatus)
	}

	var lost contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleExecutionLost).Order("sequence desc").First(&lost).Error; err != nil {
		t.Fatalf("expected execution_lost lifecycle event: %v", err)
	}
	if got := strings.TrimSpace(lost.PayloadJSON.String()); !strings.Contains(got, `"observation_kind":"unexpected_exit"`) || !strings.Contains(got, `"worker_status":"stopped"`) {
		t.Fatalf("unexpected execution_lost payload: %s", got)
	}
}

func TestCheckZombieWorkers_ActiveWithoutRuntimeStatus_RequeuesHostLoss(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-active-no-runtime-status")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now()
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("set worker running failed: %v", err)
	}

	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Recovered != 1 || out.Blocked != 0 {
		t.Fatalf("expected recovered=1 blocked=0, got recovered=%d blocked=%d errors=%v", out.Recovered, out.Blocked, out.Errors)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket queued, got=%s", ticket.WorkflowStatus)
	}

	var lost contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleExecutionLost).Order("sequence desc").First(&lost).Error; err != nil {
		t.Fatalf("expected execution_lost lifecycle event: %v", err)
	}
	if got := strings.TrimSpace(lost.PayloadJSON.String()); !strings.Contains(got, `"observation_kind":"host_loss"`) || !strings.Contains(got, `"failure_code":"active_run_missing"`) {
		t.Fatalf("unexpected execution_lost payload: %s", got)
	}
}

func TestCheckZombieWorkers_ActiveWithStoppedWorker_EmitsStatusHook(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	hook := &testStatusChangeHook{ch: make(chan StatusChangeEvent, 1)}
	svc.SetStatusChangeHook(hook)

	tk := createTicket(t, p.DB, "zombie-illegal-active-stopped-notify")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now()
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerStopped,
		"stopped_at": &now,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("mark worker stopped failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}

	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Illegal != 1 || out.Blocked != 0 {
		t.Fatalf("expected illegal=1 blocked=0, got illegal=%d blocked=%d errors=%v", out.Illegal, out.Blocked, out.Errors)
	}

	ev := waitStatusEvent(t, hook.ch)
	if ev.TicketID != tk.ID {
		t.Fatalf("unexpected ticket_id: got=%d want=%d", ev.TicketID, tk.ID)
	}
	if ev.WorkerID != w.ID {
		t.Fatalf("unexpected worker_id: got=%d want=%d", ev.WorkerID, w.ID)
	}
	if ev.FromStatus != contracts.TicketActive || ev.ToStatus != contracts.TicketQueued {
		t.Fatalf("unexpected transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.Source != "pm.zombie" {
		t.Fatalf("unexpected source: %s", ev.Source)
	}
	if !strings.Contains(ev.Detail, "worker 不在 running") {
		t.Fatalf("unexpected detail: %q", ev.Detail)
	}
}

func TestCheckZombieWorkers_ActiveWithoutWorker_DemotesBlocked(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-illegal-active-no-worker")
	now := time.Now()
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}

	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Illegal != 1 {
		t.Fatalf("expected illegal=1, got=%d errors=%v", out.Illegal, out.Errors)
	}
	if out.Blocked != 1 {
		t.Fatalf("expected blocked=1, got=%d errors=%v", out.Blocked, out.Errors)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked, got=%s", ticket.WorkflowStatus)
	}

	var inbox contracts.InboxItem
	key := inboxKeyTicketIncident(tk.ID, "active_without_worker")
	if err := p.DB.Where("key = ? AND status = ?", key, contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected active_without_worker inbox, err=%v", err)
	}
}

func TestCheckZombieWorkers_UndefinedWorkflow_DemotesBlocked(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "zombie-undefined-workflow")
	now := time.Now()
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketWorkflowStatus("old_unknown_state"),
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket old workflow failed: %v", err)
	}

	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	out := svc.checkZombieWorkers(context.Background(), p.DB, rt)
	if out.Undefined != 1 {
		t.Fatalf("expected undefined=1, got=%d errors=%v", out.Undefined, out.Errors)
	}
	if out.Blocked != 1 {
		t.Fatalf("expected blocked=1, got=%d errors=%v", out.Blocked, out.Errors)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked, got=%s", ticket.WorkflowStatus)
	}

	var inbox contracts.InboxItem
	key := inboxKeyTicketIncident(tk.ID, "undefined_workflow_status")
	if err := p.DB.Where("key = ? AND status = ?", key, contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected undefined_workflow_status inbox, err=%v", err)
	}
}

func TestManagerTick_ReportsZombieStateDriftStats(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "manager-tick-zombie-state-drift")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now()
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerStopped,
		"stopped_at": &now,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("mark worker stopped failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{
		DryRun:            true,
		MaxRunningWorkers: 1,
	})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if res.ZombieIllegal != 1 {
		t.Fatalf("expected zombie_illegal=1, got=%d errors=%v", res.ZombieIllegal, res.Errors)
	}
	if res.ZombieBlocked != 0 {
		t.Fatalf("expected zombie_blocked=0, got=%d errors=%v", res.ZombieBlocked, res.Errors)
	}
}
