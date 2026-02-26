package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"
	gatewayws "dalek/internal/services/channel/ws"
)

type gatewayWSInboundFrame = gatewayws.InboundFrame

type gatewayWSOutboundFrame = gatewayws.OutboundFrame

type gatewayWSServerOptions = gatewayws.ServerOptions

func cmdGatewayWSServer(args []string) {
	fs := flag.NewFlagSet("gateway ws-server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"启动本地测试 websocket 服务",
			"dalek gateway ws-server [--listen 127.0.0.1:18080] [--path /ws]",
			"dalek gateway ws-server",
			"dalek gateway ws-server --listen 127.0.0.1:18080 --path /ws",
		)
	}
	listenAddr := fs.String("listen", "127.0.0.1:18080", "监听地址")
	wsPath := fs.String("path", "/ws", "websocket 路径")
	defaultSender := fs.String("sender", "ws.user", "默认发送者")
	convPrefix := fs.String("conv-prefix", "ws", "自动会话前缀")
	timeout := fs.Duration("timeout", 0, "单条消息处理超时（默认取 config.gateway_agent.turn_timeout_ms）")
	inboxPoll := fs.Duration("inbox-poll", 2*time.Second, "inbox 推送轮询间隔")
	inboxLimit := fs.Int("inbox-limit", 20, "inbox 推送最多条数")
	parseFlagSetOrExit(fs, args, globalOutput, "gateway ws-server 参数解析失败", "运行 dalek gateway ws-server --help 查看参数")

	p := mustOpenProjectWithOutput(globalOutput, globalHome, globalProject)
	addr := strings.TrimSpace(*listenAddr)
	if addr == "" {
		exitUsageError(globalOutput, "缺少必填参数 --listen", "--listen 不能为空", "dalek gateway ws-server --listen 127.0.0.1:18080")
	}
	if *inboxPoll <= 0 {
		exitUsageError(globalOutput, "非法参数 --inbox-poll", "--inbox-poll 必须大于 0", "例如: dalek gateway ws-server --inbox-poll 2s")
	}
	if *inboxLimit <= 0 {
		exitUsageError(globalOutput, "非法参数 --inbox-limit", "--inbox-limit 必须大于 0", "例如: dalek gateway ws-server --inbox-limit 20")
	}

	turnTimeout := *timeout
	if turnTimeout <= 0 {
		turnTimeout = p.GatewayTurnTimeout()
	}
	path, handler := newGatewayWSServerHandler(p, gatewayWSServerOptions{
		Path:               strings.TrimSpace(*wsPath),
		DefaultSender:      strings.TrimSpace(*defaultSender),
		ConversationPrefix: strings.TrimSpace(*convPrefix),
		TurnTimeout:        turnTimeout,
		InboxPollInterval:  *inboxPoll,
		InboxLimit:         *inboxLimit,
	})
	mux := http.NewServeMux()
	mux.HandleFunc(path, handler)

	fmt.Fprintf(os.Stderr, "gateway ws-server 已启动: ws://%s%s\n", addr, path)
	fmt.Fprintln(os.Stderr, "说明：测试通道无鉴权，仅用于本地联调。")
	if err := http.ListenAndServe(addr, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		exitRuntimeError(globalOutput, "gateway ws-server 退出", err.Error(), "检查端口占用后重试")
	}
}

func newGatewayWSServerHandler(p *app.Project, rawOpt gatewayWSServerOptions) (string, http.HandlerFunc) {
	var turnProcessor gatewayws.TurnProcessor
	var listInbox gatewayws.ListInboxFunc
	if p != nil {
		turnProcessor = p.ChannelService()
		listInbox = func(ctx context.Context, limit int) ([]contracts.InboxItem, error) {
			return p.ListInbox(ctx, app.ListInboxOptions{
				Status: app.InboxOpen,
				Limit:  limit,
			})
		}
	}
	return gatewayws.NewSyncHandler(gatewayws.ServerOptions{
		Path:               rawOpt.Path,
		DefaultSender:      rawOpt.DefaultSender,
		ConversationPrefix: rawOpt.ConversationPrefix,
		TurnTimeout:        rawOpt.TurnTimeout,
		InboxPollInterval:  rawOpt.InboxPollInterval,
		InboxLimit:         rawOpt.InboxLimit,
		Logger:             newGatewayWSLogger(),
		TurnProcessor:      turnProcessor,
		ListInbox:          listInbox,
	})
}

func normalizeGatewayWSPath(rawPath string) string {
	return gatewayws.NormalizePath(rawPath)
}

func parseGatewayWSInboundText(payload []byte) (string, string, error) {
	return gatewayws.ParseInboundText(payload)
}

func buildInboxUpdateFrame(conversationID string, items []contracts.InboxItem) gatewayWSOutboundFrame {
	return gatewayws.BuildInboxUpdateFrame(conversationID, items)
}

func formatInboxSummary(items []contracts.InboxItem) string {
	return gatewayws.FormatInboxSummary(items)
}

func deriveGatewayEventType(stream, phase string) string {
	return gatewayws.DeriveEventType(stream, phase)
}

func newGatewayWSLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})).With("proc", "gateway_ws_server")
}
