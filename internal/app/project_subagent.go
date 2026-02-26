package app

import (
	"context"
	"fmt"
	"strings"
)

func (p *Project) SubmitSubagentRun(ctx context.Context, opt SubagentSubmitOptions) (SubagentSubmission, error) {
	if p == nil || p.subagent == nil {
		return SubagentSubmission{}, fmt.Errorf("project subagent service 为空")
	}
	opt.RequestID = strings.TrimSpace(opt.RequestID)
	opt.Provider = strings.TrimSpace(opt.Provider)
	opt.Model = strings.TrimSpace(opt.Model)
	opt.Prompt = strings.TrimSpace(opt.Prompt)
	res, err := p.subagent.Submit(ctx, opt)
	if err != nil {
		return SubagentSubmission{}, err
	}
	return res, nil
}

func (p *Project) RunSubagentJob(ctx context.Context, taskRunID uint, opt SubagentRunOptions) error {
	if p == nil || p.subagent == nil {
		return fmt.Errorf("project subagent service 为空")
	}
	opt.RunnerID = strings.TrimSpace(opt.RunnerID)
	return p.subagent.Run(ctx, taskRunID, opt)
}
