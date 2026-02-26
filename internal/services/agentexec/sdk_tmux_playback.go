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
	"dalek/internal/infra"
)

type sdkTmuxPlayback struct {
	runID    uint
	provider string

	tmux   infra.TmuxClient
	socket string
	target string

	logPath string

	file     *os.File
	started  bool
	mu       sync.Mutex
	renderer eventrender.Renderer
	seq      int
}

const tmuxPlaybackMaxDetailLines = 10

func startSDKTmuxPlayback(ctx context.Context, cfg SDKConfig, runID uint) (*sdkTmuxPlayback, error) {
	if cfg.Tmux == nil {
		return nil, nil
	}
	target := strings.TrimSpace(cfg.TmuxTarget)
	session := strings.TrimSpace(cfg.TmuxSession)
	if target == "" && session == "" {
		return nil, nil
	}
	if session == "" {
		return nil, fmt.Errorf("tmux session 为空，无法确认 pane 可注入")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	socket := strings.TrimSpace(cfg.TmuxSocket)
	if socket == "" {
		socket = "dalek"
	}
	pane := infra.PaneInfo{}
	// 始终实时选择活动 pane，避免使用过期 target（例如 worker 预先计算后发生 pane 漂移）。
	tctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	picked, pickedPane, err := infra.PickObservationTarget(cfg.Tmux, tctx, socket, session)
	if err != nil {
		return nil, fmt.Errorf("解析 tmux target 失败: %w", err)
	}
	target = strings.TrimSpace(picked)
	pane = pickedPane
	if target == "" || strings.TrimSpace(pane.PaneID) == "" {
		return nil, fmt.Errorf("解析 tmux target 失败: 未拿到活动 pane（session=%s）", strings.TrimSpace(session))
	}
	if target == "" {
		return nil, fmt.Errorf("tmux target 为空")
	}
	if pane.InputOff {
		return nil, fmt.Errorf("目标 pane input_off=1（pane=%s）", target)
	}
	if pane.InMode {
		return nil, fmt.Errorf("目标 pane 处于 mode=%s（pane=%s）", strings.TrimSpace(pane.Mode), target)
	}
	if pane.PaneID != "" && !isShellLikePaneCommand(pane.CurrentCommand) {
		return nil, fmt.Errorf("目标 pane 前台命令非 shell（pane=%s cmd=%s），无法安全注入", target, strings.TrimSpace(pane.CurrentCommand))
	}

	logPath := strings.TrimSpace(cfg.TmuxLogPath)
	if logPath == "" {
		workDir := strings.TrimSpace(cfg.WorkDir)
		if workDir == "" {
			return nil, fmt.Errorf("tmux_log_path/work_dir 同时为空")
		}
		logPath = filepath.Join(workDir, ".dalek", "runtime", "sdk-stream.log")
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return nil, err
	}
	fileHandle, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}

	prov := strings.TrimSpace(strings.ToLower(cfg.Provider))
	playback := &sdkTmuxPlayback{
		runID:    runID,
		provider: prov,
		tmux:     cfg.Tmux,
		socket:   socket,
		target:   strings.TrimSpace(target),
		logPath:  logPath,
		file:     fileHandle,
		renderer: eventrender.ForProvider(prov),
	}
	_ = playback.writeLine(fmt.Sprintf("[%s] info   [dalek] sdk stream started provider=%s run_id=%d",
		time.Now().Format("15:04:05"), playback.provider, playback.runID))

	header := fmt.Sprintf("[dalek] sdk stream started provider=%s run_id=%d log=%s", playback.provider, playback.runID, playback.logPath)
	tailScript := "echo " + shellQuote(header) + "; tail -n 0 -F " + shellQuote(playback.logPath)
	tailCommand := "bash -lc " + shellQuote(tailScript)
	if err := cfg.Tmux.SendLine(ctx, socket, playback.target, tailCommand); err != nil {
		_ = fileHandle.Close()
		return nil, fmt.Errorf("tmux 启动 sdk tail 失败: %w", err)
	}
	playback.started = true
	return playback, nil
}

func isShellLikePaneCommand(cmd string) bool {
	switch strings.TrimSpace(strings.ToLower(cmd)) {
	case "bash", "zsh", "sh", "dash", "ksh", "ash", "fish", "nu", "pwsh", "powershell", "xonsh":
		return true
	default:
		return false
	}
}

func (p *sdkTmuxPlayback) AppendEvent(ev sdkrunner.Event) error {
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

	// tmux pane 用 Detail（完整内容），没有 Detail 时降级到 Summary
	content := strings.TrimSpace(step.Detail)
	if content != "" {
		content = trimToMaxLines(content, tmuxPlaybackMaxDetailLines)
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

func (p *sdkTmuxPlayback) writeLine(line string) error {
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

func (p *sdkTmuxPlayback) Close(ctx context.Context) {
	if p == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_ = p.writeLine(fmt.Sprintf("[%s] info   [dalek] sdk stream finished provider=%s run_id=%d",
		time.Now().Format("15:04:05"), strings.TrimSpace(p.provider), p.runID))

	p.mu.Lock()
	fileHandle := p.file
	shouldStop := p.started
	tmuxClient := p.tmux
	socket := strings.TrimSpace(p.socket)
	target := strings.TrimSpace(p.target)
	p.mu.Unlock()

	// 两阶段 Ctrl+C：
	// 1) 若 pane 处于 copy-mode 等模式，第一次 C-c 往往只会退出 mode；
	// 2) 第二次 C-c 才能真正打到前台 tail -F 进程。
	if shouldStop && tmuxClient != nil && target != "" {
		if err := tmuxClient.SendKeys(ctx, socket, target, "C-c"); err == nil {
			_ = tmuxClient.SendKeys(ctx, socket, target, "C-c")
		}
	}

	p.mu.Lock()
	p.file = nil
	p.mu.Unlock()

	if fileHandle != nil {
		_ = fileHandle.Close()
	}
}

func (p *sdkTmuxPlayback) targetPane() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.target)
}

func (p *sdkTmuxPlayback) logFilePath() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.logPath)
}
