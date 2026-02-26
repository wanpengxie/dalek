package pm

import (
	"dalek/internal/contracts"
	"dalek/internal/services/ticket"
	"strings"
	"testing"

	"dalek/internal/services/core"
	"dalek/internal/services/worker"
	"dalek/internal/store"
	"dalek/internal/testutil"

	"gorm.io/gorm"
)

type fakeTmuxClient = testutil.FakeTmuxClient
type fakeGitClient = testutil.FakeGitClient

func newServiceForTest(t *testing.T) (*Service, *core.Project, *fakeTmuxClient, *fakeGitClient) {
	t.Helper()

	cp, fTmux, fGit := testutil.NewTestProject(t)
	workerSvc := worker.New(cp, ticket.New(cp.DB))
	pmSvc := New(cp, workerSvc)
	return pmSvc, cp, fTmux, fGit
}

func createTicket(t *testing.T, db *gorm.DB, title string) store.Ticket {
	t.Helper()

	tk := store.Ticket{
		Title:          strings.TrimSpace(title),
		Description:    "test ticket description",
		WorkflowStatus: contracts.TicketBacklog,
	}
	if err := db.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	return tk
}
