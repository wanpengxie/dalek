package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"dalek/internal/app"
	"dalek/internal/contracts"
	"dalek/internal/store"
)

type gatewayWSInboundFrame struct {
	Text     string `json:"text"`
	SenderID string `json:"sender_id,omitempty"`
}

type gatewayWSInboxItem struct {
	ID        uint   `json:"id"`
	Status    string `json:"status"`
	Severity  string `json:"severity"`
	Reason    string `json:"reason"`
	Title     string `json:"title"`
	TicketID  uint   `json:"ticket_id"`
	WorkerID  uint   `json:"worker_id"`
	UpdatedAt string `json:"updated_at"`
}

type gatewayWSOutboundFrame struct {
	Type           string               `json:"type"`
	ConversationID string               `json:"conversation_id,omitempty"`
	PeerMessageID  string               `json:"peer_message_id,omitempty"`
	RunID          string               `json:"run_id,omitempty"`
	Seq            int                  `json:"seq,omitempty"`
	Stream         string               `json:"stream,omitempty"`
	Text           string               `json:"text,omitempty"`
	EventType      string               `json:"event_type,omitempty"`
	AgentProvider  string               `json:"agent_provider,omitempty"`
	AgentModel     string               `json:"agent_model,omitempty"`
	JobStatus      string               `json:"job_status,omitempty"`
	JobErrorType   string               `json:"job_error_type,omitempty"`
	JobError       string               `json:"job_error,omitempty"`
	InboxCount     int                  `json:"inbox_count,omitempty"`
	InboxItems     []gatewayWSInboxItem `json:"inbox_items,omitempty"`
	At             string               `json:"at,omitempty"`
}

type gatewayWSTurnRequest struct {
	PeerMessageID string
	SenderID      string
	Text          string
	ReceivedAt    string
}

type gatewayWSServerOptions struct {
	Path               string
	DefaultSender      string
	ConversationPrefix string
	TurnTimeout        time.Duration
	InboxPollInterval  time.Duration
	InboxLimit         int
}

var gatewayWSUpgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

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
	opt := normalizeGatewayWSServerOptions(rawOpt)
	path := normalizeGatewayWSPath(opt.Path)
	return path, func(w http.ResponseWriter, r *http.Request) {
		conn, err := gatewayWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gateway ws upgrade 失败:", err)
			return
		}

		conversationID := strings.TrimSpace(r.URL.Query().Get("conv"))
		if conversationID == "" {
			conversationID = buildGatewayWSConversationID(opt.ConversationPrefix)
		}
		baseSender := strings.TrimSpace(r.URL.Query().Get("sender"))
		if baseSender == "" {
			baseSender = opt.DefaultSender
		}
		if baseSender == "" {
			baseSender = "ws.user"
		}

		done := make(chan struct{})
		var closeOnce sync.Once
		closeConn := func() {
			closeOnce.Do(func() {
				_ = conn.Close()
				close(done)
			})
		}

		writeCh := make(chan gatewayWSOutboundFrame, 64)
		send := func(frame gatewayWSOutboundFrame) bool {
			select {
			case <-done:
				return false
			case writeCh <- frame:
				return true
			}
		}

		go func() {
			for {
				select {
				case <-done:
					return
				case frame := <-writeCh:
					if err := conn.WriteJSON(frame); err != nil {
						closeConn()
						return
					}
				}
			}
		}()

		if !send(gatewayWSOutboundFrame{
			Type:           "ready",
			ConversationID: conversationID,
			Text:           "connected",
			At:             time.Now().Format(time.RFC3339),
		}) {
			closeConn()
			return
		}
		conn.SetPingHandler(func(appData string) error {
			return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(3*time.Second))
		})

		turnCh := make(chan gatewayWSTurnRequest, 32)

		go func() {
			for {
				select {
				case <-done:
					return
				case req := <-turnCh:
					ctx, cancel := projectCtx(opt.TurnTimeout)
					result, err := p.ProcessChannelInbound(ctx, contracts.InboundEnvelope{
						Schema:             contracts.ChannelInboundSchemaV1,
						ChannelType:        contracts.ChannelTypeWeb,
						Adapter:            "web.ws",
						PeerConversationID: conversationID,
						PeerMessageID:      req.PeerMessageID,
						SenderID:           req.SenderID,
						Text:               req.Text,
						ReceivedAt:         req.ReceivedAt,
					})
					cancel()
					if err != nil {
						_ = send(gatewayWSOutboundFrame{
							Type:           "error",
							ConversationID: conversationID,
							Text:           err.Error(),
							At:             time.Now().Format(time.RFC3339),
						})
						continue
					}

					reply := strings.TrimSpace(result.ReplyText)
					if reply == "" {
						reply = "(empty reply)"
					}
					finalRunID := strings.TrimSpace(result.RunID)
					lastSeq := 0
					finalSeq := 0
					finalEventType := "end"
					for _, ev := range result.AgentEvents {
						runID := strings.TrimSpace(ev.RunID)
						if runID == "" {
							runID = finalRunID
						}
						if finalRunID == "" && runID != "" {
							finalRunID = runID
						}
						stream := strings.TrimSpace(ev.Stream)
						evType := deriveGatewayEventType(stream, ev.Data.Phase)
						evText := strings.TrimSpace(ev.Data.Text)
						if ev.Seq > lastSeq {
							lastSeq = ev.Seq
						}
						if stream == "lifecycle" && (evType == "end" || evType == "error") {
							finalSeq = ev.Seq
							finalEventType = evType
							continue
						}
						if stream == "" && evType == "" && evText == "" {
							continue
						}
						// 避免把最终文本重复当成中间事件再发一遍。
						if evText != "" && evText == reply {
							continue
						}
						if !send(gatewayWSOutboundFrame{
							Type:           "assistant_event",
							ConversationID: conversationID,
							RunID:          runID,
							Seq:            ev.Seq,
							Stream:         stream,
							Text:           evText,
							EventType:      evType,
							AgentProvider:  strings.TrimSpace(result.AgentProvider),
							AgentModel:     strings.TrimSpace(result.AgentModel),
							At:             time.Now().Format(time.RFC3339),
						}) {
							closeConn()
							return
						}
					}
					if finalSeq <= 0 {
						finalSeq = lastSeq + 1
					}
					if result.JobStatus != store.ChannelTurnSucceeded {
						finalEventType = "error"
					}
					if !send(gatewayWSOutboundFrame{
						Type:           "assistant_message",
						ConversationID: conversationID,
						RunID:          finalRunID,
						Seq:            finalSeq,
						Stream:         "lifecycle",
						Text:           reply,
						EventType:      finalEventType,
						AgentProvider:  strings.TrimSpace(result.AgentProvider),
						AgentModel:     strings.TrimSpace(result.AgentModel),
						JobStatus:      strings.TrimSpace(string(result.JobStatus)),
						JobErrorType:   strings.TrimSpace(result.JobErrorType),
						JobError:       strings.TrimSpace(result.JobError),
						At:             time.Now().Format(time.RFC3339),
					}) {
						closeConn()
						return
					}
				}
			}
		}()

		go func() {
			var seq uint64
			for {
				msgType, payload, err := conn.ReadMessage()
				if err != nil {
					closeConn()
					return
				}
				if msgType != websocket.TextMessage && msgType != websocket.BinaryMessage {
					continue
				}

				text, senderID, parseErr := parseGatewayWSInboundText(payload)
				if parseErr != nil {
					_ = send(gatewayWSOutboundFrame{
						Type:           "error",
						ConversationID: conversationID,
						Text:           parseErr.Error(),
						At:             time.Now().Format(time.RFC3339),
					})
					continue
				}
				if senderID == "" {
					senderID = baseSender
				}

				seq++
				req := gatewayWSTurnRequest{
					PeerMessageID: nextGatewayPeerMessageID(seq),
					SenderID:      senderID,
					Text:          text,
					ReceivedAt:    time.Now().Format(time.RFC3339),
				}
				select {
				case <-done:
					return
				case turnCh <- req:
				default:
					_ = send(gatewayWSOutboundFrame{
						Type:           "error",
						ConversationID: conversationID,
						Text:           "消息处理队列已满，请稍后重试",
						At:             time.Now().Format(time.RFC3339),
					})
				}
			}
		}()

		go func() {
			lastDigest := ""
			pushInbox := func(force bool) {
				ctx, cancel := projectCtx(opt.TurnTimeout)
				items, err := p.ListInbox(ctx, app.ListInboxOptions{
					Status: store.InboxOpen,
					Limit:  opt.InboxLimit,
				})
				cancel()
				if err != nil {
					if force {
						_ = send(gatewayWSOutboundFrame{
							Type:           "error",
							ConversationID: conversationID,
							Text:           fmt.Sprintf("读取 inbox 失败: %v", err),
							At:             time.Now().Format(time.RFC3339),
						})
					}
					return
				}
				digest := digestInboxItems(items)
				if !force && digest == lastDigest {
					return
				}
				lastDigest = digest
				_ = send(buildInboxUpdateFrame(conversationID, items))
			}

			pushInbox(true)
			ticker := time.NewTicker(opt.InboxPollInterval)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ticker.C:
					pushInbox(false)
				}
			}
		}()

		<-done
	}
}

