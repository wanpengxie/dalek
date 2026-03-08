package daemon

import (
	"context"
	"fmt"
	"strings"
)

func (h *ExecutionHost) ListTicketViews(ctx context.Context, project string) ([]TicketView, error) {
	if h == nil || h.resolver == nil {
		return nil, fmt.Errorf("execution host 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName := strings.TrimSpace(project)
	if projectName == "" {
		return nil, fmt.Errorf("project 不能为空")
	}
	opened, err := h.resolver.OpenProject(projectName)
	if err != nil {
		return nil, err
	}
	return opened.ListTicketViews(ctx)
}

func (h *ExecutionHost) GetTicketViewByID(ctx context.Context, project string, ticketID uint) (*TicketView, error) {
	if h == nil || h.resolver == nil {
		return nil, fmt.Errorf("execution host 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName := strings.TrimSpace(project)
	if projectName == "" {
		return nil, fmt.Errorf("project 不能为空")
	}
	if ticketID == 0 {
		return nil, fmt.Errorf("ticket_id 不能为空")
	}
	opened, err := h.resolver.OpenProject(projectName)
	if err != nil {
		return nil, err
	}
	return opened.GetTicketViewByID(ctx, ticketID)
}
