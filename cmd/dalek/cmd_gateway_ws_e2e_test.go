package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"dalek/internal/app"
	"dalek/internal/repo"
	"dalek/internal/store"
)

func TestGatewayWS_E2E_AcceptanceFlow(t *testing.T) {
	installFakeClaudeForE2E(t)
	repoRoot := initGitRepo(t)
	homeDir := t.TempDir()
	layout := repo.NewLayout(repoRoot)
	t.Setenv("DALEK_FAKE_DB_PATH", layout.DBPath)

	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	p, err := h.InitProjectFromDir(repoRoot, "demo", repo.Config{})
	if err != nil {
		t.Fatalf("InitProjectFromDir failed: %v", err)
	}

	seedTicket, err := p.CreateTicketWithDescription(context.Background(), "ws-seed-ticket", "seed ticket for ws e2e")
	if err != nil {
		t.Fatalf("seed ticket failed: %v", err)
	}

	path, handler := newGatewayWSServerHandler(p, gatewayWSServerOptions{
		Path:               "/ws",
		DefaultSender:      "ws.user",
		ConversationPrefix: "ws-test",
		TurnTimeout:        5 * time.Second,
		InboxPollInterval:  100 * time.Millisecond,
		InboxLimit:         20,
	})
	mux := http.NewServeMux()
	mux.HandleFunc(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	targetURL, err := buildGatewayWSURLForTest(wsURL, "conv-ws-e2e", "tester")
	if err != nil {
		t.Fatalf("buildGatewayWSURLForTest failed: %v", err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	ready := mustReadWSFrame(t, conn, 5*time.Second)
	if strings.TrimSpace(ready.Type) != "ready" {
		t.Fatalf("expected ready frame, got %+v", ready)
	}

	// 1) hello world 级对话
	mustSendWSText(t, conn, "hello world")
	hello := mustReadWSFrameUntil(t, conn, 5*time.Second, func(f gatewayWSOutboundFrame) bool {
		return strings.TrimSpace(f.Type) == "assistant_message"
	})
	if strings.TrimSpace(hello.Text) == "" {
		t.Fatalf("hello reply should not be empty")
	}

	// 2) ticket 查询
	mustSendWSText(t, conn, "请给我 ticket 列表")
	listReply := mustReadWSFrameUntil(t, conn, 5*time.Second, func(f gatewayWSOutboundFrame) bool {
		return strings.TrimSpace(f.Type) == "assistant_message"
	})
	if !strings.Contains(listReply.Text, "正在查询 ticket 列表。") {
		t.Fatalf("ticket list reply unexpected:\n%s", listReply.Text)
	}

	// 3) 服务端 inbox 通知
	if err := insertOpenInboxItem(t, repoRoot, seedTicket.ID, "ws inbox notify"); err != nil {
		t.Fatalf("insert inbox failed: %v", err)
	}
	inboxFrame := mustReadWSFrameUntil(t, conn, 6*time.Second, func(f gatewayWSOutboundFrame) bool {
		return strings.TrimSpace(f.Type) == "inbox_update" && strings.Contains(f.Text, "ws inbox notify")
	})
	if inboxFrame.InboxCount <= 0 {
		t.Fatalf("expected inbox_count > 0, got %+v", inboxFrame)
	}

	// 4) 对话创建 ticket
	createTitle := "ws-e2e-created-ticket"
	mustSendWSText(t, conn, "创建 ticket: "+createTitle)
	createReply := mustReadWSFrameUntil(t, conn, 5*time.Second, func(f gatewayWSOutboundFrame) bool {
		return strings.TrimSpace(f.Type) == "assistant_message"
	})
	if !strings.Contains(createReply.Text, createTitle) {
		t.Fatalf("create ticket reply unexpected:\n%s", createReply.Text)
	}
	if !ticketExistsByTitle(t, p, createTitle) {
		t.Fatalf("ticket should be created by dialogue, title=%q", createTitle)
	}
}

func TestGatewayWS_E2E_CodexEventAndReply(t *testing.T) {
	installFakeCodexForE2E(t)
	t.Setenv("DALEK_GATEWAY_AGENT_PROVIDER", "codex")
	t.Setenv("DALEK_GATEWAY_AGENT_MODEL", "gpt-5-codex-test")

	repoRoot := initGitRepo(t)
	homeDir := t.TempDir()

	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	p, err := h.InitProjectFromDir(repoRoot, "demo", repo.Config{})
	if err != nil {
		t.Fatalf("InitProjectFromDir failed: %v", err)
	}

	path, handler := newGatewayWSServerHandler(p, gatewayWSServerOptions{
		Path:               "/ws",
		DefaultSender:      "ws.user",
		ConversationPrefix: "ws-test",
		TurnTimeout:        5 * time.Second,
		InboxPollInterval:  100 * time.Millisecond,
		InboxLimit:         20,
	})
	mux := http.NewServeMux()
	mux.HandleFunc(path, handler)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + path
	targetURL, err := buildGatewayWSURLForTest(wsURL, "conv-ws-codex", "tester")
	if err != nil {
		t.Fatalf("buildGatewayWSURLForTest failed: %v", err)
	}
	conn, _, err := websocket.DefaultDialer.Dial(targetURL, nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %v", err)
	}
	defer conn.Close()

	_ = mustReadWSFrame(t, conn, 5*time.Second) // ready

	mustSendWSText(t, conn, "hello codex")

	startFrame := mustReadWSFrameUntil(t, conn, 5*time.Second, func(f gatewayWSOutboundFrame) bool {
		return strings.TrimSpace(f.Type) == "assistant_event" && strings.TrimSpace(f.EventType) == "start"
	})
	if strings.TrimSpace(startFrame.Stream) != "lifecycle" {
		t.Fatalf("unexpected lifecycle start frame: %+v", startFrame)
	}

	eventFrame := mustReadWSFrameUntil(t, conn, 5*time.Second, func(f gatewayWSOutboundFrame) bool {
		return strings.TrimSpace(f.Type) == "assistant_event" && strings.TrimSpace(f.Stream) == "assistant"
	})
	if !strings.Contains(eventFrame.Text, "codex thinking") {
		t.Fatalf("unexpected assistant event:\n%+v", eventFrame)
	}
	if strings.TrimSpace(eventFrame.AgentProvider) != "codex" {
		t.Fatalf("unexpected agent provider in event: %s", eventFrame.AgentProvider)
	}
	if strings.TrimSpace(eventFrame.RunID) == "" {
		t.Fatalf("assistant event should include run_id")
	}
	if eventFrame.Seq <= 0 {
		t.Fatalf("assistant event should include positive seq")
	}
	if strings.TrimSpace(eventFrame.Stream) == "" {
		t.Fatalf("assistant event should include stream")
	}

	replyFrame := mustReadWSFrameUntil(t, conn, 5*time.Second, func(f gatewayWSOutboundFrame) bool {
		return strings.TrimSpace(f.Type) == "assistant_message"
	})
	if !strings.Contains(replyFrame.Text, "codex final reply") {
		t.Fatalf("unexpected assistant reply:\n%+v", replyFrame)
	}
	if strings.TrimSpace(replyFrame.AgentProvider) != "codex" {
		t.Fatalf("unexpected agent provider in reply: %s", replyFrame.AgentProvider)
	}
	if strings.TrimSpace(replyFrame.RunID) == "" {
		t.Fatalf("assistant reply should include run_id")
	}
	if replyFrame.Seq <= 0 {
		t.Fatalf("assistant reply should include positive seq")
	}
	if strings.TrimSpace(replyFrame.Stream) != "lifecycle" {
		t.Fatalf("assistant reply stream should be lifecycle, got=%q", replyFrame.Stream)
	}
}

func mustSendWSText(t *testing.T, conn *websocket.Conn, text string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(text)); err != nil {
		t.Fatalf("ws send failed: %v", err)
	}
}

func mustReadWSFrame(t *testing.T, conn *websocket.Conn, timeout time.Duration) gatewayWSOutboundFrame {
	t.Helper()
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("SetReadDeadline failed: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ws read failed: %v", err)
	}
	var frame gatewayWSOutboundFrame
	if err := json.Unmarshal(payload, &frame); err != nil {
		t.Fatalf("ws frame json decode failed: %v payload=%s", err, string(payload))
	}
	return frame
}

func mustReadWSFrameUntil(t *testing.T, conn *websocket.Conn, timeout time.Duration, pred func(gatewayWSOutboundFrame) bool) gatewayWSOutboundFrame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		remain := time.Until(deadline)
		if remain <= 0 {
			t.Fatalf("ws read until timeout")
		}
		frame := mustReadWSFrame(t, conn, remain)
		if pred(frame) {
			return frame
		}
	}
}

