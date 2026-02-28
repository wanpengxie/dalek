package pm

import (
	"context"
	"dalek/internal/contracts"
	"testing"
	"time"

	tasksvc "dalek/internal/services/task"
)

func TestEnsureWorkerTaskRunFromDispatch_CancelsPreviousAndWritesRuntime(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "pm-task-runtime")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: t.TempDir(),
		Branch:       "ts/demo-ticket-21",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	rt := tasksvc.New(p.DB)
	now := time.Now().UTC().Truncate(time.Second)
	oldRun, err := rt.CreateRun(context.Background(), contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
		ProjectKey:         p.Key,
		TicketID:           tk.ID,
		WorkerID:           w.ID,
		SubjectType:        "ticket",
		SubjectID:          "21",
		RequestID:          "wrk_old_dispatch_21",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("create old run failed: %v", err)
	}

	job := contracts.PMDispatchJob{
		RequestID: "dispatch_req_21",
		TaskRunID: 2101,
	}
	created, err := svc.ensureWorkerTaskRunFromDispatch(
		context.Background(),
		job,
		tk,
		w,
		".dalek/PLAN.md",
		contracts.TaskHealthBusy,
		contracts.TaskPhaseImplementing,
		"continue",
		"dispatch accepted",
		map[string]any{"source": "pm_test"},
	)
	if err != nil {
		t.Fatalf("ensureWorkerTaskRunFromDispatch failed: %v", err)
	}
	if created.ID == 0 {
		t.Fatalf("expected created run id")
	}
	if created.ID == oldRun.ID {
		t.Fatalf("expected a new run id, got old run id=%d", oldRun.ID)
	}

	oldAfter, err := rt.FindRunByID(context.Background(), oldRun.ID)
	if err != nil {
		t.Fatalf("find old run failed: %v", err)
	}
	if oldAfter == nil {
		t.Fatalf("old run not found")
	}
	if oldAfter.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected old run canceled, got=%s", oldAfter.OrchestrationState)
	}

	newAfter, err := rt.FindRunByID(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("find new run failed: %v", err)
	}
	if newAfter == nil {
		t.Fatalf("new run not found")
	}
	if newAfter.OrchestrationState != contracts.TaskRunning {
		t.Fatalf("expected new run running, got=%s", newAfter.OrchestrationState)
	}

	var runtimeSample contracts.TaskRuntimeSample
	if err := p.DB.Where("task_run_id = ?", created.ID).Order("id desc").First(&runtimeSample).Error; err != nil {
		t.Fatalf("query runtime sample failed: %v", err)
	}
	if runtimeSample.State != contracts.TaskHealthBusy {
		t.Fatalf("expected runtime sample state busy, got=%s", runtimeSample.State)
	}

	var semantic contracts.TaskSemanticReport
	if err := p.DB.Where("task_run_id = ?", created.ID).Order("id desc").First(&semantic).Error; err != nil {
		t.Fatalf("query semantic report failed: %v", err)
	}
	if semantic.Phase != contracts.TaskPhaseImplementing {
		t.Fatalf("expected semantic phase implementing, got=%s", semantic.Phase)
	}
	if semantic.NextAction != "continue" {
		t.Fatalf("expected next_action continue, got=%s", semantic.NextAction)
	}
}
