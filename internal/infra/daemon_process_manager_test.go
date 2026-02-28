package infra

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDaemonProcessManager_StartProcess_CaptureOutput(t *testing.T) {
	t.Parallel()

	m := NewDaemonProcessManager()
	logPath := filepath.Join(t.TempDir(), "worker.log")
	handle, err := m.StartProcess(context.Background(), WorkerProcessSpec{
		Command: "sh",
		Args:    []string{"-c", "echo hello-daemon-runtime"},
		LogPath: logPath,
	})
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}
	waitForProcessStopped(t, m, handle, 2*time.Second)

	out, err := m.CaptureOutput(context.Background(), handle, 20)
	if err != nil {
		t.Fatalf("CaptureOutput failed: %v", err)
	}
	if !strings.Contains(out, "hello-daemon-runtime") {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestDaemonProcessManager_InterruptProcess(t *testing.T) {
	t.Parallel()

	m := NewDaemonProcessManager()
	logPath := filepath.Join(t.TempDir(), "worker.log")
	handle, err := m.StartProcess(context.Background(), WorkerProcessSpec{
		Command: "sh",
		Args: []string{
			"-c",
			"echo boot; trap 'echo interrupted; exit 0' INT; while true; do sleep 1; done",
		},
		LogPath: logPath,
	})
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}
	defer func() {
		_ = m.StopProcess(context.Background(), handle, 500*time.Millisecond)
	}()

	waitForProcessStarted(t, m, handle, 2*time.Second)
	waitForLogContains(t, logPath, "boot", 2*time.Second)
	if err := m.InterruptProcess(context.Background(), handle); err != nil {
		t.Fatalf("InterruptProcess failed: %v", err)
	}
	waitForProcessStopped(t, m, handle, 3*time.Second)
}

func TestDaemonProcessManager_StopProcess_GracefulAndForced(t *testing.T) {
	t.Parallel()

	m := NewDaemonProcessManager()
	t.Run("graceful_term", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "worker-graceful.log")
		handle, err := m.StartProcess(context.Background(), WorkerProcessSpec{
			Command: "sh",
			Args: []string{
				"-c",
				"echo boot; trap 'echo terminated; exit 0' TERM; while true; do sleep 1; done",
			},
			LogPath: logPath,
		})
		if err != nil {
			t.Fatalf("StartProcess failed: %v", err)
		}
		waitForProcessStarted(t, m, handle, 2*time.Second)
		waitForLogContains(t, logPath, "boot", 2*time.Second)

		if err := m.StopProcess(context.Background(), handle, 1200*time.Millisecond); err != nil {
			t.Fatalf("StopProcess failed: %v", err)
		}
		waitForProcessStopped(t, m, handle, 2*time.Second)
	})

	t.Run("forced_kill", func(t *testing.T) {
		logPath := filepath.Join(t.TempDir(), "worker-forced.log")
		handle, err := m.StartProcess(context.Background(), WorkerProcessSpec{
			Command: "sh",
			Args: []string{
				"-c",
				"echo boot; trap '' TERM; while true; do sleep 1; done",
			},
			LogPath: logPath,
		})
		if err != nil {
			t.Fatalf("StartProcess failed: %v", err)
		}
		waitForProcessStarted(t, m, handle, 2*time.Second)
		waitForLogContains(t, logPath, "boot", 2*time.Second)

		if err := m.StopProcess(context.Background(), handle, 300*time.Millisecond); err != nil {
			t.Fatalf("StopProcess failed: %v", err)
		}
		waitForProcessStopped(t, m, handle, 2*time.Second)
	})
}

func TestDaemonProcessManager_AttachCmd(t *testing.T) {
	t.Parallel()

	m := NewDaemonProcessManager()
	handle := WorkerProcessHandle{
		LogPath: "/tmp/demo-worker.log",
	}
	cmd := m.AttachCmd(handle)
	if cmd == nil {
		t.Fatalf("AttachCmd should not be nil")
	}
	if len(cmd.Args) < 5 {
		t.Fatalf("unexpected attach args: %v", cmd.Args)
	}
	if cmd.Args[0] != "tail" || cmd.Args[1] != "-n" || cmd.Args[3] != "-F" {
		t.Fatalf("unexpected attach command: %v", cmd.Args)
	}
	if cmd.Args[4] != handle.LogPath {
		t.Fatalf("unexpected attach log path: %v", cmd.Args)
	}
}

func waitForProcessStarted(t *testing.T, m *DaemonProcessManager, handle WorkerProcessHandle, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive, err := m.IsAlive(context.Background(), handle)
		if err == nil && alive {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("process did not become alive in %s (pid=%d)", timeout, handle.PID)
}

func waitForProcessStopped(t *testing.T, m *DaemonProcessManager, handle WorkerProcessHandle, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		alive, err := m.IsAlive(context.Background(), handle)
		if err == nil && !alive {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("process did not stop in %s (pid=%d)", timeout, handle.PID)
}

func waitForLogContains(t *testing.T, path string, needle string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		b, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(b), needle) {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("log did not contain %q in %s (path=%s)", needle, timeout, path)
}
