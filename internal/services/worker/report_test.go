package worker

import (
	"context"
	"testing"

	"dalek/internal/contracts"
)

func TestApplyWorkerReport_DoesNotChangeTicketWorkflow(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

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
		TmuxSocket:   "dalek",
		TmuxSession:  "s-ticket-1",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	r := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   tk.ID,
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

func TestApplyWorkerReport_DoesNotRollbackDoneWorkflow(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

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
		TmuxSocket:   "dalek",
		TmuxSession:  "s-ticket-2",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	r := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   tk.ID,
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
	svc, p, _, _ := newServiceForTest(t)

	root := t.TempDir()
	tk := createTicket(t, p.DB, "runtime-sync-best-effort")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: root,
		Branch:       "ts/demo-ticket-4",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-ticket-4",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	// 人为破坏 runtime sample 表，模拟观测链路写入失败。
	if err := p.DB.Exec("DROP TABLE task_runtime_samples").Error; err != nil {
		t.Fatalf("drop task_runtime_samples failed: %v", err)
	}

	r := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   tk.ID,
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
	if taskRuns != 0 {
		t.Fatalf("runtime sync transaction should rollback partial task runs, got=%d", taskRuns)
	}
}
