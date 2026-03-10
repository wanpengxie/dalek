package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"
)

type mergeTicketSeedOptions struct {
	anchor            string
	target            string
	abandonedReason   string
	markMerged        bool
	createApprovalBox bool
}

func openDemoProjectForMergeE2E(t *testing.T, homeDir string) *app.Project {
	t.Helper()
	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	p, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	t.Cleanup(func() {
		_ = p.Close()
	})
	return p
}

func seedMergeTicketForE2E(t *testing.T, p *app.Project, title string, status contracts.IntegrationStatus, opt mergeTicketSeedOptions) *contracts.Ticket {
	t.Helper()
	ctx := context.Background()
	tk, err := p.CreateTicketWithDescription(ctx, title, title+" description")
	if err != nil {
		t.Fatalf("CreateTicketWithDescription failed: %v", err)
	}
	if err := p.SetTicketWorkflowStatus(ctx, tk.ID, contracts.TicketDone); err != nil {
		t.Fatalf("SetTicketWorkflowStatus(done) failed: %v", err)
	}
	db, err := p.OpenDBForTest()
	if err != nil {
		t.Fatalf("OpenDBForTest failed: %v", err)
	}
	now := time.Now()
	updates := map[string]any{
		"integration_status": status,
		"merge_anchor_sha":   strings.TrimSpace(opt.anchor),
		"target_branch":      strings.TrimSpace(opt.target),
		"abandoned_reason":   strings.TrimSpace(opt.abandonedReason),
		"updated_at":         now,
	}
	if opt.markMerged {
		updates["merged_at"] = &now
	}
	if err := db.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(updates).Error; err != nil {
		t.Fatalf("seed merge state failed: %v", err)
	}
	if opt.createApprovalBox {
		if err := db.Create(&contracts.InboxItem{
			Key:      fmt.Sprintf("approval:e2e:%d", tk.ID),
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxWarn,
			Reason:   contracts.InboxApprovalRequired,
			Title:    "待审批",
			TicketID: tk.ID,
		}).Error; err != nil {
			t.Fatalf("create approval inbox failed: %v", err)
		}
	}
	return tk
}

