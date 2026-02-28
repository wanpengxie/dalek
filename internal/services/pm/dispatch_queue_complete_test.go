package pm

import (
	"context"
	"dalek/internal/contracts"
	"strings"
	"testing"
	"time"
)

func TestCompletePMDispatchJobSuccess_RollbackOnTaskRunSyncFailure(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-complete-tx-success")
	runnerID := "runner-test-success"
	lease := time.Now().Add(2 * time.Minute)

	job := contracts.PMDispatchJob{
		RequestID:       "dsp_tx_success",
		TicketID:        tk.ID,
		WorkerID:        1,
		TaskRunID:       999999, // 故意不存在，触发 task run 同步失败
		ActiveTicketKey: func(v uint) *uint { return &v }(tk.ID),
		Status:          contracts.PMDispatchRunning,
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

	var after contracts.PMDispatchJob
	if err := p.DB.First(&after, job.ID).Error; err != nil {
		t.Fatalf("query job failed: %v", err)
	}
	if after.Status != contracts.PMDispatchRunning {
		t.Fatalf("expected rollback keep running, got=%s", after.Status)
	}
	if strings.TrimSpace(after.RunnerID) != runnerID {
		t.Fatalf("expected rollback keep runner_id=%s, got=%s", runnerID, after.RunnerID)
	}
}

func TestCompletePMDispatchJobFailed_RollbackOnTaskRunSyncFailure(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "dispatch-complete-tx-failed")
	runnerID := "runner-test-failed"
	lease := time.Now().Add(2 * time.Minute)

	job := contracts.PMDispatchJob{
		RequestID:       "dsp_tx_failed",
		TicketID:        tk.ID,
		WorkerID:        1,
		TaskRunID:       999998, // 故意不存在，触发 task run 同步失败
		ActiveTicketKey: func(v uint) *uint { return &v }(tk.ID),
		Status:          contracts.PMDispatchRunning,
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

	var after contracts.PMDispatchJob
	if err := p.DB.First(&after, job.ID).Error; err != nil {
		t.Fatalf("query job failed: %v", err)
	}
	if after.Status != contracts.PMDispatchRunning {
		t.Fatalf("expected rollback keep running, got=%s", after.Status)
	}
	if strings.TrimSpace(after.RunnerID) != runnerID {
		t.Fatalf("expected rollback keep runner_id=%s, got=%s", runnerID, after.RunnerID)
	}
}

func TestForceFailActiveDispatchesForTicket_NoActiveJobs(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
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
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()
	tk := createTicket(t, p.DB, "dispatch-force-fail-pending")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketActive).Error; err != nil {
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

	var after contracts.PMDispatchJob
	if err := p.DB.First(&after, job.ID).Error; err != nil {
		t.Fatalf("query dispatch job failed: %v", err)
	}
	if after.Status != contracts.PMDispatchFailed {
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

	var run contracts.TaskRun
	if err := p.DB.First(&run, job.TaskRunID).Error; err != nil {
		t.Fatalf("query task run failed: %v", err)
	}
	if run.OrchestrationState != contracts.TaskFailed {
		t.Fatalf("expected task run failed, got=%s", run.OrchestrationState)
	}
	if strings.TrimSpace(run.ErrorCode) != "dispatch_force_failed_on_stop" {
		t.Fatalf("unexpected task run error code: %q", run.ErrorCode)
	}
	if !strings.Contains(run.ErrorMessage, reason) {
		t.Fatalf("unexpected task run error message: %q", run.ErrorMessage)
	}

	var ev contracts.TaskEvent
	if err := p.DB.Where("task_run_id = ? AND event_type = ?", job.TaskRunID, "dispatch_force_failed_on_stop").Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("expected force-fail task event: %v", err)
	}
	if !strings.Contains(ev.Note, reason) {
		t.Fatalf("unexpected force-fail event note: %q", ev.Note)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketActive {
		t.Fatalf("ticket workflow should stay active, got=%s", ticket.WorkflowStatus)
	}

	var inboxCount int64
	if err := p.DB.Model(&contracts.InboxItem{}).Where("ticket_id = ? AND reason = ?", tk.ID, contracts.InboxIncident).Count(&inboxCount).Error; err != nil {
		t.Fatalf("count incident inbox failed: %v", err)
	}
	if inboxCount != 0 {
		t.Fatalf("force-fail-on-stop should not create incident inbox, got=%d", inboxCount)
	}
}

func TestForceFailActiveDispatchesForTicket_FailsRunningJobAndTaskRun(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()
	tk := createTicket(t, p.DB, "dispatch-force-fail-running")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketActive).Error; err != nil {
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

	var after contracts.PMDispatchJob
	if err := p.DB.First(&after, job.ID).Error; err != nil {
		t.Fatalf("query dispatch job failed: %v", err)
	}
	if after.Status != contracts.PMDispatchFailed {
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

	var run contracts.TaskRun
	if err := p.DB.First(&run, job.TaskRunID).Error; err != nil {
		t.Fatalf("query task run failed: %v", err)
	}
	if run.OrchestrationState != contracts.TaskFailed {
		t.Fatalf("expected task run failed, got=%s", run.OrchestrationState)
	}
	if strings.TrimSpace(run.ErrorCode) != "dispatch_force_failed_on_stop" {
		t.Fatalf("unexpected task run error code: %q", run.ErrorCode)
	}
}
