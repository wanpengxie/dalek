package pm

import (
	"context"
	"fmt"
	"strings"

	"dalek/internal/contracts"
	"dalek/internal/ticketatomic"
)

func (s *Service) workerBaseBranchForTicket(ctx context.Context, ticketID uint, requested string) (string, error) {
	_, db, err := s.require()
	if err != nil {
		return "", err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var ticket contracts.Ticket
	if err := db.WithContext(ctx).
		Select("id", "label", "target_branch").
		First(&ticket, ticketID).Error; err != nil {
		return "", err
	}
	currentBranch := ""
	var currentErr error
	if s != nil && s.p != nil && s.p.Git != nil {
		currentBranch, currentErr = s.p.Git.CurrentBranch(s.p.RepoRoot)
	}
	resolution, err := ticketatomic.ResolveStartBase(ticket, requested, currentBranch, currentErr)
	if err != nil {
		return "", err
	}
	return resolution.BaseBranch, nil
}

func requiredWorkerBaseBranch(ticket contracts.Ticket) (string, error) {
	targetRef, err := ticketatomic.NormalizeOptionalTargetRef(ticket.TargetBranch)
	if err != nil {
		return "", fmt.Errorf("ticket t%d 的 target_ref 非法: %w", ticket.ID, err)
	}
	if targetRef == "" {
		if strings.EqualFold(strings.TrimSpace(ticket.Label), "integration") {
			return "", fmt.Errorf("integration ticket t%d 缺少有效 target_ref", ticket.ID)
		}
		return "", nil
	}
	return targetRef, nil
}

func resolveWorkerBaseBranch(ticket contracts.Ticket, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	requestedRef, err := ticketatomic.NormalizeTargetRefInput(requested)
	if err != nil {
		return "", fmt.Errorf("ticket start 被拒绝：ticket t%d --base 非法: %w", ticket.ID, err)
	}
	targetRef, err := requiredWorkerBaseBranch(ticket)
	if err != nil {
		return "", err
	}
	if targetRef == "" {
		return requestedRef, nil
	}
	if requested == "" {
		return "", fmt.Errorf("integration ticket t%d 缺少 BaseBranch；期望 target_ref=%s", ticket.ID, targetRef)
	}
	if requestedRef != targetRef {
		return "", fmt.Errorf("ticket start 被拒绝：ticket t%d --base=%s 与 ticket.target_ref=%s 不一致。当前 ticket 是单 ref 原子任务，如需在新 ref 上执行，请创建新 ticket。", ticket.ID, requestedRef, targetRef)
	}
	return targetRef, nil
}
