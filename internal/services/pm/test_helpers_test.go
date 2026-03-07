package pm

import (
	"dalek/internal/contracts"
	"dalek/internal/services/ticket"
	"fmt"
	"strings"
	"testing"

	"dalek/internal/services/core"
	"dalek/internal/services/worker"
	"dalek/internal/testutil"

	"gorm.io/gorm"
)

type fakeGitClient = testutil.FakeGitClient

func newServiceForTest(t *testing.T) (*Service, *core.Project, *fakeGitClient) {
	t.Helper()

	cp, fGit := testutil.NewTestProject(t)
	workerSvc := worker.New(cp, ticket.New(cp.DB))
	pmSvc := New(cp, workerSvc)
	return pmSvc, cp, fGit
}

func createTicket(t *testing.T, db *gorm.DB, title string) contracts.Ticket {
	t.Helper()

	tk := contracts.Ticket{
		Title:          strings.TrimSpace(title),
		Description:    "test ticket description",
		WorkflowStatus: contracts.TicketBacklog,
	}
	if err := db.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	return tk
}

func createWorkerTaskRun(t *testing.T, db *gorm.DB, ticketID, workerID uint, requestID string) contracts.TaskRun {
	t.Helper()

	taskRun := contracts.TaskRun{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
		ProjectKey:         "test",
		TicketID:           ticketID,
		WorkerID:           workerID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", ticketID),
		RequestID:          strings.TrimSpace(requestID),
		OrchestrationState: contracts.TaskRunning,
	}
	if err := db.Create(&taskRun).Error; err != nil {
		t.Fatalf("create task run failed: %v", err)
	}
	return taskRun
}
