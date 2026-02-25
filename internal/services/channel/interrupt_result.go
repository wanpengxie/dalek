package channel

import "strings"

type InterruptStatus string

const (
	InterruptStatusHit              InterruptStatus = "hit"
	InterruptStatusMiss             InterruptStatus = "miss"
	InterruptStatusExecutionFailure InterruptStatus = "execution_failure"
)

type InterruptResult struct {
	Status            InterruptStatus
	ConversationID    uint
	RunnerInterrupted bool
	ContextCanceled   bool
	RunnerError       string
}

func (r InterruptResult) Interrupted() bool {
	return r.Status == InterruptStatusHit
}

func (r InterruptResult) RunnerErrorMessage() string {
	return strings.TrimSpace(r.RunnerError)
}
