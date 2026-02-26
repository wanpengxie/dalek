package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/contracts"
	gatewayclient "dalek/internal/gateway/client"
	"dalek/internal/store"
)

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

	chatResult, err := gatewayclient.RunChat(ctx, gatewayclient.ChatRequest{
		TargetURL: targetURL,
		Text:      strings.TrimSpace(*text),
		SenderID:  strings.TrimSpace(*senderID),
	})
	if err != nil {
		exitRuntimeError(out, "gateway chat 失败", err.Error(), "确认 daemon 已启动（dalek daemon start）后重试")
	}
	finalFrame := gatewayclient.NormalizeChatFinalFrame(chatResult.FinalFrame)
	eventFrames := chatResult.EventFrames

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
	return gatewayclient.ResolveDaemonWSURL(cliValue, homeFlag)
}

func buildGatewayChatWSURL(baseURL, projectName, conversationID, senderID string) (string, error) {
	return gatewayclient.BuildChatWSURL(baseURL, projectName, conversationID, senderID)
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
