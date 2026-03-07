package agentexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dalek/internal/agent/eventrender"
	"dalek/internal/agent/provider"
)

func TestStartSDKStreamPlayback_UsesExplicitLogPath(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "sdk-stream.log")
	playback, err := startSDKStreamPlayback(context.Background(), SDKConfig{
		AgentConfig:   provider.AgentConfig{Provider: "codex"},
		StreamLogPath: logPath,
	}, 42)
	if err != nil {
		t.Fatalf("startSDKStreamPlayback failed: %v", err)
	}
	if playback == nil {
		t.Fatalf("expected non-nil playback")
	}
	if got := strings.TrimSpace(playback.logFilePath()); got != logPath {
		t.Fatalf("unexpected log path: got=%q want=%q", got, logPath)
	}
	playback.Close(context.Background())

	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log failed: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, "sdk stream started") {
		t.Fatalf("expected start marker, got=%q", content)
	}
	if !strings.Contains(content, "sdk stream finished") {
		t.Fatalf("expected finish marker, got=%q", content)
	}
}

func TestStartSDKStreamPlayback_FallbackToWorkDir(t *testing.T) {
	workDir := t.TempDir()
	playback, err := startSDKStreamPlayback(context.Background(), SDKConfig{
		AgentConfig: provider.AgentConfig{Provider: "codex"},
		BaseConfig:  BaseConfig{WorkDir: workDir},
	}, 5)
	if err != nil {
		t.Fatalf("startSDKStreamPlayback failed: %v", err)
	}
	defer playback.Close(context.Background())

	want := filepath.Join(workDir, ".dalek", "runtime", "sdk-stream.log")
	if got := strings.TrimSpace(playback.logFilePath()); got != want {
		t.Fatalf("unexpected log path: got=%q want=%q", got, want)
	}
}

func TestStartSDKStreamPlayback_RequiresPathOrWorkDir(t *testing.T) {
	_, err := startSDKStreamPlayback(context.Background(), SDKConfig{
		AgentConfig: provider.AgentConfig{Provider: "codex"},
	}, 1)
	if err == nil || !strings.Contains(err.Error(), "stream_log_path/work_dir 同时为空") {
		t.Fatalf("expected missing path/workdir error, got=%v", err)
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
