package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"dalek/internal/app"
)

// gatewayDailyLogger 写入 ~/.dalek/logs/gateway/YYYY-MM-DD.log，同时 tee 到 stderr。
// 每次写入时检查日期，自动切换到新日志文件。
type gatewayDailyLogger struct {
	mu      sync.Mutex
	dir     string
	curDate string
	curFile *os.File
}

func newGatewayDailyLogger() *gatewayDailyLogger {
	home, err := app.ResolveHomeDir("")
	if err != nil || home == "" {
		home = ".dalek"
	}
	dir := filepath.Join(home, "logs", "gateway")
	return &gatewayDailyLogger{dir: dir}
}

// Write 实现 io.Writer：同时写入 stderr 和日志文件。
// 日志文件写入失败不影响 stderr 输出。
func (l *gatewayDailyLogger) Write(p []byte) (n int, err error) {
	// 始终写 stderr
	n, err = os.Stderr.Write(p)

	l.mu.Lock()
	defer l.mu.Unlock()

	f := l.ensureFileLocked()
	if f != nil {
		_, _ = f.Write(p)
	}
	return n, err
}

// Close 关闭当前日志文件。
func (l *gatewayDailyLogger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.curFile != nil {
		err := l.curFile.Close()
		l.curFile = nil
		l.curDate = ""
		return err
	}
	return nil
}

func (l *gatewayDailyLogger) ensureFileLocked() *os.File {
	today := time.Now().Format("2006-01-02")
	if l.curFile != nil && l.curDate == today {
		return l.curFile
	}
	// 日期变更，关闭旧文件
	if l.curFile != nil {
		_ = l.curFile.Close()
		l.curFile = nil
		l.curDate = ""
	}
	if err := os.MkdirAll(l.dir, 0o755); err != nil {
		return nil
	}
	path := filepath.Join(l.dir, today+".log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil
	}
	l.curFile = f
	l.curDate = today
	return f
}

// gatewayLogWriter 返回一个 io.Writer，在 stderr 输出的同时写入日志文件。
// 如果 logger 为 nil，退化为纯 stderr。
func gatewayLogWriter(logger *gatewayDailyLogger) io.Writer {
	if logger == nil {
		return os.Stderr
	}
	return logger
}

// fmtGatewayLog 向 writer 输出一行带时间戳的日志。
func fmtGatewayLog(w io.Writer, format string, args ...any) {
	if w == nil {
		w = os.Stderr
	}
	ts := time.Now().Format("2006-01-02T15:04:05.000Z07:00")
	msg := fmt.Sprintf(format, args...)
	_, _ = fmt.Fprintf(w, "[%s] %s\n", ts, msg)
}
