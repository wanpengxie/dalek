package pm

import (
	"context"
	"fmt"
	"strings"

	"dalek/internal/contracts"
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
	if strings.TrimSpace(requested) == "" {
		return requiredWorkerBaseBranch(ticket)
	}
	return resolveWorkerBaseBranch(ticket, requested)
}

func requiredWorkerBaseBranch(ticket contracts.Ticket) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(ticket.Label), "integration") {
		return "", nil
	}
	targetRef, err := normalizeIntegrationTargetRefInput(strings.TrimSpace(ticket.TargetBranch))
	if err != nil {
		return "", fmt.Errorf("integration ticket t%d 缺少有效 target_ref: %w", ticket.ID, err)
	}
	return targetRef, nil
}

func resolveWorkerBaseBranch(ticket contracts.Ticket, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	targetRef, err := requiredWorkerBaseBranch(ticket)
	if err != nil {
		return "", err
	}
	if targetRef == "" {
		return requested, nil
	}
	if requested == "" {
		return "", fmt.Errorf("integration ticket t%d 缺少 BaseBranch；期望 target_ref=%s", ticket.ID, targetRef)
	}
	baseBranch, err := normalizeIntegrationTargetRefInput(requested)
	if err != nil {
		return "", fmt.Errorf("integration ticket t%d base_branch 非法: %w", ticket.ID, err)
	}
	if baseBranch != targetRef {
		return "", fmt.Errorf("integration ticket t%d base_branch=%s 与 target_ref=%s 不一致", ticket.ID, baseBranch, targetRef)
	}
	return targetRef, nil
}