func TestCLI_MergeListAndStatus_E2E(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")

	p := openDemoProjectForMergeE2E(t, home)
	merged := seedMergeTicketForE2E(t, p, "merged ticket", contracts.IntegrationMerged, mergeTicketSeedOptions{
		anchor:     "merged123",
		target:     "main",
		markMerged: true,
	})
	needsMerge := seedMergeTicketForE2E(t, p, "needs merge ticket", contracts.IntegrationNeedsMerge, mergeTicketSeedOptions{
		anchor: "needs123",
		target: "main",
	})
	abandoned := seedMergeTicketForE2E(t, p, "abandoned ticket", contracts.IntegrationAbandoned, mergeTicketSeedOptions{
		abandonedReason: "需求变更",
	})

	out, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "merge", "ls")
	if !strings.Contains(out, fmt.Sprintf("t%d  workflow=done  merge=merged  anchor=merged123  target=main", merged.ID)) {
		t.Fatalf("merge ls should list merged ticket, got:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("t%d  workflow=done  merge=needs_merge  anchor=needs123  target=main", needsMerge.ID)) {
		t.Fatalf("merge ls should list needs_merge ticket, got:\n%s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("t%d  workflow=done  merge=abandoned", abandoned.ID)) {
		t.Fatalf("merge ls should list abandoned ticket, got:\n%s", out)
	}

	out, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "merge", "ls", "--status", "needs_merge")
	if !strings.Contains(out, fmt.Sprintf("t%d  workflow=done  merge=needs_merge", needsMerge.ID)) {
		t.Fatalf("merge ls --status needs_merge should keep target ticket, got:\n%s", out)
	}
	if strings.Contains(out, fmt.Sprintf("t%d  workflow=done  merge=merged", merged.ID)) {
		t.Fatalf("merge ls --status needs_merge should filter merged ticket, got:\n%s", out)
	}
	if strings.Contains(out, fmt.Sprintf("t%d  workflow=done  merge=abandoned", abandoned.ID)) {
		t.Fatalf("merge ls --status needs_merge should filter abandoned ticket, got:\n%s", out)
	}

	statusOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"merge", "status",
		"--ticket", strconv.Itoa(int(needsMerge.ID)),
		"-o", "json",
	)
	var payload struct {
		Schema            string `json:"schema"`
		TicketID          uint   `json:"ticket_id"`
		WorkflowStatus    string `json:"workflow_status"`
		IntegrationStatus string `json:"integration_status"`
		MergeAnchorSHA    string `json:"merge_anchor_sha"`
		TargetBranch      string `json:"target_branch"`
		MergedAt          string `json:"merged_at"`
		AbandonedReason   string `json:"abandoned_reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(statusOut)), &payload); err != nil {
		t.Fatalf("decode merge status json failed: %v\nraw=%s", err, statusOut)
	}
	if payload.Schema != "dalek.merge.status.v1" {
		t.Fatalf("unexpected schema: %q", payload.Schema)
	}
	if payload.TicketID != needsMerge.ID {
		t.Fatalf("unexpected ticket_id: %d", payload.TicketID)
	}
	if payload.WorkflowStatus != "done" || payload.IntegrationStatus != "needs_merge" {
		t.Fatalf("unexpected status payload: %+v", payload)
	}
	if payload.MergeAnchorSHA != "needs123" || payload.TargetBranch != "main" {
		t.Fatalf("unexpected merge detail payload: %+v", payload)
	}
	if payload.MergedAt != "" || payload.AbandonedReason != "" {
		t.Fatalf("unexpected optional merge fields: %+v", payload)
	}
}

func TestCLI_MergeAbandon_E2E(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")

	p := openDemoProjectForMergeE2E(t, home)
	tk := seedMergeTicketForE2E(t, p, "abandon target", contracts.IntegrationNeedsMerge, mergeTicketSeedOptions{
		anchor:            "needs-abandon",
		target:            "main",
		createApprovalBox: true,
	})

	out, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"merge", "abandon",
		"--ticket", strconv.Itoa(int(tk.ID)),
		"--reason", "需求变更",
		"-o", "json",
	)
	var payload struct {
		Schema            string `json:"schema"`
		TicketID          uint   `json:"ticket_id"`
		IntegrationStatus string `json:"integration_status"`
		AbandonedReason   string `json:"abandoned_reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
		t.Fatalf("decode merge abandon json failed: %v\nraw=%s", err, out)
	}
	if payload.Schema != "dalek.merge.abandon.v1" {
		t.Fatalf("unexpected schema: %q", payload.Schema)
	}
	if payload.TicketID != tk.ID || payload.IntegrationStatus != "abandoned" || payload.AbandonedReason != "需求变更" {
		t.Fatalf("unexpected abandon payload: %+v", payload)
	}

	db, err := p.OpenDBForTest()
	if err != nil {
		t.Fatalf("OpenDBForTest failed: %v", err)
	}
	var got contracts.Ticket
	if err := db.First(&got, tk.ID).Error; err != nil {
		t.Fatalf("reload ticket failed: %v", err)
	}
	if status := contracts.CanonicalIntegrationStatus(got.IntegrationStatus); status != contracts.IntegrationAbandoned {
		t.Fatalf("expected integration_status abandoned, got=%s", status)
	}
	if strings.TrimSpace(got.AbandonedReason) != "需求变更" {
		t.Fatalf("unexpected abandoned reason: %q", got.AbandonedReason)
	}

	var cnt int64
	if err := db.Model(&contracts.InboxItem{}).
		Where("ticket_id = ? AND reason = ? AND status = ?", tk.ID, contracts.InboxApprovalRequired, contracts.InboxOpen).
		Count(&cnt).Error; err != nil {
		t.Fatalf("count open approval inbox failed: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("expected approval inbox closed after abandon, got=%d", cnt)
	}
}
