package pm

import (
	"dalek/internal/contracts"
	"dalek/internal/services/ticket"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/services/core"
	"dalek/internal/services/worker"
	"dalek/internal/testutil"

	"gorm.io/gorm"
)

type fakeGitClient = testutil.FakeGitClient

const testWorkerDoneHeadSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

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

func writeWorkerLoopStateForTest(t *testing.T, worktreePath, nextAction, summary string, blockers []string, allDone bool, headSHA, workingTree string) {
	t.Helper()
	items := []map[string]any{
		{"id": "phase-understanding", "status": "done"},
		{"id": "phase-implementation", "status": "done"},
		{"id": "phase-validation", "status": "done"},
		{"id": "phase-handoff", "status": "done"},
	}
	currentStatus := "done"
	if !allDone {
		items = []map[string]any{
			{"id": "phase-understanding", "status": "done"},
			{"id": "phase-implementation", "status": "in_progress"},
			{"id": "phase-validation", "status": "pending"},
			{"id": "phase-handoff", "status": "pending"},
		}
		currentStatus = "running"
	}
	writeWorkerLoopStateWithItemsForTest(t, worktreePath, nextAction, summary, blockers, currentStatus, items, headSHA, workingTree)
}

func writeWorkerLoopStateWithItemsForTest(t *testing.T, worktreePath, nextAction, summary string, blockers []string, currentStatus string, items []map[string]any, headSHA, workingTree string) {
	t.Helper()
	statePath := filepath.Join(worktreePath, ".dalek", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir state dir failed: %v", err)
	}
	payload := map[string]any{
		"ticket": map[string]any{
			"id":        1,
			"worker_id": 1,
		},
		"phases": map[string]any{
			"current_id":     "phase-handoff",
			"current_status": strings.TrimSpace(currentStatus),
			"next_action":    strings.TrimSpace(nextAction),
			"summary":        strings.TrimSpace(summary),
			"items":          items,
		},
		"blockers": cleanStringSlice(blockers),
		"code": map[string]any{
			"head_sha":            strings.TrimSpace(headSHA),
			"working_tree":        strings.TrimSpace(workingTree),
			"last_commit_subject": "test commit",
		},
		"updated_at": time.Now().Format(time.RFC3339),
	}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal state failed: %v", err)
	}
	if err := os.WriteFile(statePath, raw, 0o644); err != nil {
		t.Fatalf("write state failed: %v", err)
	}
}

func initGitWorktreeForTest(t *testing.T, worktreePath string) string {
	t.Helper()
	gitFile := filepath.Join(worktreePath, ".git")
	if st, err := os.Stat(gitFile); err == nil && !st.IsDir() {
		if err := os.Remove(gitFile); err != nil {
			t.Fatalf("remove fake .git failed: %v", err)
		}
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", worktreePath}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, string(out))
		}
		return strings.TrimSpace(string(out))
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "PM Test")
	testFile := filepath.Join(worktreePath, "README.md")
	if err := os.WriteFile(testFile, []byte("test\n"), 0o644); err != nil {
		t.Fatalf("write git test file failed: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "test commit")
	return run("rev-parse", "HEAD")
}
