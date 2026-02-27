package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewSenderReturnsNoopWhenDisabled(t *testing.T) {
	sender := NewSender(SenderConfig{Enabled: false})
	if _, ok := sender.(*daemonFeishuNoopSender); !ok {
		t.Fatalf("expected noop sender, got %T", sender)
	}
}

func TestNewSenderReturnsNoopWhenCredentialMissing(t *testing.T) {
	sender := NewSender(SenderConfig{Enabled: true, AppID: "", AppSecret: "secret"})
	if _, ok := sender.(*daemonFeishuNoopSender); !ok {
		t.Fatalf("expected noop sender, got %T", sender)
	}
}

func TestBuildWebhookPath(t *testing.T) {
	if got := BuildWebhookPath(""); got != "/feishu/webhook" {
		t.Fatalf("unexpected default path: %s", got)
	}
	if got := BuildWebhookPath("/feishu/webhook/my-secret"); got != "/feishu/webhook/my-secret" {
		t.Fatalf("unexpected normalized path: %s", got)
	}
}

func TestNewWebhookHandlerRejectNonPost(t *testing.T) {
	handler := NewWebhookHandler(nil, nil, nil, HandlerOptions{VerifyToken: "token"})
	req := httptest.NewRequest(http.MethodGet, "/feishu/webhook", nil)
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rr.Code)
	}
}

func TestIsGatewayTurnTerminalStatus(t *testing.T) {
	if !isGatewayTurnTerminalStatus("succeeded") {
		t.Fatalf("succeeded should be terminal")
	}
	if !isGatewayTurnTerminalStatus("FAILED") {
		t.Fatalf("FAILED should be terminal")
	}
	if isGatewayTurnTerminalStatus("") {
		t.Fatalf("empty status should be non-terminal")
	}
	if isGatewayTurnTerminalStatus("running") {
		t.Fatalf("running should be non-terminal")
	}
}

func TestSendTextRefreshesTokenWhenCachedTokenInvalid(t *testing.T) {
	var tokenCalls atomic.Int32
	var sendCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			tokenCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "fresh-token",
				"expire":              7200,
			})
		case r.URL.Path == "/open-apis/im/v1/messages":
			sendCalls.Add(1)
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			switch auth {
			case "Bearer stale-token":
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": daemonFeishuInvalidTokenCode,
					"msg":  "Invalid access token for authorization. Please make a request with token attached.",
				})
			case "Bearer fresh-token":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 0,
					"msg":  "ok",
					"data": map[string]any{
						"message_id": "om_test_1",
					},
				})
			default:
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 999,
					"msg":  "unexpected authorization token",
				})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	sender := NewSender(SenderConfig{
		Enabled:   true,
		AppID:     "app-id",
		AppSecret: "app-secret",
		BaseURL:   server.URL,
	})
	httpSender, ok := sender.(*daemonFeishuHTTPSender)
	if !ok {
		t.Fatalf("expected daemonFeishuHTTPSender, got %T", sender)
	}
	httpSender.mu.Lock()
	httpSender.token = "stale-token"
	httpSender.tokenUntil = time.Now().Add(5 * time.Minute)
	httpSender.mu.Unlock()

	if err := sender.SendText(context.Background(), "oc_test", "hello"); err != nil {
		t.Fatalf("SendText returned error: %v", err)
	}
	if got := sendCalls.Load(); got != 2 {
		t.Fatalf("expected 2 send attempts, got %d", got)
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("expected 1 token refresh call, got %d", got)
	}
}

func TestGetUserNameRefreshesTokenWhenCachedTokenInvalid(t *testing.T) {
	var tokenCalls atomic.Int32
	var userCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			tokenCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "fresh-user-token",
				"expire":              7200,
			})
		case strings.HasPrefix(r.URL.Path, "/open-apis/contact/v3/users/"):
			userCalls.Add(1)
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			switch auth {
			case "Bearer stale-token":
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": daemonFeishuInvalidTokenCode,
					"msg":  "Invalid access token for authorization. Please make a request with token attached.",
				})
			case "Bearer fresh-user-token":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 0,
					"msg":  "ok",
					"data": map[string]any{
						"user": map[string]any{
							"name": "Dalek",
						},
					},
				})
			default:
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 999,
					"msg":  "unexpected authorization token",
				})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	sender := NewSender(SenderConfig{
		Enabled:   true,
		AppID:     "app-id",
		AppSecret: "app-secret",
		BaseURL:   server.URL,
	})
	httpSender, ok := sender.(*daemonFeishuHTTPSender)
	if !ok {
		t.Fatalf("expected daemonFeishuHTTPSender, got %T", sender)
	}
	httpSender.mu.Lock()
	httpSender.token = "stale-token"
	httpSender.tokenUntil = time.Now().Add(5 * time.Minute)
	httpSender.mu.Unlock()

	name, err := sender.GetUserName(context.Background(), "ou_test")
	if err != nil {
		t.Fatalf("GetUserName returned error: %v", err)
	}
	if name != "Dalek" {
		t.Fatalf("expected name Dalek, got %q", name)
	}
	if got := userCalls.Load(); got != 2 {
		t.Fatalf("expected 2 get-user attempts, got %d", got)
	}
	if got := tokenCalls.Load(); got != 1 {
		t.Fatalf("expected 1 token refresh call, got %d", got)
	}
}
