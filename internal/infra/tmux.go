package infra

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type PaneInfo struct {
	PaneID         string
	CurrentCommand string
	InMode         bool
	Mode           string
	InputOff       bool
	Pipe           bool
}

type TmuxClient interface {
	NewSession(ctx context.Context, socket, name, startDir string) error
	NewSessionWithCommand(ctx context.Context, socket, name, startDir string, cmd []string) error
	KillSession(ctx context.Context, socket, name string) error
	KillServer(ctx context.Context, socket string) error
	SendKeys(ctx context.Context, socket, target, keys string) error
	SendKeysLiteral(ctx context.Context, socket, target, text string) error
	SendLine(ctx context.Context, socket, target, line string) error
	CapturePane(ctx context.Context, socket, target string, lines int) (string, error)
	PipePaneToFile(ctx context.Context, socket, target, filePath string) error
	StopPipePane(ctx context.Context, socket, target string) error
	ListSessions(ctx context.Context, socket string) (map[string]bool, error)
	ListPanes(ctx context.Context, socket, session string) ([]PaneInfo, error)
	ActivePane(ctx context.Context, socket, session string) (PaneInfo, error)
	AttachCmd(socket, session string) *exec.Cmd
}

type TmuxExecClient struct {
	runner CommandRunner
}

func NewTmuxExecClient() *TmuxExecClient {
	return &TmuxExecClient{runner: NewExecRunner()}
}

func NewTmuxExecClientWithRunner(r CommandRunner) *TmuxExecClient {
	if r == nil {
		r = NewExecRunner()
	}
	return &TmuxExecClient{runner: r}
}

func tmuxSocket(socket string) string {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		return "dalek"
	}
	return socket
}

func (c *TmuxExecClient) NewSession(ctx context.Context, socket, name, startDir string) error {
	_, err := c.runner.Run(ctx, "", "tmux", "-L", tmuxSocket(socket), "new-session", "-d", "-s", strings.TrimSpace(name), "-c", strings.TrimSpace(startDir))
	return err
}

func (c *TmuxExecClient) NewSessionWithCommand(ctx context.Context, socket, name, startDir string, cmd []string) error {
	args := []string{"-L", tmuxSocket(socket), "new-session", "-d", "-s", strings.TrimSpace(name), "-c", strings.TrimSpace(startDir)}
	args = append(args, cmd...)
	_, err := c.runner.Run(ctx, "", "tmux", args...)
	if err != nil {
		_ = c.KillSession(context.Background(), socket, name)
		return err
	}
	return nil
}

func (c *TmuxExecClient) KillSession(ctx context.Context, socket, name string) error {
	_ = c.stopPipePaneBySession(ctx, socket, name)
	_, _, _, err := c.runner.RunExitCode(ctx, "", "tmux", "-L", tmuxSocket(socket), "kill-session", "-t", strings.TrimSpace(name))
	return err
}

func (c *TmuxExecClient) KillServer(ctx context.Context, socket string) error {
	_ = c.stopPipePaneAll(ctx, socket)
	_, _, _, err := c.runner.RunExitCode(ctx, "", "tmux", "-L", tmuxSocket(socket), "kill-server")
	return err
}

func (c *TmuxExecClient) SendKeys(ctx context.Context, socket, target, keys string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("tmux target 不能为空")
	}
	keys = strings.TrimSpace(keys)
	if keys == "" {
		return fmt.Errorf("tmux key 不能为空")
	}
	_, err := c.runner.Run(ctx, "", "tmux", "-L", tmuxSocket(socket), "send-keys", "-t", target, keys)
	return err
}

func (c *TmuxExecClient) SendKeysLiteral(ctx context.Context, socket, target, text string) error {
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("tmux target 不能为空")
	}
	_, err := c.runner.Run(ctx, "", "tmux", "-L", tmuxSocket(socket), "send-keys", "-t", target, "-l", text)
	return err
}

func (c *TmuxExecClient) SendLine(ctx context.Context, socket, target, line string) error {
	if err := c.SendKeysLiteral(ctx, socket, target, line); err != nil {
		return err
	}
	return c.SendKeys(ctx, socket, target, "Enter")
}

func (c *TmuxExecClient) CapturePane(ctx context.Context, socket, target string, lines int) (string, error) {
	if lines <= 0 {
		lines = 200
	}
	start := fmt.Sprintf("-%d", lines)
	return c.runner.Run(ctx, "", "tmux", "-L", tmuxSocket(socket), "capture-pane", "-p", "-t", strings.TrimSpace(target), "-S", start, "-J")
}

func (c *TmuxExecClient) PipePaneToFile(ctx context.Context, socket, target, filePath string) error {
	cmd := "cat >> " + ShellQuote(strings.TrimSpace(filePath))
	_, _, _, err := c.runner.RunExitCode(ctx, "", "tmux", "-L", tmuxSocket(socket), "pipe-pane", "-o", "-t", strings.TrimSpace(target), cmd)
	return err
}

func (c *TmuxExecClient) StopPipePane(ctx context.Context, socket, target string) error {
	_, _, _, err := c.runner.RunExitCode(ctx, "", "tmux", "-L", tmuxSocket(socket), "pipe-pane", "-t", strings.TrimSpace(target))
	return err
}

