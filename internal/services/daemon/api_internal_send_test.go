package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	gatewaysendsvc "dalek/internal/services/gatewaysend"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type internalSendCapturedCall struct {
	chatID string
	title  string
	text   string
}

type internalSendCaptureSender struct {
	mu    sync.Mutex
	calls []internalSendCapturedCall
}

func (s *internalSendCaptureSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	s.mu.Lock()
	s.calls = append(s.calls, internalSendCapturedCall{
		chatID: strings.TrimSpace(chatID),
		title:  strings.TrimSpace(title),
		text:   strings.TrimSpace(markdown),
	})
	s.mu.Unlock()
	return nil
}

func (s *internalSendCaptureSender) snapshot() []internalSendCapturedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]internalSendCapturedCall(nil), s.calls...)
}

func startTestInternalAPIForSend(t *testing.T, db *gorm.DB, sender gatewaysendsvc.MessageSender) *InternalAPI {
	t.Helper()

	host, err := NewExecutionHost(&testExecutionHostResolver{project: &testExecutionHostProject{}}, ExecutionHostOptions{})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	svc, err := NewInternalAPI(host, InternalAPIConfig{
		ListenAddr: "127.0.0.1:0",
	}, InternalAPIOptions{
		GatewaySendDB:     db,
		GatewaySendSender: sender,
	})
	if err != nil {
		t.Fatalf("NewInternalAPI failed: %v", err)
	}
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("InternalAPI Start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = svc.Stop(context.Background())
	})
	return svc
}

func TestInternalAPISendRoute_AllowsLoopbackWithoutToken(t *testing.T) {
	db, err := store.OpenGatewayDB(":memory:")
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	if err := db.Create(&store.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    store.ChannelIM,
		Adapter:        gatewaysendsvc.AdapterFeishu,
		PeerProjectKey: "chat-daemon-send-no-token",
		RolePolicyJSON: "{}",
		Enabled:        true,
	}).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	sender := &internalSendCaptureSender{}
	svc := startTestInternalAPIForSend(t, db, sender)
	url := "http://" + svc.listener.Addr().String() + gatewaysendsvc.Path
	reqBody := map[string]string{
		"project": "demo",
		"text":    "hello",
	}
	raw, _ := json.Marshal(reqBody)
	resp, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("http post failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status without token: %d", resp.StatusCode)
	}
	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("sender call count mismatch: %d", len(calls))
	}
}

func TestInternalAPISendRoute_Success(t *testing.T) {
	db, err := store.OpenGatewayDB(":memory:")
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db.DB failed: %v", err)
	}
	t.Cleanup(func() {
		_ = sqlDB.Close()
	})

	if err := db.Create(&store.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    store.ChannelIM,
		Adapter:        gatewaysendsvc.AdapterFeishu,
		PeerProjectKey: "chat-daemon-send-1",
		RolePolicyJSON: "{}",
		Enabled:        true,
	}).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	sender := &internalSendCaptureSender{}
	svc := startTestInternalAPIForSend(t, db, sender)
	url := "http://" + svc.listener.Addr().String() + gatewaysendsvc.Path
	reqBody := map[string]string{
		"project": "demo",
		"text":    "daemon send ok",
	}
	raw, _ := json.Marshal(reqBody)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	var payload gatewaysendsvc.Response
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.Delivered != 1 || payload.Failed != 0 {
		t.Fatalf("unexpected delivery stats: delivered=%d failed=%d", payload.Delivered, payload.Failed)
	}
	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("sender call count mismatch: %d", len(calls))
	}
	if calls[0].chatID != "chat-daemon-send-1" || calls[0].text != "daemon send ok" {
		t.Fatalf("sender call mismatch: %+v", calls[0])
	}
}
