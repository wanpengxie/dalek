package pm

import (
	"context"
	"dalek/internal/contracts"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStartTicket_PushesWorkflowAndMarksWorkerRunning(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "bootstrap-run")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if w.Status != contracts.WorkerRunning {
		t.Fatalf("expected worker status running, got=%s", w.Status)
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
		t.Fatalf("start 应推进 workflow_status backlog->queued，got=%s", got.WorkflowStatus)
	}

	var ev contracts.TicketWorkflowEvent
	if err := p.DB.Where("ticket_id = ?", tk.ID).Order("id desc").First(&ev).Error; err != nil {
		t.Fatalf("query ticket workflow event failed: %v", err)
	}
	if ev.FromStatus != contracts.TicketBacklog || ev.ToStatus != contracts.TicketQueued {
		t.Fatalf("unexpected workflow event transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
}

func TestStartTicketWithOptions_PassesBaseBranch(t *testing.T) {
	svc, p, fGit := newServiceForTest(t)

	tk := createTicket(t, p.DB, "start-options-base")
	if _, err := svc.StartTicketWithOptions(context.Background(), tk.ID, StartOptions{
		BaseBranch: "release/v2",
	}); err != nil {
		t.Fatalf("StartTicketWithOptions failed: %v", err)
	}
	if got := strings.TrimSpace(fGit.LastBaseBranch); got != "release/v2" {
		t.Fatalf("expected base branch override release/v2, got=%q", got)
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
