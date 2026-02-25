package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"dalek/internal/app"
)

const daemonStartTimeoutDefault = 8 * time.Second

func cmdDaemon(args []string) {
	if len(args) == 0 {
		printDaemonUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "start":
		cmdDaemonStart(args[1:])
	case "stop":
		cmdDaemonStop(args[1:])
	case "restart":
		cmdDaemonRestart(args[1:])
	case "status":
		cmdDaemonStatus(args[1:])
	case "logs":
		cmdDaemonLogs(args[1:])
	case "-h", "--help", "help":
		printDaemonUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 daemon 子命令: %s", sub),
			"daemon 命令组仅支持固定子命令",
			"运行 dalek daemon --help 查看可用命令",
		)
	}
}

func printDaemonUsage() {
	printGroupUsage("Daemon 进程管理", "dalek daemon <command> [flags]", []string{
		"start    启动 daemon（默认后台）",
		"stop     停止 daemon（SIGTERM）",
		"restart  重启 daemon",
		"status   查看 daemon 运行状态",
		"logs     查看 daemon 日志",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek daemon <command> --help\" for more information.")
}

func cmdDaemonStart(args []string) {
	fs := flag.NewFlagSet("daemon start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"启动 daemon",
			"dalek daemon start [--foreground] [--timeout 8s] [--output text|json]",
			"dalek daemon start",
			"dalek daemon start --foreground",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	foreground := fs.Bool("foreground", false, "前台运行（用于调试）")
	timeout := fs.Duration("timeout", daemonStartTimeoutDefault, "后台启动等待时间（例如 8s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "daemon start 参数解析失败", "运行 dalek daemon start --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek daemon start --timeout 8s")
	}

	h, paths := mustResolveDaemonPaths(out, *home)
	if *foreground {
		if err := runDaemonForeground(paths); err != nil {
			exitRuntimeError(out, "daemon 前台运行失败", err.Error(), "检查日志并修正后重试")
		}
		return
	}
	pid, err := startDaemonBackground(h.Root, paths, *timeout)
	if err != nil {
		exitRuntimeError(out, "daemon 启动失败", err.Error(), "检查日志路径与端口占用后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":   "dalek.daemon.start.v1",
			"running":  true,
			"pid":      pid,
			"pid_file": paths.PIDFile,
			"log_file": paths.LogFile,
			"mode":     "background",
		})
		return
	}
	fmt.Printf("daemon started: pid=%d\n", pid)
	fmt.Printf("pid_file=%s\n", paths.PIDFile)
	fmt.Printf("log_file=%s\n", paths.LogFile)
}

func cmdDaemonStop(args []string) {
	fs := flag.NewFlagSet("daemon stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"停止 daemon（SIGTERM）",
			"dalek daemon stop [--timeout 10s] [--output text|json]",
			"dalek daemon stop",
			"dalek daemon stop --timeout 20s",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	timeout := fs.Duration("timeout", 10*time.Second, "停止等待超时（例如 10s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "daemon stop 参数解析失败", "运行 dalek daemon stop --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek daemon stop --timeout 10s")
	}

	_, paths := mustResolveDaemonPaths(out, *home)
	pid, err := stopDaemon(paths, *timeout)
	if err != nil {
		exitRuntimeError(out, "daemon 停止失败", err.Error(), "检查 daemon 状态后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":   "dalek.daemon.stop.v1",
			"running":  false,
			"pid":      pid,
			"pid_file": paths.PIDFile,
		})
		return
	}
	if pid == 0 {
		fmt.Println("daemon not running")
		return
	}
	fmt.Printf("daemon stopped: pid=%d\n", pid)
}

func cmdDaemonRestart(args []string) {
	fs := flag.NewFlagSet("daemon restart", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"重启 daemon",
			"dalek daemon restart [--foreground] [--timeout 10s] [--output text|json]",
			"dalek daemon restart",
			"dalek daemon restart --foreground",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	foreground := fs.Bool("foreground", false, "前台运行（用于调试）")
	timeout := fs.Duration("timeout", 10*time.Second, "重启停止阶段等待超时（例如 10s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "daemon restart 参数解析失败", "运行 dalek daemon restart --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek daemon restart --timeout 10s")
	}

	h, paths := mustResolveDaemonPaths(out, *home)
	_, err := stopDaemon(paths, *timeout)
	if err != nil {
		exitRuntimeError(out, "daemon 重启失败", err.Error(), "停止旧进程失败，请先修复后重试")
	}

	if *foreground {
		if err := runDaemonForeground(paths); err != nil {
			exitRuntimeError(out, "daemon 前台运行失败", err.Error(), "检查日志并修正后重试")
		}
		return
	}

	pid, err := startDaemonBackground(h.Root, paths, daemonStartTimeoutDefault)
	if err != nil {
		exitRuntimeError(out, "daemon 重启失败", err.Error(), "检查日志路径与端口占用后重试")
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":   "dalek.daemon.restart.v1",
			"running":  true,
			"pid":      pid,
			"pid_file": paths.PIDFile,
			"log_file": paths.LogFile,
		})
		return
	}
	fmt.Printf("daemon restarted: pid=%d\n", pid)
}