func normalizeGatewayWSServerOptions(raw gatewayWSServerOptions) gatewayWSServerOptions {
	opt := raw
	if strings.TrimSpace(opt.Path) == "" {
		opt.Path = "/ws"
	}
	opt.DefaultSender = strings.TrimSpace(opt.DefaultSender)
	if opt.DefaultSender == "" {
		opt.DefaultSender = "ws.user"
	}
	opt.ConversationPrefix = strings.TrimSpace(opt.ConversationPrefix)
	if opt.ConversationPrefix == "" {
		opt.ConversationPrefix = "ws"
	}
	if opt.TurnTimeout < 0 {
		opt.TurnTimeout = 0
	}
	if opt.InboxPollInterval <= 0 {
		opt.InboxPollInterval = 2 * time.Second
	}
	if opt.InboxLimit <= 0 {
		opt.InboxLimit = 20
	}
	if opt.InboxLimit > 200 {
		opt.InboxLimit = 200
	}
	return opt
}

func buildInboxUpdateFrame(conversationID string, items []store.InboxItem) gatewayWSOutboundFrame {
	return gatewayWSOutboundFrame{
		Type:           "inbox_update",
		ConversationID: strings.TrimSpace(conversationID),
		Text:           formatInboxSummary(items),
		InboxCount:     len(items),
		InboxItems:     toGatewayWSInboxItems(items),
		At:             time.Now().Format(time.RFC3339),
	}
}

func toGatewayWSInboxItems(items []store.InboxItem) []gatewayWSInboxItem {
	out := make([]gatewayWSInboxItem, 0, len(items))
	for _, it := range items {
		out = append(out, gatewayWSInboxItem{
			ID:        it.ID,
			Status:    strings.TrimSpace(string(it.Status)),
			Severity:  strings.TrimSpace(string(it.Severity)),
			Reason:    strings.TrimSpace(string(it.Reason)),
			Title:     strings.TrimSpace(it.Title),
			TicketID:  it.TicketID,
			WorkerID:  it.WorkerID,
			UpdatedAt: it.UpdatedAt.Local().Format(time.RFC3339),
		})
	}
	return out
}

func digestInboxItems(items []store.InboxItem) string {
	if len(items) == 0 {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%d|%s|%s|%s|%s|%d|%d|%s",
			it.ID,
			strings.TrimSpace(string(it.Status)),
			strings.TrimSpace(string(it.Severity)),
			strings.TrimSpace(string(it.Reason)),
			strings.TrimSpace(it.Title),
			it.TicketID,
			it.WorkerID,
			it.UpdatedAt.Local().Format(time.RFC3339Nano),
		))
	}
	return strings.Join(parts, ";")
}

func formatInboxSummary(items []store.InboxItem) string {
	if len(items) == 0 {
		return "inbox(open)=0"
	}
	const previewN = 3
	lines := make([]string, 0, previewN+1)
	lines = append(lines, fmt.Sprintf("inbox(open)=%d", len(items)))
	n := len(items)
	if n > previewN {
		n = previewN
	}
	for i := 0; i < n; i++ {
		it := items[i]
		lines = append(lines, fmt.Sprintf("#%d %s/%s t%d %s",
			it.ID,
			strings.TrimSpace(string(it.Severity)),
			strings.TrimSpace(string(it.Reason)),
			it.TicketID,
			strings.TrimSpace(it.Title),
		))
	}
	return strings.Join(lines, "\n")
}

func deriveGatewayEventType(stream, phase string) string {
	stream = strings.TrimSpace(stream)
	phase = strings.TrimSpace(phase)
	if stream == "lifecycle" {
		if phase != "" {
			return phase
		}
		return "lifecycle"
	}
	if stream == "assistant" {
		return "assistant"
	}
	if stream == "error" {
		return "error"
	}
	if stream == "tool" {
		return "tool"
	}
	return phase
}

func parseGatewayWSInboundText(payload []byte) (string, string, error) {
	raw := strings.TrimSpace(string(payload))
	if raw == "" {
		return "", "", fmt.Errorf("消息不能为空")
	}

	var frame gatewayWSInboundFrame
	if err := json.Unmarshal(payload, &frame); err == nil {
		text := strings.TrimSpace(frame.Text)
		senderID := strings.TrimSpace(frame.SenderID)
		if text == "" {
			return "", "", fmt.Errorf("消息不能为空")
		}
		return text, senderID, nil
	}

	var asString string
	if err := json.Unmarshal(payload, &asString); err == nil {
		text := strings.TrimSpace(asString)
		if text == "" {
			return "", "", fmt.Errorf("消息不能为空")
		}
		return text, "", nil
	}

	return raw, "", nil
}

func normalizeGatewayWSPath(rawPath string) string {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "/ws"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func buildGatewayWSConversationID(prefix string) string {
	p := strings.TrimSpace(prefix)
	if p == "" {
		p = "ws"
	}
	return fmt.Sprintf("%s-%s", p, randomHex(4))
}

func nextGatewayPeerMessageID(seq uint64) string {
	return fmt.Sprintf("ws-%d-%d-%s", time.Now().UnixNano(), seq, randomHex(2))
}

func randomHex(nbytes int) string {
	if nbytes <= 0 {
		nbytes = 4
	}
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}
