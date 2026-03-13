package pm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
)

func TestEnsureWorkerBootstrap_UsesControlWorkerTemplates(t *testing.T) {
	svc, project, _ := newServiceForTest(t)

	worktree := filepath.Join(t.TempDir(), "worker-worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree failed: %v", err)
	}
	mustRun(t, worktree, "git", "init")
	mustRun(t, worktree, "git", "config", "user.name", "Test User")
	mustRun(t, worktree, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	mustRun(t, worktree, "git", "add", "README.md")
	mustRun(t, worktree, "git", "commit", "-m", "initial commit")

	customKernel := strings.TrimSpace(`
<task_context>
<title>
{{TICKET_TITLE：业务任务标题。用于定义本轮交付主题，回答“这轮要完成什么业务目标”。}}
</title>
</task_context>
<current_state>
placeholder
</current_state>
attachments={{OTHER_DOCUMENTS：需求相关的附属资料与上下文集合。可包含对话记录、GitHub issue、用户与 agent 的详细讨论文档，以及文档路径/接口说明/截图线索等。它是参考输入，不是执行步骤。}}
`)
	if err := os.WriteFile(repo.ControlWorkerKernelPath(project.Layout), []byte(customKernel+"\n"), 0o644); err != nil {
		t.Fatalf("write custom worker-kernel failed: %v", err)
	}
	customState := strings.TrimSpace(`
{
  "ticket": {
    "id": "{{DALEK_TICKET_ID}}",
    "worker_id": "{{DALEK_WORKER_ID}}"
  },
  "phases": {
    "current_id": "phase-understanding",
    "current_status": "running",
    "next_action": "continue",
    "summary": "{{SUMMARY}}",
    "items": []
  },
  "blockers": [],
  "code": {
    "head_sha": "{{HEAD_SHA}}",
    "working_tree": "{{WORKING_TREE_STATUS}}",
    "last_commit_subject": "{{LAST_COMMIT_SUBJECT}}"
  },
  "updated_at": "{{NOW_RFC3339}}"
}
`)
	if err := os.WriteFile(repo.ControlWorkerStatePath(project.Layout), []byte(customState+"\n"), 0o644); err != nil {
		t.Fatalf("write custom worker state failed: %v", err)
	}

	ticket := contracts.Ticket{
		Title:          "bootstrap title",
		Description:    "bootstrap description",
		WorkflowStatus: contracts.TicketBacklog,
	}
	if err := project.DB.Create(&ticket).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	worker := contracts.Worker{
		TicketID:     ticket.ID,
		Status:       contracts.WorkerStopped,
		WorktreePath: worktree,
		Branch:       "ticket-1",
		LogPath:      filepath.Join(worktree, "worker.log"),
	}
	if err := project.DB.Create(&worker).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	paths, err := svc.ensureWorkerBootstrap(context.Background(), ticket, worker, "入口提示", workerBootstrapModeFirstBootstrap)
	if err != nil {
		t.Fatalf("ensureWorkerBootstrap failed: %v", err)
	}

	kernelRaw, err := os.ReadFile(paths.AgentKernelMD)
	if err != nil {
		t.Fatalf("read kernel failed: %v", err)
	}
	kernel := string(kernelRaw)
	if !strings.Contains(kernel, "<title>\nbootstrap title\n</title>") {
		t.Fatalf("agent-kernel should come from control worker template, got=%q", kernel)
	}
	stateRaw, err := os.ReadFile(paths.StateJSON)
	if err != nil {
		t.Fatalf("read state failed: %v", err)
	}
	state := string(stateRaw)
	if !strings.Contains(state, "\"head_sha\":") {
		t.Fatalf("state should contain rendered code facts, got=%q", state)
	}
	if !strings.Contains(state, "\"id\": \""+strconv.FormatUint(uint64(ticket.ID), 10)+"\"") {
		t.Fatalf("state should include ticket id, got=%q", state)
	}
	if !strings.Contains(state, "\"worker_id\": \""+strconv.FormatUint(uint64(worker.ID), 10)+"\"") {
		t.Fatalf("state should include worker id, got=%q", state)
	}
	entries, err := os.ReadDir(paths.Dir)
	if err != nil {
		t.Fatalf("read .dalek dir failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("worker bootstrap should only materialize agent-kernel.md and state.json, got=%d", len(entries))
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.Name()] = true
	}
	if !seen["agent-kernel.md"] || !seen["state.json"] {
		t.Fatalf("worker bootstrap output mismatch, got=%v", seen)
	}
}

