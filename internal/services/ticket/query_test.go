package ticket

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"dalek/internal/testutil"

	"gorm.io/gorm"
)

func newQueryServiceForTest(t *testing.T) (*QueryService, *core.Project) {
	t.Helper()

	cp, _ := testutil.NewTestProject(t)
	return NewQueryService(cp), cp
}

func createTicketForQueryTest(t *testing.T, db *gorm.DB, title string) contracts.Ticket {
	t.Helper()

	tk := contracts.Ticket{Title: strings.TrimSpace(title), Description: "", WorkflowStatus: contracts.TicketBacklog}
	if err := db.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	return tk
}

func TestQueryService_ListTicketViews_IncludesLabel(t *testing.T) {
	svc, p := newQueryServiceForTest(t)

	tk := contracts.Ticket{
		Title:          "ticket-with-label",
		Description:    "desc",
		Label:          "backend",
		WorkflowStatus: contracts.TicketBacklog,
	}
	if err := p.DB.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}

	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].Ticket.Label != "backend" {
		t.Fatalf("expected label backend, got=%q", views[0].Ticket.Label)
	}
}

func TestQueryService_ListTicketViews_ReflectsSessionAndDerivedRuntime(t *testing.T) {
	svc, p := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-view")
	a := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w1",
		Branch:       "ts/demo-ticket-1",
		LogPath:      "/tmp/w1.log",
	}
	if err := p.DB.Create(&a).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	run := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         p.Key,
		TicketID:           tk.ID,
		WorkerID:           a.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          "test-view-run-1",
		OrchestrationState: contracts.TaskRunning,
	}
	if err := p.DB.Create(&run).Error; err != nil {
		t.Fatalf("create task run failed: %v", err)
	}
	if err := p.DB.Create(&contracts.TaskRuntimeSample{
		TaskRunID:  run.ID,
		State:      contracts.TaskHealthBusy,
		NeedsUser:  false,
		Summary:    "working",
		Source:     "test",
		ObservedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("create runtime sample failed: %v", err)
	}
	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if !views[0].SessionAlive {
		t.Fatalf("expected session alive")
	}
	if views[0].RuntimeHealthState != contracts.TaskHealthBusy {
		t.Fatalf("expected runtime busy, got %s", views[0].RuntimeHealthState)
	}

	// active run 结束 + worker stopped => 运行态派生为 dead
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", a.ID).Update("status", contracts.WorkerStopped).Error; err != nil {
		t.Fatalf("update worker failed: %v", err)
	}
	if err := p.DB.Model(&contracts.TaskRun{}).Where("id = ?", run.ID).Update("orchestration_state", contracts.TaskFailed).Error; err != nil {
		t.Fatalf("update task run failed: %v", err)
	}
	views, err = svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].RuntimeHealthState != contracts.TaskHealthDead {
		t.Fatalf("expected runtime dead when process gone, got %s", views[0].RuntimeHealthState)
	}
}

func TestQueryService_ListTicketViews_UsesTaskRunForSessionAlive(t *testing.T) {
	svc, p := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-view-runtime")
	a := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w2",
		Branch:       "ts/demo-ticket-2",
		LogPath:      "/tmp/w2.log",
	}
	if err := p.DB.Create(&a).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	run := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         p.Key,
		TicketID:           tk.ID,
		WorkerID:           a.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          "test-view-run-2",
		OrchestrationState: contracts.TaskRunning,
	}
	if err := p.DB.Create(&run).Error; err != nil {
		t.Fatalf("create task run failed: %v", err)
	}
	if err := p.DB.Create(&contracts.TaskRuntimeSample{
		TaskRunID:  run.ID,
		State:      contracts.TaskHealthBusy,
		NeedsUser:  false,
		Summary:    "working",
		Source:     "test",
		ObservedAt: time.Now(),
	}).Error; err != nil {
		t.Fatalf("create runtime sample failed: %v", err)
	}
	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if !views[0].SessionAlive {
		t.Fatalf("expected process alive by runtime handle")
	}
	if views[0].RuntimeHealthState != contracts.TaskHealthBusy {
		t.Fatalf("expected runtime busy, got %s", views[0].RuntimeHealthState)
	}
}

func TestQueryService_ListTicketViews_BacklogWithAliveRunningWorkerKeepsWorkflowBacklog(t *testing.T) {
	svc, p := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-derived-running")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w3",
		Branch:       "ts/demo-ticket-3",
		LogPath:      "/tmp/w3.log",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	run := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         p.Key,
		TicketID:           tk.ID,
		WorkerID:           w.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          "test-view-run-3",
		OrchestrationState: contracts.TaskRunning,
	}
	if err := p.DB.Create(&run).Error; err != nil {
		t.Fatalf("create task run failed: %v", err)
	}

	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].DerivedStatus != contracts.TicketBacklog {
		t.Fatalf("expected workflow backlog, got %s", views[0].DerivedStatus)
	}
	if !views[0].SessionAlive {
		t.Fatalf("expected session alive")
	}
}

