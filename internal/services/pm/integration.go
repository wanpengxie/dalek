package pm

import (
	"context"
	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/infra"
	"dalek/internal/services/ticketlifecycle"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

const integrationGitCheckTimeout = 3 * time.Second
const integrationDefaultTargetRef = "refs/heads/main"

type doneIntegrationFreeze struct {
	AnchorSHA string
	TargetRef string
}

func (s *Service) AbandonTicketIntegration(ctx context.Context, ticketID uint, reason string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 {
		return fmt.Errorf("ticket_id 不能为空")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "manual abandon integration"
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t contracts.Ticket
		if err := tx.WithContext(ctx).First(&t, ticketID).Error; err != nil {
			return err
		}
		if !fsm.CanAbandonTicketIntegration(t.WorkflowStatus, t.IntegrationStatus) {
			return fmt.Errorf("ticket 当前 integration 状态不允许 abandon：t%d (%s/%s)", ticketID, contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus), contracts.CanonicalIntegrationStatus(t.IntegrationStatus))
		}

		now := time.Now()
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleMergeAbandoned,
			Source:         "pm.integration",
			ActorType:      contracts.TicketLifecycleActorUser,
			IdempotencyKey: ticketlifecycle.MergeAbandonedIdempotencyKey(ticketID, now),
			Payload: map[string]any{
				"ticket_id":          ticketID,
				"reason":             reason,
				"integration_status": string(contracts.IntegrationAbandoned),
			},
			CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if lifecycleResult.IntegrationChanged() {
			if err := s.applyAbandonedIntegrationSnapshotTx(ctx, tx, ticketID, reason, now); err != nil {
				return err
			}
		}

		return tx.WithContext(ctx).Model(&contracts.InboxItem{}).
			Where("ticket_id = ? AND reason = ? AND status = ?", ticketID, contracts.InboxApprovalRequired, contracts.InboxOpen).
			Updates(map[string]any{
				"status":     contracts.InboxDone,
				"closed_at":  &now,
				"updated_at": now,
			}).Error
	})
}

