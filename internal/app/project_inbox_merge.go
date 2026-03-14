package app

import (
	"context"
	"fmt"
	"time"

	"dalek/internal/contracts"
	pmsvc "dalek/internal/services/pm"
)

// ----- inbox facade -----

func (p *Project) ListInbox(ctx context.Context, opt ListInboxOptions) ([]contracts.InboxItem, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ListInbox(ctx, pmsvc.ListInboxOptions{
		Status: opt.Status,
		Limit:  opt.Limit,
	})
}

func (p *Project) GetInboxItem(ctx context.Context, id uint) (*contracts.InboxItem, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.GetInboxItem(ctx, id)
}

func (p *Project) CloseInboxItem(ctx context.Context, id uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.CloseInboxItem(ctx, id)
}

func (p *Project) ReplyInboxItem(ctx context.Context, id uint, action, reply string) (InboxReplyResult, error) {
	if p == nil || p.pm == nil {
		return InboxReplyResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ReplyInboxItem(ctx, id, action, reply)
}

func (p *Project) SnoozeInboxItem(ctx context.Context, id uint, until time.Time) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.SnoozeInboxItem(ctx, id, until)
}

func (p *Project) UnsnoozeInboxItem(ctx context.Context, id uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.UnsnoozeInboxItem(ctx, id)
}

func (p *Project) DeleteInboxItem(ctx context.Context, id uint) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.DeleteInboxItem(ctx, id)
}

// ----- merge facade -----

func (p *Project) ListMergeItems(ctx context.Context, opt ListMergeOptions) ([]contracts.MergeItem, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ListMergeItems(ctx, pmsvc.ListMergeOptions{
		Status: opt.Status,
		Limit:  opt.Limit,
	})
}
