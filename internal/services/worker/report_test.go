package worker

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestApplyWorkerReport_DoesNotChangeTicketWorkflow(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	root := t.TempDir()

	tk := createTicket(t, p.DB, "continue-backflow")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketBlocked).Error; err != nil {
		t.Fatalf("set blocked failed: %v", err)
	}

	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: root,
		Branch:       "ts/demo-ticket-1",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	r := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  createBoundWorkerRunForReport(t, svc, w),
		Summary:    "继续执行",
		NextAction: string(contracts.NextContinue),
	}
	if err := svc.ApplyWorkerReport(context.Background(), r, "test"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	var got contracts.Ticket
	if err := p.DB.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if got.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("worker report 不应直接修改 workflow_status, got=%s", got.WorkflowStatus)
	}
}

func TestApplyWorkerReport_RejectsMissingTaskRunBinding(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "missing-task-run-binding")
	w := contracts.Worker{TicketID: tk.ID, Status: contracts.WorkerRunning}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	err := svc.ApplyWorkerReport(context.Background(), contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		Summary:    "继续执行",
		NextAction: string(contracts.NextContinue),
	}, "test")
	if err == nil {
		t.Fatalf("expected missing task_run_id to be rejected")
	}
	if !errors.Is(err, ErrInvalidWorkerReportTaskRun) {
		t.Fatalf("expected ErrInvalidWorkerReportTaskRun, got=%v", err)
	}
}

func TestApplyWorkerReport_DoesNotRollbackDoneWorkflow(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	root := t.TempDir()

	tk := createTicket(t, p.DB, "continue-not-rollback-done")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketDone).Error; err != nil {
		t.Fatalf("set done failed: %v", err)
	}

	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: root,
		Branch:       "ts/demo-ticket-2",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	r := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  createBoundWorkerRunForReport(t, svc, w),
		Summary:    "继续执行",
		NextAction: string(contracts.NextContinue),
	}
	if err := svc.ApplyWorkerReport(context.Background(), r, "test"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	var got contracts.Ticket
	if err := p.DB.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if got.WorkflowStatus != contracts.TicketDone {
		t.Fatalf("done ticket should not rollback by report, got=%s", got.WorkflowStatus)
	}
}

func TestApplyWorkerReport_RuntimeSyncFailureIsBestEffort(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	root := t.TempDir()
	tk := createTicket(t, p.DB, "runtime-sync-best-effort")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: root,
		Branch:       "ts/demo-ticket-4",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	runID := createBoundWorkerRunForReport(t, svc, w)

	// 人为破坏 runtime sample 表，模拟观测链路写入失败。
	if err := p.DB.Exec("DROP TABLE task_runtime_samples").Error; err != nil {
		t.Fatalf("drop task_runtime_samples failed: %v", err)
	}

	r := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  runID,
		Summary:    "继续执行中",
		NextAction: string(contracts.NextContinue),
	}
	if err := svc.ApplyWorkerReport(context.Background(), r, "test"); err != nil {
		t.Fatalf("ApplyWorkerReport should not fail on runtime sync error, got=%v", err)
	}

	var gotTicket contracts.Ticket
	if err := p.DB.First(&gotTicket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if gotTicket.WorkflowStatus != contracts.TicketBacklog {
		t.Fatalf("worker report 不应推进 workflow_status, got=%s", gotTicket.WorkflowStatus)
	}

	var taskRuns int64
	if err := p.DB.Model(&contracts.TaskRun{}).Where("worker_id = ?", w.ID).Count(&taskRuns).Error; err != nil {
		t.Fatalf("count task runs failed: %v", err)
	}
	if taskRuns != 1 {
		t.Fatalf("runtime sync transaction should not create extra task runs, got=%d", taskRuns)
	}
}

