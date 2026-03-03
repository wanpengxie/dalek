package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/app"
	"dalek/internal/services/feishudoc"
)

var (
	globalHome   string
	globalOutput = outputText
)

const authTimeoutDefault = 8 * time.Second

func main() {
	gfs := flag.NewFlagSet("feishu", flag.ContinueOnError)
	gfs.SetOutput(os.Stderr)
	gh := gfs.String("home", "", "dalek Home 目录（默认 ~/.dalek，env: DALEK_HOME）")
	goOutput := gfs.String("output", string(outputText), "输出格式: text|json（默认 text）")
	gfs.StringVar(goOutput, "o", string(outputText), "输出格式: text|json（默认 text）")
	help := gfs.Bool("help", false, "显示帮助")
	helpShort := gfs.Bool("h", false, "显示帮助")
	if err := gfs.Parse(os.Args[1:]); err != nil {
		if err == flag.ErrHelp {
			usage(0)
		}
		exitUsageError(outputText,
			"命令参数解析失败",
			err.Error(),
			"运行 feishu --help 查看完整用法",
		)
	}
	if *help || *helpShort {
		usage(0)
	}

	globalHome = strings.TrimSpace(*gh)
	globalOutput = parseOutputOrExit(*goOutput, true)

	rest := gfs.Args()
	if len(rest) == 0 {
		usage(2)
	}

	switch rest[0] {
	case "auth":
		cmdAuth(rest[1:])
	case "doc":
		cmdDoc(rest[1:])
	case "wiki":
		cmdWiki(rest[1:])
	case "perm":
		cmdPerm(rest[1:])
	case "help", "-h", "--help":
		usage(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知命令: %s", rest[0]),
			"仅支持 auth|doc|wiki|perm",
			"运行 feishu --help 查看可用命令",
		)
	}
}

func usage(code int) {
	out := os.Stderr
	fmt.Fprintln(out, "feishu - 飞书文档协同 CLI")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  feishu <command> [flags]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  auth     验证飞书凭据并检查 tenant_access_token 获取")
	fmt.Fprintln(out, "  doc      文档操作（create/read/write/ls）")
	fmt.Fprintln(out, "  wiki     知识空间操作（ls/nodes/create）")
	fmt.Fprintln(out, "  perm     权限管理（share/add/ls）")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Global Flags:")
	fmt.Fprintln(out, "  --home string          dalek Home 目录 (默认 ~/.dalek, env: DALEK_HOME)")
	fmt.Fprintln(out, "  --output, -o string    输出格式: text|json (默认 text)")
	fmt.Fprintln(out, "  -h, --help             显示帮助")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Use \"feishu <command> --help\" for more information about a command.")
	os.Exit(code)
}

func cmdAuth(args []string) {
	fs := flag.NewFlagSet("feishu auth", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"验证飞书凭据",
			"feishu auth [--home <path>] [--timeout 8s] [--output text|json]",
			"feishu auth",
			"feishu auth --timeout 15s -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	timeout := fs.Duration("timeout", authTimeoutDefault, "认证请求超时（例如 8s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu auth 参数解析失败", "运行 feishu auth --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: feishu auth --timeout 8s",
		)
	}

	h := mustOpenHome(out, *home)
	svc := mustBuildService(out, h)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	authResult, err := svc.Auth(ctx)
	if err != nil {
		exitRuntimeError(out,
			"飞书凭据验证失败",
			err.Error(),
			"检查 app_id/app_secret 与飞书开发者后台权限后重试",
		)
	}

	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":   "feishu.auth.v1",
			"ok":       true,
			"app_id":   authResult.AppID,
			"base_url": authResult.BaseURL,
			"expire":   authResult.Expire,
		})
		return
	}

	fmt.Println("feishu auth ok")
	fmt.Printf("app_id=%s\n", authResult.AppID)
	fmt.Printf("base_url=%s\n", authResult.BaseURL)
	fmt.Printf("expire=%ds\n", authResult.Expire)
}

func resolveClientConfig(cfg app.HomeConfig) feishudoc.Config {
	normalized := cfg.WithDefaults()
	return feishudoc.Config{
		AppID:     strings.TrimSpace(normalized.Daemon.Public.Feishu.AppID),
		AppSecret: strings.TrimSpace(normalized.Daemon.Public.Feishu.AppSecret),
		BaseURL:   strings.TrimSpace(normalized.Daemon.Public.Feishu.BaseURL),
	}
}

func mustOpenHome(out cliOutputFormat, homeFlag string) *app.Home {
	homeDir, err := app.ResolveHomeDir(homeFlag)
	if err != nil {
		exitRuntimeError(out, "解析 Home 目录失败", err.Error(), "通过 --home 指定有效目录，或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}
	return h
}

func mustBuildService(out cliOutputFormat, h *app.Home) *feishudoc.Service {
	if h == nil {
		exitRuntimeError(out, "初始化 feishu 服务失败", "home 为空", "检查 Home 配置后重试")
	}
	clientCfg := resolveClientConfig(h.Config)
	svc, err := feishudoc.New(clientCfg)
	if err != nil {
		exitRuntimeError(out,
			"飞书凭据配置无效",
			err.Error(),
			fmt.Sprintf("检查 %s 的 daemon.public.feishu.app_id / app_secret", h.ConfigPath),
		)
	}
	return svc
}
