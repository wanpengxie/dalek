package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dalek/internal/app"
)

func cmdTmux(args []string) {
	if len(args) == 0 {
		printTmuxUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "sockets":
		cmdTmuxSockets(args[1:])
	case "prune-sockets":
		cmdTmuxPruneSockets(args[1:])
	case "sessions":
		cmdTmuxSessions(args[1:])
	case "kill-server":
		cmdTmuxKillServer(args[1:])
	case "kill-session":
		cmdTmuxKillSession(args[1:])
	case "kill-prefix":
		cmdTmuxKillPrefix(args[1:])
	case "-h", "--help", "help":
		printTmuxUsage()
		os.Exit(0)
	default:
		if sub == "ls" {
			exitUsageError(globalOutput,
				"旧命令已移除: tmux ls",
				"tmux 会话查询命令已统一为 sessions",
				"改用：dalek tmux sessions",
			)
		}
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 tmux 子命令: %s", sub),
			"tmux 命令组仅支持固定子命令",
			"运行 dalek tmux --help 查看可用命令",
		)
	}
}

func printTmuxUsage() {
	printGroupUsage("Tmux 基础设施管理", "dalek tmux <command> [flags]", []string{
		"sockets        列出 tmux sockets",
		"sessions       列出 tmux sessions",
		"prune-sockets  清理 stale sockets",
		"kill-server    关闭指定 tmux server",
		"kill-session   关闭指定 session",
		"kill-prefix    批量关闭匹配前缀的 sessions",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek tmux <command> --help\" for more information.")
}

func tmuxTmpDir() string {
	if v := strings.TrimSpace(os.Getenv("TMUX_TMPDIR")); v != "" {
		return v
	}
	return "/tmp"
}

func tmuxSocketDir(tmpDir string, uid int) string {
	return app.TmuxSocketDir(tmpDir, uid)
}

func listTmuxSockets(tmpDir string) ([]string, error) {
	return app.ListTmuxSocketFiles(tmpDir)
}

func cmdTmuxSockets(args []string) {
	fs := flag.NewFlagSet("tmux sockets", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出 tmux sockets",
			"dalek tmux sockets [--all] [--prefix dalek] [--output text|json]",
			"dalek tmux sockets",
			"dalek tmux sockets --all -o json",
		)
	}
	tmpDir := fs.String("tmpdir", "", "tmux socket 临时目录（可选；默认 $TMUX_TMPDIR 或 /tmp）")
	all := fs.Bool("all", false, "列出所有 socket 文件（包含 stale 的；默认只显示运行中的 server）")
	prefix := fs.String("prefix", "", "只显示以该前缀开头的 socket 名（可选；例如 dalek）")
	timeout := fs.Duration("timeout", 500*time.Millisecond, "探测单个 socket 的超时（例如 500ms）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "tmux sockets 参数解析失败", "运行 dalek tmux sockets --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek tmux sockets --timeout 500ms")
	}
	socks, err := listTmuxSockets(*tmpDir)
	if err != nil {
		exitRuntimeError(out, "读取 tmux sockets 失败", err.Error(), "检查 tmux 临时目录权限后重试")
	}

	filterPrefix := strings.TrimSpace(*prefix)
	type item struct {
		name   string
		alive  bool
		hasErr bool
	}
	var items []item
	for _, sock := range socks {
		sock = strings.TrimSpace(sock)
		if sock == "" {
			continue
		}
		if filterPrefix != "" && !strings.HasPrefix(sock, filterPrefix) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		m, err := app.ListTmuxSessions(ctx, sock)
		cancel()
		it := item{name: sock}
		if err != nil {
			it.hasErr = true
		} else if len(m) > 0 {
			it.alive = true
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].name < items[j].name })

	type socketItem struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	jsonItems := make([]socketItem, 0, len(items))
	for _, it := range items {
		st := "stale"
		if it.alive {
			st = "alive"
		} else if it.hasErr {
			st = "error"
		}
		if !*all && st != "alive" {
			continue
		}
		jsonItems = append(jsonItems, socketItem{Name: it.name, Status: st})
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.tmux.sockets.v1",
			"sockets": jsonItems,
		})
		return
	}

	if *all {
		if len(jsonItems) == 0 {
			fmt.Println("(empty)")
			return
		}
		for _, it := range jsonItems {
			fmt.Printf("%s\t%s\n", it.Name, it.Status)
		}
		return
	}

	if len(jsonItems) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, it := range jsonItems {
		fmt.Println(it.Name)
	}
}

