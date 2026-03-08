package agentexec

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"dalek/internal/agent/progresstimeout"
	"dalek/internal/services/core"
)

const leaseRenewTimeout = 10 * time.Second

func touchAgentProgress(watchdog *progresstimeout.Watchdog, rt core.TaskRuntime, runID uint, runnerID string) *time.Time {
	if watchdog == nil {
		return nil
	}
	lease := watchdog.Touch()
	renewTaskRunLease(rt, runID, runnerID, lease)
	return lease
}

func renewTaskRunLease(rt core.TaskRuntime, runID uint, runnerID string, lease *time.Time) {
	if rt == nil || runID == 0 || strings.TrimSpace(runnerID) == "" || lease == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), leaseRenewTimeout)
	defer cancel()
	_ = rt.RenewLease(ctx, runID, strings.TrimSpace(runnerID), lease)
}

type progressBuffer struct {
	buf      *bytes.Buffer
	watchdog *progresstimeout.Watchdog
	runtime  core.TaskRuntime

	mu       sync.RWMutex
	runID    uint
	runnerID string
}

type agentRunError struct {
	cause  error
	detail string
}

func (e agentRunError) Error() string {
	detail := strings.TrimSpace(e.detail)
	if detail == "" && e.cause != nil {
		detail = strings.TrimSpace(e.cause.Error())
	}
	if detail == "" {
		return "agent 执行失败"
	}
	return fmt.Sprintf("agent 执行失败: %s", detail)
}

func (e agentRunError) Unwrap() error {
	return e.cause
}

func wrapAgentRunError(cause error, stdout, stderr string) error {
	if cause == nil {
		return nil
	}
	return agentRunError{
		cause:  cause,
		detail: errStringWithOutput(cause, stdout, stderr),
	}
}

func newProgressBuffer(buf *bytes.Buffer, watchdog *progresstimeout.Watchdog, rt core.TaskRuntime) *progressBuffer {
	return &progressBuffer{
		buf:      buf,
		watchdog: watchdog,
		runtime:  rt,
	}
}

func (w *progressBuffer) SetRun(runID uint, runnerID string) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.runID = runID
	w.runnerID = strings.TrimSpace(runnerID)
}

func (w *progressBuffer) Write(p []byte) (int, error) {
	if w == nil || w.buf == nil {
		return 0, nil
	}
	if len(p) > 0 {
		w.mu.RLock()
		runID := w.runID
		runnerID := w.runnerID
		w.mu.RUnlock()
		touchAgentProgress(w.watchdog, w.runtime, runID, runnerID)
	}
	return w.buf.Write(p)
}
