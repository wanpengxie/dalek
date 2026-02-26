package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const dispatchDepthEnvKey = "DALEK_DISPATCH_DEPTH"

func enforceDispatchDepthGuardOrExit(out cliOutputFormat, command string) {
	_, blocked, cause := dispatchDepthGuardState(os.Getenv(dispatchDepthEnvKey))
	if !blocked {
		return
	}
	exitRuntimeError(out,
		fmt.Sprintf("禁止在二次派发上下文执行 %s", strings.TrimSpace(command)),
		cause,
		"请在当前 ticket/worktree 直接执行所需命令；如需人工介入，使用 dalek worker report --next wait_user",
	)
}

func dispatchDepthGuardState(raw string) (int, bool, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false, ""
	}
	depth, err := strconv.Atoi(raw)
	if err != nil {
		return 0, true, fmt.Sprintf("%s 值非法：%q（期望整数；非 0 视为二次派发）", dispatchDepthEnvKey, raw)
	}
	if depth == 0 {
		return depth, false, ""
	}
	return depth, true, fmt.Sprintf("%s=%d（非 0，命中二次派发拦截）", dispatchDepthEnvKey, depth)
}
