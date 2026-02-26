package app

import (
	"context"
	"fmt"
	"strings"

	subagentsvc "dalek/internal/services/subagent"
)

func (p *Project) SubmitSubagentRun(ctx context.Context, opt SubagentSubmitOptions) (SubagentSubmission, error) {
	if p == nil || p.subagent == nil {
		return SubagentSubmission{}, fmt.Errorf("project subagent service 为空")
	}
	res, err := p.subagent.Submit(ctx, subagentsvc.SubmitInput{
		RequestID: strings.TrimSpace(opt.RequestID),
		Provider:  strings.TrimSpace(opt.Provider),
		Model:     strings.TrimSpace(opt.Model),
		Prompt:    strings.TrimSpace(opt.Prompt),
	})
	if err != nil {
		return SubagentSubmission{}, err
	}
	return SubagentSubmission{
		Accepted:   res.Accepted,
		TaskRunID:  res.TaskRunID,
		RequestID:  strings.TrimSpace(res.RequestID),
		Provider:   strings.TrimSpace(res.Provider),
		Model:      strings.TrimSpace(res.Model),
		RuntimeDir: strings.TrimSpace(res.RuntimeDir),
	}, nil
}

func (p *Project) RunSubagentJob(ctx context.Context, taskRunID uint, opt SubagentRunOptions) error {
	if p == nil || p.subagent == nil {
		return fmt.Errorf("project subagent service 为空")
	}
	return p.subagent.Run(ctx, taskRunID, subagentsvc.RunInput{
		RunnerID: strings.TrimSpace(opt.RunnerID),
	})
}
