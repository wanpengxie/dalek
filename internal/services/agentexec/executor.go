package agentexec

import "context"

// Executor 是 agent 执行的统一入口。
type Executor interface {
	Execute(ctx context.Context, prompt string) (AgentRunHandle, error)
}
