package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/infra"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

type SyncRefResult struct {
	Ref              string   `json:"ref"`
	OldSHA           string   `json:"old_sha,omitempty"`
	NewSHA           string   `json:"new_sha,omitempty"`
	CandidateTickets int      `json:"candidate_tickets"`
	MergedTicketIDs  []uint   `json:"merged_ticket_ids"`
	Errors           []string `json:"errors,omitempty"`
}

type RetargetResult struct {
	TicketID    uint   `json:"ticket_id"`
	PreviousRef string `json:"previous_ref,omitempty"`
	TargetRef   string `json:"target_ref"`
}

type RescanResult struct {
	RefFilter string          `json:"ref_filter,omitempty"`
	Results   []SyncRefResult `json:"results"`
	Errors    []string        `json:"errors,omitempty"`
}

func (s *Service) SyncRef(ctx context.Context, ref, oldSHA, newSHA string) (SyncRefResult, error) {
	_, db, err := s.require()
	if err != nil {
		return SyncRefResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	targetRef, err := normalizeIntegrationTargetRefInput(ref)
	if err != nil {
		return SyncRefResult{}, err
	}

	res := SyncRefResult{
		Ref:    targetRef,
		OldSHA: strings.TrimSpace(oldSHA),
		NewSHA: strings.TrimSpace(newSHA),
	}
	aliases := integrationTargetRefAliases(targetRef)
	if len(aliases) == 0 {
		return res, nil
	}

	var tickets []contracts.Ticket
	q := db.WithContext(ctx).
		Model(&contracts.Ticket{}).
		Where("workflow_status = ? AND integration_status = ?", contracts.TicketDone, contracts.IntegrationNeedsMerge)
	if len(aliases) == 1 {
		q = q.Where("TRIM(COALESCE(target_branch, '')) = ?", aliases[0])
	} else {
		q = q.Where("TRIM(COALESCE(target_branch, '')) IN ?", aliases)
	}
	if err := q.Order("id asc").Find(&tickets).Error; err != nil {
		return res, err
	}
	res.CandidateTickets = len(tickets)
	if !looksLikeGitCommit(res.NewSHA) {
		return res, nil
	}

	for _, tk := range tickets {
		anchor := strings.TrimSpace(tk.MergeAnchorSHA)
		if !looksLikeGitCommit(anchor) {
			res.Errors = append(res.Errors, fmt.Sprintf("t%d anchor 非法: %q", tk.ID, anchor))
			continue
		}
		merged, checkErr := s.isAnchorMergedIntoTarget(ctx, anchor, res.NewSHA)
		if checkErr != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("t%d merge 检查失败: %v", tk.ID, checkErr))
			continue
		}
		if !merged {
			continue
		}
		now := time.Now()
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.WithContext(ctx).
				Model(&contracts.Ticket{}).
				Where("id = ? AND workflow_status = ? AND integration_status = ?", tk.ID, contracts.TicketDone, contracts.IntegrationNeedsMerge).
				Updates(map[string]any{
					"integration_status": contracts.IntegrationMerged,
					"merged_at":          &now,
					"updated_at":         now,
				}).Error; err != nil {
				return err
			}
			return s.appendTicketLifecycleEventTx(ctx, tx, ticketlifecycle.AppendInput{
				TicketID:       tk.ID,
				EventType:      contracts.TicketLifecycleMergeObserved,
				Source:         "pm.integration_sync",
				ActorType:      contracts.TicketLifecycleActorSystem,
				IdempotencyKey: ticketlifecycle.MergeObservedIdempotencyKey(tk.ID, anchor),
				Payload: map[string]any{
					"ticket_id":          tk.ID,
					"target_ref":         targetRef,
					"anchor_sha":         anchor,
					"new_sha":            res.NewSHA,
					"integration_status": string(contracts.IntegrationMerged),
				},
				CreatedAt: now,
			})
		}); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("t%d 标记 merged 失败: %v", tk.ID, err))
			continue
		}
		res.MergedTicketIDs = append(res.MergedTicketIDs, tk.ID)
	}
	return res, nil
}

