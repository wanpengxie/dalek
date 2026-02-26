package feishu

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
