package worker

import (
	"context"
	"dalek/internal/contracts"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupTicketWorktree_DryRunThenClean(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "cleanup-ticket")

	worktreePath := filepath.Join(t.TempDir(), "ticket-worktree")
	if err := os.MkdirAll(worktreePath, 0o755); err != nil {
		t.Fatalf("mkdir worktree failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktreePath, ".git"), []byte("gitdir: /tmp/fake\n"), 0o644); err != nil {
		t.Fatalf("write .git failed: %v", err)
	}

	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerStopped,
		WorktreePath: worktreePath,
		Branch:       "ts/demo/t1",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketArchived).Error; err != nil {
		t.Fatalf("archive ticket failed: %v", err)
	}

	dry, err := svc.CleanupTicketWorktree(context.Background(), tk.ID, CleanupWorktreeOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run cleanup failed: %v", err)
	}
	if !dry.Pending || dry.Cleaned {
		t.Fatalf("unexpected dry-run result: %+v", dry)
	}
	if dry.RequestedAt == nil {
		t.Fatalf("expected requested_at set in dry-run")
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("worktree should remain after dry-run: %v", err)
	}

	got, err := svc.WorkerByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("WorkerByID failed: %v", err)
	}
	if got.WorktreeGCRequestedAt == nil || got.WorktreeGCCleanedAt != nil {
		t.Fatalf("unexpected cleanup state after dry-run: requested=%v cleaned=%v", got.WorktreeGCRequestedAt, got.WorktreeGCCleanedAt)
	}

	done, err := svc.CleanupTicketWorktree(context.Background(), tk.ID, CleanupWorktreeOptions{})
	if err != nil {
		t.Fatalf("cleanup failed: %v", err)
	}
	if !done.Cleaned || done.Pending {
		t.Fatalf("unexpected cleanup result: %+v", done)
	}
	if _, err := os.Stat(worktreePath); !os.IsNotExist(err) {
		t.Fatalf("worktree should be removed, stat err=%v", err)
	}

	got, err = svc.WorkerByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("WorkerByID after cleanup failed: %v", err)
	}
	if got.WorktreeGCCleanedAt == nil {
		t.Fatalf("expected cleaned_at set after cleanup")
	}
}

func TestCleanupTicketWorktree_RejectsNonArchived(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "cleanup-reject-ticket")

	w := contracts.Worker{
		TicketID:     tk.ID,
		Status:       contracts.WorkerStopped,
		WorktreePath: filepath.Join(t.TempDir(), "wt"),
		Branch:       "ts/demo/t2",
	}
	if err := p.DB.Create(&w).Error; err != nil {
		t.Fatalf("create worker failed: %v", err)
	}
	if _, err := svc.CleanupTicketWorktree(context.Background(), tk.ID, CleanupWorktreeOptions{}); err == nil {
		t.Fatalf("expected cleanup reject when ticket not archived")
	}
}
