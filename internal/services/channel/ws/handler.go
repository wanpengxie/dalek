package ws

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

type turnRequest struct {
	PeerMessageID string
	SenderID      string
	Text          string
	ReceivedAt    string
}

func NewSyncHandler(rawOpt ServerOptions) (string, http.HandlerFunc) {
	opt := normalizeServerOptions(rawOpt)
	path := NormalizePath(opt.Path)
	logger := core.EnsureLogger(opt.Logger).With("component", "gateway_ws_server")

	return path, func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error("gateway ws handler panic recovered",
					"panic", rec,
					"method", r.Method,
					"path", r.URL.Path,
				)
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()

		if opt.TurnProcessor == nil {
			http.Error(w, "channel service unavailable", http.StatusInternalServerError)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Warn("gateway ws upgrade failed", "error", err)
			return
		}

		conversationID := strings.TrimSpace(r.URL.Query().Get("conv"))
		if conversationID == "" {
			conversationID = BuildConversationID(opt.ConversationPrefix)
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

		safeGo := func(name string, fn func()) {
			go func() {
				defer func() {
					if rec := recover(); rec != nil {
						logger.Error("gateway ws goroutine panic recovered",
							"panic", rec,
							"goroutine", name,
						)
						closeConn()
					}
				}()
				fn()
			}()
		}

		writeCh := make(chan OutboundFrame, 64)
		send := func(frame OutboundFrame) bool {
			select {
			case <-done:
				return false
			case writeCh <- frame:
				return true
			}
		}

		safeGo("writer", func() {
			for {
				select {
				case <-done:
					return
				case frame := <-writeCh:
					if err := conn.WriteJSON(frame); err != nil {
						logger.Warn("gateway ws write failed", "error", err)
						closeConn()
						return
					}
				}
			}
		})

		if !send(OutboundFrame{
			Type:           FrameTypeReady,
			ConversationID: conversationID,
			Text:           "connected",
			At:             FormatTimestamp(time.Now()),
		}) {
			closeConn()
			return
		}
		conn.SetPingHandler(func(appData string) error {
			return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(3*time.Second))
		})

		turnCh := make(chan turnRequest, 32)

		safeGo("turn_loop", func() {
			for {
				select {
				case <-done:
					return
				case req := <-turnCh:
					ctx, cancel := turnContext(opt.TurnTimeout)
					result, err := opt.TurnProcessor.ProcessInbound(ctx, contracts.InboundEnvelope{
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
						_ = send(OutboundFrame{
							Type:           FrameTypeError,
							ConversationID: conversationID,
							Text:           err.Error(),
							At:             FormatTimestamp(time.Now()),
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
						stream := strings.TrimSpace(string(ev.Stream))
						evType := DeriveEventType(stream, ev.Data.Phase)
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
						if evText != "" && evText == reply {
							continue
						}
						if !send(OutboundFrame{
							Type:           FrameTypeAssistantEvent,
							ConversationID: conversationID,
							RunID:          runID,
							Seq:            ev.Seq,
							Stream:         stream,
							Text:           evText,
							EventType:      evType,
							AgentProvider:  strings.TrimSpace(result.AgentProvider),
							AgentModel:     strings.TrimSpace(result.AgentModel),
							At:             FormatTimestamp(time.Now()),
						}) {
							closeConn()
							return
						}
					}
					if finalSeq <= 0 {
						finalSeq = lastSeq + 1
					}
					if result.JobStatus != contracts.ChannelTurnSucceeded {
						finalEventType = "error"
					}
					if !send(OutboundFrame{
						Type:           FrameTypeAssistantMessage,
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
						At:             FormatTimestamp(time.Now()),
					}) {
						closeConn()
						return
					}
				}
			}
		})

		safeGo("read_loop", func() {
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

				text, senderID, parseErr := ParseInboundText(payload)
				if parseErr != nil {
					_ = send(OutboundFrame{
						Type:           FrameTypeError,
						ConversationID: conversationID,
						Text:           parseErr.Error(),
						At:             FormatTimestamp(time.Now()),
					})
					continue
				}
				if senderID == "" {
					senderID = baseSender
				}

				seq++
				req := turnRequest{
					PeerMessageID: NextPeerMessageID(seq),
					SenderID:      senderID,
					Text:          text,
					ReceivedAt:    FormatTimestamp(time.Now()),
				}
				select {
				case <-done:
					return
				case turnCh <- req:
				default:
					_ = send(OutboundFrame{
						Type:           FrameTypeError,
						ConversationID: conversationID,
						Text:           "消息处理队列已满，请稍后重试",
						At:             FormatTimestamp(time.Now()),
					})
				}
			}
		})

		if opt.ListInbox != nil {
			safeGo("inbox_loop", func() {
				lastDigest := ""
				pushInbox := func(force bool) {
					ctx, cancel := turnContext(opt.TurnTimeout)
					items, err := opt.ListInbox(ctx, opt.InboxLimit)
					cancel()
					if err != nil {
						if force {
							_ = send(OutboundFrame{
								Type:           FrameTypeError,
								ConversationID: conversationID,
								Text:           fmt.Sprintf("读取 inbox 失败: %v", err),
								At:             FormatTimestamp(time.Now()),
							})
						}
						return
					}
					digest := digestInboxItems(items)
					if !force && digest == lastDigest {
						return
					}
					lastDigest = digest
					_ = send(BuildInboxUpdateFrame(conversationID, items))
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
			})
		}

		<-done
	}
}

func turnContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout > 0 {
		return context.WithTimeout(context.Background(), timeout)
	}
	return context.WithCancel(context.Background())
}
