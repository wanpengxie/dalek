package ticket

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"dalek/internal/store"
	"dalek/internal/testutil"

	"gorm.io/gorm"
)

func newQueryServiceForTest(t *testing.T) (*QueryService, *core.Project, *testutil.FakeTmuxClient) {
	t.Helper()

	cp, fTmux, _ := testutil.NewTestProject(t)
	return NewQueryService(cp), cp, fTmux
}

func createTicketForQueryTest(t *testing.T, db *gorm.DB, title string) store.Ticket {
	t.Helper()

	tk := store.Ticket{Title: strings.TrimSpace(title), Description: "", WorkflowStatus: contracts.TicketBacklog}
	if err := db.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	return tk
}

func TestQueryService_ListTicketViews_ReflectsSessionAndDerivedRuntime(t *testing.T) {
	svc, p, fTmux := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-view")
	a := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w1",
		Branch:       "ts/demo-ticket-1",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-ticket-1",
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
	fTmux.Sessions[a.TmuxSession] = true

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

	// session 不在 + worker stopped => 运行态派生为 dead
	delete(fTmux.Sessions, a.TmuxSession)
	if err := p.DB.Model(&store.Worker{}).Where("id = ?", a.ID).Update("status", contracts.WorkerStopped).Error; err != nil {
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
		t.Fatalf("expected runtime dead when session gone, got %s", views[0].RuntimeHealthState)
	}
}

func TestQueryService_ListTicketViews_UsesWorkerSocketForSessionAlive(t *testing.T) {
	svc, p, fTmux := newQueryServiceForTest(t)
	p.Config.TmuxSocket = "dalek-default"

	tk := createTicketForQueryTest(t, p.DB, "ticket-view-socket")
	a := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w2",
		Branch:       "ts/demo-ticket-2",
		TmuxSocket:   "dalek-alt",
		TmuxSession:  "s-ticket-2",
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
	fTmux.Ensure()
	fTmux.SessionsBySocket["dalek-default"] = map[string]bool{}
	fTmux.SessionsBySocket["dalek-alt"] = map[string]bool{
		"s-ticket-2": true,
	}

	views, err := svc.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected 1 view, got %d", len(views))
	}
	if !views[0].SessionAlive {
		t.Fatalf("expected session alive by worker tmux_socket")
	}
	if views[0].RuntimeHealthState != contracts.TaskHealthBusy {
		t.Fatalf("expected runtime busy, got %s", views[0].RuntimeHealthState)
	}
}

func TestQueryService_ListTicketViews_BacklogWithAliveRunningWorkerKeepsWorkflowBacklog(t *testing.T) {
	svc, p, fTmux := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-derived-running")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w3",
		Branch:       "ts/demo-ticket-3",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-ticket-3",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	fTmux.Ensure()
	fTmux.SessionsBySocket["dalek"] = map[string]bool{
		"s-ticket-3": true,
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

func TestQueryService_ListTicketViews_SessionProbeFailureKeepsWorkflow(t *testing.T) {
	svc, p, fTmux := newQueryServiceForTest(t)

	tk := createTicketForQueryTest(t, p.DB, "ticket-probe-failed")
	w := store.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerRunning,
		WorktreePath: "/tmp/w4",
		Branch:       "ts/demo-ticket-4",
		TmuxSocket:   "dalek",
		TmuxSession:  "s-ticket-4",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	fTmux.Ensure()
	fTmux.ListErrBySocket["dalek"] = context.DeadlineExceeded

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
