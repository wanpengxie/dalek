package main

import (
	"fmt"
	"strings"

	"dalek/internal/app"
)

func openDaemonClient(homeFlag string) (*app.Home, *app.DaemonAPIClient, error) {
	homeDir, err := app.ResolveHomeDir(homeFlag)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 Home 目录失败: %w", err)
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		return nil, nil, fmt.Errorf("打开 Home 失败: %w", err)
	}
	client, err := app.NewDaemonAPIClientFromHome(h)
	if err != nil {
		return nil, nil, fmt.Errorf("创建 daemon 客户端失败: %w", err)
	}
	return h, client, nil
}

func mustOpenDaemonClient(out cliOutputFormat, homeFlag string) (*app.Home, *app.DaemonAPIClient) {
	h, client, err := openDaemonClient(homeFlag)
	if err != nil {
		fix := "通过 --home 指定有效目录，或检查 ~/.dalek/config.json 的 daemon.internal 配置"
		cause := strings.TrimSpace(err.Error())
		switch {
		case strings.Contains(cause, "解析 Home 目录失败"):
			fix = "通过 --home 指定有效目录，或设置 DALEK_HOME"
		case strings.Contains(cause, "打开 Home 失败"):
			fix = "检查 Home 目录权限与文件完整性"
		case strings.Contains(cause, "创建 daemon 客户端失败"):
			fix = "检查 ~/.dalek/config.json 的 daemon.internal 配置"
		}
		exitRuntimeError(out, "打开 daemon 客户端失败", cause, fix)
	}
	return h, client
}

func daemonUnavailableDispatchFix(ticketID uint) string {
	return "`dalek daemon start`\n" +
		"当前命令属于长时任务，预计耗时：P50=28m, P90=96m（无历史时默认 20~120m）"
}

func daemonUnavailableWorkerRunFix(ticketID uint) string {
	return "`dalek daemon start`\n" +
		"当前命令属于长时任务，预计耗时：P50=28m, P90=96m（无历史时默认 20~120m）"
}

func daemonUnavailableFocusFix() string {
	return "`dalek daemon start`\n" +
		"focus 控制面已切到 daemon-owned 模式；run/stop/tail 需要 daemon 在线。"
}

func daemonUnavailableAgentRunFix() string {
	return "`dalek daemon start`\n" +
		"当前命令属于长时任务，建议使用异步模式；如需同步执行可改用：\n" +
		"  dalek agent run --prompt \"...\" --sync --timeout 120m"
}

func daemonRuntimeErrorCause(err error) string {
	if err == nil {
		return "未知错误"
	}
	return strings.TrimSpace(err.Error())
}
