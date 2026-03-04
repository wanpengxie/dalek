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

const feishuAuthTimeoutDefault = 8 * time.Second

func cmdFeishu(args []string) {
	if len(args) == 0 {
		printFeishuUsage()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "auth":
		cmdFeishuAuth(args[1:])
	case "doc":
		cmdFeishuDoc(args[1:])
	case "wiki":
		cmdFeishuWiki(args[1:])
	case "perm":
		cmdFeishuPerm(args[1:])
	case "comment":
		cmdFeishuComment(args[1:])
	case "help", "-h", "--help":
		printFeishuUsage()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 feishu 子命令: %s", sub),
			"feishu 命令组仅支持固定子命令",
			"运行 dalek feishu --help 查看可用命令",
		)
	}
}

func printFeishuUsage() {
	printGroupUsage("飞书文档协同", "dalek feishu <command> [flags]", []string{
		"auth     验证飞书凭据并检查 tenant_access_token 获取",
		"doc      文档操作（create/read/write/ls）",
		"wiki     知识空间操作（ls/nodes/create）",
		"perm     权限管理（share/add/ls）",
		"comment  评论管理（ls/get/create/reply/resolve）",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek feishu <command> --help\" for more information.")
}

func cmdFeishuAuth(args []string) {
	fs := flag.NewFlagSet("feishu auth", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"验证飞书凭据",
			"dalek feishu auth [--home <path>] [--timeout 8s] [--output text|json]",
			"dalek feishu auth",
			"dalek feishu auth --timeout 15s -o json",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	timeout := fs.Duration("timeout", feishuAuthTimeoutDefault, "认证请求超时（例如 8s）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "feishu auth 参数解析失败", "运行 dalek feishu auth --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if *timeout <= 0 {
		exitUsageError(out,
			"非法参数 --timeout",
			"--timeout 必须大于 0",
			"例如: dalek feishu auth --timeout 8s",
		)
	}

	h := mustOpenFeishuHome(out, *home)
	svc := mustBuildFeishuService(out, h)

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
			"schema":   "dalek.feishu.auth.v1",
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

func resolveFeishuClientConfig(cfg app.HomeConfig) feishudoc.Config {
	normalized := cfg.WithDefaults()
	return feishudoc.Config{
		AppID:     strings.TrimSpace(normalized.Daemon.Public.Feishu.AppID),
		AppSecret: strings.TrimSpace(normalized.Daemon.Public.Feishu.AppSecret),
		BaseURL:   strings.TrimSpace(normalized.Daemon.Public.Feishu.BaseURL),
	}
}

func mustOpenFeishuHome(out cliOutputFormat, homeFlag string) *app.Home {
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

func mustBuildFeishuService(out cliOutputFormat, h *app.Home) *feishudoc.Service {
	if h == nil {
		exitRuntimeError(out, "初始化 feishu 服务失败", "home 为空", "检查 Home 配置后重试")
	}
	clientCfg := resolveFeishuClientConfig(h.Config)
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
