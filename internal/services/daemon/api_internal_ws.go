package daemon

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	gatewayws "dalek/internal/services/channel/ws"
	"dalek/internal/services/core"
)

type internalGatewayWSOptions struct {
	Path          string
	DefaultSender string
}

var internalGatewayWSUpgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

func newInternalGatewayWSHandler(gateway *channelsvc.Gateway, rawOpt internalGatewayWSOptions, logger *slog.Logger) (string, http.HandlerFunc) {
	path := gatewayws.NormalizePath(rawOpt.Path)
	defaultSender := strings.TrimSpace(rawOpt.DefaultSender)
	if defaultSender == "" {
		defaultSender = "ws.user"
	}
	baseLogger := core.EnsureLogger(logger).With("component", "daemon_gateway_ws")

	return path, func(w http.ResponseWriter, r *http.Request) {
		if gateway == nil {
			http.Error(w, "gateway runtime unavailable", http.StatusServiceUnavailable)
			return
		}

		projectName := strings.TrimSpace(r.URL.Query().Get("project"))
		if projectName == "" {
			http.Error(w, "missing query: project", http.StatusBadRequest)
			return
		}

		conn, err := internalGatewayWSUpgrader.Upgrade(w, r, nil)
		if err != nil {
			baseLogger.Warn("ws upgrade failed", "error", err)
			return
		}

		conversationID := strings.TrimSpace(r.URL.Query().Get("conv"))
		if conversationID == "" {
			conversationID = gatewayws.BuildConversationID("gw")
		}
		senderID := strings.TrimSpace(r.URL.Query().Get("sender"))
		if senderID == "" {
			senderID = defaultSender
		}

		done := make(chan struct{})
		var closeOnce sync.Once
		closeConn := func() {
			closeOnce.Do(func() {
				_ = conn.Close()
				close(done)
			})
		}

		sub, unsubscribe := gateway.EventBus().Subscribe(projectName, conversationID, 128)
		defer unsubscribe()

		writeCh := make(chan gatewayws.OutboundFrame, 128)
		send := func(frame gatewayws.OutboundFrame) bool {
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

		go func() {
			for {
				select {
				case <-done:
					return
				case ev, ok := <-sub:
					if !ok {
						return
					}
					frame := gatewayws.OutboundFrame{
						Type:           ev.Type,
						ConversationID: conversationID,
						PeerMessageID:  ev.PeerMessageID,
						RunID:          ev.RunID,
						Seq:            ev.Seq,
						Stream:         ev.Stream,
						Text:           ev.Text,
						EventType:      ev.EventType,
						AgentProvider:  ev.AgentProvider,
						AgentModel:     ev.AgentModel,
						JobStatus:      string(ev.JobStatus),
						JobErrorType:   ev.JobErrorType,
						JobError:       ev.JobError,
						At:             gatewayws.FormatTimestamp(time.Now()),
					}
					if !send(frame) {
						return
					}
				}
			}
		}()

		if !send(gatewayws.OutboundFrame{
			Type:           gatewayws.FrameTypeReady,
			ConversationID: conversationID,
			Text:           "connected",
			At:             gatewayws.FormatTimestamp(time.Now()),
		}) {
			closeConn()
			return
		}

		conn.SetPingHandler(func(appData string) error {
			return conn.WriteControl(websocket.PongMessage, []byte(appData), time.Now().Add(3*time.Second))
		})

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
			text, parsedSenderID, parseErr := gatewayws.ParseInboundText(payload)
			if parseErr != nil {
				_ = send(gatewayws.OutboundFrame{
					Type:           gatewayws.FrameTypeError,
					ConversationID: conversationID,
					Text:           parseErr.Error(),
					At:             gatewayws.FormatTimestamp(time.Now()),
				})
				continue
			}
			if parsedSenderID != "" {
				senderID = parsedSenderID
			}

			seq++
			env := contracts.InboundEnvelope{
				Schema:             contracts.ChannelInboundSchemaV1,
				ChannelType:        contracts.ChannelTypeWeb,
				Adapter:            "web.ws",
				PeerConversationID: conversationID,
				PeerMessageID:      gatewayws.NextPeerMessageID(seq),
				SenderID:           senderID,
				Text:               text,
				ReceivedAt:         gatewayws.FormatTimestamp(time.Now()),
			}
			err = gateway.Submit(context.Background(), channelsvc.GatewayInboundRequest{
				ProjectName: projectName,
				Envelope:    env,
			})
			if err == nil {
				continue
			}
			msg := err.Error()
			if errors.Is(err, channelsvc.ErrInboundQueueFull) {
				msg = "消息排队中，请稍后重试"
			}
			_ = send(gatewayws.OutboundFrame{
				Type:           gatewayws.FrameTypeError,
				ConversationID: conversationID,
				Text:           msg,
				At:             gatewayws.FormatTimestamp(time.Now()),
			})
		}
	}
}
