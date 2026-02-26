package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"dalek/internal/contracts"
	gatewayws "dalek/internal/services/channel/ws"
)

var testUpgrader = websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}

func TestRunChat_HappyPath(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		if err := conn.WriteJSON(gatewayws.OutboundFrame{
			Type:           gatewayws.FrameTypeReady,
			ConversationID: "conv-1",
			Text:           "connected",
		}); err != nil {
			t.Errorf("write ready failed: %v", err)
			return
		}
		var inbound gatewayws.InboundFrame
		if err := conn.ReadJSON(&inbound); err != nil {
			t.Errorf("read inbound failed: %v", err)
			return
		}
		if strings.TrimSpace(inbound.Text) != "hello" {
			t.Errorf("inbound text mismatch: %q", inbound.Text)
			return
		}
		if err := conn.WriteJSON(gatewayws.OutboundFrame{
			Type:      gatewayws.FrameTypeAssistantEvent,
			Text:      "thinking",
			EventType: "assistant",
		}); err != nil {
			t.Errorf("write assistant_event failed: %v", err)
			return
		}
		if err := conn.WriteJSON(gatewayws.OutboundFrame{
			Type:           gatewayws.FrameTypeAssistantMessage,
			ConversationID: "conv-1",
			Text:           "done",
		}); err != nil {
			t.Errorf("write assistant_message failed: %v", err)
			return
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	targetURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := RunChat(ctx, ChatRequest{
		TargetURL: targetURL,
		Text:      "hello",
		SenderID:  "tester",
	})
	if err != nil {
		t.Fatalf("RunChat failed: %v", err)
	}
	if strings.TrimSpace(result.FinalFrame.Type) != gatewayws.FrameTypeAssistantMessage {
		t.Fatalf("final frame type mismatch: %+v", result.FinalFrame)
	}
	if strings.TrimSpace(result.FinalFrame.Text) != "done" {
		t.Fatalf("final frame text mismatch: %+v", result.FinalFrame)
	}
	if strings.TrimSpace(result.FinalFrame.JobStatus) != string(contracts.ChannelTurnSucceeded) {
		t.Fatalf("final frame job_status mismatch: %+v", result.FinalFrame)
	}
	if len(result.EventFrames) != 1 || strings.TrimSpace(result.EventFrames[0].Type) != gatewayws.FrameTypeAssistantEvent {
		t.Fatalf("event frames mismatch: %+v", result.EventFrames)
	}
}

func TestRunChat_HandshakeErrorFrame(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := testUpgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		defer conn.Close()

		if err := conn.WriteJSON(gatewayws.OutboundFrame{
			Type: gatewayws.FrameTypeError,
			Text: "boom",
		}); err != nil {
			t.Errorf("write error frame failed: %v", err)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	targetURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	result, err := RunChat(ctx, ChatRequest{
		TargetURL: targetURL,
		Text:      "hello",
	})
	if err != nil {
		t.Fatalf("RunChat failed: %v", err)
	}
	if strings.TrimSpace(result.FinalFrame.Type) != gatewayws.FrameTypeError {
		t.Fatalf("expected error frame, got %+v", result.FinalFrame)
	}
	if strings.TrimSpace(result.FinalFrame.JobStatus) != string(contracts.ChannelTurnFailed) {
		t.Fatalf("expected failed status, got %+v", result.FinalFrame)
	}
	if strings.TrimSpace(result.FinalFrame.JobError) != "boom" {
		t.Fatalf("expected job_error=boom, got %+v", result.FinalFrame)
	}
}
