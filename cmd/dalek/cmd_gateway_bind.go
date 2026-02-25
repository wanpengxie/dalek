package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalek/internal/app"
	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	"dalek/internal/store"
)

func cmdGatewayBind(args []string) {
	fs := flag.NewFlagSet("gateway bind", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"绑定飞书群到项目",
			"dalek gateway bind --chat-id <id> --project <name> [--output text|json]",
			"dalek gateway bind --chat-id oc_xxx --project demo",
			"dalek gateway bind --chat-id oc_xxx --project demo -o json",
		)
	}
	homeFlag := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	chatID := fs.String("chat-id", "", "飞书 chat_id")
	projectName := fs.String("project", "", "项目名")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "gateway bind 参数解析失败", "运行 dalek gateway bind --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*chatID) == "" || strings.TrimSpace(*projectName) == "" {
		exitUsageError(out, "缺少必填参数", "--chat-id 与 --project 均不能为空", "dalek gateway bind --chat-id <id> --project <name>")
	}

	gateway, resolver := mustOpenGatewayRuntime(out, *homeFlag)
	if _, err := resolver.Resolve(strings.TrimSpace(*projectName)); err != nil {
		exitRuntimeError(out, "项目不存在", err.Error(), "先运行 dalek project ls 确认项目名")
	}
	prev, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, "im.feishu", strings.TrimSpace(*chatID), strings.TrimSpace(*projectName))
	if err != nil {
		exitRuntimeError(out, "绑定失败", err.Error(), "确认 chat_id 与 project 参数后重试")
	}
	prev = strings.TrimSpace(prev)

	status := "bound"
	text := fmt.Sprintf("chat %s 已绑定到 %s", strings.TrimSpace(*chatID), strings.TrimSpace(*projectName))
	if prev != "" && prev != strings.TrimSpace(*projectName) {
		status = "switched"
		text = fmt.Sprintf("chat %s 已从 %s 切换到 %s", strings.TrimSpace(*chatID), prev, strings.TrimSpace(*projectName))
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":   "dalek.gateway.bind.v1",
			"status":   status,
			"chat_id":  strings.TrimSpace(*chatID),
			"project":  strings.TrimSpace(*projectName),
			"previous": prev,
		})
		return
	}
	fmt.Println(text)
}

func cmdGatewayUnbind(args []string) {
	fs := flag.NewFlagSet("gateway unbind", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"解绑飞书群",
			"dalek gateway unbind --chat-id <id> [--output text|json]",
			"dalek gateway unbind --chat-id oc_xxx",
			"dalek gateway unbind --chat-id oc_xxx -o json",
		)
	}
	homeFlag := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	chatID := fs.String("chat-id", "", "飞书 chat_id")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "gateway unbind 参数解析失败", "运行 dalek gateway unbind --help 查看参数")
	out := parseOutputOrExit(*output, true)
	if strings.TrimSpace(*chatID) == "" {
		exitUsageError(out, "缺少必填参数 --chat-id", "chat-id 不能为空", "dalek gateway unbind --chat-id <id>")
	}

	gateway, _ := mustOpenGatewayRuntime(out, *homeFlag)
	removed, err := gateway.UnbindProject(context.Background(), contracts.ChannelTypeIM, "im.feishu", strings.TrimSpace(*chatID))
	if err != nil {
		exitRuntimeError(out, "解绑失败", err.Error(), "确认 chat_id 参数后重试")
	}
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":  "dalek.gateway.unbind.v1",
			"chat_id": strings.TrimSpace(*chatID),
			"removed": removed,
		})
		return
	}
	if removed {
		fmt.Printf("chat %s 已解绑\n", strings.TrimSpace(*chatID))
		return
	}
	fmt.Printf("chat %s 当前未绑定\n", strings.TrimSpace(*chatID))
}

func mustOpenGatewayRuntime(out cliOutputFormat, homeFlag string) (*channelsvc.Gateway, *homeProjectResolver) {
	homeDir, err := app.ResolveHomeDir(homeFlag)
	if err != nil {
		exitRuntimeError(out, "解析 --home 失败", err.Error(), "指定有效 Home 目录或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}
	gatewayDBPath := strings.TrimSpace(h.GatewayDBPath)
	if gatewayDBPath == "" {
		gatewayDBPath = filepath.Join(homeDir, "gateway.db")
	}
	db, err := store.OpenGatewayDB(gatewayDBPath)
	if err != nil {
		exitRuntimeError(out, "打开 gateway.db 失败", err.Error(), "检查 gateway.db 权限与完整性")
	}
	resolver := newHomeProjectResolver(h)
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{})
	if err != nil {
		exitRuntimeError(out, "创建 gateway runtime 失败", err.Error(), "检查 gateway 配置后重试")
	}
	return gateway, resolver
}
