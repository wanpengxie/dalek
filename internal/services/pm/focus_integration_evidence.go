package pm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

var defaultFocusIntegrationEvidenceRefs = []string{
	"docs/architecture/focus-run-batch-v1-lean-spec.md",
	"docs/architecture/focus-run-batch-v1-remediation-spec.md",
}

func (s *Service) focusConflictEvidenceRefs() []string {
	candidates := []string{
		".dalek/pm/plan.md",
		".dalek/pm/plan.json",
		".dalek/pm/acceptance.md",
	}
	refs := make([]string, 0, len(candidates))
	if s != nil && s.p != nil {
		repoRoot := strings.TrimSpace(s.p.RepoRoot)
		if repoRoot != "" {
			for _, relPath := range candidates {
				absPath := filepath.Join(repoRoot, filepath.FromSlash(relPath))
				info, err := os.Stat(absPath)
				if err != nil || info.IsDir() {
					continue
				}
				refs = append(refs, relPath)
			}
		}
	}
	return normalizeEvidenceRefs(refs, defaultFocusIntegrationEvidenceRefs)
}

func normalizeEvidenceRefs(groups ...[]string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0)
	for _, group := range groups {
		for _, ref := range group {
			ref = strings.TrimSpace(ref)
			if ref == "" {
				continue
			}
			if _, ok := seen[ref]; ok {
				continue
			}
			seen[ref] = struct{}{}
			out = append(out, ref)
		}
	}
	return out
}

func normalizeCreateIntegrationTicketInput(in contracts.CreateIntegrationTicketInput) (contracts.CreateIntegrationTicketInput, error) {
	out := contracts.CreateIntegrationTicketInput{
		SourceTicketIDs:       normalizeFocusTicketIDs(in.SourceTicketIDs),
		ConflictTargetHeadSHA: strings.TrimSpace(in.ConflictTargetHeadSHA),
		SourceAnchorSHAs:      trimNonEmptyStrings(in.SourceAnchorSHAs),
		ConflictFiles:         trimNonEmptyStrings(in.ConflictFiles),
		MergeSummary:          strings.TrimSpace(in.MergeSummary),
		EvidenceRefs:          normalizeEvidenceRefs(trimNonEmptyStrings(in.EvidenceRefs)),
	}
	if len(out.SourceTicketIDs) == 0 {
		return contracts.CreateIntegrationTicketInput{}, fmt.Errorf("source tickets 不能为空")
	}
	targetRef, err := normalizeIntegrationTargetRefInput(in.TargetRef)
	if err != nil {
		return contracts.CreateIntegrationTicketInput{}, err
	}
	out.TargetRef = targetRef
	return out, nil
}

func validateIntegrationTicketEvidence(in contracts.CreateIntegrationTicketInput) error {
	switch {
	case strings.TrimSpace(in.ConflictTargetHeadSHA) == "":
		return fmt.Errorf("conflict_target_head_sha 不能为空")
	case len(trimNonEmptyStrings(in.SourceAnchorSHAs)) == 0:
		return fmt.Errorf("source_anchor_shas 不能为空")
	case len(trimNonEmptyStrings(in.ConflictFiles)) == 0:
		return fmt.Errorf("conflict_files 不能为空")
	case strings.TrimSpace(in.MergeSummary) == "":
		return fmt.Errorf("merge_summary 不能为空")
	case len(trimNonEmptyStrings(in.EvidenceRefs)) == 0:
		return fmt.Errorf("evidence_refs 不能为空")
	default:
		return nil
	}
}

func loadIntegrationSourceTickets(ctx context.Context, db *gorm.DB, ticketIDs []uint) ([]contracts.Ticket, error) {
	if len(ticketIDs) == 0 {
		return nil, fmt.Errorf("source tickets 不能为空")
	}
	var rows []contracts.Ticket
	if err := db.WithContext(ctx).
		Model(&contracts.Ticket{}).
		Where("id IN ?", ticketIDs).
		Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]contracts.Ticket, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	ordered := make([]contracts.Ticket, 0, len(ticketIDs))
	for _, id := range ticketIDs {
		ticket, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("ticket t%d 不存在", id)
		}
		ordered = append(ordered, ticket)
	}
	return ordered, nil
}

func validateIntegrationSourceTickets(sourceTickets []contracts.Ticket, targetRef string) error {
	if len(sourceTickets) == 0 {
		return fmt.Errorf("source tickets 不能为空")
	}
	for _, ticket := range sourceTickets {
		workflow := contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus)
		integration := contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus)
		if workflow != contracts.TicketDone || integration != contracts.IntegrationNeedsMerge {
			return fmt.Errorf(
				"source ticket t%d 必须处于 done + needs_merge，当前 workflow=%s integration=%s",
				ticket.ID,
				strings.TrimSpace(string(ticket.WorkflowStatus)),
				strings.TrimSpace(string(ticket.IntegrationStatus)),
			)
		}
		sourceTargetRef, err := normalizeIntegrationTargetRefInput(ticket.TargetBranch)
		if err != nil {
			return fmt.Errorf("source ticket t%d 缺少有效 target_ref: %w", ticket.ID, err)
		}
		if sourceTargetRef != targetRef {
			return fmt.Errorf("source ticket t%d target_ref=%s 与输入 target_ref=%s 不一致", ticket.ID, sourceTargetRef, targetRef)
		}
	}
	return nil
}
