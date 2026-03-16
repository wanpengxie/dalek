package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"dalek/internal/app"
	gatewaysendsvc "dalek/internal/services/gatewaysend"
)

const (
	gatewayServeSendPath        = gatewaysendsvc.Path
	gatewaySendResponseSchemaV1 = gatewaysendsvc.ResponseSchemaV1
)

type gatewaySendDelivery = app.DaemonGatewaySendDelivery
type gatewaySendResponse = app.DaemonGatewaySendResponse

func cmdGatewaySend(args []string) {
	fs := flag.NewFlagSet("gateway send", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"发送项目通知到已绑定飞书群",
			"dalek gateway send --project <name> --text \"...\" [--timeout 12s] [--output text|json]",
			"dalek gateway send --project demo --text \"部署完成\"",
			"dalek gateway send --project demo --text \"部署完成\" -o json",
		)
	}
	homeFlag := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	projectName := fs.String("project", globalProject, "project 名称")
	text := fs.String("text", "", "通知文本")
	timeout := fs.Duration("timeout", 12*time.Second, "HTTP 请求超时时间")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "gateway send 参数解析失败", "运行 dalek gateway send --help 查看参数")
	out := parseOutputOrExit(*output, true)

	project := strings.TrimSpace(*projectName)
	if project == "" {
		exitUsageError(out, "缺少必填参数 --project", "--project 不能为空", "dalek gateway send --project demo --text \"...\"")
	}
	msg := strings.TrimSpace(*text)
	if msg == "" {
		exitUsageError(out, "缺少必填参数 --text", "--text 不能为空", "dalek gateway send --project demo --text \"部署完成\"")
	}
	if *timeout <= 0 {
		exitUsageError(out, "非法参数 --timeout", "--timeout 必须大于 0", "例如: dalek gateway send --timeout 12s")
	}

	_, remote, derr := openRemoteProject(*homeFlag, project)
	if derr != nil {
		if app.IsDaemonUnavailable(derr) {
			exitRuntimeError(out, "gateway send 失败（daemon 不在线）", derr.Error(), "请先执行 dalek daemon start 后重试")
		}
		exitRuntimeError(out, "gateway send 失败", derr.Error(), "检查项目绑定与 gateway 配置后重试")
	}

	reqTimeout := *timeout
	if reqTimeout <= 0 {
		reqTimeout = 12 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), reqTimeout)
	defer cancel()

	resp, err := remote.SendProjectText(ctx, app.DaemonGatewaySendRequest{Text: msg})
	if err != nil {
		if app.IsDaemonUnavailable(err) {
			exitRuntimeError(out, "gateway send 失败（daemon 不在线）", err.Error(), "请先执行 dalek daemon start 后重试")
		}
		exitRuntimeError(out, "gateway send 失败", err.Error(), "检查项目绑定与 gateway 配置后重试")
	}
	if resp.Failed > 0 {
		exitRuntimeError(out,
			"gateway send 部分发送失败",
			fmt.Sprintf("project=%s delivered=%d failed=%d", resp.Project, resp.Delivered, resp.Failed),
			"查看失败条目并修复绑定或网络问题后重试",
		)
	}

	if out == outputJSON {
		printGatewayResultJSON(resp)
	} else {
		fmt.Printf("gateway send 已完成: project=%s delivered=%d failed=%d\n", resp.Project, resp.Delivered, resp.Failed)
		for _, item := range resp.Results {
			if strings.TrimSpace(item.Error) == "" {
				continue
			}
			fmt.Fprintf(os.Stderr, "chat=%s 发送失败: %s\n", strings.TrimSpace(item.ChatID), strings.TrimSpace(item.Error))
		}
	}
}

func buildGatewaySendAPIURL(listenAddr string) (string, error) {
	addr := strings.TrimSpace(listenAddr)
	if addr == "" {
		addr = "127.0.0.1:18081"
	}
	if strings.Contains(addr, "://") {
		u, err := url.Parse(addr)
		if err != nil || strings.TrimSpace(u.Host) == "" {
			return "", fmt.Errorf("daemon.internal.listen 非法: %q", addr)
		}
		switch strings.ToLower(strings.TrimSpace(u.Scheme)) {
		case "http", "https":
			// keep scheme
		case "ws":
			u.Scheme = "http"
		case "wss":
			u.Scheme = "https"
		default:
			return "", fmt.Errorf("daemon.internal.listen 仅支持 http/https/ws/wss: %q", addr)
		}
		basePath := strings.TrimSpace(u.Path)
		basePath = strings.TrimSuffix(basePath, "/")
		if basePath == "" || basePath == "/" {
			u.Path = gatewayServeSendPath
		} else {
			u.Path = basePath + gatewayServeSendPath
		}
		u.RawQuery = ""
		u.Fragment = ""
		return u.String(), nil
	}
	normalized, err := normalizeGatewayListenAddr("daemon.internal.listen", addr)
	if err != nil {
		return "", err
	}
	return "http://" + normalized + gatewayServeSendPath, nil
}