func cmdTmuxPruneSockets(args []string) {
	fs := flag.NewFlagSet("tmux prune-sockets", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"清理 stale sockets",
			"dalek tmux prune-sockets [--prefix dalek] [--dry-run]",
			"dalek tmux prune-sockets --prefix dalek",
			"dalek tmux prune-sockets --dry-run",
		)
	}
	tmpDir := fs.String("tmpdir", "", "tmux socket 临时目录（可选；默认 $TMUX_TMPDIR 或 /tmp）")
	prefix := fs.String("prefix", "dalek", "只清理以该前缀开头的 socket 文件（默认 dalek）")
	dryRun := fs.Bool("dry-run", false, "只打印将要删除的 socket 文件，不执行")
	timeout := fs.Duration("timeout", 500*time.Millisecond, "探测单个 socket 的超时（例如 500ms）")
	parseFlagSetOrExit(fs, args, globalOutput, "tmux prune-sockets 参数解析失败", "运行 dalek tmux prune-sockets --help 查看参数")
	filterPrefix := strings.TrimSpace(*prefix)
	if filterPrefix == "" {
		exitUsageError(globalOutput, "缺少必填参数 --prefix", "--prefix 不能为空（避免误删）", "例如: dalek tmux prune-sockets --prefix dalek")
	}
	if *timeout <= 0 {
		exitUsageError(globalOutput, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek tmux prune-sockets --timeout 500ms")
	}

	dir := tmuxSocketDir(*tmpDir, 0)
	socks, err := listTmuxSockets(*tmpDir)
	if err != nil {
		exitRuntimeError(globalOutput, "读取 tmux sockets 失败", err.Error(), "检查 tmux 临时目录权限后重试")
	}
	if len(socks) == 0 {
		fmt.Println("(empty)")
		return
	}
	type del struct {
		name string
		path string
	}
	var dels []del
	for _, sock := range socks {
		sock = strings.TrimSpace(sock)
		if sock == "" {
			continue
		}
		if !strings.HasPrefix(sock, filterPrefix) {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		m, err := app.ListTmuxSessions(ctx, sock)
		cancel()
		if err != nil {
			// 探测失败也不删，避免误删正在运行的 server。
			continue
		}
		if len(m) > 0 {
			// server 仍在运行，不删
			continue
		}
		dels = append(dels, del{name: sock, path: filepath.Join(dir, sock)})
	}
	sort.Slice(dels, func(i, j int) bool { return dels[i].name < dels[j].name })

	if len(dels) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, d := range dels {
		fmt.Println(d.name)
	}
	if *dryRun {
		fmt.Printf("dry_run: would_delete=%d\n", len(dels))
		return
	}
	deleted := 0
	for _, d := range dels {
		if err := os.Remove(d.path); err == nil {
			deleted++
		}
	}
	fmt.Printf("deleted=%d\n", deleted)
}

func cmdTmuxSessions(args []string) {
	fs := flag.NewFlagSet("tmux sessions", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"列出 tmux sessions",
			"dalek tmux sessions (--socket NAME | --all) [--output text|json]",
			"dalek tmux sessions --socket dalek",
			"dalek tmux sessions --all -o json",
		)
	}
	socket := fs.String("socket", "", "tmux socket 名（例如 dalek 或 dalek_e2e）")
	all := fs.Bool("all", false, "列出所有 socket 下的 sessions")
	prefix := fs.String("prefix", "", "只显示以该前缀开头的 session（可选；例如 ts-）")
	timeout := fs.Duration("timeout", 2*time.Second, "超时（例如 2s）")
	tmpDir := fs.String("tmpdir", "", "tmux socket 临时目录（仅 -all 需要；默认 $TMUX_TMPDIR 或 /tmp）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "tmux sessions 参数解析失败", "运行 dalek tmux sessions --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek tmux sessions --timeout 2s")
	}

	filterPrefix := strings.TrimSpace(*prefix)

	listOne := func(sock string) ([]string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		m, err := app.ListTmuxSessions(ctx, sock)
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(m))
		for name := range m {
			if filterPrefix != "" && !strings.HasPrefix(name, filterPrefix) {
				continue
			}
			names = append(names, name)
		}
		sort.Strings(names)
		return names, nil
	}

	if *all {
		socks, err := listTmuxSockets(*tmpDir)
		if err != nil {
			exitRuntimeError(out, "读取 tmux sockets 失败", err.Error(), "检查 tmux 临时目录权限后重试")
		}
		type sessionItem struct {
			Socket  string `json:"socket"`
			Session string `json:"session"`
		}
		items := make([]sessionItem, 0)
		for _, sock := range socks {
			names, err := listOne(sock)
			if err != nil {
				// 某些 socket 可能是 stale 文件，忽略即可。
				continue
			}
			for _, name := range names {
				items = append(items, sessionItem{Socket: sock, Session: name})
			}
		}
		if out == outputJSON {
			printJSONOrExit(map[string]any{
				"schema":   "dalek.tmux.sessions.v1",
				"sessions": items,
			})
			return
		}
		if len(items) == 0 {
			fmt.Println("(empty)")
			return
		}
		for _, it := range items {
			fmt.Printf("%s\t%s\n", it.Socket, it.Session)
		}
		return
	}

	sock := strings.TrimSpace(*socket)
	if sock == "" {
		exitUsageError(out, "缺少必填参数 --socket", "tmux sessions 在未使用 --all 时必须指定 socket", "dalek tmux sessions --socket dalek")
	}
	names, err := listOne(sock)
	if err != nil {
		exitRuntimeError(out, "读取 tmux sessions 失败", err.Error(), "确认 socket 存在并重试")
	}
	if out == outputJSON {
		type sessionItem struct {
			Socket  string `json:"socket"`
			Session string `json:"session"`
		}
		items := make([]sessionItem, 0, len(names))
		for _, name := range names {
			items = append(items, sessionItem{Socket: sock, Session: name})
		}
		printJSONOrExit(map[string]any{
			"schema":   "dalek.tmux.sessions.v1",
			"sessions": items,
		})
		return
	}
	if len(names) == 0 {
		fmt.Println("(empty)")
		return
	}
	for _, name := range names {
		fmt.Println(name)
	}
}

