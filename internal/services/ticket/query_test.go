package ticket

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"dalek/internal/testutil"

	"gorm.io/gorm"
)

func newQueryServiceForTest(t *testing.T) (*QueryService, *core.Project, *testutil.FakeWorkerRuntime) {
	t.Helper()

	cp, _, _ := testutil.NewTestProject(t)
	fRuntime, ok := cp.WorkerRuntime.(*testutil.FakeWorkerRuntime)
	if !ok {
		t.Fatalf("unexpected worker runtime type: %T", cp.WorkerRuntime)
	}
	return NewQueryService(cp), cp, fRuntime
}

func createTicketForQueryTest(t *testing.T, db *gorm.DB, title string) contracts.Ticket {
	t.Helper()

	tk := contracts.Ticket{Title: strings.TrimSpace(title), Description: "", WorkflowStatus: contracts.TicketBacklog}
	if err := db.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	return tk
}

func setRuntimeAlive(f *testutil.FakeWorkerRuntime, pid int, alive bool) {
	if f.AliveByPID == nil {
		f.AliveByPID = map[int]bool{}
	}
	f.AliveByPID[pid] = alive
}

func TestQueryService_ListTicketViews_ReflectsSessionAndDerivedRuntime(t *testing.T) {
	svc, p, fRuntime := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-view")
	a := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w1",
		Branch:       "ts/demo-ticket-1",
		ProcessPID:   1001,
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
	setRuntimeAlive(fRuntime, a.ProcessPID, true)

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

	// runtime 不在线 + worker stopped => 运行态派生为 dead
	setRuntimeAlive(fRuntime, a.ProcessPID, false)
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", a.ID).Update("status", contracts.WorkerStopped).Error; err != nil {
		t.Fatalf("update worker failed: %v", err)
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

func TestQueryService_ListTicketViews_UsesWorkerRuntimeForSessionAlive(t *testing.T) {
	svc, p, fRuntime := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-view-runtime")
	a := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w2",
		Branch:       "ts/demo-ticket-2",
		ProcessPID:   1002,
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
	setRuntimeAlive(fRuntime, a.ProcessPID, true)

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
	svc, p, fRuntime := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-derived-running")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w3",
		Branch:       "ts/demo-ticket-3",
		ProcessPID:   1003,
		LogPath:      "/tmp/w3.log",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	setRuntimeAlive(fRuntime, w.ProcessPID, true)

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

func TestQueryService_ListTicketViews_SessionProbeFailureKeepsWorkflow(t *testing.T) {
	svc, p, fRuntime := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-probe-failed")
	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w4",
		Branch:       "ts/demo-ticket-4",
		ProcessPID:   1004,
		LogPath:      "/tmp/w4.log",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	fRuntime.IsAliveErr = context.DeadlineExceeded

	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if views[0].DerivedStatus != contracts.TicketBacklog {
		t.Fatalf("expected workflow backlog when probe failed, got %s", views[0].DerivedStatus)
	}
	if views[0].RuntimeHealthState == contracts.TaskHealthDead {
		t.Fatalf("probe failure should not degrade runtime to dead")
	}
}

func TestQueryService_ListTicketViews_SortsByPriorityThenCreatedAtThenID(t *testing.T) {
	svc, p, _ := newQueryServiceForTest(t)

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
