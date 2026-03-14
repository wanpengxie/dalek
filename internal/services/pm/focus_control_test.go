package pm

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

func TestCreateIntegrationTicket_SucceedsWithStructuredEvidence(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	source := createTicket(t, p.DB, "integration-source-success")
	prepareIntegrationSourceTicket(t, p.DB, source.ID, contracts.TicketDone, contracts.IntegrationNeedsMerge, "main")

	res, err := svc.CreateIntegrationTicket(ctx, contracts.CreateIntegrationTicketInput{
		SourceTicketIDs:       []uint{source.ID},
		TargetRef:             "refs/heads/main",
		ConflictTargetHeadSHA: "deadbeef",
		SourceAnchorSHAs:      []string{"anchor-a"},
		ConflictFiles:         []string{"internal/focus.go"},
		MergeSummary:          "CONFLICT (content): Merge conflict in internal/focus.go",
		EvidenceRefs: []string{
			"docs/architecture/focus-run-batch-v1-lean-spec.md",
			"docs/architecture/focus-run-batch-v1-remediation-spec.md",
		},
	})
	if err != nil {
		t.Fatalf("CreateIntegrationTicket failed: %v", err)
	}

	var created contracts.Ticket
	if err := p.DB.First(&created, res.TicketID).Error; err != nil {
		t.Fatalf("load created integration ticket failed: %v", err)
	}
	if created.Label != "integration" {
		t.Fatalf("expected integration label, got=%q", created.Label)
	}
	if created.TargetBranch != "refs/heads/main" {
		t.Fatalf("expected normalized target branch, got=%q", created.TargetBranch)
	}
	for _, want := range []string{
		"source_tickets: t",
		"target_ref: refs/heads/main",
		"conflict_target_head_sha: deadbeef",
		"source_anchor_shas: anchor-a",
		"internal/focus.go",
		"docs/architecture/focus-run-batch-v1-lean-spec.md",
		"docs/architecture/focus-run-batch-v1-remediation-spec.md",
	} {
		if !strings.Contains(created.Description, want) {
			t.Fatalf("description should include %q, got:\n%s", want, created.Description)
		}
	}
	if strings.Contains(created.Description, "(none)") || strings.Contains(created.Description, "(unknown)") {
		t.Fatalf("description should not contain weak placeholders, got:\n%s", created.Description)
	}
}

func TestCreateIntegrationTicket_RejectsInvalidSourcesOrWeakEvidence(t *testing.T) {
	testCases := []struct {
		name    string
		prepare func(t *testing.T, db *gorm.DB, ticketID uint)
		mutate  func(in *contracts.CreateIntegrationTicketInput)
		wantErr string
	}{
		{
			name: "source_not_found",
			mutate: func(in *contracts.CreateIntegrationTicketInput) {
				in.SourceTicketIDs = []uint{9999}
			},
			wantErr: "ticket t9999 不存在",
		},
		{
			name: "source_not_done",
			prepare: func(t *testing.T, db *gorm.DB, ticketID uint) {
				prepareIntegrationSourceTicket(t, db, ticketID, contracts.TicketBacklog, contracts.IntegrationNeedsMerge, "refs/heads/main")
			},
			wantErr: "done + needs_merge",
		},
		{
			name: "source_not_needs_merge",
			prepare: func(t *testing.T, db *gorm.DB, ticketID uint) {
				prepareIntegrationSourceTicket(t, db, ticketID, contracts.TicketDone, contracts.IntegrationNone, "refs/heads/main")
			},
			wantErr: "done + needs_merge",
		},
		{
			name: "target_ref_mismatch",
			prepare: func(t *testing.T, db *gorm.DB, ticketID uint) {
				prepareIntegrationSourceTicket(t, db, ticketID, contracts.TicketDone, contracts.IntegrationNeedsMerge, "refs/heads/release")
			},
			wantErr: "target_ref=refs/heads/release 与输入 target_ref=refs/heads/main 不一致",
		},
		{
			name: "missing_conflict_target_head",
			mutate: func(in *contracts.CreateIntegrationTicketInput) {
				in.ConflictTargetHeadSHA = ""
			},
			wantErr: "conflict_target_head_sha 不能为空",
		},
		{
			name: "missing_source_anchor_shas",
			mutate: func(in *contracts.CreateIntegrationTicketInput) {
				in.SourceAnchorSHAs = nil
			},
			wantErr: "source_anchor_shas 不能为空",
		},
		{
			name: "missing_conflict_files",
			mutate: func(in *contracts.CreateIntegrationTicketInput) {
				in.ConflictFiles = nil
			},
			wantErr: "conflict_files 不能为空",
		},
		{
			name: "missing_merge_summary",
			mutate: func(in *contracts.CreateIntegrationTicketInput) {
				in.MergeSummary = ""
			},
			wantErr: "merge_summary 不能为空",
		},
		{
			name: "missing_evidence_refs",
			mutate: func(in *contracts.CreateIntegrationTicketInput) {
				in.EvidenceRefs = nil
			},
			wantErr: "evidence_refs 不能为空",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			svc, p, _ := newServiceForTest(t)
			ctx := context.Background()

			source := createTicket(t, p.DB, "integration-source-failure")
			prepareIntegrationSourceTicket(t, p.DB, source.ID, contracts.TicketDone, contracts.IntegrationNeedsMerge, "refs/heads/main")
			if tc.prepare != nil {
				tc.prepare(t, p.DB, source.ID)
			}

			input := contracts.CreateIntegrationTicketInput{
				SourceTicketIDs:       []uint{source.ID},
				TargetRef:             "refs/heads/main",
				ConflictTargetHeadSHA: "deadbeef",
				SourceAnchorSHAs:      []string{"anchor-a"},
				ConflictFiles:         []string{"internal/focus.go"},
				MergeSummary:          "CONFLICT (content): Merge conflict in internal/focus.go",
				EvidenceRefs:          []string{"docs/architecture/focus-run-batch-v1-remediation-spec.md"},
			}
			if tc.mutate != nil {
				tc.mutate(&input)
			}

			if _, err := svc.CreateIntegrationTicket(ctx, input); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got=%v", tc.wantErr, err)
			}

			var ticketCount int64
			if err := p.DB.Model(&contracts.Ticket{}).Count(&ticketCount).Error; err != nil {
				t.Fatalf("count tickets failed: %v", err)
			}
			if ticketCount != 1 {
				t.Fatalf("expected no integration ticket created on error, got count=%d", ticketCount)
			}
		})
	}
}

func prepareIntegrationSourceTicket(t *testing.T, db *gorm.DB, ticketID uint, workflow contracts.TicketWorkflowStatus, integration contracts.IntegrationStatus, targetRef string) {
	t.Helper()
	if err := db.Model(&contracts.Ticket{}).
		Where("id = ?", ticketID).
		Updates(map[string]any{
			"workflow_status":    workflow,
			"integration_status": integration,
			"target_branch":      targetRef,
			"updated_at":         time.Now(),
		}).Error; err != nil {
		t.Fatalf("prepare integration source ticket failed: %v", err)
	}
}
