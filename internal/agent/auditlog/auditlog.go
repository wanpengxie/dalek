package auditlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	schemaV1           = "dalek.runner_audit.v1"
	defaultAuditRelLog = ".dalek/runtime/runner-audit.jsonl"
)

// Append 以 JSONL 形式追加一条 runner 审计日志。
// 失败不应中断主链路，由调用方决定是否忽略错误。
func Append(workDir string, payload map[string]any) error {
	path := resolveAuditPath(workDir)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	record := map[string]any{
		"schema": schemaV1,
		"ts":     time.Now().Format(time.RFC3339Nano),
	}
	for k, v := range payload {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		record[key] = v
	}
	b, err := json.Marshal(record)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

func resolveAuditPath(workDir string) string {
	if raw := strings.TrimSpace(os.Getenv("DALEK_RUNNER_AUDIT_LOG")); raw != "" {
		if filepath.IsAbs(raw) {
			return raw
		}
		base := strings.TrimSpace(workDir)
		if base == "" {
			if cwd, err := os.Getwd(); err == nil {
				base = strings.TrimSpace(cwd)
			}
		}
		if base == "" {
			return raw
		}
		return filepath.Join(base, raw)
	}
	base := strings.TrimSpace(workDir)
	if base != "" {
		return filepath.Join(base, defaultAuditRelLog)
	}
	home := strings.TrimSpace(os.Getenv("DALEK_HOME"))
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".dalek")
		}
	}
	if home == "" {
		return ""
	}
	return filepath.Join(home, "runtime", "runner-audit.jsonl")
}

func SortedEnvKeys(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	if len(keys) == 0 {
		return nil
	}
	// 小规模 keys 下原地插入排序，避免额外依赖。
	for i := 1; i < len(keys); i++ {
		cur := keys[i]
		j := i - 1
		for ; j >= 0 && keys[j] > cur; j-- {
			keys[j+1] = keys[j]
		}
		keys[j+1] = cur
	}
	return keys
}