func TestDetermineWorkerBootstrapMode_UsesWorkerDeliverTicketHistory(t *testing.T) {
	svc, project, _ := newServiceForTest(t)
	ticketWithHistory := createTicket(t, project.DB, "bootstrap-mode-history")
	workerWithHistory := contracts.Worker{
		TicketID:     ticketWithHistory.ID,
		Status:       contracts.WorkerStopped,
		WorktreePath: filepath.Join(t.TempDir(), "worker-with-history"),
		Branch:       "ticket-history",
		LogPath:      filepath.Join(t.TempDir(), "worker-with-history.log"),
	}
	if err := project.DB.Create(&workerWithHistory).Error; err != nil {
		t.Fatalf("create workerWithHistory failed: %v", err)
	}
	createWorkerTaskRun(t, project.DB, ticketWithHistory.ID, workerWithHistory.ID, "bootstrap_mode_worker_1")

	ticketWithoutHistory := createTicket(t, project.DB, "bootstrap-mode-fresh")
	workerWithoutHistory := contracts.Worker{
		TicketID:     ticketWithoutHistory.ID,
		Status:       contracts.WorkerStopped,
		WorktreePath: filepath.Join(t.TempDir(), "worker-without-history"),
		Branch:       "ticket-fresh",
		LogPath:      filepath.Join(t.TempDir(), "worker-without-history.log"),
	}
	if err := project.DB.Create(&workerWithoutHistory).Error; err != nil {
		t.Fatalf("create workerWithoutHistory failed: %v", err)
	}

	mode1, err := svc.determineWorkerBootstrapMode(context.Background(), workerWithHistory.ID)
	if err != nil {
		t.Fatalf("determineWorkerBootstrapMode(workerWithHistory) failed: %v", err)
	}
	if mode1 != workerBootstrapModeRecoveryRepair {
		t.Fatalf("expected workerWithHistory recovery mode, got=%s", mode1)
	}

	mode2, err := svc.determineWorkerBootstrapMode(context.Background(), workerWithoutHistory.ID)
	if err != nil {
		t.Fatalf("determineWorkerBootstrapMode(workerWithoutHistory) failed: %v", err)
	}
	if mode2 != workerBootstrapModeFirstBootstrap {
		t.Fatalf("expected workerWithoutHistory first bootstrap mode, got=%s", mode2)
	}
}

func writeControlWorkerTemplatesForTest(t *testing.T, layout repo.Layout, kernelTemplate, stateTemplate string) {
	t.Helper()
	if err := os.WriteFile(repo.ControlWorkerKernelPath(layout), []byte(strings.TrimSpace(kernelTemplate)+"\n"), 0o644); err != nil {
		t.Fatalf("write custom worker-kernel failed: %v", err)
	}
	if err := os.WriteFile(repo.ControlWorkerStatePath(layout), []byte(strings.TrimSpace(stateTemplate)+"\n"), 0o644); err != nil {
		t.Fatalf("write custom worker state failed: %v", err)
	}
}

func primeWorkerBootstrapFilesForTest(t *testing.T, worktreePath, kernel, state string) repo.ContractPaths {
	t.Helper()
	paths, err := repo.EnsureWorktreeContract(worktreePath)
	if err != nil {
		t.Fatalf("ensure worktree contract failed: %v", err)
	}
	if err := os.WriteFile(paths.AgentKernelMD, []byte(kernel), 0o644); err != nil {
		t.Fatalf("write agent-kernel failed: %v", err)
	}
	if err := os.WriteFile(paths.StateJSON, []byte(state), 0o644); err != nil {
		t.Fatalf("write state.json failed: %v", err)
	}
	return paths
}

func readWorkerBootstrapFilesForTest(t *testing.T, worktreePath string) (string, string) {
	t.Helper()
	paths, err := repo.EnsureWorktreeContract(worktreePath)
	if err != nil {
		t.Fatalf("ensure worktree contract failed: %v", err)
	}
	kernel, err := os.ReadFile(paths.AgentKernelMD)
	if err != nil {
		t.Fatalf("read agent-kernel failed: %v", err)
	}
	state, err := os.ReadFile(paths.StateJSON)
	if err != nil {
		t.Fatalf("read state.json failed: %v", err)
	}
	return string(kernel), string(state)
}

func mustRun(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run %s %v failed: %v output=%s", name, args, err, strings.TrimSpace(string(out)))
	}
}
