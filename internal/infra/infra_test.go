package infra

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type stubRunner struct {
	runFn         func(ctx context.Context, dir string, name string, args ...string) (string, error)
	runExitCodeFn func(ctx context.Context, dir string, name string, args ...string) (int, string, string, error)
}

func (s *stubRunner) Run(ctx context.Context, dir string, name string, args ...string) (string, error) {
	if s.runFn == nil {
		return "", errors.New("unexpected Run call")
	}
	return s.runFn(ctx, dir, name, args...)
}

func (s *stubRunner) RunExitCode(ctx context.Context, dir string, name string, args ...string) (int, string, string, error) {
	if s.runExitCodeFn == nil {
		return -1, "", "", errors.New("unexpected RunExitCode call")
	}
	return s.runExitCodeFn(ctx, dir, name, args...)
}

func TestEnsureLineInFile_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "git", "info", "exclude")

	if err := EnsureLineInFile(path, ".dalek/"); err != nil {
		t.Fatalf("first write failed: %v", err)
	}
	if err := EnsureLineInFile(path, ".dalek/"); err != nil {
		t.Fatalf("second write failed: %v", err)
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 1 || strings.TrimSpace(lines[0]) != ".dalek/" {
		t.Fatalf("unexpected file content: %q", string(b))
	}
}

func TestGitExecClient_WorktreeBranchCheckedOut_Parse(t *testing.T) {
	r := &stubRunner{
		runFn: func(ctx context.Context, dir string, name string, args ...string) (string, error) {
			return `worktree /repo/main
HEAD 111111
branch refs/heads/main

worktree /repo/tickets/t1
HEAD 222222
branch refs/heads/ts/demo-ticket-1
`, nil
		},
	}
	git := NewGitExecClientWithRunner(r)

	ok, wt, err := git.WorktreeBranchCheckedOut("/repo", "ts/demo-ticket-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !ok || wt != "/repo/tickets/t1" {
		t.Fatalf("unexpected result: ok=%v wt=%q", ok, wt)
	}

	ok, wt, err = git.WorktreeBranchCheckedOut("/repo", "ts/demo-ticket-999")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok || wt != "" {
		t.Fatalf("unexpected not-found result: ok=%v wt=%q", ok, wt)
	}
}