func (s *Service) RetargetTicketIntegration(ctx context.Context, ticketID uint, ref string) (RetargetResult, error) {
	_, db, err := s.require()
	if err != nil {
		return RetargetResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return RetargetResult{}, fmt.Errorf("ticket_id 不能为空")
	}
	targetRef, err := normalizeIntegrationTargetRefInput(ref)
	if err != nil {
		return RetargetResult{}, err
	}

	out := RetargetResult{TicketID: ticketID, TargetRef: targetRef}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tk contracts.Ticket
		if err := tx.WithContext(ctx).First(&tk, ticketID).Error; err != nil {
			return err
		}
		if contracts.CanonicalTicketWorkflowStatus(tk.WorkflowStatus) != contracts.TicketDone {
			return fmt.Errorf("ticket t%d workflow=%s，不允许 retarget", ticketID, contracts.CanonicalTicketWorkflowStatus(tk.WorkflowStatus))
		}
		if contracts.CanonicalIntegrationStatus(tk.IntegrationStatus) != contracts.IntegrationNeedsMerge {
			return fmt.Errorf("ticket t%d integration=%s，不允许 retarget", ticketID, contracts.CanonicalIntegrationStatus(tk.IntegrationStatus))
		}
		out.PreviousRef = normalizeIntegrationTargetRef(tk.TargetBranch)
		now := time.Now()
		return tx.WithContext(ctx).
			Model(&contracts.Ticket{}).
			Where("id = ? AND workflow_status = ? AND integration_status = ?", ticketID, contracts.TicketDone, contracts.IntegrationNeedsMerge).
			Updates(map[string]any{
				"target_branch": targetRef,
				"updated_at":    now,
			}).Error
	})
	if err != nil {
		return RetargetResult{}, err
	}
	return out, nil
}

func (s *Service) RescanMergeStatus(ctx context.Context, ref string) (RescanResult, error) {
	_, db, err := s.require()
	if err != nil {
		return RescanResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	out := RescanResult{Results: []SyncRefResult{}}
	var refs []string
	ref = strings.TrimSpace(ref)
	if ref != "" {
		normalized, err := normalizeIntegrationTargetRefInput(ref)
		if err != nil {
			return out, err
		}
		out.RefFilter = normalized
		refs = []string{normalized}
	} else {
		var tickets []contracts.Ticket
		if err := db.WithContext(ctx).
			Model(&contracts.Ticket{}).
			Select("id", "target_branch").
			Where("workflow_status = ? AND integration_status = ?", contracts.TicketDone, contracts.IntegrationNeedsMerge).
			Order("id asc").
			Find(&tickets).Error; err != nil {
			return out, err
		}
		seen := map[string]struct{}{}
		for _, tk := range tickets {
			normalized := normalizeIntegrationTargetRef(tk.TargetBranch)
			if normalized == "" {
				continue
			}
			if _, ok := seen[normalized]; ok {
				continue
			}
			seen[normalized] = struct{}{}
			refs = append(refs, normalized)
		}
	}

	for _, targetRef := range refs {
		newSHA, resolveErr := s.resolveRefCommit(ctx, targetRef)
		if resolveErr != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s 解析失败: %v", targetRef, resolveErr))
			continue
		}
		res, syncErr := s.SyncRef(ctx, targetRef, "", newSHA)
		if syncErr != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("%s sync 失败: %v", targetRef, syncErr))
			continue
		}
		out.Results = append(out.Results, res)
	}
	return out, nil
}

func (s *Service) resolveRefCommit(ctx context.Context, ref string) (string, error) {
	p, _, err := s.require()
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("ref 不能为空")
	}
	checkCtx, cancel := context.WithTimeout(ctx, integrationGitCheckTimeout)
	defer cancel()
	code, stdout, stderr, runErr := infra.RunExitCode(checkCtx, p.RepoRoot, "git", "rev-parse", "--verify", ref)
	if runErr != nil {
		return "", runErr
	}
	if code != 0 {
		return "", fmt.Errorf("git rev-parse --verify %s 失败(code=%d): %s", ref, code, strings.TrimSpace(firstNonEmpty(stderr, stdout)))
	}
	sha := strings.TrimSpace(stdout)
	if !looksLikeGitCommit(sha) {
		return "", fmt.Errorf("ref %s 解析结果不是 commit: %q", ref, sha)
	}
	return sha, nil
}
