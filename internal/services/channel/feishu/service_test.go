package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	"dalek/internal/store"
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

func TestTryHandleDaemonFeishuQuietCommand(t *testing.T) {
	gateway, resolver := newFeishuQuietTestGateway(t)
	sender := &captureFeishuSender{}
	ctx := context.Background()

	if handled := tryHandleDaemonFeishuQuietCommand(ctx, gateway, resolver, sender, "im.feishu", "chat-q", "hello"); handled {
		t.Fatalf("non-quiet message should not be handled")
	}

	if handled := tryHandleDaemonFeishuQuietCommand(ctx, gateway, resolver, sender, "im.feishu", "chat-q", "/quiet"); !handled {
		t.Fatalf("/quiet should be handled")
	}
	if got := sender.LastText(); !strings.Contains(got, "本群尚未绑定项目") {
		t.Fatalf("unexpected unbound hint: %q", got)
	}

	if _, err := gateway.BindProject(ctx, contracts.ChannelTypeIM, "im.feishu", "chat-q", "alpha"); err != nil {
		t.Fatalf("bind project failed: %v", err)
	}

	if handled := tryHandleDaemonFeishuQuietCommand(ctx, gateway, resolver, sender, "im.feishu", "chat-q", "/quiet"); !handled {
		t.Fatalf("/quiet status should be handled")
	}
	if got := sender.LastText(); got != "当前安静模式：关闭" {
		t.Fatalf("unexpected quiet status: %q", got)
	}

	if handled := tryHandleDaemonFeishuQuietCommand(ctx, gateway, resolver, sender, "im.feishu", "chat-q", "/quiet on"); !handled {
		t.Fatalf("/quiet on should be handled")
	}
	if got := sender.LastText(); got != "安静模式已开启，仅在被 @ 时回复" {
		t.Fatalf("unexpected /quiet on reply: %q", got)
	}
	quietMode, err := gateway.GetBindingQuietMode(ctx, contracts.ChannelTypeIM, "im.feishu", "chat-q")
	if err != nil {
		t.Fatalf("GetBindingQuietMode after on failed: %v", err)
	}
	if !quietMode {
		t.Fatalf("quiet mode should be true after /quiet on")
	}

	if handled := tryHandleDaemonFeishuQuietCommand(ctx, gateway, resolver, sender, "im.feishu", "chat-q", "/quiet"); !handled {
		t.Fatalf("/quiet status after on should be handled")
	}
	if got := sender.LastText(); got != "当前安静模式：开启" {
		t.Fatalf("unexpected quiet status after on: %q", got)
	}

	if handled := tryHandleDaemonFeishuQuietCommand(ctx, gateway, resolver, sender, "im.feishu", "chat-q", "/quiet off"); !handled {
		t.Fatalf("/quiet off should be handled")
	}
	if got := sender.LastText(); got != "安静模式已关闭，将响应所有消息" {
		t.Fatalf("unexpected /quiet off reply: %q", got)
	}
	quietMode, err = gateway.GetBindingQuietMode(ctx, contracts.ChannelTypeIM, "im.feishu", "chat-q")
	if err != nil {
		t.Fatalf("GetBindingQuietMode after off failed: %v", err)
	}
	if quietMode {
		t.Fatalf("quiet mode should be false after /quiet off")
	}

	if handled := tryHandleDaemonFeishuQuietCommand(ctx, gateway, resolver, sender, "im.feishu", "chat-q", "/quiet invalid"); !handled {
		t.Fatalf("invalid /quiet should still be handled")
	}
	if got := sender.LastText(); got != "命令格式错误，请使用 /quiet [on|off]" {
		t.Fatalf("unexpected invalid /quiet reply: %q", got)
	}
}

func TestHasDaemonFeishuMention(t *testing.T) {
	if hasDaemonFeishuMention(nil) {
		t.Fatalf("nil mentions should be false")
	}
	if hasDaemonFeishuMention([]daemonFeishuMention{}) {
		t.Fatalf("empty mentions should be false")
	}
	if !hasDaemonFeishuMention([]daemonFeishuMention{{Key: "@_user_1"}}) {
		t.Fatalf("non-empty mentions should be true")
	}
}

type captureFeishuSender struct {
	texts []string
}

func (s *captureFeishuSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	_ = chatID
	s.texts = append(s.texts, text)
	return nil
}

func (s *captureFeishuSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	_ = chatID
	_ = title
	_ = markdown
	return nil
}

func (s *captureFeishuSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	_ = ctx
	_ = chatID
	_ = cardJSON
	return "", nil
}

func (s *captureFeishuSender) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	_ = ctx
	_ = messageID
	_ = cardJSON
	return nil
}

func (s *captureFeishuSender) GetUserName(ctx context.Context, userID string) (string, error) {
	_ = ctx
	_ = userID
	return "", nil
}

func (s *captureFeishuSender) LastText() string {
	if len(s.texts) == 0 {
		return ""
	}
	return s.texts[len(s.texts)-1]
}

type feishuQuietTestRuntime struct{}

func (r *feishuQuietTestRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	_ = env
	return channelsvc.ProcessResult{ReplyText: "ok"}, nil
}

func (r *feishuQuietTestRuntime) GatewayTurnTimeout() time.Duration {
	return time.Second
}

type feishuQuietTestResolver struct {
	projects map[string]*channelsvc.ProjectContext
}

func (r *feishuQuietTestResolver) Resolve(name string) (*channelsvc.ProjectContext, error) {
	if r == nil || r.projects == nil {
		return nil, fmt.Errorf("resolver empty")
	}
	key := strings.TrimSpace(name)
	ctx, ok := r.projects[key]
	if !ok || ctx == nil {
		return nil, fmt.Errorf("project not found: %s", key)
	}
	return ctx, nil
}

func (r *feishuQuietTestResolver) ListProjects() ([]string, error) {
	if r == nil || r.projects == nil {
		return nil, nil
	}
	out := make([]string, 0, len(r.projects))
	for name := range r.projects {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func newFeishuQuietTestGateway(t *testing.T) (*channelsvc.Gateway, channelsvc.ProjectResolver) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "feishu-quiet-gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	resolver := &feishuQuietTestResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"alpha": {
				Name:     "alpha",
				RepoRoot: "/tmp/alpha",
				Runtime:  &feishuQuietTestRuntime{},
			},
		},
	}

	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 4})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}
	return gateway, resolver
}