func (c *TmuxExecClient) stopPipePaneBySession(ctx context.Context, socket, session string) error {
	session = strings.TrimSpace(session)
	if session == "" {
		return nil
	}
	code, out, _, err := c.runner.RunExitCode(ctx, "", "tmux", "-L", tmuxSocket(socket), "list-panes", "-t", session, "-F", "#{pane_id}")
	if err != nil {
		return err
	}
	if code != 0 {
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		paneID := strings.TrimSpace(line)
		if paneID == "" {
			continue
		}
		if perr := c.StopPipePane(ctx, socket, paneID); perr != nil {
			return perr
		}
	}
	return nil
}

func (c *TmuxExecClient) stopPipePaneAll(ctx context.Context, socket string) error {
	code, out, _, err := c.runner.RunExitCode(ctx, "", "tmux", "-L", tmuxSocket(socket), "list-panes", "-a", "-F", "#{pane_id}")
	if err != nil {
		return err
	}
	if code != 0 {
		return nil
	}
	for _, line := range strings.Split(out, "\n") {
		paneID := strings.TrimSpace(line)
		if paneID == "" {
			continue
		}
		if perr := c.StopPipePane(ctx, socket, paneID); perr != nil {
			return perr
		}
	}
	return nil
}

func (c *TmuxExecClient) ListSessions(ctx context.Context, socket string) (map[string]bool, error) {
	code, out, _, err := c.runner.RunExitCode(ctx, "", "tmux", "-L", tmuxSocket(socket), "list-sessions", "-F", "#{session_name}")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return map[string]bool{}, nil
	}
	m := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		m[line] = true
	}
	return m, nil
}

func (c *TmuxExecClient) ListPanes(ctx context.Context, socket, session string) ([]PaneInfo, error) {
	socket = tmuxSocket(socket)
	session = strings.TrimSpace(session)
	if session == "" {
		return nil, fmt.Errorf("session 不能为空")
	}
	const sep = "\x1f"
	format := strings.Join([]string{
		"#{pane_id}",
		"#{pane_current_command}",
		"#{pane_in_mode}",
		"#{pane_mode}",
		"#{pane_input_off}",
		"#{pane_pipe}",
	}, sep)
	out, err := c.runner.Run(ctx, "", "tmux", "-L", socket, "list-panes", "-t", session, "-F", format)
	if err != nil {
		return nil, err
	}
	items := make([]PaneInfo, 0)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fs := strings.Split(line, sep)
		if len(fs) < 6 {
			continue
		}
		items = append(items, PaneInfo{
			PaneID:         strings.TrimSpace(fs[0]),
			CurrentCommand: strings.TrimSpace(fs[1]),
			InMode:         parseBool01(fs[2]),
			Mode:           strings.TrimSpace(fs[3]),
			InputOff:       parseBool01(fs[4]),
			Pipe:           parseBool01(fs[5]),
		})
	}
	return items, nil
}

func (c *TmuxExecClient) ActivePane(ctx context.Context, socket, session string) (PaneInfo, error) {
	socket = tmuxSocket(socket)
	session = strings.TrimSpace(session)
	if session == "" {
		return PaneInfo{}, fmt.Errorf("session 不能为空")
	}
	const sep = "\x1f"
	format := strings.Join([]string{
		"#{pane_id}",
		"#{pane_current_command}",
		"#{pane_in_mode}",
		"#{pane_mode}",
		"#{pane_input_off}",
		"#{pane_pipe}",
	}, sep)
	out, err := c.runner.Run(ctx, "", "tmux", "-L", socket, "display-message", "-p", "-t", session, format)
	if err != nil {
		return PaneInfo{}, err
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return PaneInfo{}, fmt.Errorf("tmux display-message 输出为空")
	}
	fs := strings.Split(out, sep)
	if len(fs) < 6 {
		return PaneInfo{}, fmt.Errorf("tmux display-message 输出格式不符合预期: %q", out)
	}
	return PaneInfo{
		PaneID:         strings.TrimSpace(fs[0]),
		CurrentCommand: strings.TrimSpace(fs[1]),
		InMode:         parseBool01(fs[2]),
		Mode:           strings.TrimSpace(fs[3]),
		InputOff:       parseBool01(fs[4]),
		Pipe:           parseBool01(fs[5]),
	}, nil
}

func (c *TmuxExecClient) AttachCmd(socket, session string) *exec.Cmd {
	socket = tmuxSocket(socket)
	session = strings.TrimSpace(session)
	cmd := exec.Command("tmux", "-L", socket, "attach", "-t", session)

	term := strings.TrimSpace(os.Getenv("TERM"))
	insideTmux := strings.TrimSpace(os.Getenv("TMUX")) != ""
	if term == "" || term == "dumb" || insideTmux {
		env := os.Environ()
		if term == "" || term == "dumb" {
			env = append(env, "TERM=xterm-256color")
		}
		if insideTmux {
			env = append(env, "TMUX=")
		}
		cmd.Env = env
	}
	return cmd
}

func parseBool01(s string) bool {
	s = strings.TrimSpace(strings.ToLower(s))
	return s == "1" || s == "true" || s == "on" || s == "yes"
}
