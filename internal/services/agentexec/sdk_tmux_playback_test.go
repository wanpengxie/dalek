package agentexec

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"dalek/internal/agent/eventrender"
	"dalek/internal/infra"
)

type tmuxCall struct {
	socket string
	target string
	text   string
}

type fakePlaybackTmux struct {
	activePane infra.PaneInfo
	activeErr  error
	listPanes  []infra.PaneInfo
	listErr    error

	sendLineCalls []tmuxCall
	sendKeyCalls  []tmuxCall
}

func (f *fakePlaybackTmux) NewSession(ctx context.Context, socket, name, startDir string) error {
	return nil
}

func (f *fakePlaybackTmux) NewSessionWithCommand(ctx context.Context, socket, name, startDir string, cmd []string) error {
	return nil
}

func (f *fakePlaybackTmux) KillSession(ctx context.Context, socket, name string) error {
	return nil
}

func (f *fakePlaybackTmux) KillServer(ctx context.Context, socket string) error {
	return nil
}

func (f *fakePlaybackTmux) SendKeys(ctx context.Context, socket, target, keys string) error {
	f.sendKeyCalls = append(f.sendKeyCalls, tmuxCall{socket: strings.TrimSpace(socket), target: strings.TrimSpace(target), text: strings.TrimSpace(keys)})
	return nil
}

func (f *fakePlaybackTmux) SendKeysLiteral(ctx context.Context, socket, target, text string) error {
	return nil
}

func (f *fakePlaybackTmux) SendLine(ctx context.Context, socket, target, line string) error {
	f.sendLineCalls = append(f.sendLineCalls, tmuxCall{socket: strings.TrimSpace(socket), target: strings.TrimSpace(target), text: line})
	return nil
}

func (f *fakePlaybackTmux) CapturePane(ctx context.Context, socket, target string, lines int) (string, error) {
	return "", nil
}

func (f *fakePlaybackTmux) PipePaneToFile(ctx context.Context, socket, target, filePath string) error {
	return nil
}

func (f *fakePlaybackTmux) StopPipePane(ctx context.Context, socket, target string) error {
	return nil
}

func (f *fakePlaybackTmux) ListSessions(ctx context.Context, socket string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (f *fakePlaybackTmux) ListPanes(ctx context.Context, socket, session string) ([]infra.PaneInfo, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]infra.PaneInfo(nil), f.listPanes...), nil
}

func (f *fakePlaybackTmux) ActivePane(ctx context.Context, socket, session string) (infra.PaneInfo, error) {
	if f.activeErr != nil {
		return infra.PaneInfo{}, f.activeErr
	}
	return f.activePane, nil
}

func (f *fakePlaybackTmux) AttachCmd(socket, session string) *exec.Cmd {
	return nil
}

func TestStartSDKTmuxPlayback_AlwaysUsesActivePane(t *testing.T) {
	fakeTmux := &fakePlaybackTmux{
		activePane: infra.PaneInfo{
			PaneID:         "%9",
			CurrentCommand: "zsh",
		},
	}
	logPath := filepath.Join(t.TempDir(), "sdk-stream.log")
	playback, err := startSDKTmuxPlayback(context.Background(), SDKConfig{
		Provider:    "codex",
		Tmux:        fakeTmux,
		TmuxSocket:  "dalek",
		TmuxSession: "ts-demo",
		TmuxTarget:  "%123", // 故意传入过期 target，应被 active pane 覆盖。
		TmuxLogPath: logPath,
	}, 42)
	if err != nil {
		t.Fatalf("startSDKTmuxPlayback failed: %v", err)
	}
	defer playback.Close(context.Background())

	if len(fakeTmux.sendLineCalls) != 1 {
		t.Fatalf("expected 1 send-line call, got=%d", len(fakeTmux.sendLineCalls))
	}
	call := fakeTmux.sendLineCalls[0]
	if call.target != "%9" {
		t.Fatalf("expected active pane target %%9, got=%q", call.target)
	}
	if !strings.Contains(call.text, "tail -n 0 -F") {
		t.Fatalf("expected tail command, got=%q", call.text)
	}
}

func TestSDKTmuxPlaybackClose_SendsCtrlCTwice(t *testing.T) {
	fakeTmux := &fakePlaybackTmux{
		activePane: infra.PaneInfo{
			PaneID:         "%19",
			CurrentCommand: "zsh",
		},
	}
	logPath := filepath.Join(t.TempDir(), "sdk-stream.log")
	playback, err := startSDKTmuxPlayback(context.Background(), SDKConfig{
		Provider:    "codex",
		Tmux:        fakeTmux,
		TmuxSocket:  "dalek",
		TmuxSession: "ts-demo",
		TmuxLogPath: logPath,
	}, 77)
	if err != nil {
		t.Fatalf("startSDKTmuxPlayback failed: %v", err)
	}

	playback.Close(context.Background())

	if len(fakeTmux.sendKeyCalls) < 2 {
		t.Fatalf("expected at least 2 send-keys calls, got=%d", len(fakeTmux.sendKeyCalls))
	}
	last := fakeTmux.sendKeyCalls[len(fakeTmux.sendKeyCalls)-2:]
	if last[0].text != "C-c" || last[1].text != "C-c" {
		t.Fatalf("expected last two keys are C-c, got=%q and %q", last[0].text, last[1].text)
	}
}

