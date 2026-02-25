package infra

import (
	"sort"
	"strings"
)

// ShellQuote 用最简单的方式单引号转义，避免路径里有空格导致命令注入/拼接失败。
func ShellQuote(s string) string {
	s = sanitizeShellArg(s)
	if s == "" {
		return "''"
	}
	// ' -> '"'"'
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func sanitizeShellArg(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case 0:
			continue
		case '\n', '\r':
			b.WriteByte(' ')
		default:
			if r < 0x20 {
				continue
			}
			b.WriteRune(r)
		}
	}
	return b.String()
}

// BuildBashScriptWithEnv 将 env 以 `export K='V'` 的形式拼接到脚本前缀，便于 `bash -lc` 执行。
// 这是机制工具，不涉及策略。
func BuildBashScriptWithEnv(env map[string]string, runCmd string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		parts = append(parts, "export "+k+"="+ShellQuote(env[k]))
	}
	parts = append(parts, strings.TrimSpace(runCmd))
	return strings.Join(parts, "; ")
}
