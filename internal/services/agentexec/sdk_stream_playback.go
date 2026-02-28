package agentexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dalek/internal/agent/eventrender"
	"dalek/internal/agent/sdkrunner"
)

type sdkStreamPlayback struct {
	runID    uint
	provider string
	logPath  string

	file *os.File
	mu   sync.Mutex

	renderer eventrender.Renderer
	seq      int
}

const streamPlaybackMaxDetailLines = 10

func sdkStreamLogPathHint(cfg SDKConfig) string {
	if v := strings.TrimSpace(cfg.StreamLogPath); v != "" {
		return v
	}
	if v := strings.TrimSpace(cfg.TmuxLogPath); v != "" {
		return v
	}
	if v := strings.TrimSpace(cfg.WorkDir); v != "" {
		return filepath.Join(v, ".dalek", "runtime", "sdk-stream.log")
	}
	return ""
}

func resolveSDKStreamLogPath(cfg SDKConfig) (string, error) {
	logPath := strings.TrimSpace(sdkStreamLogPathHint(cfg))
	if logPath == "" {
		return "", fmt.Errorf("stream_log_path/work_dir 同时为空")
	}
	return logPath, nil
}

func startSDKStreamPlayback(_ context.Context, cfg SDKConfig, runID uint) (*sdkStreamPlayback, error) {
	logPath, err := resolveSDKStreamLogPath(cfg)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	fileHandle, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	prov := strings.TrimSpace(strings.ToLower(cfg.Provider))
	playback := &sdkStreamPlayback{
		runID:    runID,
		provider: prov,
		logPath:  logPath,
		file:     fileHandle,
		renderer: eventrender.ForProvider(prov),
	}
	_ = playback.writeLine(fmt.Sprintf("[%s] info   [dalek] sdk stream started provider=%s run_id=%d",
		time.Now().Format("15:04:05"), playback.provider, playback.runID))
	return playback, nil
}

func (p *sdkStreamPlayback) AppendEvent(ev sdkrunner.Event) error {
	if p == nil {
		return nil
	}
	p.seq++
	steps := p.renderer.Render(p.seq, strings.TrimSpace(ev.Type), strings.TrimSpace(ev.RawJSON), strings.TrimSpace(ev.Text))
	for _, step := range steps {
		lines := formatStepLines(step)
		if lines != "" {
			if err := p.writeLine(lines); err != nil {
				return err
			}
		}
	}
	return nil
}

func formatStepLines(step eventrender.UnifiedStep) string {
	ts := time.UnixMilli(step.Ts).Format("15:04:05")
	tag := stepTag(step.StepType)

	// 日志回放优先写 Detail（完整内容），没有 Detail 时降级为 Summary。
	content := strings.TrimSpace(step.Detail)
	if content != "" {
		content = trimToMaxLines(content, streamPlaybackMaxDetailLines)
	} else {
		content = strings.TrimSpace(step.Summary)
	}
	if content == "" {
		return ""
	}

	return fmt.Sprintf("[%s] %s %s", ts, tag, content)
}

func trimToMaxLines(content string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	lines := strings.Split(content, "\n")
	if len(lines) <= maxLines {
		return content
	}
	hidden := len(lines) - maxLines
	return strings.Join(lines[:maxLines], "\n") + fmt.Sprintf("\n... (%d more lines)", hidden)
}

func stepTag(st eventrender.StepType) string {
	switch st {
	case eventrender.StepThinking:
		return "think "
	case eventrender.StepToolCall:
		return "exec  "
	case eventrender.StepToolResult:
		return "result"
	case eventrender.StepMessage:
		return "msg   "
	case eventrender.StepError:
		return "ERROR "
	case eventrender.StepLifecycle:
		return "info  "
	default:
		return "      "
	}
}

func (p *sdkStreamPlayback) writeLine(line string) error {
	if p == nil {
		return nil
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.file == nil {
		return fmt.Errorf("sdk playback 文件未打开")
	}
	if _, err := p.file.WriteString(line + "\n"); err != nil {
		return err
	}
	return nil
}

func (p *sdkStreamPlayback) Close(_ context.Context) {
	if p == nil {
		return
	}
	_ = p.writeLine(fmt.Sprintf("[%s] info   [dalek] sdk stream finished provider=%s run_id=%d",
		time.Now().Format("15:04:05"), strings.TrimSpace(p.provider), p.runID))

	p.mu.Lock()
	fileHandle := p.file
	p.file = nil
	p.mu.Unlock()

	if fileHandle != nil {
		_ = fileHandle.Close()
	}
}

func (p *sdkStreamPlayback) logFilePath() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.logPath)
}
