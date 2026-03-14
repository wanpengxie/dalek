package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"dalek/internal/app"
	"dalek/internal/contracts"
)

func TestCLI_ManagerFocusCommands_DaemonUnavailable(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	prepareDemoProjectWithOneTicket(t, bin, repo, home)
	configureDaemonInternalListenForE2E(t, home, "http://127.0.0.1:1")

	cases := []struct {
		name string
		args []string
	}{
		{
			name: "run",
			args: []string{"-home", home, "-project", "demo", "manager", "run", "--mode", "batch", "--tickets", "1"},
		},
		{
			name: "stop",
			args: []string{"-home", home, "-project", "demo", "manager", "stop", "--focus-id", "1"},
		},
		{
			name: "tail",
			args: []string{"-home", home, "-project", "demo", "manager", "tail", "--focus-id", "1"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runCLI(t, bin, repo, tc.args...)
			if err == nil {
				t.Fatalf("expected manager %s fail when daemon unavailable\nstdout:\n%s\nstderr:\n%s", tc.name, stdout, stderr)
			}
			if strings.TrimSpace(stdout) != "" {
				t.Fatalf("stdout should be empty for manager %s runtime error, got:\n%s", tc.name, stdout)
			}
			if !strings.Contains(stderr, "daemon 不在线") {
				t.Fatalf("stderr should mention daemon unavailable for manager %s:\n%s", tc.name, stderr)
			}
			if !strings.Contains(stderr, "请先执行 dalek daemon start 后重试") {
				t.Fatalf("stderr should contain daemon start guide for manager %s:\n%s", tc.name, stderr)
			}
		})
	}
}

func TestCLI_ManagerShow_DaemonUnavailableFallsBackToReadonlyFocusView(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	h, err := app.OpenHome(home)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	project, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	focusRes, err := project.FocusStart(context.Background(), app.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{1},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	configureDaemonInternalListenForE2E(t, home, "http://127.0.0.1:1")

	textStdout, textStderr, err := runCLI(t, bin, repo, "-home", home, "-project", "demo", "manager", "show")
	if err != nil {
		t.Fatalf("manager show text should fallback to local readonly view: stderr=%s err=%v", textStderr, err)
	}
	if strings.TrimSpace(textStderr) != "" {
		t.Fatalf("manager show text stderr should be empty on readonly fallback, got:\n%s", textStderr)
	}
	if !strings.Contains(textStdout, "[readonly-stale] focus_id=") {
		t.Fatalf("manager show text should include readonly-stale status line, got:\n%s", textStdout)
	}
	if strings.Contains(textStdout, "active_ticket=") {
		t.Fatalf("manager show text should not use legacy active_ticket output, got:\n%s", textStdout)
	}

	stdout, stderr, err := runCLI(t, bin, repo, "-home", home, "-project", "demo", "manager", "show", "-o", "json")
	if err != nil {
		t.Fatalf("manager show should fallback to local readonly view: stderr=%s err=%v", stderr, err)
	}
	if strings.TrimSpace(stderr) != "" {
		t.Fatalf("manager show stderr should be empty on readonly fallback, got:\n%s", stderr)
	}

	var payload struct {
		ReadonlyStale bool `json:"readonly_stale"`
		Focus         struct {
			Run struct {
				ID uint `json:"id"`
			} `json:"run"`
			Items []struct {
				TicketID uint `json:"ticket_id"`
			} `json:"items"`
			ReadonlyStale bool `json:"readonly_stale"`
		} `json:"focus"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("decode manager show json failed: %v\nraw=%s", err, stdout)
	}
	if !payload.ReadonlyStale {
		t.Fatalf("expected readonly_stale=true, got raw=%s", stdout)
	}
	if !payload.Focus.ReadonlyStale {
		t.Fatalf("expected nested focus.readonly_stale=true, got raw=%s", stdout)
	}
	if payload.Focus.Run.ID != focusRes.FocusID {
		t.Fatalf("expected focus id %d, got %d", focusRes.FocusID, payload.Focus.Run.ID)
	}
	if len(payload.Focus.Items) != 1 || payload.Focus.Items[0].TicketID != 1 {
		t.Fatalf("expected readonly focus items to come from FocusRunView, got raw=%s", stdout)
	}

	var raw map[string]any
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		t.Fatalf("decode raw json failed: %v", err)
	}
	focusMap, ok := raw["focus"].(map[string]any)
	if !ok {
		t.Fatalf("expected focus payload object, got raw=%s", stdout)
	}
	if _, ok := focusMap["run"]; !ok {
		t.Fatalf("expected focus.run in readonly fallback payload, got raw=%s", stdout)
	}
	if _, ok := focusMap["mode"]; ok {
		t.Fatalf("readonly fallback should not use legacy FocusRun payload, got raw=%s", stdout)
	}
}
