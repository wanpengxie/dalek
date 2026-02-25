package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveProcessPaths(t *testing.T) {
	root := t.TempDir()
	paths, err := ResolveProcessPaths(root, ProcessPathConfig{
		PIDFile:  "runtime/daemon.pid",
		LockFile: "",
		LogFile:  "logs/daemon.log",
	})
	if err != nil {
		t.Fatalf("ResolveProcessPaths failed: %v", err)
	}
	if paths.PIDFile != filepath.Join(root, "runtime", "daemon.pid") {
		t.Fatalf("unexpected pid path: %s", paths.PIDFile)
	}
	if paths.LockFile != filepath.Join(root, defaultLockFileName) {
		t.Fatalf("unexpected lock path: %s", paths.LockFile)
	}
	if paths.LogFile != filepath.Join(root, "logs", "daemon.log") {
		t.Fatalf("unexpected log path: %s", paths.LogFile)
	}
}

func TestAcquireLockExclusive(t *testing.T) {
	lockFile := filepath.Join(t.TempDir(), "daemon.lock")
	lock1, err := AcquireLock(lockFile)
	if err != nil {
		t.Fatalf("AcquireLock first failed: %v", err)
	}
	defer func() { _ = lock1.Release() }()

	lock2, err := AcquireLock(lockFile)
	if !errors.Is(err, ErrAlreadyRunning) {
		if lock2 != nil {
			_ = lock2.Release()
		}
		t.Fatalf("AcquireLock second should fail with ErrAlreadyRunning, got err=%v", err)
	}
}

func TestInspectStalePIDFile(t *testing.T) {
	root := t.TempDir()
	paths, err := ResolveProcessPaths(root, ProcessPathConfig{})
	if err != nil {
		t.Fatalf("ResolveProcessPaths failed: %v", err)
	}
	if err := WritePID(paths.PIDFile, 999999); err != nil {
		t.Fatalf("WritePID failed: %v", err)
	}
	st, err := Inspect(paths)
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if st.Running {
		t.Fatalf("expected running=false for stale pid")
	}
	if !st.StalePIDFile {
		t.Fatalf("expected stale pid file")
	}
	if st.PID != 999999 {
		t.Fatalf("unexpected pid: %d", st.PID)
	}
}

func TestInspectRunningCurrentProcess(t *testing.T) {
	root := t.TempDir()
	paths, err := ResolveProcessPaths(root, ProcessPathConfig{})
	if err != nil {
		t.Fatalf("ResolveProcessPaths failed: %v", err)
	}
	if err := WritePID(paths.PIDFile, os.Getpid()); err != nil {
		t.Fatalf("WritePID failed: %v", err)
	}
	st, err := Inspect(paths)
	if err != nil {
		t.Fatalf("Inspect failed: %v", err)
	}
	if !st.Running {
		t.Fatalf("expected current process to be alive")
	}
	if st.StalePIDFile {
		t.Fatalf("current pid should not be stale")
	}
}