func cmdDaemonStatus(args []string) {
	fs := flag.NewFlagSet("daemon status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 daemon 状态",
			"dalek daemon status [--output text|json]",
			"dalek daemon status",
			"dalek daemon status -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "daemon status 参数解析失败", "运行 dalek daemon status --help 查看参数")
	out := parseOutputOrExit(*output, true)

	_, paths := mustResolveDaemonPaths(out, *home)
	st, err := app.InspectDaemon(paths)
	if err != nil {
		exitRuntimeError(out, "读取 daemon 状态失败", err.Error(), "检查 pid_file 权限与内容后重试")
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":         "dalek.daemon.status.v1",
			"running":        st.Running,
			"pid":            st.PID,
			"stale_pid_file": st.StalePIDFile,
			"pid_file":       st.PIDFile,
			"lock_file":      st.LockFile,
			"log_file":       st.LogFile,
		})
		return
	}

	if st.Running {
		fmt.Printf("daemon running: pid=%d\n", st.PID)
	} else {
		fmt.Println("daemon stopped")
	}
	if st.StalePIDFile {
		fmt.Printf("warning: stale pid file detected (%s)\n", st.PIDFile)
	}
	fmt.Printf("pid_file=%s\n", st.PIDFile)
	fmt.Printf("lock_file=%s\n", st.LockFile)
	fmt.Printf("log_file=%s\n", st.LogFile)
}

