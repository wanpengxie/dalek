package eventlog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const schemaV1 = "dalek.run_event_log.v1"

// RunMeta 描述一次 run 的元信息，写入 JSONL 第 1 行。
type RunMeta struct {
	RunID           string `json:"run_id"`
	Project         string `json:"project"`
	TicketID        string `json:"ticket_id,omitempty"`
	ConversationID  string `json:"conversation_id,omitempty"`
	Provider        string `json:"provider"`
	Model           string `json:"model,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	WorkDir         string `json:"work_dir,omitempty"`
	Layer           string `json:"layer,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// RunFooter 描述一次 run 的结束摘要，写入 JSONL 最后一行。
type RunFooter struct {
	RunID      string `json:"run_id"`
	ReplyText  string `json:"reply_text,omitempty"`
	Error      string `json:"error,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}

// RunLogger 管理单次 run 的 JSONL 日志文件。
// 生命周期：Open → WriteHeader → WriteEvent*N → WriteFooter → Close
type RunLogger struct {
	f *os.File
}

// Open 创建日志文件 ~/.dalek/logs/{project}/{runID}.jsonl。
// 自动创建目录。使用 DALEK_HOME 环境变量或默认 ~/.dalek。
func Open(project, runID string) (*RunLogger, error) {
	dir := resolveLogDir(strings.TrimSpace(project))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, strings.TrimSpace(runID)+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	return &RunLogger{f: f}, nil
}

// WriteHeader 写入 run 元信息（第 1 行）。
func (l *RunLogger) WriteHeader(meta RunMeta) error {
	rec := map[string]any{
		"schema":  schemaV1,
		"phase":   "start",
		"run_id":  meta.RunID,
		"ts":      time.Now().Format(time.RFC3339Nano),
		"project": meta.Project,
	}
	if meta.TicketID != "" {
		rec["ticket_id"] = meta.TicketID
	}
	if meta.ConversationID != "" {
		rec["conversation_id"] = meta.ConversationID
	}
	rec["provider"] = meta.Provider
	if meta.Model != "" {
		rec["model"] = meta.Model
	}
	if meta.SessionID != "" {
		rec["session_id"] = meta.SessionID
	}
	if meta.WorkDir != "" {
		rec["work_dir"] = meta.WorkDir
	}
	if meta.Layer != "" {
		rec["layer"] = meta.Layer
	}
	if meta.ReasoningEffort != "" {
		rec["reasoning_effort"] = meta.ReasoningEffort
	}
	return l.writeLine(rec)
}

// WriteEvent 追加一条 SDK 原始事件（中间行）。
// rawJSON 是原始 JSON 字符串，写入 raw 字段时使用 json.RawMessage 避免二次转义。
func (l *RunLogger) WriteEvent(seq int, eventType, rawJSON string) error {
	rec := map[string]any{
		"schema":     schemaV1,
		"phase":      "event",
		"seq":        seq,
		"ts":         time.Now().Format(time.RFC3339Nano),
		"event_type": eventType,
	}
	rawJSON = strings.TrimSpace(rawJSON)
	if rawJSON != "" && json.Valid([]byte(rawJSON)) {
		rec["raw"] = json.RawMessage(rawJSON)
	} else if rawJSON != "" {
		// 无效 JSON fallback 为字符串
		rec["raw"] = rawJSON
	}
	return l.writeLine(rec)
}

// WriteFooter 写入 run 结束摘要（最后一行）。
func (l *RunLogger) WriteFooter(footer RunFooter) error {
	rec := map[string]any{
		"schema":      schemaV1,
		"phase":       "end",
		"run_id":      footer.RunID,
		"ts":          time.Now().Format(time.RFC3339Nano),
		"duration_ms": footer.DurationMS,
	}
	if footer.ReplyText != "" {
		rec["reply_text"] = footer.ReplyText
	}
	if footer.Error != "" {
		rec["error"] = footer.Error
	}
	if footer.Stderr != "" {
		rec["stderr"] = footer.Stderr
	}
	if footer.SessionID != "" {
		rec["session_id"] = footer.SessionID
	}
	return l.writeLine(rec)
}

// Close 关闭日志文件。
func (l *RunLogger) Close() error {
	if l.f == nil {
		return nil
	}
	return l.f.Close()
}

func (l *RunLogger) writeLine(rec map[string]any) error {
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := l.f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}

// ResolveProjectName 从工作目录推断项目名。
// 优先级: 环境变量 DALEK_PROJECT_KEY → workDir/.dalek/.dalek_project_name → "unknown"
func ResolveProjectName(workDir string) string {
	if envKey := strings.TrimSpace(os.Getenv("DALEK_PROJECT_KEY")); envKey != "" {
		return envKey
	}
	workDir = strings.TrimSpace(workDir)
	if workDir != "" {
		nameFile := filepath.Join(workDir, ".dalek", ".dalek_project_name")
		if data, err := os.ReadFile(nameFile); err == nil {
			if name := strings.TrimSpace(string(data)); name != "" {
				return name
			}
		}
	}
	return "unknown"
}

func resolveLogDir(project string) string {
	home := strings.TrimSpace(os.Getenv("DALEK_HOME"))
	if home == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			home = filepath.Join(userHome, ".dalek")
		}
	}
	if home == "" {
		home = ".dalek"
	}
	return filepath.Join(home, "logs", project)
}
