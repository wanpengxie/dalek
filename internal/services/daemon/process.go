package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultPIDFileName  = "daemon.pid"
	defaultLockFileName = "daemon.lock"
	defaultLogFileName  = "daemon.log"
)

var ErrAlreadyRunning = errors.New("daemon 已在运行")

type ProcessPathConfig struct {
	PIDFile  string
	LockFile string
	LogFile  string
}

type ProcessPaths struct {
	HomeDir  string
	PIDFile  string
	LockFile string
	LogFile  string
}

type ProcessStatus struct {
	Running      bool
	PID          int
	PIDFile      string
	LockFile     string
	LogFile      string
	StalePIDFile bool
}

type FileLock struct {
	f        *os.File
	released bool
}

func ResolveProcessPaths(homeDir string, cfg ProcessPathConfig) (ProcessPaths, error) {
	homeDir = strings.TrimSpace(homeDir)
	if homeDir == "" {
		return ProcessPaths{}, fmt.Errorf("home 目录不能为空")
	}
	absHome, err := filepath.Abs(homeDir)
	if err != nil {
		return ProcessPaths{}, err
	}
	return ProcessPaths{
		HomeDir:  absHome,
		PIDFile:  resolveProcessPath(absHome, cfg.PIDFile, defaultPIDFileName),
		LockFile: resolveProcessPath(absHome, cfg.LockFile, defaultLockFileName),
		LogFile:  resolveProcessPath(absHome, cfg.LogFile, defaultLogFileName),
	}, nil
}

func resolveProcessPath(homeDir, raw, fallback string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = fallback
	}
	if filepath.IsAbs(raw) {
		return raw
	}
	return filepath.Join(homeDir, raw)
}

func EnsureProcessPaths(paths ProcessPaths) error {
	for _, p := range []string{paths.PIDFile, paths.LockFile, paths.LogFile} {
		if strings.TrimSpace(p) == "" {
			return fmt.Errorf("进程路径配置不完整")
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
	}
	return nil
}

func AcquireLock(lockFile string) (*FileLock, error) {
	lockFile = strings.TrimSpace(lockFile)
	if lockFile == "" {
		return nil, fmt.Errorf("lock_file 不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(lockFile), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, ErrAlreadyRunning
		}
		return nil, err
	}
	return &FileLock{f: f}, nil
}

func (l *FileLock) Release() error {
	if l == nil || l.released {
		return nil
	}
	l.released = true
	if l.f == nil {
		return nil
	}
	unlockErr := syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	closeErr := l.f.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func WritePID(pidFile string, pid int) error {
	if strings.TrimSpace(pidFile) == "" {
		return fmt.Errorf("pid_file 不能为空")
	}
	if pid <= 0 {
		return fmt.Errorf("pid 非法: %d", pid)
	}
	if err := os.MkdirAll(filepath.Dir(pidFile), 0o755); err != nil {
		return err
	}
	return os.WriteFile(pidFile, []byte(fmt.Sprintf("%d\n", pid)), 0o644)
}

func RemovePID(pidFile string) error {
	pidFile = strings.TrimSpace(pidFile)
	if pidFile == "" {
		return nil
	}
	err := os.Remove(pidFile)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func ReadPID(pidFile string) (int, error) {
	pidFile = strings.TrimSpace(pidFile)
	if pidFile == "" {
		return 0, fmt.Errorf("pid_file 不能为空")
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		return 0, err
	}
	raw := strings.TrimSpace(string(b))
	if raw == "" {
		return 0, fmt.Errorf("pid 文件为空: %s", pidFile)
	}
	pid, err := strconv.Atoi(raw)
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("pid 文件格式非法: %s", pidFile)
	}
	return pid, nil
}

func IsProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func Inspect(paths ProcessPaths) (ProcessStatus, error) {
	st := ProcessStatus{
		PIDFile:  strings.TrimSpace(paths.PIDFile),
		LockFile: strings.TrimSpace(paths.LockFile),
		LogFile:  strings.TrimSpace(paths.LogFile),
	}
	pid, err := ReadPID(st.PIDFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return st, nil
		}
		return st, err
	}
	st.PID = pid
	st.Running = IsProcessAlive(pid)
	st.StalePIDFile = pid > 0 && !st.Running
	return st, nil
}

func TerminatePID(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("pid 非法: %d", pid)
	}
	return syscall.Kill(pid, syscall.SIGTERM)
}

func WaitForExit(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return fmt.Errorf("pid 非法: %d", pid)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		if !IsProcessAlive(pid) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 daemon 退出超时（pid=%d）", pid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
