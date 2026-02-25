package pm

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

func TestCompletePMDispatchJobSuccess_RollbackOnTaskRunSyncFailure(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-complete-tx-success")
	runnerID := "runner-test-success"
	lease := time.Now().Add(2 * time.Minute)

	job := store.PMDispatchJob{
		RequestID:       "dsp_tx_success",
		TicketID:        tk.ID,
		WorkerID:        1,
		TaskRunID:       999999, // 故意不存在，触发 task run 同步失败
		ActiveTicketKey: func(v uint) *uint { return &v }(tk.ID),
		Status:          store.PMDispatchRunning,
		RunnerID:        runnerID,
		LeaseExpiresAt:  &lease,
		Attempt:         1,
	}
	if err := p.DB.Create(&job).Error; err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	err := svc.completePMDispatchJobSuccess(context.Background(), job.ID, runnerID, `{"ok":true}`)
	if err == nil {
		t.Fatalf("expected completePMDispatchJobSuccess fail when task_run missing")
	}
	if !strings.Contains(err.Error(), "task_run 不存在") {
		t.Fatalf("unexpected error: %v", err)
	}

	var after store.PMDispatchJob
	if err := p.DB.First(&after, job.ID).Error; err != nil {
		t.Fatalf("query job failed: %v", err)
	}
	if after.Status != store.PMDispatchRunning {
		t.Fatalf("expected rollback keep running, got=%s", after.Status)
	}
	if strings.TrimSpace(after.RunnerID) != runnerID {
		t.Fatalf("expected rollback keep runner_id=%s, got=%s", runnerID, after.RunnerID)
	}
}

func TestCompletePMDispatchJobFailed_RollbackOnTaskRunSyncFailure(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-complete-tx-failed")
	runnerID := "runner-test-failed"
	lease := time.Now().Add(2 * time.Minute)

	job := store.PMDispatchJob{
		RequestID:       "dsp_tx_failed",
		TicketID:        tk.ID,
		WorkerID:        1,
		TaskRunID:       999998, // 故意不存在，触发 task run 同步失败
		ActiveTicketKey: func(v uint) *uint { return &v }(tk.ID),
		Status:          store.PMDispatchRunning,
		RunnerID:        runnerID,
		LeaseExpiresAt:  &lease,
		Attempt:         1,
	}
	if err := p.DB.Create(&job).Error; err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	err := svc.completePMDispatchJobFailed(context.Background(), job.ID, runnerID, "boom")
	if err == nil {
		t.Fatalf("expected completePMDispatchJobFailed fail when task_run missing")
	}
	if !strings.Contains(err.Error(), "task_run 不存在") {
		t.Fatalf("unexpected error: %v", err)
	}

	var after store.PMDispatchJob
	if err := p.DB.First(&after, job.ID).Error; err != nil {
		t.Fatalf("query job failed: %v", err)
	}
	if after.Status != store.PMDispatchRunning {
		t.Fatalf("expected rollback keep running, got=%s", after.Status)
	}
	if strings.TrimSpace(after.RunnerID) != runnerID {
		t.Fatalf("expected rollback keep runner_id=%s, got=%s", runnerID, after.RunnerID)
	}
}

func TestEnqueuePMDispatchJob_RejectsActiveJobOnDifferentWorker(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-enqueue-worker-mismatch")
	active := store.PMDispatchJob{
		RequestID:       "dsp_active_other_worker",
		TicketID:        tk.ID,
		WorkerID:        101,
		TaskRunID:       0,
		ActiveTicketKey: func(v uint) *uint { return &v }(tk.ID),
		Status:          store.PMDispatchPending,
	}
	if err := p.DB.Create(&active).Error; err != nil {
		t.Fatalf("create active job failed: %v", err)
	}

	_, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, 202, "")
	if err == nil {
		t.Fatalf("expected enqueue fail on active job worker mismatch")
	}
	if !strings.Contains(err.Error(), "绑定其他 worker") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnqueuePMDispatchJob_IdempotentByRequestID(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-enqueue-idempotent-request-id")
	w := createDispatchWorker(t, p.DB, tk.ID)

	reqID := "req_dispatch_idempotent_001"
	first, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, reqID)
	if err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}
	second, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, reqID)
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
	if err := p.DB.Model(&store.PMDispatchJob{}).Where("request_id = ?", reqID).Count(&cnt).Error; err != nil {
		t.Fatalf("count request_id failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected only one job for same request_id, got=%d", cnt)
	}
}

