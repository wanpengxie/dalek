package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"dalek/internal/app"
	"dalek/internal/contracts"
	"dalek/internal/store"
)

const defaultGatewayDaemonWSURL = "ws://127.0.0.1:18081/ws"

type gatewayChatJSONResult struct {
	Schema         string                   `json:"schema"`
	Mode           string                   `json:"mode"`
	URL            string                   `json:"url"`
	Project        string                   `json:"project"`
	ConversationID string                   `json:"conversation_id,omitempty"`
	RunID          string                   `json:"run_id,omitempty"`
	ReplyText      string                   `json:"reply_text,omitempty"`
	JobStatus      string                   `json:"job_status,omitempty"`
	JobErrorType   string                   `json:"job_error_type,omitempty"`
	JobError       string                   `json:"job_error,omitempty"`
	Events         []gatewayWSOutboundFrame `json:"events,omitempty"`
}

func cmdGateway(args []string) {
	if len(args) == 0 {
		cmdGatewayHelp()
		os.Exit(2)
	}
	sub := strings.TrimSpace(args[0])
	switch sub {
	case "ingress":
		cmdGatewayIngress(args[1:])
	case "chat":
		cmdGatewayChat(args[1:])
	case "serve":
		cmdGatewayServe(args[1:])
	case "send":
		cmdGatewaySend(args[1:])
	case "bind":
		cmdGatewayBind(args[1:])
	case "unbind":
		cmdGatewayUnbind(args[1:])
	case "ws-server":
		cmdGatewayWSServer(args[1:])
	case "-h", "--help", "help":
		cmdGatewayHelp()
		os.Exit(0)
	default:
		exitUsageError(globalOutput,
			fmt.Sprintf("未知 gateway 子命令: %s", sub),
			"gateway 命令组仅支持固定子命令",
			"运行 dalek gateway --help 查看可用命令",
		)
	}
}

func cmdGatewayHelp() {
	printGroupUsage("Channel Gateway", "dalek gateway <command> [flags]", []string{
		"serve      显示迁移提示（gateway 已并入 daemon）",
		"chat       通过 websocket 与 daemon 对话",
		"ingress    直接写入入站消息并执行 turn job",
		"send       向绑定飞书群发送通知",
		"bind       绑定飞书群到项目",
		"unbind     解绑飞书群",
		"ws-server  启动本地测试 ws 服务（无鉴权）",
	})
	fmt.Fprintln(os.Stderr, "Use \"dalek gateway <command> --help\" for more information.")
}

func cmdGatewayIngress(args []string) {
	fs := flag.NewFlagSet("gateway ingress", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"写入入站消息并执行 turn job",
			"dalek gateway ingress --text \"...\" [--channel cli] [--output text|json]",
			"dalek gateway ingress --text \"请给我 ticket 列表\"",
			"dalek gateway ingress --text \"你好\" -o json",
		)
	}
	channelType := fs.String("channel", contracts.ChannelTypeCLI, "channel type: web|im|cli|api")
	adapter := fs.String("adapter", "", "adapter 名（如 web.ws/cli.local）")
	bindingID := fs.Uint("binding", 0, "binding id（可选）")
	conversationID := fs.String("conv", "", "外部会话 id（可选）")
	messageID := fs.String("msg", "", "外部消息 id（可选）")
	senderID := fs.String("sender", "cli.user", "发送者")
	text := fs.String("text", "", "消息文本")
	timeout := fs.Duration("timeout", 0, "超时时间（默认取 config.gateway_agent.turn_timeout_ms）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "gateway ingress 参数解析失败", "运行 dalek gateway ingress --help 查看参数")
	out := parseOutputOrExit(*output, true)

	if strings.TrimSpace(*text) == "" {
		exitUsageError(out, "缺少必填参数 --text", "--text 不能为空", "dalek gateway ingress --text \"...\"")
	}

	p := mustOpenProjectWithOutput(out, globalHome, globalProject)
	turnTimeout := *timeout
	if turnTimeout <= 0 {
		turnTimeout = p.GatewayTurnTimeout()
	}
	ctx, cancel := projectCtx(turnTimeout)
	defer cancel()

	env := contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        strings.TrimSpace(*channelType),
		Adapter:            strings.TrimSpace(*adapter),
		BindingID:          uint(*bindingID),
		PeerConversationID: strings.TrimSpace(*conversationID),
		PeerMessageID:      strings.TrimSpace(*messageID),
		SenderID:           strings.TrimSpace(*senderID),
		Text:               strings.TrimSpace(*text),
		ReceivedAt:         time.Now().Format(time.RFC3339),
	}

	channelSvc := p.ChannelService()
	if channelSvc == nil {
		exitRuntimeError(out, "gateway ingress 失败", "project channel service 不可用", "检查项目初始化状态后重试")
	}
	result, err := channelSvc.ProcessInbound(ctx, env)
	if err != nil {
		exitRuntimeError(out, "gateway ingress 失败", err.Error(), "检查 gateway 与项目运行状态后重试")
	}
	failed := result.JobStatus != store.ChannelTurnSucceeded

	if out == outputJSON {
		if failed {
			exitOnGatewayTurnFailed(out, "gateway ingress", result.JobStatus, result.JobError, result.JobErrorType)
		}
		printGatewayResultJSON(result)
	} else {
		fmt.Println(strings.TrimSpace(result.ReplyText))
		if failed {
			exitOnGatewayTurnFailed(outputText, "gateway ingress", result.JobStatus, result.JobError, result.JobErrorType)
		}
	}
}

