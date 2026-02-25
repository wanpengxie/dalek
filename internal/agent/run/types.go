package run

import "dalek/internal/agent/provider"

type AgentRunResult struct {
	ExitCode int                   `json:"exit_code"`
	Stdout   string                `json:"stdout,omitempty"`
	Stderr   string                `json:"stderr,omitempty"`
	Parsed   provider.ParsedOutput `json:"parsed"`
}

type AgentRunHandle interface {
	RunID() uint
	Wait() (AgentRunResult, error)
	Cancel() error
}
