package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
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

func TestNewSender_DefaultDisablesSystemProxy(t *testing.T) {
	sender := NewSender(SenderConfig{
		Enabled:   true,
		AppID:     "app-id",
		AppSecret: "app-secret",
	})
	httpSender, ok := sender.(*daemonFeishuHTTPSender)
	if !ok {
		t.Fatalf("expected daemonFeishuHTTPSender, got %T", sender)
	}
	transport, ok := httpSender.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", httpSender.client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatalf("default proxy resolver should be nil")
	}
}

func TestNewSender_UseSystemProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:8999")
	sender := NewSender(SenderConfig{
		Enabled:        true,
		AppID:          "app-id",
		AppSecret:      "app-secret",
		UseSystemProxy: true,
	})
	httpSender, ok := sender.(*daemonFeishuHTTPSender)
	if !ok {
		t.Fatalf("expected daemonFeishuHTTPSender, got %T", sender)
	}
	transport, ok := httpSender.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", httpSender.client.Transport)
	}
	if transport.Proxy == nil {
		t.Fatalf("proxy resolver should not be nil when use_system_proxy=true")
	}
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "open.feishu.cn"}}
	proxyURL, err := transport.Proxy(req)
	if err != nil {
		t.Fatalf("proxy resolver returned error: %v", err)
	}
	if proxyURL == nil {
		t.Fatalf("proxy resolver should return proxy URL")
	}
	if proxyURL.String() != "http://127.0.0.1:8999" {
		t.Fatalf("unexpected proxy URL: %s", proxyURL.String())
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

type captureChatReplySender struct {
	calls chan chatReplyCall
}

type chatReplyCall struct {
	project  string
	chatID   string
	text     string
	cardJSON string
}

func (s *captureChatReplySender) SendChatReply(ctx context.Context, projectName, chatID, text, cardJSON string) error {
	_ = ctx
	select {
	case s.calls <- chatReplyCall{
		project:  strings.TrimSpace(projectName),
		chatID:   strings.TrimSpace(chatID),
		text:     strings.TrimSpace(text),
		cardJSON: strings.TrimSpace(cardJSON),
	}:
	default:
	}
	return nil
}

func TestNewWebhookHandler_UsesChatReplySenderWhenConfigured(t *testing.T) {
	gateway, resolver := newFeishuQuietTestGateway(t)
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-reply", "alpha"); err != nil {
		t.Fatalf("bind project failed: %v", err)
	}

	sender := &captureFeishuSender{}
	replySender := &captureChatReplySender{calls: make(chan chatReplyCall, 1)}
	handler := NewWebhookHandler(gateway, resolver, sender, HandlerOptions{
		VerifyToken:      "token-ok",
		RelayTimeout:     5 * time.Second,
		RelayIdleTimeout: 5 * time.Second,
		ChatReplySender:  replySender,
	})

	body := map[string]any{
		"header": map[string]any{
			"event_type": "im.message.receive_v1",
			"token":      "token-ok",
			"event_id":   "evt-reply-1",
		},
		"event": map[string]any{
			"sender": map[string]any{
				"sender_type": "user",
				"sender_id": map[string]any{
					"open_id": "ou_reply_1",
				},
			},
			"message": map[string]any{
				"message_id":   "om-reply-1",
				"chat_id":      "chat-reply",
				"message_type": "text",
				"content":      "{\"text\":\"hello\"}",
			},
		},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal webhook body failed: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/feishu/webhook", strings.NewReader(string(raw)))
	rr := httptest.NewRecorder()
	handler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rr.Code)
	}

	select {
	case call := <-replySender.calls:
		if call.project != "alpha" {
			t.Fatalf("project mismatch: %q", call.project)
		}
		if call.chatID != "chat-reply" {
			t.Fatalf("chat id mismatch: %q", call.chatID)
		}
		if call.text == "" {
			t.Fatalf("reply text should not be empty")
		}
		if call.cardJSON == "" {
			t.Fatalf("reply card json should not be empty")
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("wait chat reply sender timeout")
	}

	if sender.InteractiveCalls() != 0 {
		t.Fatalf("configured chat reply sender should bypass direct interactive sender")
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

func TestGetBotOpenIDCachesAndRefreshesTokenWhenCachedTokenInvalid(t *testing.T) {
	var tokenCalls atomic.Int32
	var botInfoCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			tokenCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "fresh-bot-token",
				"expire":              7200,
			})
		case r.URL.Path == "/open-apis/bot/v3/info":
			botInfoCalls.Add(1)
			auth := strings.TrimSpace(r.Header.Get("Authorization"))
			switch auth {
			case "Bearer stale-token":
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": daemonFeishuInvalidTokenCode,
					"msg":  "Invalid access token for authorization. Please make a request with token attached.",
				})
			case "Bearer fresh-bot-token":
				_ = json.NewEncoder(w).Encode(map[string]any{
					"code": 0,
					"msg":  "ok",
					"bot": map[string]any{
						"open_id": "ou_bot_1",
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

	openID1, err := sender.GetBotOpenID(context.Background())
	if err != nil {
		t.Fatalf("first GetBotOpenID returned error: %v", err)
	}
	openID2, err := sender.GetBotOpenID(context.Background())
	if err != nil {
		t.Fatalf("second GetBotOpenID returned error: %v", err)
	}
	if openID1 != "ou_bot_1" || openID2 != "ou_bot_1" {
		t.Fatalf("unexpected open_id values: first=%q second=%q", openID1, openID2)
	}
	if got := botInfoCalls.Load(); got != 2 {
		t.Fatalf("expected 2 bot info attempts (invalid token retry), got %d", got)
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

func TestNewWebhookHandlerQuietModeMentionGate(t *testing.T) {
	cases := []struct {
		name          string
		quietMode     bool
		botOpenID     string
		botOpenIDErr  error
		mentions      []daemonFeishuMention
		wantReply     bool
		wantBotIDCall int
	}{
		{
			name:      "quiet_on_mention_non_bot_skip",
			quietMode: true,
			botOpenID: "ou_bot",
			mentions: []daemonFeishuMention{
				{ID: daemonFeishuMentionID{OpenID: "ou_other"}},
			},
			wantReply:     false,
			wantBotIDCall: 1,
		},
		{
			name:      "quiet_on_mention_bot_reply",
			quietMode: true,
			botOpenID: "ou_bot",
			mentions: []daemonFeishuMention{
				{ID: daemonFeishuMentionID{OpenID: "ou_bot"}},
			},
			wantReply:     true,
			wantBotIDCall: 1,
		},
		{
			name:          "quiet_on_no_mention_skip",
			quietMode:     true,
			botOpenID:     "ou_bot",
			mentions:      nil,
			wantReply:     false,
			wantBotIDCall: 1,
		},
		{
			name:          "quiet_off_normal_reply",
			quietMode:     false,
			botOpenID:     "ou_bot",
			mentions:      nil,
			wantReply:     true,
			wantBotIDCall: 0,
		},
		{
			name:         "quiet_on_get_bot_open_id_failed_fallback_len",
			quietMode:    true,
			botOpenIDErr: fmt.Errorf("mock GetBotOpenID failed"),
			mentions: []daemonFeishuMention{
				{ID: daemonFeishuMentionID{OpenID: "ou_other"}},
			},
			wantReply:     true,
			wantBotIDCall: 1,
		},
	}

	for idx, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gateway, resolver := newFeishuQuietTestGateway(t)
			if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-quiet", "alpha"); err != nil {
				t.Fatalf("bind project failed: %v", err)
			}
			if err := gateway.SetBindingQuietMode(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-quiet", tc.quietMode); err != nil {
				t.Fatalf("SetBindingQuietMode failed: %v", err)
			}

			sender := &captureFeishuSender{
				botOpenID:    tc.botOpenID,
				botOpenIDErr: tc.botOpenIDErr,
			}
			replySender := &captureChatReplySender{calls: make(chan chatReplyCall, 1)}
			handler := NewWebhookHandler(gateway, resolver, sender, HandlerOptions{
				VerifyToken:      "token-ok",
				RelayTimeout:     5 * time.Second,
				RelayIdleTimeout: 5 * time.Second,
				ChatReplySender:  replySender,
			})

			body := map[string]any{
				"header": map[string]any{
					"event_type": "im.message.receive_v1",
					"token":      "token-ok",
					"event_id":   fmt.Sprintf("evt-quiet-%d", idx+1),
				},
				"event": map[string]any{
					"sender": map[string]any{
						"sender_type": "user",
						"sender_id": map[string]any{
							"open_id": "ou_sender_1",
						},
					},
					"message": map[string]any{
						"message_id":   fmt.Sprintf("om-quiet-%d", idx+1),
						"chat_id":      "chat-quiet",
						"message_type": "text",
						"content":      "{\"text\":\"hello\"}",
						"mentions":     tc.mentions,
					},
				},
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal webhook body failed: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/feishu/webhook", strings.NewReader(string(raw)))
			rr := httptest.NewRecorder()
			handler(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("unexpected status: %d", rr.Code)
			}

			if tc.wantReply {
				select {
				case <-replySender.calls:
				case <-time.After(3 * time.Second):
					t.Fatalf("wait chat reply timeout")
				}
			} else {
				select {
				case call := <-replySender.calls:
					t.Fatalf("quiet mode should skip, but got reply: %+v", call)
				case <-time.After(1200 * time.Millisecond):
				}
			}
			if got := sender.BotOpenIDCalls(); got != tc.wantBotIDCall {
				t.Fatalf("GetBotOpenID calls mismatch: got=%d want=%d", got, tc.wantBotIDCall)
			}
		})
	}
}

func TestTryHandleDaemonFeishuHelpCommand(t *testing.T) {
	sender := &captureFeishuSender{}
	ctx := context.Background()

	if handled := tryHandleDaemonFeishuHelpCommand(ctx, sender, "chat-help", "hello"); handled {
		t.Fatalf("non-help message should not be handled")
	}
	if handled := tryHandleDaemonFeishuHelpCommand(ctx, sender, "chat-help", "/help more"); handled {
		t.Fatalf("/help with args should not be handled")
	}
	if handled := tryHandleDaemonFeishuHelpCommand(ctx, sender, "chat-help", "/HELP"); !handled {
		t.Fatalf("/HELP should be handled case-insensitively")
	}

	got := sender.LastText()
	wantLines := []string{
		"支持的命令：",
		"/help         显示此帮助",
		"/quiet        查看安静模式状态",
		"/quiet on     开启安静模式（只有被 @ 才回复）",
		"/quiet off    关闭安静模式",
	}
	for _, line := range wantLines {
		if !strings.Contains(got, line) {
			t.Fatalf("help text missing line %q, got: %q", line, got)
		}
	}
}

func TestHasDaemonFeishuMention(t *testing.T) {
	tests := []struct {
		name      string
		mentions  []daemonFeishuMention
		botOpenID string
		want      bool
	}{
		{
			name:      "empty_mentions_false",
			mentions:  nil,
			botOpenID: "ou_bot",
			want:      false,
		},
		{
			name: "bot_open_id_match_true",
			mentions: []daemonFeishuMention{
				{ID: daemonFeishuMentionID{OpenID: "ou_bot"}},
			},
			botOpenID: "ou_bot",
			want:      true,
		},
		{
			name: "bot_open_id_not_match_false",
			mentions: []daemonFeishuMention{
				{ID: daemonFeishuMentionID{OpenID: "ou_other"}},
			},
			botOpenID: "ou_bot",
			want:      false,
		},
		{
			name: "bot_open_id_empty_fallback_non_empty_mentions_true",
			mentions: []daemonFeishuMention{
				{ID: daemonFeishuMentionID{OpenID: "ou_other"}},
			},
			botOpenID: "",
			want:      true,
		},
		{
			name:      "bot_open_id_empty_fallback_empty_mentions_false",
			mentions:  []daemonFeishuMention{},
			botOpenID: "",
			want:      false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasDaemonFeishuMention(tc.mentions, tc.botOpenID); got != tc.want {
				t.Fatalf("hasDaemonFeishuMention() = %v, want %v", got, tc.want)
			}
		})
	}
}

type captureFeishuSender struct {
	mu               sync.Mutex
	texts            []string
	interactiveCalls int
	botOpenID        string
	botOpenIDErr     error
	botOpenIDCalls   int
}

func (s *captureFeishuSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	_ = chatID
	s.mu.Lock()
	s.texts = append(s.texts, text)
	s.mu.Unlock()
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
	s.mu.Lock()
	s.interactiveCalls++
	s.mu.Unlock()
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

func (s *captureFeishuSender) GetBotOpenID(ctx context.Context) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.botOpenIDCalls++
	if s.botOpenIDErr != nil {
		return "", s.botOpenIDErr
	}
	return strings.TrimSpace(s.botOpenID), nil
}

func (s *captureFeishuSender) LastText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.texts) == 0 {
		return ""
	}
	return s.texts[len(s.texts)-1]
}

func (s *captureFeishuSender) InteractiveCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interactiveCalls
}

func (s *captureFeishuSender) BotOpenIDCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.botOpenIDCalls
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