func cmdGatewayChat(args []string) {
	fs := flag.NewFlagSet("gateway chat", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"通过 websocket 与 gateway daemon 对话",
			"dalek gateway chat --text \"...\" [--ws-url URL] [--output text|json]",
			"dalek gateway chat --text \"请给我 ticket 列表\"",
			"dalek gateway chat --text \"hello\" --ws-url ws://127.0.0.1:18081/ws -o json",
		)
	}
	conversationID := fs.String("conv", "cli.default", "会话 id")
	senderID := fs.String("sender", "cli.user", "发送者")
	messageID := fs.String("msg", "", "消息 id（可选）")
	text := fs.String("text", "", "消息文本")
	timeout := fs.Duration("timeout", 0, "超时时间（默认取 config.gateway_agent.turn_timeout_ms）")
	wsURL := fs.String("ws-url", "", "gateway daemon websocket 地址（优先级: flag > DALEK_GATEWAY_WS_URL > ~/.dalek/config.json）")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "gateway chat 参数解析失败", "运行 dalek gateway chat --help 查看参数")
	out := parseOutputOrExit(*output, true)
	_ = messageID // 保留参数兼容，M2 daemon 模式由服务端生成 peer_message_id。
	if strings.TrimSpace(*text) == "" {
		exitUsageError(out, "缺少必填参数 --text", "--text 不能为空", "dalek gateway chat --text \"...\"")
	}

	p := mustOpenProjectWithOutput(out, globalHome, globalProject)
	projectName := strings.TrimSpace(p.Name())
	if projectName == "" {
		exitRuntimeError(out, "gateway chat 失败", "project 名为空", "通过 --project 指定有效项目名")
	}

	turnTimeout := *timeout
	if turnTimeout <= 0 {
		turnTimeout = p.GatewayTurnTimeout()
	}

	baseWSURL := resolveGatewayDaemonWSURL(strings.TrimSpace(*wsURL), globalHome)
	targetURL, err := buildGatewayChatWSURL(baseWSURL, projectName, strings.TrimSpace(*conversationID), strings.TrimSpace(*senderID))
	if err != nil {
		exitUsageError(out, "gateway chat 参数非法", err.Error(), "检查 --ws-url/--conv/--sender 参数后重试")
	}

	ctx, cancel := projectCtx(turnTimeout)
	defer cancel()

	finalFrame, eventFrames, err := runGatewayChatViaDaemon(ctx, targetURL, strings.TrimSpace(*text), strings.TrimSpace(*senderID))
	if err != nil {
		exitRuntimeError(out, "gateway chat 失败", err.Error(), "确认 daemon 已启动（dalek daemon start）后重试")
	}
	finalFrame = normalizeGatewayChatFinalFrame(finalFrame)

	if out == outputJSON {
		jobStatus := store.ChannelTurnJobStatus(strings.TrimSpace(finalFrame.JobStatus))
		if jobStatus != store.ChannelTurnSucceeded {
			exitOnGatewayTurnFailed(
				out,
				"gateway chat",
				jobStatus,
				strings.TrimSpace(finalFrame.JobError),
				strings.TrimSpace(finalFrame.JobErrorType),
			)
		}
		printGatewayResultJSON(gatewayChatJSONResult{
			Schema:         "dalek.gateway_chat.daemon.v1",
			Mode:           "daemon_ws",
			URL:            targetURL,
			Project:        projectName,
			ConversationID: strings.TrimSpace(finalFrame.ConversationID),
			RunID:          strings.TrimSpace(finalFrame.RunID),
			ReplyText:      strings.TrimSpace(finalFrame.Text),
			JobStatus:      strings.TrimSpace(finalFrame.JobStatus),
			JobErrorType:   strings.TrimSpace(finalFrame.JobErrorType),
			JobError:       strings.TrimSpace(finalFrame.JobError),
			Events:         eventFrames,
		})
	} else {
		fmt.Println(strings.TrimSpace(finalFrame.Text))
		exitOnGatewayTurnFailed(
			outputText,
			"gateway chat",
			store.ChannelTurnJobStatus(strings.TrimSpace(finalFrame.JobStatus)),
			strings.TrimSpace(finalFrame.JobError),
			strings.TrimSpace(finalFrame.JobErrorType),
		)
	}
}

