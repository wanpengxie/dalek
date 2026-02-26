package app

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"dalek/internal/contracts"
)

func (p *Project) ManagerSessionName() string {
	if p == nil || p.pm == nil {
		return ""
	}
	return strings.TrimSpace(p.pm.ManagerSessionName())
}

func (p *Project) GetPMState(ctx context.Context) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.GetState(ctx)
}

func (p *Project) SetAutopilotEnabled(ctx context.Context, enabled bool) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.SetAutopilotEnabled(ctx, enabled)
}

func (p *Project) SetMaxRunningWorkers(ctx context.Context, n int) (contracts.PMState, error) {
	if p == nil || p.pm == nil {
		return contracts.PMState{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.SetMaxRunningWorkers(ctx, n)
}

func (p *Project) EnsureManagerSession(ctx context.Context) (string, error) {
	if p == nil || p.pm == nil {
		return "", fmt.Errorf("project pm service 为空")
	}
	return p.pm.EnsureManagerSession(ctx)
}

func (p *Project) SendManagerLine(ctx context.Context, line string) error {
	if p == nil || p.pm == nil {
		return fmt.Errorf("project pm service 为空")
	}
	return p.pm.SendManagerLine(ctx, line)
}

func (p *Project) ManagerAttachCmd(ctx context.Context) (*exec.Cmd, error) {
	if p == nil || p.pm == nil {
		return nil, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ManagerAttachCmd(ctx)
}

func (p *Project) CaptureManagerTailPreview(ctx context.Context, lastLines int) (contracts.TailPreview, error) {
	if p == nil || p.pm == nil {
		return contracts.TailPreview{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.CaptureManagerTailPreview(ctx, lastLines)
}

func (p *Project) ManagerTick(ctx context.Context, opt ManagerTickOptions) (ManagerTickResult, error) {
	if p == nil || p.pm == nil {
		return ManagerTickResult{}, fmt.Errorf("project pm service 为空")
	}
	return p.pm.ManagerTick(ctx, opt)
}