func insertOpenInboxItem(t *testing.T, repoRoot string, ticketID uint, title string) error {
	t.Helper()
	layout := repo.NewLayout(repoRoot)
	db, err := store.OpenAndMigrate(layout.DBPath)
	if err != nil {
		return err
	}
	return db.Create(&store.InboxItem{
		Key:      fmt.Sprintf("ws-test-inbox-%d", time.Now().UnixNano()),
		Status:   store.InboxOpen,
		Severity: store.InboxWarn,
		Reason:   store.InboxQuestion,
		Title:    strings.TrimSpace(title),
		Body:     "ws test inbox item",
		TicketID: ticketID,
	}).Error
}

func ticketExistsByTitle(t *testing.T, p *app.Project, title string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tickets, err := p.ListTickets(ctx, true)
	if err != nil {
		t.Fatalf("ListTickets failed: %v", err)
	}
	want := strings.TrimSpace(title)
	for _, tk := range tickets {
		if strings.TrimSpace(tk.Title) == want {
			return true
		}
	}
	return false
}

func buildGatewayWSURLForTest(rawURL, conversationID, senderID string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	q := u.Query()
	if strings.TrimSpace(conversationID) != "" {
		q.Set("conv", strings.TrimSpace(conversationID))
	}
	if strings.TrimSpace(senderID) != "" {
		q.Set("sender", strings.TrimSpace(senderID))
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