func TestApplyWorkerReport_ResetsZombieRetryState(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	root := t.TempDir()
	tk := createTicket(t, p.DB, "report-reset-zombie-retry")
	lastRetryAt := time.Now().Add(-5 * time.Minute)
	w := contracts.Worker{
		TicketID:      tk.ID,
		Status:        contracts.WorkerRunning,
		WorktreePath:  root,
		Branch:        "ts/demo-ticket-5",
		RetryCount:    2,
		LastRetryAt:   &lastRetryAt,
		LastErrorHash: "deadbeef",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	r := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  createBoundWorkerRunForReport(t, svc, w),
		Summary:    "恢复正常继续执行",
		NextAction: string(contracts.NextContinue),
	}
	if err := svc.ApplyWorkerReport(context.Background(), r, "test"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	var got contracts.Worker
	if err := p.DB.First(&got, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if got.RetryCount != 0 {
		t.Fatalf("expected retry_count reset to 0, got=%d", got.RetryCount)
	}
	if got.LastRetryAt != nil {
		t.Fatalf("expected last_retry_at cleared, got=%v", got.LastRetryAt)
	}
	if strings.TrimSpace(got.LastErrorHash) != "" {
		t.Fatalf("expected last_error_hash cleared, got=%q", got.LastErrorHash)
	}
}

func TestApplyWorkerReport_DoneIsIdempotentForTerminalRun(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	root := t.TempDir()
	tk := createTicket(t, p.DB, "report-done-idempotent")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: root,
		Branch:       "ts/demo-ticket-done-idempotent",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	runID := createBoundWorkerRunForReport(t, svc, w)
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  runID,
		Summary:    "任务已完成",
		NextAction: string(contracts.NextDone),
		HeadSHA:    "abc123",
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test"); err != nil {
		t.Fatalf("first ApplyWorkerReport failed: %v", err)
	}

	var firstRun contracts.TaskRun
	if err := p.DB.Where("worker_id = ?", w.ID).Order("id desc").First(&firstRun).Error; err != nil {
		t.Fatalf("load first run failed: %v", err)
	}
	if firstRun.OrchestrationState != contracts.TaskSucceeded {
		t.Fatalf("expected first run succeeded, got=%s", firstRun.OrchestrationState)
	}
	firstPayload := strings.TrimSpace(firstRun.ResultPayloadJSON)

	if err := svc.ApplyWorkerReport(context.Background(), report, "test"); err == nil {
		t.Fatalf("expected duplicate done to surface duplicate terminal report error")
	} else if !errors.Is(err, ErrDuplicateTerminalReport) {
		t.Fatalf("expected duplicate terminal report error, got=%v", err)
	}

	var runCount int64
	if err := p.DB.Model(&contracts.TaskRun{}).Where("worker_id = ?", w.ID).Count(&runCount).Error; err != nil {
		t.Fatalf("count task runs failed: %v", err)
	}
	if runCount != 1 {
		t.Fatalf("expected duplicate done not create extra run, got=%d", runCount)
	}

	var after contracts.TaskRun
	if err := p.DB.First(&after, firstRun.ID).Error; err != nil {
		t.Fatalf("load run after duplicate done failed: %v", err)
	}
	if after.OrchestrationState != contracts.TaskSucceeded {
		t.Fatalf("expected run remains succeeded, got=%s", after.OrchestrationState)
	}
	if strings.TrimSpace(after.ResultPayloadJSON) != firstPayload {
		t.Fatalf("expected result payload unchanged on duplicate done")
	}

	var succeededCount int64
	if err := p.DB.Model(&contracts.TaskEvent{}).
		Where("task_run_id = ? AND event_type = ?", firstRun.ID, "task_succeeded").
		Count(&succeededCount).Error; err != nil {
		t.Fatalf("count task_succeeded failed: %v", err)
	}
	if succeededCount != 1 {
		t.Fatalf("expected only one task_succeeded event, got=%d", succeededCount)
	}

	var duplicateCount int64
	if err := p.DB.Model(&contracts.TaskEvent{}).
		Where("task_run_id = ? AND event_type = ?", firstRun.ID, "duplicate_terminal_report").
		Count(&duplicateCount).Error; err != nil {
		t.Fatalf("count duplicate_terminal_report failed: %v", err)
	}
	if duplicateCount != 1 {
		t.Fatalf("expected one duplicate_terminal_report event, got=%d", duplicateCount)
	}
}

func createBoundWorkerRunForReport(t *testing.T, svc *Service, w contracts.Worker) uint {
	t.Helper()
	run, err := svc.ensureActiveWorkerTaskRun(context.Background(), w, fmt.Sprintf("report test bootstrap w%d", w.ID), time.Now())
	if err != nil {
		t.Fatalf("ensure active worker task run failed: %v", err)
	}
	return run.ID
}