func TestClaimPMDispatchJob_PromotesTicketWorkflowToActive(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-claim-promote-active")
	if err := p.DB.Model(&store.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", store.TicketQueued).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}
	w := createDispatchWorker(t, p.DB, tk.ID)
	job, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, "")
	if err != nil {
		t.Fatalf("enqueue dispatch job failed: %v", err)
	}

	runnerID := "runner-claim-active"
	got, claimed, err := svc.claimPMDispatchJob(context.Background(), job.ID, runnerID, 2*time.Minute)
	if err != nil {
		t.Fatalf("claim dispatch job failed: %v", err)
	}
	if !claimed {
		t.Fatalf("expected claimed=true")
	}
	if got.Status != store.PMDispatchRunning {
		t.Fatalf("expected running job, got=%s", got.Status)
	}

	var ticket store.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != store.TicketActive {
		t.Fatalf("expected ticket active after claim, got=%s", ticket.WorkflowStatus)
	}

	var ev store.TicketWorkflowEvent
	if err := p.DB.Where("ticket_id = ? AND to_workflow_status = ?", tk.ID, store.TicketActive).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("query workflow event failed: %v", err)
	}
	if ev.FromStatus != store.TicketQueued || ev.ToStatus != store.TicketActive {
		t.Fatalf("unexpected workflow event transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.Source != "pm.dispatch" {
		t.Fatalf("unexpected workflow event source: %s", ev.Source)
	}
}

func TestCompletePMDispatchJobFailed_DemotesTicketWorkflowToBlocked(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-failed-demote-blocked")
	if err := p.DB.Model(&store.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", store.TicketActive).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	w := createDispatchWorker(t, p.DB, tk.ID)
	job, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, "")
	if err != nil {
		t.Fatalf("enqueue dispatch job failed: %v", err)
	}

	runnerID := "runner-failed-blocked"
	if _, claimed, err := svc.claimPMDispatchJob(context.Background(), job.ID, runnerID, 2*time.Minute); err != nil {
		t.Fatalf("claim dispatch job failed: %v", err)
	} else if !claimed {
		t.Fatalf("expected claimed=true")
	}

	if err := svc.completePMDispatchJobFailed(context.Background(), job.ID, runnerID, "dispatch boom"); err != nil {
		t.Fatalf("complete dispatch failed state failed: %v", err)
	}

	var ticket store.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != store.TicketBlocked {
		t.Fatalf("expected ticket blocked after dispatch failed, got=%s", ticket.WorkflowStatus)
	}

	var ev store.TicketWorkflowEvent
	if err := p.DB.Where("ticket_id = ? AND to_workflow_status = ?", tk.ID, store.TicketBlocked).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("query workflow event failed: %v", err)
	}
	if ev.FromStatus != store.TicketActive || ev.ToStatus != store.TicketBlocked {
		t.Fatalf("unexpected workflow event transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.Source != "pm.dispatch" {
		t.Fatalf("unexpected workflow event source: %s", ev.Source)
	}

	var inbox store.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyWorkerIncident(w.ID, "dispatch_failed"), store.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("dispatch failed should create incident inbox: %v", err)
	}
	if inbox.Reason != store.InboxIncident {
		t.Fatalf("unexpected inbox reason: %s", inbox.Reason)
	}
	if inbox.TicketID != tk.ID || inbox.WorkerID != w.ID {
		t.Fatalf("unexpected inbox refs: ticket=%d worker=%d", inbox.TicketID, inbox.WorkerID)
	}
}

func TestForceFailActiveDispatchesForTicket_NoActiveJobs(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-force-fail-no-active")

	got, err := svc.ForceFailActiveDispatchesForTicket(context.Background(), tk.ID, "ticket stop")
	if err != nil {
		t.Fatalf("ForceFailActiveDispatchesForTicket failed: %v", err)
	}
	if got != 0 {
		t.Fatalf("expected zero active dispatch jobs force failed, got=%d", got)
	}
}

