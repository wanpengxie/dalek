package pm

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestAbandonTicketIntegration_UpdatesStatusAndClosesApprovalInbox(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "integration-abandon")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"target_branch":      "main",
		"merge_anchor_sha":   "abc1234",
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare ticket integration state failed: %v", err)
	}
	if err := p.DB.Create(&contracts.InboxItem{
		Key:      "approval:test",
		Status:   contracts.InboxOpen,
		Severity: contracts.InboxWarn,
		Reason:   contracts.InboxApprovalRequired,
		Title:    "待审批",
		TicketID: tk.ID,
	}).Error; err != nil {
		t.Fatalf("create approval inbox failed: %v", err)
	}

	if err := svc.AbandonTicketIntegration(context.Background(), tk.ID, "需求变更"); err != nil {
		t.Fatalf("AbandonTicketIntegration failed: %v", err)
	}

	var got contracts.Ticket
	if err := p.DB.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("reload ticket failed: %v", err)
	}
	if status := contracts.CanonicalIntegrationStatus(got.IntegrationStatus); status != contracts.IntegrationAbandoned {
		t.Fatalf("expected integration_status abandoned, got=%s", status)
	}
	if strings.TrimSpace(got.AbandonedReason) != "需求变更" {
		t.Fatalf("unexpected abandoned reason: %q", got.AbandonedReason)
	}

	var cnt int64
	if err := p.DB.Model(&contracts.InboxItem{}).
		Where("ticket_id = ? AND reason = ? AND status = ?", tk.ID, contracts.InboxApprovalRequired, contracts.InboxOpen).
		Count(&cnt).Error; err != nil {
		t.Fatalf("count open approval inbox failed: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("expected open approval inbox closed, got=%d", cnt)
	}
}

func TestProposeMergesForDoneTickets_ObservesMergedByGitAncestor(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	branch, head := initGitRepoForIntegrationObserveTest(t, p.RepoRoot)

	tk := createTicket(t, p.DB, "integration-observe-merged")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"merge_anchor_sha":   head,
		"target_branch":      branch,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare ticket integration state failed: %v", err)
	}

	st, err := svc.getOrInitPMState(context.Background())
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	out := svc.proposeMergesForDoneTickets(context.Background(), p.DB, st, false)
	if len(out.Errors) != 0 {
		t.Fatalf("expected no observe errors, got=%v", out.Errors)
	}

	var got contracts.Ticket
	if err := p.DB.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("reload ticket failed: %v", err)
	}
	if status := contracts.CanonicalIntegrationStatus(got.IntegrationStatus); status != contracts.IntegrationMerged {
		t.Fatalf("expected integration_status merged, got=%s", status)
	}
	if got.MergedAt == nil || got.MergedAt.IsZero() {
		t.Fatalf("expected merged_at populated")
	}
}

func initGitRepoForIntegrationObserveTest(t *testing.T, repoRoot string) (branch string, head string) {
	t.Helper()
	mustRunGit(t, repoRoot, "init")
	mustRunGit(t, repoRoot, "config", "user.email", "test@example.com")
	mustRunGit(t, repoRoot, "config", "user.name", "Test User")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("integration observe test\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	mustRunGit(t, repoRoot, "add", "README.md")
	mustRunGit(t, repoRoot, "commit", "-m", "init")
	branch = strings.TrimSpace(mustRunGit(t, repoRoot, "branch", "--show-current"))
	head = strings.TrimSpace(mustRunGit(t, repoRoot, "rev-parse", "HEAD"))
	if branch == "" || head == "" {
		t.Fatalf("expected git branch/head populated, got branch=%q head=%q", branch, head)
	}
	return branch, head
}

func mustRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}