func TestGitExecClient_WorktreeDirty_IgnoresDalekControlPlane(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不可用，跳过真实 worktree dirty 测试: %v", err)
	}

	repoRoot := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v stderr=%s", args, err, string(out))
		}
	}
	run("init")
	run("config", "user.email", "dalek-test@example.com")
	run("config", "user.name", "dalek-test")
	if err := os.WriteFile(filepath.Join(repoRoot, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "init")

	git := NewGitExecClient()

	if err := os.MkdirAll(filepath.Join(repoRoot, ".dalek"), 0o755); err != nil {
		t.Fatalf("mkdir .dalek failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, ".dalek", "state.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write .dalek/state.json failed: %v", err)
	}
	dirty, err := git.WorktreeDirty(repoRoot)
	if err != nil {
		t.Fatalf("WorktreeDirty failed: %v", err)
	}
	if dirty {
		t.Fatalf(".dalek control-plane files should be ignored by WorktreeDirty")
	}

	if err := os.WriteFile(filepath.Join(repoRoot, "feature.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatalf("write feature.txt failed: %v", err)
	}
	dirty, err = git.WorktreeDirty(repoRoot)
	if err != nil {
		t.Fatalf("WorktreeDirty failed after feature.txt: %v", err)
	}
	if !dirty {
		t.Fatalf("product file changes should still mark worktree dirty")
	}
}

func TestTmuxExecClient_ListSessions_NoServerReturnsEmpty(t *testing.T) {
	r := &stubRunner{
		runExitCodeFn: func(ctx context.Context, dir string, name string, args ...string) (int, string, string, error) {
			return 1, "", "no server running", nil
		},
	}
	tmux := NewTmuxExecClientWithRunner(r)

	sessions, err := tmux.ListSessions(context.Background(), "dalek")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("expected empty sessions, got: %#v", sessions)
	}
}

func TestTmuxExecClient_NewSessionWithCommand_FailureTriggersCleanup(t *testing.T) {
	calls := make([]string, 0, 8)
	r := &stubRunner{
		runFn: func(ctx context.Context, dir string, name string, args ...string) (string, error) {
			_ = ctx
			_ = dir
			if name != "tmux" {
				return "", errors.New("unexpected binary")
			}
			calls = append(calls, "new-session")
			return "", errors.New("new-session failed")
		},
		runExitCodeFn: func(ctx context.Context, dir string, name string, args ...string) (int, string, string, error) {
			_ = ctx
			_ = dir
			if name != "tmux" {
				return -1, "", "", errors.New("unexpected binary")
			}
			cmd := ""
			for _, a := range args {
				if a == "list-panes" || a == "kill-session" {
					cmd = a
					break
				}
			}
			switch cmd {
			case "list-panes":
				calls = append(calls, "list-panes")
				return 1, "", "no session", nil
			case "kill-session":
				calls = append(calls, "kill-session")
				return 0, "", "", nil
			default:
				return -1, "", "", errors.New("unexpected command")
			}
		},
	}
	tmux := NewTmuxExecClientWithRunner(r)

	err := tmux.NewSessionWithCommand(context.Background(), "dalek", "demo", "/tmp/demo", []string{"bash"})
	if err == nil {
		t.Fatalf("NewSessionWithCommand should fail")
	}

	want := []string{"new-session", "list-panes", "kill-session"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected command sequence: got=%v want=%v", calls, want)
	}
}

func TestTmuxExecClient_KillSession_StopsPipeBeforeKill(t *testing.T) {
	calls := make([]string, 0, 8)
	r := &stubRunner{
		runExitCodeFn: func(ctx context.Context, dir string, name string, args ...string) (int, string, string, error) {
			_ = ctx
			_ = dir
			if name != "tmux" {
				return -1, "", "", errors.New("unexpected binary")
			}
			cmd := ""
			for _, a := range args {
				if a == "list-panes" || a == "pipe-pane" || a == "kill-session" {
					cmd = a
					break
				}
			}
			switch cmd {
			case "list-panes":
				calls = append(calls, "list-panes")
				return 0, "%1\n%2", "", nil
			case "pipe-pane":
				target := ""
				for i := 0; i < len(args)-1; i++ {
					if args[i] == "-t" {
						target = strings.TrimSpace(args[i+1])
						break
					}
				}
				calls = append(calls, "pipe-pane:"+target)
				return 0, "", "", nil
			case "kill-session":
				calls = append(calls, "kill-session")
				return 0, "", "", nil
			default:
				return -1, "", "", errors.New("unexpected command")
			}
		},
	}
	tmux := NewTmuxExecClientWithRunner(r)

	if err := tmux.KillSession(context.Background(), "dalek", "demo"); err != nil {
		t.Fatalf("KillSession failed: %v", err)
	}

	want := []string{"list-panes", "pipe-pane:%1", "pipe-pane:%2", "kill-session"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected command sequence: got=%v want=%v", calls, want)
	}
}

func TestTmuxExecClient_KillSession_StillKillsWhenListPanesFails(t *testing.T) {
	calls := make([]string, 0, 4)
	r := &stubRunner{
		runExitCodeFn: func(ctx context.Context, dir string, name string, args ...string) (int, string, string, error) {
			_ = ctx
			_ = dir
			if name != "tmux" {
				return -1, "", "", errors.New("unexpected binary")
			}
			cmd := ""
			for _, a := range args {
				if a == "list-panes" || a == "kill-session" {
					cmd = a
					break
				}
			}
			switch cmd {
			case "list-panes":
				calls = append(calls, "list-panes")
				return 1, "", "no session", nil
			case "kill-session":
				calls = append(calls, "kill-session")
				return 0, "", "", nil
			default:
				return -1, "", "", errors.New("unexpected command")
			}
		},
	}
	tmux := NewTmuxExecClientWithRunner(r)

	if err := tmux.KillSession(context.Background(), "dalek", "missing"); err != nil {
		t.Fatalf("KillSession failed: %v", err)
	}

	want := []string{"list-panes", "kill-session"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected command sequence: got=%v want=%v", calls, want)
	}
}

func TestTmuxExecClient_KillServer_StopsPipeBeforeKill(t *testing.T) {
	calls := make([]string, 0, 8)
	r := &stubRunner{
		runExitCodeFn: func(ctx context.Context, dir string, name string, args ...string) (int, string, string, error) {
			_ = ctx
			_ = dir
			if name != "tmux" {
				return -1, "", "", errors.New("unexpected binary")
			}
			cmd := ""
			for _, a := range args {
				if a == "list-panes" || a == "pipe-pane" || a == "kill-server" {
					cmd = a
					break
				}
			}
			switch cmd {
			case "list-panes":
				calls = append(calls, "list-panes")
				return 0, "%9\n%10", "", nil
			case "pipe-pane":
				target := ""
				for i := 0; i < len(args)-1; i++ {
					if args[i] == "-t" {
						target = strings.TrimSpace(args[i+1])
						break
					}
				}
				calls = append(calls, "pipe-pane:"+target)
				return 0, "", "", nil
			case "kill-server":
				calls = append(calls, "kill-server")
				return 0, "", "", nil
			default:
				return -1, "", "", errors.New("unexpected command")
			}
		},
	}
	tmux := NewTmuxExecClientWithRunner(r)

	if err := tmux.KillServer(context.Background(), "dalek"); err != nil {
		t.Fatalf("KillServer failed: %v", err)
	}

	want := []string{"list-panes", "pipe-pane:%9", "pipe-pane:%10", "kill-server"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("unexpected command sequence: got=%v want=%v", calls, want)
	}
}

func TestShellQuote_StripsControlCharacters(t *testing.T) {
	got := ShellQuote("line1\nline2\rline3\x00ok")
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") || strings.Contains(got, "\x00") {
		t.Fatalf("quoted result should not contain control chars: %q", got)
	}
	if got != "'line1 line2 line3ok'" {
		t.Fatalf("unexpected quoted result: %q", got)
	}
}

func TestBuildBashScriptWithEnv_StripsControlCharacters(t *testing.T) {
	got := BuildBashScriptWithEnv(map[string]string{
		"DEMO": "a\nb\rc\x00d",
	}, "echo ok")
	if strings.Contains(got, "\n") || strings.Contains(got, "\r") || strings.Contains(got, "\x00") {
		t.Fatalf("script should not contain control chars: %q", got)
	}
	if !strings.Contains(got, "export DEMO='a b cd'") {
		t.Fatalf("unexpected env export: %q", got)
	}
}
