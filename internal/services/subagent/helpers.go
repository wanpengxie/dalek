package subagent

import (
	"context"
	"dalek/internal/agent/progresstimeout"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func inferHomeRootFromWorktreesDir(worktreesDir string) string {
	cur := filepath.Clean(strings.TrimSpace(worktreesDir))
	if cur == "" || cur == "." {
		return ""
	}
	for {
		if strings.EqualFold(filepath.Base(cur), "worktrees") {
			return filepath.Dir(cur)
		}
		next := filepath.Dir(cur)
		if next == cur {
			break
		}
		cur = next
	}
	return ""
}

func isSubagentCanceled(runErr error, ctxErr error) bool {
	if progresstimeout.Is(runErr) {
		return false
	}
	return errors.Is(runErr, context.Canceled) ||
		errors.Is(runErr, context.DeadlineExceeded) ||
		errors.Is(ctxErr, context.Canceled) ||
		errors.Is(ctxErr, context.DeadlineExceeded)
}

func isSubagentTimedOut(runErr error) bool {
	return progresstimeout.Is(runErr)
}

func newSubagentRequestID() string {
	return fmt.Sprintf("sub_%d", time.Now().UnixNano())
}
