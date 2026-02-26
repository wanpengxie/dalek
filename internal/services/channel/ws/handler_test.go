package ws

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	"dalek/internal/store"
)

type fakeTurnProcessor struct {
	mu      sync.Mutex
	calls   int
	lastEnv contracts.InboundEnvelope
	result  channelsvc.ProcessResult
	err     error
}

func (f *fakeTurnProcessor) ProcessInbound(_ context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastEnv = env
	return f.result, f.err
}

func (f *fakeTurnProcessor) snapshot() (int, contracts.InboundEnvelope) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.lastEnv
}

func TestNewSyncHandler_KeyPath(t *testing.T) {
	processor := &fakeTurnProcessor{
		result: channelsvc.ProcessResult{
			RunID:         "run-1",
			ReplyText:     "hello from ws",
			JobStatus:     contracts.ChannelTurnSucceeded,
			AgentProvider: "codex",
			AgentModel:    "gpt-5",
		},
	}
	path, handler := NewSyncHandler(ServerOptions{
		Path:               "/ws",
		DefaultSender:      "ws.user",
		ConversationPrefix: "ws-test",
		TurnTimeout:        2 * time.Second,
		InboxPollInterval:  50 * time.Millisecond,
		InboxLimit:         20,
		TurnProcessor:      processor,
		ListInbox: func(_ context.Context, _ int) ([]store.InboxItem, error) {
			return []store.InboxItem{{
				ID:       1,
				Status:   contracts.InboxOpen,
				Severity: contracts.InboxWarn,
				Reason:   contracts.InboxQuestion,
				Title:    "need follow up",
				TicketID: 7,
			}}, nil
		},
	})
	mux := http.NewServeMux()
	mux.HandleFunc(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	targetURL := "ws" + strings.TrimPrefix(srv.URL, "http") + path + "?conv=conv-1&sender=tester"
	conn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	ready := mustReadWSFrame(t, conn, 3*time.Second)
	if strings.TrimSpace(ready.Type) != FrameTypeReady {
		t.Fatalf("expected ready frame, got %+v", ready)
	}

	if err := conn.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatalf("ws send failed: %v", err)
	}

	deadline := time.Now().Add(4 * time.Second)
	var gotAssistant bool
	var gotInbox bool
	for time.Now().Before(deadline) {
		frame := mustReadWSFrame(t, conn, time.Until(deadline))
		switch strings.TrimSpace(frame.Type) {
		case FrameTypeAssistantMessage:
			gotAssistant = true
			if strings.TrimSpace(frame.Text) != "hello from ws" {
				t.Fatalf("assistant text mismatch: %q", frame.Text)
			}
		case FrameTypeInboxUpdate:
			gotInbox = true
			if frame.InboxCount != 1 {
				t.Fatalf("expected inbox_count=1, got=%d", frame.InboxCount)
			}
		}
		if gotAssistant && gotInbox {
			break
		}
	}
	if !gotAssistant {
		t.Fatalf("did not receive assistant_message frame")
	}
	if !gotInbox {
		t.Fatalf("did not receive inbox_update frame")
	}

	calls, env := processor.snapshot()
	if calls != 1 {
		t.Fatalf("ProcessInbound call count=%d, want=1", calls)
	}
	if strings.TrimSpace(env.PeerConversationID) != "conv-1" {
		t.Fatalf("conversation mismatch: %q", env.PeerConversationID)
	}
	if strings.TrimSpace(env.SenderID) != "tester" {
		t.Fatalf("sender mismatch: %q", env.SenderID)
	}
	if strings.TrimSpace(env.Text) != "hello" {
		t.Fatalf("text mismatch: %q", env.Text)
	}
}

func mustReadWSFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) OutboundFrame {
	t.Helper()
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("SetReadDeadline failed: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read failed: %v", err)
	}
	var frame OutboundFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("ws frame json decode failed: %v payload=%s", err, string(payload))
	}
	return frame
}
