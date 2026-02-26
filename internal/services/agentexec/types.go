package agentexec

import (
	"context"

	"dalek/internal/agent/provider"
)

type AgentRunResult struct {
	ExitCode int                   `json:"exit_code"`
	Stdout   string                `json:"stdout,omitempty"`
	Stderr   string                `json:"stderr,omitempty"`
	Parsed   provider.ParsedOutput `json:"parsed"`
}

type AgentRunHandle interface {
	RunID() uint
	Wait(ctx context.Context) (AgentRunResult, error)
	Cancel() error
}
