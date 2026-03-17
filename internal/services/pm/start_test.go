package pm

import (
	"context"
	"dalek/internal/contracts"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStartTicket_PushesWorkflowQueuedAndLeavesWorkerStopped(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "bootstrap-run")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if w.Status != contracts.WorkerStopped {
		t.Fatalf("expected worker status stopped, got=%s", w.Status)
	}
	if strings.TrimSpace(w.LogPath) == "" {
		t.Fatalf("expected runtime log path")
	}
	if _, err := os.Stat(filepath.Join(w.WorktreePath, ".dalek")); err != nil {
		t.Fatalf("expected .dalek dir exists: %v", err)
	}
	var got contracts.Ticket
	if err := p.DB.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if got.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("start 应推进 workflow_status 到 queued，got=%s", got.WorkflowStatus)
	}
	if strings.TrimSpace(got.TargetBranch) != "refs/heads/main" {
		t.Fatalf("start 应冻结 target_branch，got=%q", got.TargetBranch)
	}

	var ev contracts.TicketWorkflowEvent
	if err := p.DB.Where("ticket_id = ?", tk.ID).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("query ticket workflow event failed: %v", err)
	}
	if ev.FromStatus != contracts.TicketBacklog || ev.ToStatus != contracts.TicketQueued {
		t.Fatalf("unexpected workflow event transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}

	var lifecycle contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleStartRequested).Order("sequence desc").First(&lifecycle).Error; err != nil {
		t.Fatalf("query ticket lifecycle event failed: %v", err)
	}
	if lifecycle.Sequence == 0 {
		t.Fatalf("expected lifecycle sequence > 0")
	}
}

func TestStartTicketWithOptions_PassesBaseBranch(t *testing.T) {
	svc, p, fGit := newServiceForTest(t)

	tk := createTicket(t, p.DB, "start-options-base")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"target_branch": "refs/heads/main",
		"updated_at":    time.Now(),
	}).Error; err != nil {
		t.Fatalf("freeze target_branch failed: %v", err)
	}
	if _, err := svc.StartTicketWithOptions(context.Background(), tk.ID, StartOptions{
		BaseBranch: "release/v2",
	}); err == nil || !strings.Contains(err.Error(), "与 ticket.target_ref=refs/heads/main 不一致") {
		t.Fatalf("expected target_ref mismatch error, got=%v", err)
	}
	if fGit.AddCalls != 0 {
		t.Fatalf("worktree should not be created on mismatch, got add_calls=%d", fGit.AddCalls)
	}
}

func TestStartTicket_LegacyEmptyTargetRejectedOnDetachedHead(t *testing.T) {
	svc, p, fGit := newServiceForTest(t)
	fGit.CurrentBranchValue = ""
	fGit.CurrentBranchErr = errors.New("detached HEAD")

	tk := createTicket(t, p.DB, "start-legacy-detached")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err == nil || !strings.Contains(err.Error(), "当前仓库不在明确分支上") {
		t.Fatalf("expected detached head rejection, got=%v", err)
	}
	if fGit.AddCalls != 0 {
		t.Fatalf("worktree should not be created on detached HEAD, got add_calls=%d", fGit.AddCalls)
	}
}

func TestStartTicket_RunsBootstrapScript(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	bootstrapPath := p.Layout.ProjectBootstrapPath
	bootstrapScript := `#!/usr/bin/env bash
set -euo pipefail
echo "bootstrap" > "${DALEK_WORKTREE_PATH}/.dalek/bootstrap-ran.txt"
`
	if err := os.WriteFile(bootstrapPath, []byte(bootstrapScript), 0o755); err != nil {
		t.Fatalf("write bootstrap script failed: %v", err)
	}
	_ = os.Chmod(bootstrapPath, 0o755)

	tk := createTicket(t, p.DB, "bootstrap-exec")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	markerPath := filepath.Join(w.WorktreePath, ".dalek", "bootstrap-ran.txt")
	b, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatalf("bootstrap marker missing: %v", err)
	}
	if strings.TrimSpace(string(b)) != "bootstrap" {
		t.Fatalf("unexpected bootstrap marker content: %q", string(b))
	}
}

func TestStartTicket_BlockedPromotesWorkflowQueued(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "start-blocked-ticket")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketBlocked,
		"updated_at":      tk.UpdatedAt.Add(time.Second),
	}).Error; err != nil {
		t.Fatalf("set ticket blocked failed: %v", err)
	}
	inbox := contracts.InboxItem{
		Key:              inboxKeyNeedsUserChain(tk.ID, 41),
		Status:           contracts.InboxOpen,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "需要补充信息后继续执行",
		Body:             "请补充 /tmp/context.md",
		TicketID:         tk.ID,
		OriginTaskRunID:  41,
		CurrentTaskRunID: 41,
		WaitRoundCount:   1,
	}
	if err := p.DB.Create(&inbox).Error; err != nil {
		t.Fatalf("create needs_user inbox failed: %v", err)
	}

	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if w.Status != contracts.WorkerStopped {
		t.Fatalf("expected worker stopped, got=%s", w.Status)
	}

	var got contracts.Ticket
	if err := p.DB.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if got.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("start should promote blocked ticket to queued, got=%s", got.WorkflowStatus)
	}

	var ev contracts.TicketWorkflowEvent
	if err := p.DB.Where("ticket_id = ?", tk.ID).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("query workflow event failed: %v", err)
	}
	if ev.FromStatus != contracts.TicketBlocked || ev.ToStatus != contracts.TicketQueued {
		t.Fatalf("unexpected workflow event transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}

	var lifecycle contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleStartRequested).Order("sequence desc").First(&lifecycle).Error; err != nil {
		t.Fatalf("query lifecycle event failed: %v", err)
	}

	var inboxAfter contracts.InboxItem
	if err := p.DB.First(&inboxAfter, inbox.ID).Error; err != nil {
		t.Fatalf("reload inbox failed: %v", err)
	}
	if inboxAfter.Status != contracts.InboxDone {
		t.Fatalf("expected blocked exit resolves needs_user inbox, got=%s", inboxAfter.Status)
	}
	if inboxAfter.ChainResolvedAt != nil {
		t.Fatalf("expected blocked exit only closes current notification, got chain_resolved_at=%v", inboxAfter.ChainResolvedAt)
	}
}