func TestStartSDKTmuxPlayback_RejectsNonShellPane(t *testing.T) {
	fakeTmux := &fakePlaybackTmux{
		activePane: infra.PaneInfo{
			PaneID:         "%10",
			CurrentCommand: "node",
		},
	}
	_, err := startSDKTmuxPlayback(context.Background(), SDKConfig{
		Provider:    "codex",
		Tmux:        fakeTmux,
		TmuxSocket:  "dalek",
		TmuxSession: "ts-demo",
		TmuxLogPath: filepath.Join(t.TempDir(), "sdk-stream.log"),
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "前台命令非 shell") {
		t.Fatalf("expected non-shell pane error, got=%v", err)
	}
}

func TestStartSDKTmuxPlayback_RejectsInputOffPane(t *testing.T) {
	fakeTmux := &fakePlaybackTmux{
		activePane: infra.PaneInfo{
			PaneID:         "%11",
			CurrentCommand: "zsh",
			InputOff:       true,
		},
	}
	_, err := startSDKTmuxPlayback(context.Background(), SDKConfig{
		Provider:    "codex",
		Tmux:        fakeTmux,
		TmuxSocket:  "dalek",
		TmuxSession: "ts-demo",
		TmuxLogPath: filepath.Join(t.TempDir(), "sdk-stream.log"),
	}, 2)
	if err == nil || !strings.Contains(err.Error(), "input_off=1") {
		t.Fatalf("expected input_off error, got=%v", err)
	}
}

func TestStartSDKTmuxPlayback_RejectsInModePane(t *testing.T) {
	fakeTmux := &fakePlaybackTmux{
		activePane: infra.PaneInfo{
			PaneID:         "%12",
			CurrentCommand: "zsh",
			InMode:         true,
			Mode:           "copy-mode",
		},
	}
	_, err := startSDKTmuxPlayback(context.Background(), SDKConfig{
		Provider:    "codex",
		Tmux:        fakeTmux,
		TmuxSocket:  "dalek",
		TmuxSession: "ts-demo",
		TmuxLogPath: filepath.Join(t.TempDir(), "sdk-stream.log"),
	}, 3)
	if err == nil || !strings.Contains(err.Error(), "处于 mode=") {
		t.Fatalf("expected in-mode error, got=%v", err)
	}
}

func TestStartSDKTmuxPlayback_RequiresSessionForInjectabilityCheck(t *testing.T) {
	fakeTmux := &fakePlaybackTmux{}
	_, err := startSDKTmuxPlayback(context.Background(), SDKConfig{
		Provider:   "codex",
		Tmux:       fakeTmux,
		TmuxSocket: "dalek",
		TmuxTarget: "%20",
	}, 4)
	if err == nil || !strings.Contains(err.Error(), "tmux session 为空") {
		t.Fatalf("expected missing session error, got=%v", err)
	}
}

func TestFormatStepLines_DetailTruncatedToTenLines(t *testing.T) {
	detail := strings.Join([]string{
		"line-01",
		"line-02",
		"line-03",
		"line-04",
		"line-05",
		"line-06",
		"line-07",
		"line-08",
		"line-09",
		"line-10",
		"line-11",
		"line-12",
	}, "\n")
	got := formatStepLines(eventrender.UnifiedStep{
		Ts:       0,
		StepType: eventrender.StepMessage,
		Detail:   detail,
	})
	if !strings.Contains(got, "line-10") {
		t.Fatalf("expected keep line-10, got=%q", got)
	}
	if strings.Contains(got, "line-11") || strings.Contains(got, "line-12") {
		t.Fatalf("expected truncate after 10 lines, got=%q", got)
	}
	if !strings.Contains(got, "... (2 more lines)") {
		t.Fatalf("expected hidden line hint, got=%q", got)
	}
}

func TestFormatStepLines_SummaryKeepsAllLines(t *testing.T) {
	summary := strings.Join([]string{
		"s1", "s2", "s3", "s4", "s5", "s6", "s7", "s8", "s9", "s10", "s11",
	}, "\n")
	got := formatStepLines(eventrender.UnifiedStep{
		Ts:       0,
		StepType: eventrender.StepMessage,
		Summary:  summary,
	})
	if !strings.Contains(got, "s11") {
		t.Fatalf("expected summary keep all lines, got=%q", got)
	}
	if strings.Contains(got, "more lines") {
		t.Fatalf("expected summary without truncation hint, got=%q", got)
	}
}
