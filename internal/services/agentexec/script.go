package agentexec

import (
	"fmt"
	"sort"
	"strings"
)

type tmuxScriptInput struct {
	WorkDir     string
	Env         map[string]string
	RunID       uint
	WorkerID    uint
	BinPath     string
	CommandLine string
}

func buildTmuxInjectScript(in tmuxScriptInput) string {
	binPath := strings.TrimSpace(in.BinPath)
	if binPath == "" {
		binPath = "dalek"
	}

	lines := []string{
		"#!/usr/bin/env bash",
		"set -euo pipefail",
		"",
		"cd " + shellQuote(strings.TrimSpace(in.WorkDir)),
	}

	env := map[string]string{}
	for k, v := range in.Env {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		env[key] = v
	}
	if in.WorkerID != 0 {
		env["DALEK_WORKER_ID"] = fmt.Sprintf("%d", in.WorkerID)
	}
	if in.RunID != 0 {
		env["DALEK_RUN_ID"] = fmt.Sprintf("%d", in.RunID)
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		lines = append(lines, "export "+k+"="+shellQuote(env[k]))
	}
	lines = append(lines, []string{
		"_ts_bin=\"${DALEK_BIN_PATH:-" + binPath + "}\"",
		"_ts_finish_run() {",
		"  _ts_code=\"$1\"",
		"  if [ \"${DALEK_RUN_ID:-0}\" != \"0\" ]; then",
		"    \"${_ts_bin}\" agent finish --run-id \"${DALEK_RUN_ID}\" --exit-code \"${_ts_code}\" >/dev/null 2>&1 || true",
		"  fi",
		"}",
		"_ts_cleanup() {",
		"  _ts_ec=$?",
		"  trap - EXIT",
		"  _ts_finish_run \"${_ts_ec}\"",
		"  exit \"$_ts_ec\"",
		"}",
		"trap _ts_cleanup EXIT",
		"",
		strings.TrimSpace(in.CommandLine),
		"",
	}...)
	return strings.Join(lines, "\n")
}
