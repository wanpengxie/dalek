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

func TestFreezeMergesForDoneTickets_ObservesMergedByGitAncestor(t *testing.T) {
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
	out := svc.freezeMergesForDoneTickets(context.Background(), p.DB, st, false)
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

func TestSyncRef_MergesMatchingNeedsMergeTickets(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	branch, head := initGitRepoForIntegrationObserveTest(t, p.RepoRoot)
	targetRef := "refs/heads/" + branch
	wakeCalls := 0
	svc.SetProjectWakeHook(func() {
		wakeCalls++
	})

	okTicket := createTicket(t, p.DB, "sync-ref-ok")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", okTicket.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"merge_anchor_sha":   head,
		"target_branch":      targetRef,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare ok ticket failed: %v", err)
	}
	badAnchorTicket := createTicket(t, p.DB, "sync-ref-bad-anchor")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", badAnchorTicket.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"merge_anchor_sha":   "not-a-commit",
		"target_branch":      targetRef,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare bad anchor ticket failed: %v", err)
	}

	res, err := svc.SyncRef(context.Background(), targetRef, strings.Repeat("0", 40), head)
	if err != nil {
		t.Fatalf("SyncRef failed: %v", err)
	}
	if res.CandidateTickets != 2 {
		t.Fatalf("expected 2 candidate tickets, got=%d", res.CandidateTickets)
	}
	if len(res.MergedTicketIDs) != 1 || res.MergedTicketIDs[0] != okTicket.ID {
		t.Fatalf("unexpected merged tickets: %v", res.MergedTicketIDs)
	}
	if len(res.Errors) == 0 {
		t.Fatalf("expected bad anchor error to be recorded")
	}
	if wakeCalls != 1 {
		t.Fatalf("expected SyncRef to wake project once after merged ticket, got=%d", wakeCalls)
	}

	var got contracts.Ticket
	if err := p.DB.First(&got, okTicket.ID).Error; err != nil {
		t.Fatalf("reload merged ticket failed: %v", err)
	}
	if contracts.CanonicalIntegrationStatus(got.IntegrationStatus) != contracts.IntegrationMerged {
		t.Fatalf("expected merged ticket status, got=%s", got.IntegrationStatus)
	}
}

func TestFinalizeTicketSuperseded_RequiresMergedReplacementAndSetsMapping(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	source := createTicket(t, p.DB, "finalize-superseded-source")
	replacement := createTicket(t, p.DB, "finalize-superseded-replacement")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", source.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare source ticket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", replacement.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare replacement ticket failed: %v", err)
	}

	if err := svc.FinalizeTicketSuperseded(ctx, source.ID, replacement.ID, ""); err == nil {
		t.Fatalf("expected finalize to reject non-merged replacement")
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", replacement.ID).Updates(map[string]any{
		"integration_status": contracts.IntegrationMerged,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("mark replacement merged failed: %v", err)
	}

	if err := svc.FinalizeTicketSuperseded(ctx, source.ID, replacement.ID, ""); err != nil {
		t.Fatalf("FinalizeTicketSuperseded failed: %v", err)
	}
	if err := svc.FinalizeTicketSuperseded(ctx, source.ID, replacement.ID, ""); err != nil {
		t.Fatalf("FinalizeTicketSuperseded second call should stay idempotent: %v", err)
	}

	var sourceAfter contracts.Ticket
	if err := p.DB.First(&sourceAfter, source.ID).Error; err != nil {
		t.Fatalf("reload source ticket failed: %v", err)
	}
	if contracts.CanonicalIntegrationStatus(sourceAfter.IntegrationStatus) != contracts.IntegrationAbandoned {
		t.Fatalf("expected source integration_status abandoned, got=%s", sourceAfter.IntegrationStatus)
	}
	if sourceAfter.SupersededByTicketID == nil || *sourceAfter.SupersededByTicketID != replacement.ID {
		t.Fatalf("expected superseded_by=%d, got=%v", replacement.ID, sourceAfter.SupersededByTicketID)
	}
	if !strings.Contains(sourceAfter.AbandonedReason, "integration ticket") {
		t.Fatalf("expected default abandoned reason written, got=%q", sourceAfter.AbandonedReason)
	}

	var lifecycleCount int64
	if err := p.DB.Model(&contracts.TicketLifecycleEvent{}).
		Where("ticket_id = ? AND event_type = ?", source.ID, contracts.TicketLifecycleMergeAbandoned).
		Count(&lifecycleCount).Error; err != nil {
		t.Fatalf("count merge_abandoned lifecycle failed: %v", err)
	}
	if lifecycleCount != 1 {
		t.Fatalf("expected one merge_abandoned lifecycle event, got=%d", lifecycleCount)
	}
}

func TestRetargetTicketIntegration_OnlyNeedsMergeAllowed(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "retarget-needs-merge")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"target_branch":      "refs/heads/main",
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare ticket failed: %v", err)
	}

	res, err := svc.RetargetTicketIntegration(context.Background(), tk.ID, "release/v1")
	if err != nil {
		t.Fatalf("RetargetTicketIntegration failed: %v", err)
	}
	if res.TargetRef != "refs/heads/release/v1" {
		t.Fatalf("unexpected target ref: %q", res.TargetRef)
	}

	var got contracts.Ticket
	if err := p.DB.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("reload retarget ticket failed: %v", err)
	}
	if strings.TrimSpace(got.TargetBranch) != "refs/heads/release/v1" {
		t.Fatalf("expected target_branch updated, got=%q", got.TargetBranch)
	}

	mergedTicket := createTicket(t, p.DB, "retarget-merged")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", mergedTicket.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationMerged,
		"target_branch":      "refs/heads/main",
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare merged ticket failed: %v", err)
	}
	if _, err := svc.RetargetTicketIntegration(context.Background(), mergedTicket.ID, "refs/heads/release/v2"); err == nil {
		t.Fatalf("retarget on merged ticket should fail")
	}
}

func TestRescanMergeStatus_ResolvesRefAndMerges(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	branch, head := initGitRepoForIntegrationObserveTest(t, p.RepoRoot)
	targetRef := "refs/heads/" + branch

	tk := createTicket(t, p.DB, "rescan-ticket")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"merge_anchor_sha":   head,
		"target_branch":      targetRef,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare rescan ticket failed: %v", err)
	}

	out, err := svc.RescanMergeStatus(context.Background(), targetRef)
	if err != nil {
		t.Fatalf("RescanMergeStatus failed: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("expected one rescan result, got=%d", len(out.Results))
	}
	if len(out.Results[0].MergedTicketIDs) != 1 || out.Results[0].MergedTicketIDs[0] != tk.ID {
		t.Fatalf("unexpected merged tickets from rescan: %+v", out.Results[0])
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