func cmdTmuxKillServer(args []string) {
	fs := flag.NewFlagSet("tmux kill-server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"关闭指定 tmux server",
			"dalek tmux kill-server --socket <name>",
			"dalek tmux kill-server --socket dalek",
			"dalek tmux kill-server --socket dalek --timeout 10s",
		)
	}
	socket := fs.String("socket", "", "tmux socket 名（必填）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时（例如 5s）")
	parseFlagSetOrExit(fs, args, globalOutput, "tmux kill-server 参数解析失败", "运行 dalek tmux kill-server --help 查看参数")
	sock := strings.TrimSpace(*socket)
	if sock == "" {
		exitUsageError(globalOutput, "缺少必填参数 --socket", "tmux kill-server 需要 socket 名", "dalek tmux kill-server --socket dalek")
	}
	if *timeout <= 0 {
		exitUsageError(globalOutput, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek tmux kill-server --timeout 5s")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	_ = app.KillTmuxServer(ctx, sock)
	fmt.Printf("killed server: %s\n", sock)
}

func cmdTmuxKillSession(args []string) {
	fs := flag.NewFlagSet("tmux kill-session", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"关闭指定 tmux session",
			"dalek tmux kill-session --socket <name> --session <session>",
			"dalek tmux kill-session --socket dalek --session ts-t1-demo",
			"dalek tmux kill-session --socket dalek --session ts-t1-demo --timeout 10s",
		)
	}
	socket := fs.String("socket", "", "tmux socket 名（必填）")
	session := fs.String("session", "", "session 名（必填）")
	timeout := fs.Duration("timeout", 5*time.Second, "超时（例如 5s）")
	parseFlagSetOrExit(fs, args, globalOutput, "tmux kill-session 参数解析失败", "运行 dalek tmux kill-session --help 查看参数")
	sock := strings.TrimSpace(*socket)
	name := strings.TrimSpace(*session)
	if sock == "" || name == "" {
		exitUsageError(globalOutput, "缺少必填参数", "tmux kill-session 需要 --socket 和 --session", "dalek tmux kill-session --socket dalek --session ts-t1-demo")
	}
	if *timeout <= 0 {
		exitUsageError(globalOutput, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek tmux kill-session --timeout 5s")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	_ = app.KillTmuxSession(ctx, sock, name)
	fmt.Printf("killed session: %s\t%s\n", sock, name)
}

func cmdTmuxKillPrefix(args []string) {
	fs := flag.NewFlagSet("tmux kill-prefix", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"批量关闭匹配前缀的 sessions",
			"dalek tmux kill-prefix [--socket NAME | --all] [--prefix ts-] [--dry-run]",
			"dalek tmux kill-prefix --socket dalek --prefix ts-",
			"dalek tmux kill-prefix --all --dry-run",
		)
	}
	socket := fs.String("socket", "", "tmux socket 名（-all=false 时必填）")
	all := fs.Bool("all", false, "清理所有 socket")
	prefix := fs.String("prefix", "ts-", "要清理的 session 前缀（默认 ts-）")
	dryRun := fs.Bool("dry-run", false, "只打印将要 kill 的 session，不执行")
	timeout := fs.Duration("timeout", 10*time.Second, "超时（例如 10s）")
	tmpDir := fs.String("tmpdir", "", "tmux socket 临时目录（仅 -all 需要；默认 $TMUX_TMPDIR 或 /tmp）")
	parseFlagSetOrExit(fs, args, globalOutput, "tmux kill-prefix 参数解析失败", "运行 dalek tmux kill-prefix --help 查看参数")

	pre := strings.TrimSpace(*prefix)
	if pre == "" {
		exitUsageError(globalOutput, "缺少必填参数 --prefix", "--prefix 不能为空", "例如: dalek tmux kill-prefix --prefix ts-")
	}
	if *timeout <= 0 {
		exitUsageError(globalOutput, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek tmux kill-prefix --timeout 10s")
	}
	type target struct {
		socket  string
		session string
	}
	var targets []target

	gather := func(sock string) error {
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		m, err := app.ListTmuxSessions(ctx, sock)
		if err != nil {
			return err
		}
		for name := range m {
			if strings.HasPrefix(name, pre) {
				targets = append(targets, target{socket: sock, session: name})
			}
		}
		return nil
	}

	if *all {
		socks, err := listTmuxSockets(*tmpDir)
		if err != nil {
			exitRuntimeError(globalOutput, "读取 tmux sockets 失败", err.Error(), "检查 tmux 临时目录权限后重试")
		}
		for _, sock := range socks {
			_ = gather(sock)
		}
	} else {
		sock := strings.TrimSpace(*socket)
		if sock == "" {
			exitUsageError(globalOutput, "缺少必填参数 --socket", "未使用 --all 时必须指定 --socket", "dalek tmux kill-prefix --socket dalek --prefix ts-")
		}
		_ = gather(sock)
	}

	if len(targets) == 0 {
		fmt.Println("(empty)")
		return
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].socket != targets[j].socket {
			return targets[i].socket < targets[j].socket
		}
		return targets[i].session < targets[j].session
	})

	killed := 0
	for _, t := range targets {
		fmt.Printf("%s\t%s\n", t.socket, t.session)
		if *dryRun {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		_ = app.KillTmuxSession(ctx, t.socket, t.session)
		cancel()
		killed++
	}
	if *dryRun {
		fmt.Printf("dry_run: would_kill=%d\n", len(targets))
		return
	}
	fmt.Printf("killed=%d\n", killed)
}