func cmdDaemonLogs(args []string) {
	fs := flag.NewFlagSet("daemon logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"查看 daemon 日志",
			"dalek daemon logs [--follow] [--lines 200]",
			"dalek daemon logs",
			"dalek daemon logs --follow",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	follow := fs.Bool("follow", false, "持续跟随日志输出")
	lines := fs.Int("lines", 200, "输出最近 N 行（<=0 表示完整输出）")
	parseFlagSetOrExit(fs, args, globalOutput, "daemon logs 参数解析失败", "运行 dalek daemon logs --help 查看参数")

	_, paths := mustResolveDaemonPaths(outputText, *home)
	if err := printDaemonLogs(paths.LogFile, *lines, *follow); err != nil {
		exitRuntimeError(outputText, "读取 daemon 日志失败", err.Error(), "确认 daemon 已启动且日志文件可读")
	}
}

func mustResolveDaemonPaths(out cliOutputFormat, homeFlag string) (*app.Home, app.DaemonPaths) {
	homeDir, err := app.ResolveHomeDir(homeFlag)
	if err != nil {
		exitRuntimeError(out, "解析 Home 目录失败", err.Error(), "通过 --home 指定有效目录，或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}
	paths, err := h.ResolveDaemonPaths()
	if err != nil {
		exitRuntimeError(out, "解析 daemon 配置失败", err.Error(), "检查 ~/.dalek/config.json 的 daemon 段")
	}
	return h, paths
}

func runDaemonForeground(paths app.DaemonPaths) error {
	if err := app.EnsureDaemonPaths(paths); err != nil {
		return err
	}
	logFile, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()

	writer := io.Writer(logFile)
	if isInteractiveTTY(os.Stderr) {
		writer = io.MultiWriter(os.Stderr, logFile)
	}
	logger := log.New(writer, "daemon ", log.LstdFlags|log.Lmicroseconds)
	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return app.RunDaemon(sigCtx, paths, logger)
}

func startDaemonBackground(homeDir string, paths app.DaemonPaths, timeout time.Duration) (int, error) {
	if err := app.EnsureDaemonPaths(paths); err != nil {
		return 0, err
	}
	st, err := app.InspectDaemon(paths)
	if err != nil {
		return 0, err
	}
	if st.Running {
		return st.PID, fmt.Errorf("daemon 已在运行（pid=%d）", st.PID)
	}
	if st.StalePIDFile {
		_ = app.RemoveDaemonPID(paths)
	}

	logFile, err := os.OpenFile(paths.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() { _ = logFile.Close() }()

	exe, err := os.Executable()
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(exe, "--home", homeDir, "daemon", "start", "--foreground")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	childPID := 0
	if cmd.Process != nil {
		childPID = cmd.Process.Pid
		_ = cmd.Process.Release()
	}

	deadline := time.Now().Add(timeout)
	for {
		st, serr := app.InspectDaemon(paths)
		if serr == nil && st.Running {
			return st.PID, nil
		}
		if time.Now().After(deadline) {
			tail, _ := readDaemonLogTail(paths.LogFile, 20)
			tail = strings.TrimSpace(tail)
			if tail == "" {
				tail = "（日志为空）"
			}
			return 0, fmt.Errorf("等待 daemon 启动超时（child_pid=%d）\n最近日志:\n%s", childPID, tail)
		}
		time.Sleep(120 * time.Millisecond)
	}
}

func isInteractiveTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func stopDaemon(paths app.DaemonPaths, timeout time.Duration) (int, error) {
	st, err := app.InspectDaemon(paths)
	if err != nil {
		return 0, err
	}
	if !st.Running {
		if st.StalePIDFile {
			_ = app.RemoveDaemonPID(paths)
		}
		return 0, nil
	}
	if err := app.TerminateDaemonPID(st.PID); err != nil && !errors.Is(err, syscall.ESRCH) {
		return st.PID, err
	}
	if err := app.WaitDaemonExit(st.PID, timeout); err != nil && !errors.Is(err, syscall.ESRCH) {
		return st.PID, err
	}
	_ = app.RemoveDaemonPID(paths)
	return st.PID, nil
}

func printDaemonLogs(path string, lines int, follow bool) error {
	initial, err := readDaemonLogTail(path, lines)
	if err != nil {
		return err
	}
	if strings.TrimSpace(initial) != "" {
		fmt.Print(initial)
	}
	if !follow {
		return nil
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	offset, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-sigCtx.Done():
			return nil
		case <-ticker.C:
			info, err := os.Stat(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return err
			}
			if info.Size() < offset {
				_ = f.Close()
				f, err = os.Open(path)
				if err != nil {
					return err
				}
				offset = 0
			}
			if info.Size() == offset {
				continue
			}
			if _, err := f.Seek(offset, io.SeekStart); err != nil {
				return err
			}
			n, err := io.CopyN(os.Stdout, f, info.Size()-offset)
			if err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			offset += n
		}
	}
}

func readDaemonLogTail(path string, lines int) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if lines <= 0 {
		return string(b), nil
	}
	content := strings.ReplaceAll(string(b), "\r\n", "\n")
	parts := strings.Split(content, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	if len(parts) == 0 {
		return "", nil
	}
	return strings.Join(parts, "\n") + "\n", nil
}