func TestForceFailActiveDispatchesForTicket_FailsPendingJobAndTaskRun(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	ctx := context.Background()
	tk := createTicket(t, p.DB, "dispatch-force-fail-pending")
	if err := p.DB.Model(&store.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", store.TicketActive).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	w := createDispatchWorker(t, p.DB, tk.ID)
	job, err := svc.enqueuePMDispatchJob(ctx, tk.ID, w.ID, "req_force_pending")
	if err != nil {
		t.Fatalf("enqueue dispatch job failed: %v", err)
	}

	reason := "ticket stop: force fail active dispatch"
	got, err := svc.ForceFailActiveDispatchesForTicket(ctx, tk.ID, reason)
	if err != nil {
		t.Fatalf("ForceFailActiveDispatchesForTicket failed: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected exactly 1 force-failed dispatch, got=%d", got)
	}

	var after store.PMDispatchJob
	if err := p.DB.First(&after, job.ID).Error; err != nil {
		t.Fatalf("query dispatch job failed: %v", err)
	}
	if after.Status != store.PMDispatchFailed {
		t.Fatalf("expected failed dispatch status, got=%s", after.Status)
	}
	if strings.TrimSpace(after.RunnerID) != "" {
		t.Fatalf("expected runner_id cleared, got=%q", after.RunnerID)
	}
	if after.LeaseExpiresAt != nil {
		t.Fatalf("expected lease_expires_at cleared")
	}
	if after.ActiveTicketKey != nil {
		t.Fatalf("expected active_ticket_key cleared")
	}
	if after.FinishedAt == nil {
		t.Fatalf("expected finished_at set")
	}
	if !strings.Contains(after.Error, reason) {
		t.Fatalf("unexpected dispatch error message: %q", after.Error)
	}

	var run store.TaskRun
	if err := p.DB.First(&run, job.TaskRunID).Error; err != nil {
		t.Fatalf("query task run failed: %v", err)
	}
	if run.OrchestrationState != store.TaskFailed {
		t.Fatalf("expected task run failed, got=%s", run.OrchestrationState)
	}
	if strings.TrimSpace(run.ErrorCode) != "dispatch_force_failed_on_stop" {
		t.Fatalf("unexpected task run error code: %q", run.ErrorCode)
	}
	if !strings.Contains(run.ErrorMessage, reason) {
		t.Fatalf("unexpected task run error message: %q", run.ErrorMessage)
	}

	var ev store.TaskEvent
	if err := p.DB.Where("task_run_id = ? AND event_type = ?", job.TaskRunID, "dispatch_force_failed_on_stop").Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("expected force-fail task event: %v", err)
	}
	if !strings.Contains(ev.Note, reason) {
		t.Fatalf("unexpected force-fail event note: %q", ev.Note)
	}

	var ticket store.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != store.TicketActive {
		t.Fatalf("ticket workflow should stay active, got=%s", ticket.WorkflowStatus)
	}

	var inboxCount int64
	if err := p.DB.Model(&store.InboxItem{}).Where("ticket_id = ? AND reason = ?", tk.ID, store.InboxIncident).Count(&inboxCount).Error; err != nil {
		t.Fatalf("count incident inbox failed: %v", err)
	}
	if inboxCount != 0 {
		t.Fatalf("force-fail-on-stop should not create incident inbox, got=%d", inboxCount)
	}
}

func TestForceFailActiveDispatchesForTicket_FailsRunningJobAndTaskRun(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	ctx := context.Background()
	tk := createTicket(t, p.DB, "dispatch-force-fail-running")
	if err := p.DB.Model(&store.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", store.TicketActive).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	w := createDispatchWorker(t, p.DB, tk.ID)
	job, err := svc.enqueuePMDispatchJob(ctx, tk.ID, w.ID, "req_force_running")
	if err != nil {
		t.Fatalf("enqueue dispatch job failed: %v", err)
	}
	if _, claimed, err := svc.claimPMDispatchJob(ctx, job.ID, "runner-force-stop", 2*time.Minute); err != nil {
		t.Fatalf("claim dispatch job failed: %v", err)
	} else if !claimed {
		t.Fatalf("expected claimed=true")
	}

	reason := "ticket stop: force fail running dispatch"
	got, err := svc.ForceFailActiveDispatchesForTicket(ctx, tk.ID, reason)
	if err != nil {
		t.Fatalf("ForceFailActiveDispatchesForTicket failed: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected exactly 1 force-failed dispatch, got=%d", got)
	}

	var after store.PMDispatchJob
	if err := p.DB.First(&after, job.ID).Error; err != nil {
		t.Fatalf("query dispatch job failed: %v", err)
	}
	if after.Status != store.PMDispatchFailed {
		t.Fatalf("expected failed dispatch status, got=%s", after.Status)
	}
	if strings.TrimSpace(after.RunnerID) != "" {
		t.Fatalf("expected runner_id cleared, got=%q", after.RunnerID)
	}
	if after.LeaseExpiresAt != nil {
		t.Fatalf("expected lease_expires_at cleared")
	}
	if after.ActiveTicketKey != nil {
		t.Fatalf("expected active_ticket_key cleared")
	}

	var run store.TaskRun
	if err := p.DB.First(&run, job.TaskRunID).Error; err != nil {
		t.Fatalf("query task run failed: %v", err)
	}
	if run.OrchestrationState != store.TaskFailed {
		t.Fatalf("expected task run failed, got=%s", run.OrchestrationState)
	}
	if strings.TrimSpace(run.ErrorCode) != "dispatch_force_failed_on_stop" {
		t.Fatalf("unexpected task run error code: %q", run.ErrorCode)
	}
}

func createDispatchWorker(t *testing.T, db *gorm.DB, ticketID uint) store.Worker {
	t.Helper()
	w := store.Worker{
		TicketID:     ticketID,
		Status:       store.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/test-dispatch-worker",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-dispatch-test",
	}
	if err := db.Create(&w).Error; err != nil {
		t.Fatalf("create dispatch worker failed: %v", err)
	}
	return w
}