func TestQueryService_ListTicketViews_NoActiveRunKeepsWorkflowBacklog(t *testing.T) {
	svc, p := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-probe-failed")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w4",
		Branch:       "ts/demo-ticket-4",
		LogPath:      "/tmp/w4.log",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].DerivedStatus != contracts.TicketBacklog {
		t.Fatalf("expected workflow backlog when no active run, got %s", views[0].DerivedStatus)
	}
	if views[0].SessionAlive {
		t.Fatalf("expected session offline when no active run")
	}
	if views[0].RuntimeHealthState != contracts.TaskHealthDead {
		t.Fatalf("expected runtime dead when no active run, got=%s", views[0].RuntimeHealthState)
	}
}

func TestQueryService_ListTicketViews_ActiveDispatchMasksDeadWorkerRuntime(t *testing.T) {
	svc, p := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-dispatch-runtime")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketActive).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w5",
		Branch:       "ts/demo-ticket-5",
		LogPath:      "/tmp/w5.log",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	run := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeDispatchTicket,
		ProjectKey:         p.Key,
		TicketID:           tk.ID,
		WorkerID:           w.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          "test-view-run-dispatch",
		OrchestrationState: contracts.TaskRunning,
	}
	if err := p.DB.Create(&run).Error; err != nil {
		t.Fatalf("create dispatch task run failed: %v", err)
	}
	job := contracts.PMDispatchJob{
		RequestID:       "dsp_query_dispatch_active",
		TicketID:        tk.ID,
		WorkerID:        w.ID,
		TaskRunID:       run.ID,
		ActiveTicketKey: func(v uint) *uint { return &v }(tk.ID),
		Status:          contracts.PMDispatchRunning,
		RunnerID:        "runner-query-dispatch",
	}
	if err := p.DB.Create(&job).Error; err != nil {
		t.Fatalf("create dispatch job failed: %v", err)
	}

	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].RuntimeHealthState != contracts.TaskHealthBusy {
		t.Fatalf("expected active dispatch to project runtime busy, got=%s", views[0].RuntimeHealthState)
	}
	if views[0].SessionAlive {
		t.Fatalf("expected session still false before worker loop starts")
	}
}

func TestQueryService_ListTicketViews_SortsByPriorityThenCreatedAtThenID(t *testing.T) {
	svc, p := newQueryServiceForTest(t)

	t1 := createTicketForQueryTest(t, p.DB, "t1")
	t2 := createTicketForQueryTest(t, p.DB, "t2")
	t3 := createTicketForQueryTest(t, p.DB, "t3")

	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", t1.ID).Update("priority", 1).Error; err != nil {
		t.Fatalf("update t1 priority failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", t2.ID).Update("priority", 1).Error; err != nil {
		t.Fatalf("update t2 priority failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", t3.ID).Update("priority", 2).Error; err != nil {
		t.Fatalf("update t3 priority failed: %v", err)
	}

	base := time.Date(2026, 2, 27, 10, 0, 0, 0, time.UTC)
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", t1.ID).Updates(map[string]any{
		"created_at": base.Add(2 * time.Hour),
		"updated_at": base.Add(9 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("update t1 timestamps failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", t2.ID).Updates(map[string]any{
		"created_at": base.Add(1 * time.Hour),
		"updated_at": base.Add(8 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("update t2 timestamps failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", t3.ID).Updates(map[string]any{
		"created_at": base.Add(3 * time.Hour),
		"updated_at": base.Add(7 * time.Hour),
	}).Error; err != nil {
		t.Fatalf("update t3 timestamps failed: %v", err)
	}

	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 3 {
		t.Fatalf("expected 3 views, got %d", len(views))
	}

	got := []uint{views[0].Ticket.ID, views[1].Ticket.ID, views[2].Ticket.ID}
	want := []uint{t3.ID, t2.ID, t1.ID}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected order: got=%v want=%v", got, want)
		}
	}
}

func TestQueryService_GetTicketViewByID_ArchivedTicketIncluded(t *testing.T) {
	svc, p := newQueryServiceForTest(t)

	tk := contracts.Ticket{
		Title:          "archived-ticket",
		Description:    "done",
		WorkflowStatus: contracts.TicketArchived,
	}
	if err := p.DB.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}

	view, err := svc.GetTicketViewByID(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("GetTicketViewByID failed: %v", err)
	}
	if view == nil {
		t.Fatalf("expected non-nil view")
	}
	if view.Ticket.ID != tk.ID {
		t.Fatalf("unexpected ticket id: got=%d want=%d", view.Ticket.ID, tk.ID)
	}
	if view.Ticket.WorkflowStatus != contracts.TicketArchived {
		t.Fatalf("unexpected workflow status: got=%s", view.Ticket.WorkflowStatus)
	}

	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 0 {
		t.Fatalf("expected archived ticket excluded from list, got=%d", len(views))
	}
}

func TestQueryService_GetTicketViewByID_NotFound(t *testing.T) {
	svc, _ := newQueryServiceForTest(t)

	view, err := svc.GetTicketViewByID(context.Background(), 99999)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected gorm.ErrRecordNotFound, got err=%v", err)
	}
	if view != nil {
		t.Fatalf("expected nil view when not found")
	}
}
