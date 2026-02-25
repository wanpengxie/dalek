package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
)

type internalGatewayWSOptions struct {
	Path          string
	DefaultSender string
}

type internalGatewayWSInboundFrame struct {
	Text     string `json:"text"`
	SenderID string `json:"sender_id,omitempty"`
}

type internalGatewayWSOutboundFrame struct {
	Type           string `json:"type"`
	ConversationID string `json:"conversation_id,omitempty"`
	PeerMessageID  string `json:"peer_message_id,omitempty"`
	RunID          string `json:"run_id,omitempty"`
	Seq            int    `json:"seq,omitempty"`
	Stream         string `json:"stream,omitempty"`
	Text           string `json:"text,omitempty"`
	EventType      string `json:"event_type,omitempty"`
	AgentProvider  string `json:"agent_provider,omitempty"`
	AgentModel     string `json:"agent_model,omitempty"`
	JobStatus      string `json:"job_status,omitempty"`
	JobErrorType   string `json:"job_error_type,omitempty"`
	JobError       string `json:"job_error,omitempty"`
	At             string `json:"at,omitempty"`
}

var internalGatewayWSUpgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

func newInternalGatewayWSHandler(gateway *channelsvc.Gateway, rawOpt internalGatewayWSOptions, logger *log.Logger) (string, http.HandlerFunc) {
	path := normalizeInternalGatewayWSPath(rawOpt.Path)
	defaultSender := strings.TrimSpace(rawOpt.DefaultSender)
	if defaultSender == "" {
		defaultSender = "ws.user"
	}

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
			logInternalGatewayWSf(logger, "ws upgrade failed: %v", err)
			return
		}

		conversationID := strings.TrimSpace(r.URL.Query().Get("conv"))
		if conversationID == "" {
			conversationID = buildInternalGatewayWSConversationID("gw")
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

		writeCh := make(chan internalGatewayWSOutboundFrame, 128)
		send := func(frame internalGatewayWSOutboundFrame) bool {
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
					frame := internalGatewayWSOutboundFrame{
						Type:           strings.TrimSpace(ev.Type),
						ConversationID: conversationID,
						PeerMessageID:  strings.TrimSpace(ev.PeerMessageID),
						RunID:          strings.TrimSpace(ev.RunID),
						Seq:            ev.Seq,
						Stream:         strings.TrimSpace(ev.Stream),
						Text:           strings.TrimSpace(ev.Text),
						EventType:      strings.TrimSpace(ev.EventType),
						AgentProvider:  strings.TrimSpace(ev.AgentProvider),
						AgentModel:     strings.TrimSpace(ev.AgentModel),
						JobStatus:      strings.TrimSpace(string(ev.JobStatus)),
						JobErrorType:   strings.TrimSpace(ev.JobErrorType),
						JobError:       strings.TrimSpace(ev.JobError),
						At:             formatInternalGatewayWSTimestamp(time.Now()),
					}
					if !send(frame) {
						return
					}
				}
			}
		}()

		if !send(internalGatewayWSOutboundFrame{
			Type:           "ready",
			ConversationID: conversationID,
			Text:           "connected",
			At:             formatInternalGatewayWSTimestamp(time.Now()),
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
			text, parsedSenderID, parseErr := parseInternalGatewayWSInboundText(payload)
			if parseErr != nil {
				_ = send(internalGatewayWSOutboundFrame{
					Type:           "error",
					ConversationID: conversationID,
					Text:           parseErr.Error(),
					At:             formatInternalGatewayWSTimestamp(time.Now()),
				})
				continue
			}
			if strings.TrimSpace(parsedSenderID) != "" {
				senderID = strings.TrimSpace(parsedSenderID)
			}

			seq++
			env := contracts.InboundEnvelope{
				Schema:             contracts.ChannelInboundSchemaV1,
				ChannelType:        contracts.ChannelTypeWeb,
				Adapter:            "web.ws",
				PeerConversationID: conversationID,
				PeerMessageID:      nextInternalGatewayPeerMessageID(seq),
				SenderID:           senderID,
				Text:               text,
				ReceivedAt:         formatInternalGatewayWSTimestamp(time.Now()),
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
			_ = send(internalGatewayWSOutboundFrame{
				Type:           "error",
				ConversationID: conversationID,
				Text:           msg,
				At:             formatInternalGatewayWSTimestamp(time.Now()),
			})
		}
	}
}

func formatInternalGatewayWSTimestamp(at time.Time) string {
	return at.UTC().Format(time.RFC3339)
}

func parseInternalGatewayWSInboundText(payload []byte) (string, string, error) {
	raw := strings.TrimSpace(string(payload))
	if raw == "" {
		return "", "", fmt.Errorf("消息不能为空")
	}

	var frame internalGatewayWSInboundFrame
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

func normalizeInternalGatewayWSPath(rawPath string) string {
	path := strings.TrimSpace(rawPath)
	if path == "" {
		return "/ws"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return path
}

func buildInternalGatewayWSConversationID(prefix string) string {
	p := strings.TrimSpace(prefix)
	if p == "" {
		p = "ws"
	}
	return fmt.Sprintf("%s-%s", p, randomInternalGatewayHex(4))
}

func nextInternalGatewayPeerMessageID(seq uint64) string {
	return fmt.Sprintf("ws-%d-%d-%s", time.Now().UnixNano(), seq, randomInternalGatewayHex(2))
}

func randomInternalGatewayHex(nbytes int) string {
	if nbytes <= 0 {
		nbytes = 4
	}
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func logInternalGatewayWSf(logger *log.Logger, format string, args ...any) {
	if logger == nil {
		return
	}
	logger.Printf(format, args...)
}
