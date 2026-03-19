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
	{{TICKET_TITLE}}
	</title>
	<description>
	{{TICKET_DESCRIPTION}}
	</description>
	<attachments>
	- ticket_id: t{{DALEK_TICKET_ID}}
	- worker_id: w{{DALEK_WORKER_ID}}
	- target_ref: {{TARGET_REF}}
	- worktree: {{WORKTREE_PATH}}
	- worker_branch: {{WORKER_BRANCH}}
	</attachments>
	</task_context>
	<current_state>
	placeholder
	</current_state>
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
	    "summary": "bootstrap summary",
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
	if !strings.Contains(kernel, "bootstrap title") {
		t.Fatalf("agent-kernel should come from control worker template, got=%q", kernel)
	}
	if !strings.Contains(kernel, "bootstrap description") {
		t.Fatalf("agent-kernel should render description placeholder, got=%q", kernel)
	}
	if !strings.Contains(kernel, "- ticket_id: t"+strconv.FormatUint(uint64(ticket.ID), 10)) {
		t.Fatalf("agent-kernel should render ticket facts, got=%q", kernel)
	}
	if !strings.Contains(kernel, "- worker_id: w"+strconv.FormatUint(uint64(worker.ID), 10)) {
		t.Fatalf("agent-kernel should render worker facts, got=%q", kernel)
	}
	if !strings.Contains(kernel, "- target_ref: (unset)") {
		t.Fatalf("agent-kernel should render target_ref fallback, got=%q", kernel)
	}
	if strings.Contains(kernel, "{{") {
		t.Fatalf("agent-kernel placeholders should be replaced, got=%q", kernel)
	}
	stateRaw, err := os.ReadFile(paths.StateJSON)
	if err != nil {
		t.Fatalf("read state failed: %v", err)
	}
	state := string(stateRaw)
	if strings.Contains(state, "{{") {
		t.Fatalf("state placeholders should be replaced, got=%q", state)
	}
	if !strings.Contains(state, "\"head_sha\":") {
		t.Fatalf("state should contain rendered code facts, got=%q", state)
	}
	if !strings.Contains(state, "\"id\": \""+strconv.FormatUint(uint64(ticket.ID), 10)+"\"") {
		t.Fatalf("state should include ticket id, got=%q", state)
	}
	if !strings.Contains(state, "\"worker_id\": \""+strconv.FormatUint(uint64(worker.ID), 10)+"\"") {
		t.Fatalf("state should include worker id, got=%q", state)
	}
	if !strings.Contains(state, "\"summary\": \"bootstrap summary\"") {
		t.Fatalf("state should preserve control template summary, got=%q", state)
	}
	entries, err := os.ReadDir(paths.Dir)
	if err != nil {
		t.Fatalf("read .dalek dir failed: %v", err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.Name()] = true
	}
	if !seen["agent-kernel.md"] || !seen["state.json"] {
		t.Fatalf("worker bootstrap must contain agent-kernel.md and state.json, got=%v", seen)
	}
	// control/ is copied from repo root during bootstrap
	if !seen["control"] {
		t.Fatalf("worker bootstrap should copy control/ from repo root, got=%v", seen)
	}
}

func TestEnsureWorkerBootstrap_CopiesControlAndPMToWorktree(t *testing.T) {
	svc, project, _ := newServiceForTest(t)

	worktree := filepath.Join(t.TempDir(), "worker-worktree-copy")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatalf("mkdir worktree failed: %v", err)
	}
	mustRun(t, worktree, "git", "init")
	mustRun(t, worktree, "git", "config", "user.name", "Test User")
	mustRun(t, worktree, "git", "config", "user.email", "test@example.com")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, worktree, "git", "add", "README.md")
	mustRun(t, worktree, "git", "commit", "-m", "initial commit")

	// Create pm/ in repo root with a plan.md
	pmDir := project.Layout.PMDir
	if err := os.MkdirAll(pmDir, 0o755); err != nil {
		t.Fatalf("mkdir pm dir failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pmDir, "plan.md"), []byte("# Plan\ntest plan"), 0o644); err != nil {
		t.Fatalf("write plan.md failed: %v", err)
	}

	// Ensure control/ has content (already created by EnsureControlPlaneSeed in NewTestProject)
	controlSkillFile := filepath.Join(project.Layout.ControlSkillsDir, "test-skill.md")
	if err := os.WriteFile(controlSkillFile, []byte("# Test Skill"), 0o644); err != nil {
		t.Fatalf("write test skill failed: %v", err)
	}

	ticket := contracts.Ticket{
		Title:          "copy test",
		Description:    "test description",
		WorkflowStatus: contracts.TicketBacklog,
	}
	if err := project.DB.Create(&ticket).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	worker := contracts.Worker{
		TicketID:     ticket.ID,
		Status:       contracts.WorkerStopped,
		WorktreePath: worktree,
		Branch:       "ticket-copy-test",
		LogPath:      filepath.Join(worktree, "worker.log"),
	}
	if err := project.DB.Create(&worker).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}

	paths, err := svc.ensureWorkerBootstrap(context.Background(), ticket, worker, "", workerBootstrapModeFirstBootstrap)
	if err != nil {
		t.Fatalf("ensureWorkerBootstrap failed: %v", err)
	}

	// Verify control/ was copied
	wtControlDir := filepath.Join(paths.Dir, "control")
	if _, err := os.Stat(wtControlDir); err != nil {
		t.Fatalf("control/ should exist in worktree .dalek/: %v", err)
	}
	wtSkillFile := filepath.Join(wtControlDir, "skills", "test-skill.md")
	raw, err := os.ReadFile(wtSkillFile)
	if err != nil {
		t.Fatalf("read copied skill file failed: %v", err)
	}
	if string(raw) != "# Test Skill" {
		t.Fatalf("copied skill content mismatch: got=%q", string(raw))
	}

	// Verify pm/ was copied
	wtPMDir := filepath.Join(paths.Dir, "pm")
	if _, err := os.Stat(wtPMDir); err != nil {
		t.Fatalf("pm/ should exist in worktree .dalek/: %v", err)
	}
	planRaw, err := os.ReadFile(filepath.Join(wtPMDir, "plan.md"))
	if err != nil {
		t.Fatalf("read copied plan.md failed: %v", err)
	}
	if string(planRaw) != "# Plan\ntest plan" {
		t.Fatalf("copied plan content mismatch: got=%q", string(planRaw))
	}
}

func TestCopyControlAndPMToWorktree_MissingSrcSilent(t *testing.T) {
	// When control/ and pm/ don't exist in repo root, no error
	layout := repo.NewLayout(filepath.Join(t.TempDir(), "empty-repo"))
	dst := filepath.Join(t.TempDir(), "wt-dalek")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyControlAndPMToWorktree(layout, dst); err != nil {
		t.Fatalf("expected no error for missing src dirs, got: %v", err)
	}

	// Neither control/ nor pm/ should exist in dst
	if _, err := os.Stat(filepath.Join(dst, "control")); !os.IsNotExist(err) {
		t.Fatalf("control/ should not be created when src is missing")
	}
	if _, err := os.Stat(filepath.Join(dst, "pm")); !os.IsNotExist(err) {
		t.Fatalf("pm/ should not be created when src is missing")
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