func resolveGatewayDaemonWSURL(cliValue, homeFlag string) string {
	if strings.TrimSpace(cliValue) != "" {
		return strings.TrimSpace(cliValue)
	}
	if fromEnv := strings.TrimSpace(os.Getenv("DALEK_GATEWAY_WS_URL")); fromEnv != "" {
		return fromEnv
	}
	if fromConfig := resolveGatewayDaemonWSURLFromHome(homeFlag); fromConfig != "" {
		return fromConfig
	}
	return defaultGatewayDaemonWSURL
}

func resolveGatewayDaemonWSURLFromHome(homeFlag string) string {
	homeDir, err := app.ResolveHomeDir(homeFlag)
	if err != nil {
		return ""
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		return ""
	}
	listenAddr := strings.TrimSpace(h.Config.Daemon.Internal.Listen)
	if listenAddr == "" {
		return ""
	}
	if strings.Contains(listenAddr, "://") {
		u, err := url.Parse(listenAddr)
		if err != nil || strings.TrimSpace(u.Host) == "" {
			return ""
		}
		scheme := strings.ToLower(strings.TrimSpace(u.Scheme))
		switch scheme {
		case "http":
			u.Scheme = "ws"
		case "https":
			u.Scheme = "wss"
		case "ws", "wss":
			// keep as-is
		default:
			return ""
		}
		if strings.TrimSpace(u.Path) == "" || strings.TrimSpace(u.Path) == "/" {
			u.Path = "/ws"
		}
		return u.String()
	}
	return "ws://" + listenAddr + "/ws"
}

func buildGatewayChatWSURL(baseURL, projectName, conversationID, senderID string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return "", fmt.Errorf("ws url 不能为空")
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return "", fmt.Errorf("project 不能为空")
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	if strings.EqualFold(strings.TrimSpace(u.Scheme), "http") {
		u.Scheme = "ws"
	}
	if strings.EqualFold(strings.TrimSpace(u.Scheme), "https") {
		u.Scheme = "wss"
	}
	if strings.TrimSpace(u.Scheme) == "" || strings.TrimSpace(u.Host) == "" {
		return "", fmt.Errorf("ws url 非法: %s", baseURL)
	}
	if strings.TrimSpace(u.Path) == "" || strings.TrimSpace(u.Path) == "/" {
		u.Path = "/ws"
	}

	query := u.Query()
	query.Set("project", projectName)
	if strings.TrimSpace(conversationID) != "" {
		query.Set("conv", strings.TrimSpace(conversationID))
	}
	if strings.TrimSpace(senderID) != "" {
		query.Set("sender", strings.TrimSpace(senderID))
	}
	u.RawQuery = query.Encode()
	return u.String(), nil
}