func (s *Service) resolveDoneIntegrationFreezeTx(ctx context.Context, tx *gorm.DB, ticketID, workerID, taskRunID uint, reportHeadSHA string) (doneIntegrationFreeze, error) {
	if tx == nil || ticketID == 0 {
		return doneIntegrationFreeze{}, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	var t contracts.Ticket
	if err := tx.WithContext(ctx).
		Select("id", "target_branch").
		First(&t, ticketID).Error; err != nil {
		return doneIntegrationFreeze{}, err
	}

	var w contracts.Worker
	foundWorker := false
	if workerID > 0 {
		if err := tx.WithContext(ctx).
			Select("id", "ticket_id", "branch", "worktree_path").
			First(&w, workerID).Error; err == nil && w.TicketID == ticketID {
			foundWorker = true
		}
	}
	if !foundWorker {
		if err := tx.WithContext(ctx).
			Where("ticket_id = ?", ticketID).
			Order("id desc").
			Select("id", "ticket_id", "branch", "worktree_path").
			First(&w).Error; err != nil {
			if err != gorm.ErrRecordNotFound {
				return doneIntegrationFreeze{}, err
			}
			w = contracts.Worker{}
		}
	}

	targetRef := normalizeIntegrationTargetRef(t.TargetBranch)
	if targetRef == "" {
		targetRef = s.defaultIntegrationTargetBranch(ctx)
	}
	return doneIntegrationFreeze{
		AnchorSHA: strings.TrimSpace(s.tryResolveMergeAnchorSHA(ctx, &w, taskRunID, reportHeadSHA)),
		TargetRef: targetRef,
	}, nil
}

func (s *Service) applyDoneIntegrationFreezeTx(ctx context.Context, tx *gorm.DB, ticketID uint, freeze doneIntegrationFreeze, now time.Time) error {
	if tx == nil || ticketID == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	updates := map[string]any{
		"merge_anchor_sha": strings.TrimSpace(freeze.AnchorSHA),
		"merged_at":        nil,
		"abandoned_reason": "",
		"updated_at":       now,
	}
	if strings.TrimSpace(freeze.TargetRef) != "" {
		updates["target_branch"] = strings.TrimSpace(freeze.TargetRef)
	}
	return tx.WithContext(ctx).Model(&contracts.Ticket{}).
		Where("id = ?", ticketID).
		Updates(updates).Error
}

func (s *Service) applyAbandonedIntegrationSnapshotTx(ctx context.Context, tx *gorm.DB, ticketID uint, reason string, now time.Time) error {
	if tx == nil || ticketID == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	return tx.WithContext(ctx).Model(&contracts.Ticket{}).
		Where("id = ?", ticketID).
		Updates(map[string]any{
			"abandoned_reason": strings.TrimSpace(reason),
			"merged_at":        nil,
			"updated_at":       now,
		}).Error
}

func (s *Service) applyMergedIntegrationSnapshotTx(ctx context.Context, tx *gorm.DB, ticketID uint, targetRef string, now time.Time) error {
	if tx == nil || ticketID == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	updates := map[string]any{
		"merged_at":        &now,
		"abandoned_reason": "",
		"updated_at":       now,
	}
	if strings.TrimSpace(targetRef) != "" {
		updates["target_branch"] = strings.TrimSpace(targetRef)
	}
	return tx.WithContext(ctx).Model(&contracts.Ticket{}).
		Where("id = ?", ticketID).
		Updates(updates).Error
}

func (s *Service) defaultIntegrationTargetBranch(ctx context.Context) string {
	if ref := s.currentHeadTargetRef(ctx); ref != "" {
		return ref
	}
	return integrationDefaultTargetRef
}

func (s *Service) currentHeadTargetRef(ctx context.Context) string {
	p, _, err := s.require()
	if err != nil {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	branch, err := p.Git.CurrentBranch(p.RepoRoot)
	if err != nil {
		return ""
	}
	return normalizeIntegrationTargetRef(branch)
}

func (s *Service) tryResolveMergeAnchorSHA(ctx context.Context, w *contracts.Worker, _ uint, reportHeadSHA string) string {
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, integrationGitCheckTimeout)
	defer cancel()

	reportHeadSHA = strings.TrimSpace(reportHeadSHA)
	if looksLikeGitCommit(reportHeadSHA) {
		return reportHeadSHA
	}
	if w != nil {
		wt := strings.TrimSpace(w.WorktreePath)
		if wt != "" {
			if code, out, _, err := infra.RunExitCode(checkCtx, wt, "git", "rev-parse", "HEAD"); err == nil && code == 0 {
				head := strings.TrimSpace(out)
				if looksLikeGitCommit(head) {
					return head
				}
			}
		}
	}
	return ""
}

func (s *Service) isAnchorMergedIntoTarget(ctx context.Context, anchorSHA, targetBranch string) (bool, error) {
	p, _, err := s.require()
	if err != nil {
		return false, err
	}
	anchorSHA = strings.TrimSpace(anchorSHA)
	targetBranch = strings.TrimSpace(targetBranch)
	if !looksLikeGitCommit(anchorSHA) || targetBranch == "" {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	checkCtx, cancel := context.WithTimeout(ctx, integrationGitCheckTimeout)
	defer cancel()
	code, stdout, stderr, runErr := infra.RunExitCode(checkCtx, p.RepoRoot, "git", "merge-base", "--is-ancestor", anchorSHA, targetBranch)
	if runErr != nil {
		return false, runErr
	}
	switch code {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		msg := strings.ToLower(strings.TrimSpace(firstNonEmpty(stderr, stdout)))
		if strings.Contains(msg, "not a git repository") || strings.Contains(msg, "unknown revision") || strings.Contains(msg, "invalid object") {
			return false, nil
		}
		return false, fmt.Errorf("git merge-base --is-ancestor %s %s 失败(code=%d): %s", anchorSHA, targetBranch, code, strings.TrimSpace(firstNonEmpty(stderr, stdout)))
	}
}

func looksLikeGitCommit(v string) bool {
	v = strings.TrimSpace(strings.ToLower(v))
	if len(v) < 7 || len(v) > 64 {
		return false
	}
	for _, ch := range v {
		if (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f') {
			continue
		}
		return false
	}
	return true
}

func normalizeIntegrationTargetRef(raw string) string {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "refs/heads/") {
		if strings.TrimSpace(strings.TrimPrefix(ref, "refs/heads/")) == "" {
			return ""
		}
		return ref
	}
	if strings.HasPrefix(ref, "refs/") {
		return ""
	}
	short := strings.TrimSpace(strings.TrimPrefix(ref, "heads/"))
	if short == "" {
		return ""
	}
	return "refs/heads/" + short
}

func normalizeIntegrationTargetRefInput(raw string) (string, error) {
	ref := strings.TrimSpace(raw)
	if ref == "" {
		return "", fmt.Errorf("target_ref 不能为空")
	}
	normalized := normalizeIntegrationTargetRef(ref)
	if normalized == "" {
		if strings.HasPrefix(ref, "refs/") {
			return "", fmt.Errorf("target_ref 仅支持 refs/heads/*: %s", raw)
		}
		return "", fmt.Errorf("target_ref 非法: %s", raw)
	}
	return normalized, nil
}

func shortIntegrationTargetRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if strings.HasPrefix(ref, "refs/heads/") {
		return strings.TrimSpace(strings.TrimPrefix(ref, "refs/heads/"))
	}
	return ref
}

func integrationTargetRefAliases(ref string) []string {
	normalized := normalizeIntegrationTargetRef(ref)
	if normalized == "" {
		return nil
	}
	short := shortIntegrationTargetRef(normalized)
	if short == "" || short == normalized {
		return []string{normalized}
	}
	return []string{normalized, short}
}
