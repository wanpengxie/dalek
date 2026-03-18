package contracts

import (
	"errors"
	"strings"
)

type TaskCancelCause string

const (
	TaskCancelCauseUnknown        TaskCancelCause = ""
	TaskCancelCauseUserStop       TaskCancelCause = "user_stop"
	TaskCancelCauseUserInterrupt  TaskCancelCause = "user_interrupt"
	TaskCancelCauseFocusCancel    TaskCancelCause = "focus_cancel"
	TaskCancelCauseDaemonShutdown TaskCancelCause = "daemon_shutdown"
)

var (
	ErrUserStop       error = TaskCancelCauseUserStop
	ErrUserInterrupt  error = TaskCancelCauseUserInterrupt
	ErrFocusCancel    error = TaskCancelCauseFocusCancel
	ErrDaemonShutdown error = TaskCancelCauseDaemonShutdown
)

func (c TaskCancelCause) Error() string {
	return strings.TrimSpace(string(c))
}

func (c TaskCancelCause) Valid() bool {
	switch c {
	case TaskCancelCauseUserStop, TaskCancelCauseUserInterrupt, TaskCancelCauseFocusCancel, TaskCancelCauseDaemonShutdown:
		return true
	default:
		return false
	}
}

func ParseTaskCancelCause(raw string) TaskCancelCause {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(TaskCancelCauseUserStop):
		return TaskCancelCauseUserStop
	case string(TaskCancelCauseUserInterrupt):
		return TaskCancelCauseUserInterrupt
	case string(TaskCancelCauseFocusCancel):
		return TaskCancelCauseFocusCancel
	case string(TaskCancelCauseDaemonShutdown):
		return TaskCancelCauseDaemonShutdown
	default:
		return TaskCancelCauseUnknown
	}
}

func TaskCancelCauseFromError(err error) TaskCancelCause {
	if err == nil {
		return TaskCancelCauseUnknown
	}
	var cause TaskCancelCause
	if errors.As(err, &cause) && cause.Valid() {
		return cause
	}
	return ParseTaskCancelCause(err.Error())
}

func (c TaskCancelCause) ErrorCode() string {
	if c.Valid() {
		return string(c)
	}
	return "agent_canceled"
}

func (c TaskCancelCause) Summary() string {
	switch c {
	case TaskCancelCauseUserStop:
		return "ticket stopped by user"
	case TaskCancelCauseUserInterrupt:
		return "ticket interrupted by user"
	case TaskCancelCauseFocusCancel:
		return "ticket loop canceled by focus controller"
	case TaskCancelCauseDaemonShutdown:
		return "ticket loop canceled by daemon shutdown"
	default:
		return ""
	}
}