func runGatewayChatViaDaemon(ctx context.Context, targetURL, text, senderID string) (gatewayWSOutboundFrame, []gatewayWSOutboundFrame, error) {
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, targetURL, nil)
	if err != nil {
		if resp != nil {
			return gatewayWSOutboundFrame{}, nil, fmt.Errorf("连接 gateway daemon 失败（http=%d）: %w；请先执行 `dalek daemon start`", resp.StatusCode, err)
		}
		lower := strings.ToLower(strings.TrimSpace(err.Error()))
		if strings.Contains(lower, "connection refused") || strings.Contains(lower, "cannot assign requested address") || strings.Contains(lower, "no such host") {
			return gatewayWSOutboundFrame{}, nil, fmt.Errorf("gateway daemon 未启动或不可达（%s），请先执行 `dalek daemon start`", targetURL)
		}
		return gatewayWSOutboundFrame{}, nil, fmt.Errorf("连接 gateway daemon 失败: %w", err)
	}
	defer conn.Close()

	for {
		frame, readErr := readGatewayWSFrame(ctx, conn)
		if readErr != nil {
			return gatewayWSOutboundFrame{}, nil, fmt.Errorf("读取 gateway 握手帧失败: %w", readErr)
		}
		switch strings.TrimSpace(frame.Type) {
		case "ready":
			goto SEND
		case "error":
			return normalizeGatewayChatFinalFrame(frame), nil, nil
		default:
			// 忽略非 ready 的握手前帧。
		}
	}

SEND:
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
	}
	if err := conn.WriteJSON(gatewayWSInboundFrame{
		Text:     strings.TrimSpace(text),
		SenderID: strings.TrimSpace(senderID),
	}); err != nil {
		return gatewayWSOutboundFrame{}, nil, fmt.Errorf("发送消息到 gateway daemon 失败: %w", err)
	}
	_ = conn.SetWriteDeadline(time.Time{})

	events := make([]gatewayWSOutboundFrame, 0, 8)
	for {
		frame, readErr := readGatewayWSFrame(ctx, conn)
		if readErr != nil {
			return gatewayWSOutboundFrame{}, events, fmt.Errorf("读取 gateway 响应失败: %w", readErr)
		}
		switch strings.TrimSpace(frame.Type) {
		case "assistant_event", "inbox_update":
			events = append(events, frame)
		case "assistant_message", "error":
			return frame, events, nil
		default:
			// 兼容未来扩展帧，当前忽略。
		}
	}
}

func readGatewayWSFrame(ctx context.Context, conn *websocket.Conn) (gatewayWSOutboundFrame, error) {
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		if ctx != nil && ctx.Err() != nil {
			return gatewayWSOutboundFrame{}, ctx.Err()
		}
		return gatewayWSOutboundFrame{}, err
	}
	var frame gatewayWSOutboundFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		return gatewayWSOutboundFrame{}, fmt.Errorf("解析 gateway 帧失败: %w", err)
	}
	return frame, nil
}

func normalizeGatewayChatFinalFrame(frame gatewayWSOutboundFrame) gatewayWSOutboundFrame {
	frame.Type = strings.TrimSpace(frame.Type)
	frame.Text = strings.TrimSpace(frame.Text)
	frame.JobStatus = strings.TrimSpace(frame.JobStatus)
	frame.JobErrorType = strings.TrimSpace(frame.JobErrorType)
	frame.JobError = strings.TrimSpace(frame.JobError)
	if frame.Type == "error" {
		if frame.JobStatus == "" {
			frame.JobStatus = string(store.ChannelTurnFailed)
		}
		if frame.JobError == "" {
			frame.JobError = frame.Text
		}
		if frame.JobErrorType == "" {
			frame.JobErrorType = "runtime"
		}
	}
	if frame.JobStatus == "" {
		frame.JobStatus = string(store.ChannelTurnSucceeded)
	}
	return frame
}

func printGatewayResultJSON(result any) {
	printJSONOrExit(result)
}

func exitOnGatewayTurnFailed(out cliOutputFormat, cmd string, status store.ChannelTurnJobStatus, jobErr, jobErrType string) {
	if status == store.ChannelTurnSucceeded {
		return
	}
	errMsg := strings.TrimSpace(jobErr)
	if errMsg == "" {
		errMsg = "turn job failed"
	}
	errType := strings.TrimSpace(jobErrType)
	if errType == "" {
		errType = "unknown"
	}
	exitRuntimeError(
		out,
		fmt.Sprintf("%s 执行失败", strings.TrimSpace(cmd)),
		fmt.Sprintf("job_status=%s, job_error_type=%s, job_error=%s", strings.TrimSpace(string(status)), errType, errMsg),
		"根据错误类型修复后重试，或查看 gateway/agent 日志",
	)
}
