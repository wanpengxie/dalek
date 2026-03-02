package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/app"
	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"

	"gorm.io/gorm"
)

type staticProjectResolver struct {
	projects map[string]*channelsvc.ProjectContext
}

func (r *staticProjectResolver) Resolve(name string) (*channelsvc.ProjectContext, error) {
	if r == nil || r.projects == nil {
		return nil, fmt.Errorf("resolver 未初始化")
	}
	key := strings.TrimSpace(name)
	project, ok := r.projects[key]
	if !ok || project == nil {
		return nil, fmt.Errorf("project not found: %s", key)
	}
	return project, nil
}

func (r *staticProjectResolver) ListProjects() ([]string, error) {
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
	sort.Strings(out)
	return out, nil
}

func openGatewayDBForFeishuTest(t *testing.T) *gorm.DB {
	t.Helper()
	h, err := app.OpenHome(t.TempDir())
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	db, err := h.OpenGatewayDB()
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	return db
}

type echoProjectRuntime struct{}

func (r *echoProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	return channelsvc.ProcessResult{
		RunID:         "run-feishu-e2e-1",
		JobStatus:     contracts.ChannelTurnSucceeded,
		ReplyText:     "echo: " + strings.TrimSpace(env.Text),
		AgentProvider: "fake",
		AgentModel:    "test",
	}, nil
}

func (r *echoProjectRuntime) GatewayTurnTimeout() time.Duration {
	return 3 * time.Second
}

type noEventProjectRuntime struct{}

func (r *noEventProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	return channelsvc.ProcessResult{
		RunID:         "run-feishu-no-event",
		JobStatus:     contracts.ChannelTurnSucceeded,
		ReplyText:     "done: " + strings.TrimSpace(env.Text),
		AgentProvider: "fake",
		AgentModel:    "test",
	}, nil
}

func (r *noEventProjectRuntime) GatewayTurnTimeout() time.Duration {
	return 3 * time.Second
}

type countingProjectRuntime struct {
	mu    sync.Mutex
	calls int
}

func (r *countingProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	_ = env
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return channelsvc.ProcessResult{
		RunID:         "run-feishu-counting",
		JobStatus:     contracts.ChannelTurnSucceeded,
		ReplyText:     "ok",
		AgentProvider: "fake",
		AgentModel:    "test",
	}, nil
}

func (r *countingProjectRuntime) GatewayTurnTimeout() time.Duration {
	return 3 * time.Second
}

func (r *countingProjectRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type captureInboundRuntime struct {
	mu      sync.Mutex
	lastEnv contracts.InboundEnvelope
}

func (r *captureInboundRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	r.mu.Lock()
	r.lastEnv = env
	r.mu.Unlock()
	return channelsvc.ProcessResult{
		RunID:         "run-feishu-capture-inbound",
		JobStatus:     contracts.ChannelTurnSucceeded,
		ReplyText:     "ok",
		AgentProvider: "fake",
		AgentModel:    "test",
	}, nil
}

func (r *captureInboundRuntime) GatewayTurnTimeout() time.Duration {
	return 3 * time.Second
}

func (r *captureInboundRuntime) snapshot() contracts.InboundEnvelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.lastEnv
}

type feishuScriptedEventRuntime struct {
	delay time.Duration
}

func (r *feishuScriptedEventRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	if r != nil && r.delay > 0 {
		timer := time.NewTimer(r.delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return channelsvc.ProcessResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	msgID := strings.TrimSpace(env.PeerMessageID)
	if msgID == "" {
		msgID = "unknown"
	}
	runID := "run-" + msgID
	return channelsvc.ProcessResult{
		RunID:         runID,
		JobStatus:     contracts.ChannelTurnSucceeded,
		ReplyText:     "done " + msgID,
		AgentProvider: "fake",
		AgentModel:    "test",
		AgentEvents: []channelsvc.AgentEvent{
			{
				RunID:  runID,
				Seq:    1,
				Stream: channelsvc.StreamAssistant,
				Ts:     time.Now().UnixMilli(),
				Data: channelsvc.AgentEventData{
					Text: "progress " + msgID,
				},
			},
		},
	}, nil
}

func (r *feishuScriptedEventRuntime) GatewayTurnTimeout() time.Duration {
	return 5 * time.Second
}

type interruptableProjectRuntime struct {
	interruptOK  bool
	interruptErr error
	resetOK      bool
	resetErr     error
	hardResetOK  bool
	hardResetErr error

	mu                 sync.Mutex
	interruptCalls     int
	lastChannelType    contracts.ChannelType
	lastAdapter        string
	lastConversationID string
	resetCalls         int
	lastResetConvID    string
	hardResetCalls     int
	lastHardResetConv  string
}

func (r *interruptableProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	return channelsvc.ProcessResult{
		RunID:         "run-feishu-interrupt",
		JobStatus:     contracts.ChannelTurnSucceeded,
		ReplyText:     "echo: " + strings.TrimSpace(env.Text),
		AgentProvider: "fake",
		AgentModel:    "test",
	}, nil
}

func (r *interruptableProjectRuntime) GatewayTurnTimeout() time.Duration {
	return 3 * time.Second
}

func (r *interruptableProjectRuntime) InterruptConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (channelsvc.InterruptResult, error) {
	_ = ctx
	r.mu.Lock()
	r.interruptCalls++
	r.lastChannelType = contracts.ChannelType(strings.TrimSpace(string(channelType)))
	r.lastAdapter = strings.TrimSpace(adapter)
	r.lastConversationID = strings.TrimSpace(peerConversationID)
	r.mu.Unlock()
	if r.interruptErr != nil {
		return channelsvc.InterruptResult{}, r.interruptErr
	}
	if r.interruptOK {
		return channelsvc.InterruptResult{
			Status:            channelsvc.InterruptStatusHit,
			RunnerInterrupted: true,
		}, nil
	}
	return channelsvc.InterruptResult{Status: channelsvc.InterruptStatusMiss}, nil
}

func (r *interruptableProjectRuntime) ResetConversationSession(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (bool, error) {
	_ = ctx
	r.mu.Lock()
	r.resetCalls++
	r.lastResetConvID = strings.TrimSpace(peerConversationID)
	r.mu.Unlock()
	if r.resetErr != nil {
		return false, r.resetErr
	}
	return r.resetOK, nil
}

func (r *interruptableProjectRuntime) HardResetConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (bool, error) {
	_ = ctx
	r.mu.Lock()
	r.hardResetCalls++
	r.lastHardResetConv = strings.TrimSpace(peerConversationID)
	r.mu.Unlock()
	if r.hardResetErr != nil {
		return false, r.hardResetErr
	}
	return r.hardResetOK, nil
}

type senderMessage struct {
	chatID string
	kind   string
	title  string
	text   string
	msgID  string
}

type captureFeishuSender struct {
	mu       sync.Mutex
	messages []senderMessage
	msgIDSeq int

	userNames        map[string]string
	getUserNameErr   error
	getUserNameCalls int
}

func (s *captureFeishuSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	s.mu.Lock()
	s.messages = append(s.messages, senderMessage{
		chatID: strings.TrimSpace(chatID),
		kind:   "text",
		text:   strings.TrimSpace(text),
	})
	s.mu.Unlock()
	return nil
}

func (s *captureFeishuSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	s.mu.Lock()
	s.messages = append(s.messages, senderMessage{
		chatID: strings.TrimSpace(chatID),
		kind:   "card",
		title:  strings.TrimSpace(title),
		text:   strings.TrimSpace(markdown),
	})
	s.mu.Unlock()
	return nil
}

func (s *captureFeishuSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	_ = ctx
	s.mu.Lock()
	s.msgIDSeq++
	mid := fmt.Sprintf("om_%d", s.msgIDSeq)
	s.messages = append(s.messages, senderMessage{
		chatID: strings.TrimSpace(chatID),
		kind:   "card_interactive",
		text:   strings.TrimSpace(cardJSON),
		msgID:  mid,
	})
	s.mu.Unlock()
	return mid, nil
}

func (s *captureFeishuSender) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	_ = ctx
	s.mu.Lock()
	s.messages = append(s.messages, senderMessage{
		kind:  "patch",
		text:  strings.TrimSpace(cardJSON),
		msgID: strings.TrimSpace(messageID),
	})
	s.mu.Unlock()
	return nil
}

func (s *captureFeishuSender) GetUserName(ctx context.Context, userID string) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getUserNameCalls++
	if s.getUserNameErr != nil {
		return "", s.getUserNameErr
	}
	return strings.TrimSpace(s.userNames[strings.TrimSpace(userID)]), nil
}

func (s *captureFeishuSender) GetBotOpenID(ctx context.Context) (string, error) {
	_ = ctx
	return "", nil
}

func (s *captureFeishuSender) waitMessages(t *testing.T, want int, timeout time.Duration) []senderMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := append([]senderMessage(nil), s.messages...)
		s.mu.Unlock()
		if len(got) >= want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	got := append([]senderMessage(nil), s.messages...)
	s.mu.Unlock()
	t.Fatalf("wait sender messages timeout: want>=%d got=%d", want, len(got))
	return nil
}

func (s *captureFeishuSender) snapshot() []senderMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]senderMessage(nil), s.messages...)
}

type flakyFinalReplySender struct {
	captureFeishuSender

	failSendCardTimes int
	sendCardCalls     int
	failSendTextTimes int
	sendTextCalls     int
}

func (s *flakyFinalReplySender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendTextCalls++
	if s.failSendTextTimes > 0 {
		s.failSendTextTimes--
		return fmt.Errorf("mock text send failed")
	}
	s.messages = append(s.messages, senderMessage{
		chatID: strings.TrimSpace(chatID),
		kind:   "text",
		text:   strings.TrimSpace(text),
	})
	return nil
}

func (s *flakyFinalReplySender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendCardCalls++
	if s.failSendCardTimes > 0 {
		s.failSendCardTimes--
		return fmt.Errorf("mock send failed")
	}
	s.messages = append(s.messages, senderMessage{
		chatID: strings.TrimSpace(chatID),
		kind:   "card",
		title:  strings.TrimSpace(title),
		text:   strings.TrimSpace(markdown),
	})
	return nil
}

func (s *flakyFinalReplySender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sendCardCalls++
	if s.failSendCardTimes > 0 {
		s.failSendCardTimes--
		return "", fmt.Errorf("mock send failed")
	}
	s.msgIDSeq++
	mid := fmt.Sprintf("om_%d", s.msgIDSeq)
	s.messages = append(s.messages, senderMessage{
		chatID: strings.TrimSpace(chatID),
		kind:   "card_interactive",
		text:   strings.TrimSpace(cardJSON),
		msgID:  mid,
	})
	return mid, nil
}

func (s *flakyFinalReplySender) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sendCardCalls
}

type cardFailTextOKSender struct {
	captureFeishuSender
}

func (s *cardFailTextOKSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	_ = chatID
	_ = title
	_ = markdown
	return fmt.Errorf("feishu send message failed: http=400 body={\"code\":230099,\"msg\":\"Failed to create card content, ext=ErrCode: 11310; ErrMsg: card table number over limit; ErrorValue: table; \"}")
}

func (s *cardFailTextOKSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	_ = ctx
	_ = chatID
	_ = cardJSON
	return "", fmt.Errorf("feishu send message failed: http=400 body={\"code\":230099,\"msg\":\"Failed to create card content, ext=ErrCode: 11310; ErrMsg: card table number over limit; ErrorValue: table; \"}")
}

func (s *cardFailTextOKSender) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	_ = ctx
	_ = messageID
	_ = cardJSON
	return fmt.Errorf("feishu patch card failed: http=400 body={\"code\":230099,\"msg\":\"Failed to create card content, ext=ErrCode: 11310; ErrMsg: card table number over limit; ErrorValue: table; \"}")
}

func TestGatewayFeishuWebhook_BindForwardUnbindFlow(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  &echoProjectRuntime{},
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	sender := &captureFeishuSender{}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-1", "msg-bind", "open-user-1", "/bind demo")
	msgs := sender.waitMessages(t, 1, 2*time.Second)
	if !strings.Contains(msgs[0].text, "已绑定到 project demo") {
		t.Fatalf("bind reply unexpected: %q", msgs[0].text)
	}

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-1", "msg-hello", "open-user-1", "你好")
	msgs = sender.waitMessages(t, 2, 3*time.Second)
	if msgs[1].kind != "card_interactive" {
		t.Fatalf("forward reply should send interactive card, got kind=%q", msgs[1].kind)
	}
	if !strings.Contains(msgs[1].text, "echo: 你好") {
		t.Fatalf("forward reply unexpected: %q", msgs[1].text)
	}

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-1", "msg-unbind", "open-user-1", "/unbind")
	msgs = sender.waitMessages(t, 3, 2*time.Second)
	if !strings.Contains(msgs[2].text, "已解绑") {
		t.Fatalf("unbind reply unexpected: %q", msgs[2].text)
	}

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-1", "msg-after-unbind", "open-user-1", "还在吗")
	msgs = sender.waitMessages(t, 4, 2*time.Second)
	if !strings.Contains(msgs[3].text, "本群尚未绑定项目") {
		t.Fatalf("unbound hint unexpected: %q", msgs[3].text)
	}
	if !strings.Contains(msgs[3].text, "demo") {
		t.Fatalf("unbound hint should include project list, got: %q", msgs[3].text)
	}
}

func TestGatewayFeishuWebhook_ForwardIncludesSenderName(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	runtime := &captureInboundRuntime{}
	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  runtime,
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	sender := &captureFeishuSender{
		userNames: map[string]string{
			"open-user-1": "Alice",
		},
	}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-sender", "msg-bind", "open-user-1", "/bind demo")
	_ = sender.waitMessages(t, 1, 2*time.Second)

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-sender", "msg-hello", "open-user-1", "你好")
	_ = sender.waitMessages(t, 2, 3*time.Second)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got := runtime.snapshot()
		if strings.TrimSpace(got.PeerMessageID) == "msg-hello" {
			if got.SenderID != "open-user-1" {
				t.Fatalf("sender id mismatch: %q", got.SenderID)
			}
			if got.SenderName != "Alice" {
				t.Fatalf("sender name mismatch: %q", got.SenderName)
			}
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("runtime did not capture forwarded message")
}

func TestGatewayFeishuWebhook_InterruptCommand(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	runtime := &interruptableProjectRuntime{interruptOK: true}
	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  runtime,
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	sender := &captureFeishuSender{}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-2", "msg-bind", "open-user-1", "/bind demo")
	msgs := sender.waitMessages(t, 1, 2*time.Second)
	if !strings.Contains(msgs[0].text, "已绑定到 project demo") {
		t.Fatalf("bind reply unexpected: %q", msgs[0].text)
	}

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-2", "msg-interrupt", "open-user-1", "/stop")
	msgs = sender.waitMessages(t, 2, 2*time.Second)
	if !strings.Contains(msgs[1].text, "已发送中断信号") {
		t.Fatalf("interrupt reply unexpected: %q", msgs[1].text)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.interruptCalls != 1 {
		t.Fatalf("interrupt calls mismatch: %d", runtime.interruptCalls)
	}
	if runtime.lastChannelType != contracts.ChannelTypeIM {
		t.Fatalf("channel type mismatch: %q", runtime.lastChannelType)
	}
	if runtime.lastAdapter != "im.feishu" {
		t.Fatalf("adapter mismatch: %q", runtime.lastAdapter)
	}
	if runtime.lastConversationID != "chat-feishu-2" {
		t.Fatalf("conversation id mismatch: %q", runtime.lastConversationID)
	}
}

func TestGatewayFeishuWebhook_NewCommand(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	runtime := &interruptableProjectRuntime{resetOK: true}
	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  runtime,
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	sender := &captureFeishuSender{}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-new", "msg-bind", "open-user-1", "/bind demo")
	msgs := sender.waitMessages(t, 1, 2*time.Second)
	if !strings.Contains(msgs[0].text, "已绑定到 project demo") {
		t.Fatalf("bind reply unexpected: %q", msgs[0].text)
	}

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-new", "msg-new", "open-user-1", "/new")
	msgs = sender.waitMessages(t, 2, 2*time.Second)
	if !strings.Contains(msgs[1].text, "已重置会话") {
		t.Fatalf("new reply unexpected: %q", msgs[1].text)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.resetCalls != 1 {
		t.Fatalf("reset calls mismatch: %d", runtime.resetCalls)
	}
	if runtime.lastResetConvID != "chat-feishu-new" {
		t.Fatalf("reset conversation id mismatch: %q", runtime.lastResetConvID)
	}
}

func TestGatewayFeishuWebhook_ResetCommand(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	runtime := &interruptableProjectRuntime{hardResetOK: true}
	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  runtime,
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	sender := &captureFeishuSender{}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-reset", "msg-bind", "open-user-1", "/bind demo")
	msgs := sender.waitMessages(t, 1, 2*time.Second)
	if !strings.Contains(msgs[0].text, "已绑定到 project demo") {
		t.Fatalf("bind reply unexpected: %q", msgs[0].text)
	}

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-reset", "msg-reset", "open-user-1", "/reset")
	msgs = sender.waitMessages(t, 2, 2*time.Second)
	if !strings.Contains(msgs[1].text, "已彻底重置会话") {
		t.Fatalf("reset reply unexpected: %q", msgs[1].text)
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.hardResetCalls != 1 {
		t.Fatalf("hard reset calls mismatch: %d", runtime.hardResetCalls)
	}
	if runtime.lastHardResetConv != "chat-feishu-reset" {
		t.Fatalf("hard reset conversation id mismatch: %q", runtime.lastHardResetConv)
	}
}

func TestGatewayFeishuWebhook_EventBusRelayFiltersByPeerMessageID(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  &feishuScriptedEventRuntime{delay: 200 * time.Millisecond},
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	sender := &captureFeishuSender{}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-filter", "msg-bind", "open-user-1", "/bind demo")
	_ = sender.waitMessages(t, 1, 2*time.Second)

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-filter", "msg-a", "open-user-1", "A")
	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-filter", "msg-b", "open-user-1", "B")

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		doneA := 0
		doneB := 0
		for _, m := range msgs {
			switch m.kind {
			case "patch":
				if strings.Contains(m.text, "done msg-a") {
					doneA++
				}
				if strings.Contains(m.text, "done msg-b") {
					doneB++
				}
			case "card", "card_interactive":
				if strings.Contains(m.text, "done msg-a") {
					doneA++
				}
				if strings.Contains(m.text, "done msg-b") {
					doneB++
				}
			}
		}
		if doneA == 1 && doneB == 1 {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}

	msgs := sender.snapshot()
	t.Fatalf("peer_message_id 过滤失败，messages=%+v", msgs)
}

func TestGatewayFeishuWebhook_DedupByEventID(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	runtime := &countingProjectRuntime{}
	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  runtime,
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-feishu-dedup", "demo"); err != nil {
		t.Fatalf("bind project failed: %v", err)
	}

	sender := &captureFeishuSender{}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})

	postFeishuTextEventWithEventID(t, handler, "verify-token", "chat-feishu-dedup", "msg-dedup", "evt-dedup", "open-user-1", "hello")

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.callCount() == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if runtime.callCount() != 1 {
		t.Fatalf("first event should be processed once, got=%d", runtime.callCount())
	}

	postFeishuTextEventWithEventID(t, handler, "verify-token", "chat-feishu-dedup", "msg-dedup", "evt-dedup", "open-user-1", "hello")
	time.Sleep(250 * time.Millisecond)

	if runtime.callCount() != 1 {
		t.Fatalf("duplicate event should be skipped, got runtime calls=%d", runtime.callCount())
	}

	var inboundCount int64
	if err := db.Model(&contracts.ChannelMessage{}).Where("direction = ?", contracts.ChannelMessageIn).Count(&inboundCount).Error; err != nil {
		t.Fatalf("count inbound message failed: %v", err)
	}
	if inboundCount != 1 {
		t.Fatalf("duplicate event should not create new inbound record, got=%d", inboundCount)
	}

	var jobCount int64
	if err := db.Model(&contracts.ChannelTurnJob{}).Count(&jobCount).Error; err != nil {
		t.Fatalf("count turn jobs failed: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("duplicate event should not create new turn job, got=%d", jobCount)
	}
}

func TestGatewayFeishuWebhook_FinalReplyRetryAndOutboxSent(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  &noEventProjectRuntime{},
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	sender := &flakyFinalReplySender{
		failSendCardTimes: 1,
		failSendTextTimes: 1,
	}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-feishu-retry", "demo"); err != nil {
		t.Fatalf("bind project failed: %v", err)
	}

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-retry", "msg-retry", "open-user-1", "hello")

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		if sender.calls() >= 2 && len(msgs) >= 2 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if sender.calls() < 2 {
		t.Fatalf("expected at least 2 SendCard attempts, got=%d", sender.calls())
	}
	msgs := sender.snapshot()
	foundFinal := false
	for _, m := range msgs {
		if m.kind == "card_interactive" && strings.Contains(m.text, "done: hello") {
			foundFinal = true
			break
		}
	}
	if !foundFinal {
		t.Fatalf("final card not delivered, messages=%+v", msgs)
	}

	outDeadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(outDeadline) {
		var outbox contracts.ChannelOutbox
		if qErr := db.Order("id DESC").First(&outbox).Error; qErr == nil {
			if outbox.Status == contracts.ChannelOutboxSent {
				if strings.TrimSpace(outbox.LastError) != "" {
					t.Fatalf("outbox last_error should be empty when sent, got=%q", outbox.LastError)
				}
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	var outbox contracts.ChannelOutbox
	if qErr := db.Order("id DESC").First(&outbox).Error; qErr != nil {
		t.Fatalf("query outbox failed: %v", qErr)
	}
	t.Fatalf("outbox status should become sent, got=%s last_error=%q", outbox.Status, outbox.LastError)
}

func TestGatewayFeishuWebhook_FinalReplyFailedMarksOutboxFailed(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  &noEventProjectRuntime{},
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	sender := &flakyFinalReplySender{
		failSendCardTimes: 8,
		failSendTextTimes: 8,
	}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-feishu-failed", "demo"); err != nil {
		t.Fatalf("bind project failed: %v", err)
	}

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-failed", "msg-failed", "open-user-1", "hello")

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		if sender.calls() >= 2 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if sender.calls() < 2 {
		t.Fatalf("expected at least 2 SendCard attempts, got=%d", sender.calls())
	}

	outDeadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(outDeadline) {
		var outbox contracts.ChannelOutbox
		if qErr := db.Order("id DESC").First(&outbox).Error; qErr == nil {
			if outbox.Status == contracts.ChannelOutboxFailed {
				if !strings.Contains(outbox.LastError, "mock send failed") {
					t.Fatalf("outbox last_error should include send error, got=%q", outbox.LastError)
				}
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	var outbox contracts.ChannelOutbox
	if qErr := db.Order("id DESC").First(&outbox).Error; qErr != nil {
		t.Fatalf("query outbox failed: %v", qErr)
	}
	t.Fatalf("outbox status should become failed, got=%s last_error=%q", outbox.Status, outbox.LastError)
}

func TestGatewayFeishuWebhook_FinalReplyCardFailed_FallbackToText(t *testing.T) {
	db := openGatewayDBForFeishuTest(t)

	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"demo": {
				Name:     "demo",
				RepoRoot: "/tmp/demo",
				Runtime:  &noEventProjectRuntime{},
			},
		},
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}

	sender := &cardFailTextOKSender{}
	handler := newGatewayFeishuWebhookHandler(gateway, resolver, sender, gatewayFeishuHandlerOptions{
		Adapter:     "im.feishu",
		VerifyToken: "verify-token",
	})

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-text-fallback", "msg-bind", "open-user-1", "/bind demo")
	_ = sender.waitMessages(t, 1, 2*time.Second)

	postFeishuTextEvent(t, handler, "verify-token", "chat-feishu-text-fallback", "msg-fallback", "open-user-1", "hello")

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		foundText := false
		for i := range msgs {
			if strings.TrimSpace(msgs[i].kind) == "text" && strings.Contains(msgs[i].text, "done: hello") {
				foundText = true
				break
			}
		}
		if foundText {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	msgs := sender.snapshot()
	foundText := false
	for i := range msgs {
		if strings.TrimSpace(msgs[i].kind) == "text" && strings.Contains(msgs[i].text, "done: hello") {
			foundText = true
			break
		}
	}
	if !foundText {
		t.Fatalf("final text fallback not delivered, messages=%+v", msgs)
	}

	outDeadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(outDeadline) {
		var outbox contracts.ChannelOutbox
		if qErr := db.Order("id DESC").First(&outbox).Error; qErr == nil {
			if outbox.Status == contracts.ChannelOutboxSent {
				if strings.TrimSpace(outbox.LastError) != "" {
					t.Fatalf("outbox last_error should be empty when text fallback succeeded, got=%q", outbox.LastError)
				}
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	var outbox contracts.ChannelOutbox
	if qErr := db.Order("id DESC").First(&outbox).Error; qErr != nil {
		t.Fatalf("query outbox failed: %v", qErr)
	}
	t.Fatalf("outbox status should become sent on text fallback, got=%s last_error=%q", outbox.Status, outbox.LastError)
}

func postFeishuTextEvent(t *testing.T, handler http.HandlerFunc, token, chatID, msgID, senderID, text string) {
	t.Helper()
	postFeishuTextEventWithEventID(t, handler, token, chatID, msgID, msgID, senderID, text)
}

func postFeishuTextEventWithEventID(t *testing.T, handler http.HandlerFunc, token, chatID, msgID, eventID, senderID, text string) {
	t.Helper()
	contentBytes, err := json.Marshal(map[string]string{"text": strings.TrimSpace(text)})
	if err != nil {
		t.Fatalf("marshal content failed: %v", err)
	}
	payload := map[string]any{
		"header": map[string]any{
			"event_type": "im.message.receive_v1",
			"token":      strings.TrimSpace(token),
			"event_id":   strings.TrimSpace(eventID),
		},
		"event": map[string]any{
			"sender": map[string]any{
				"sender_id": map[string]any{
					"open_id": strings.TrimSpace(senderID),
				},
				"sender_type": "user",
			},
			"message": map[string]any{
				"message_id":   strings.TrimSpace(msgID),
				"chat_id":      strings.TrimSpace(chatID),
				"message_type": "text",
				"content":      string(contentBytes),
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/feishu/webhook", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("feishu webhook status=%d body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if !strings.Contains(strings.TrimSpace(rec.Body.String()), `"code":0`) {
		t.Fatalf("feishu webhook response unexpected: %s", strings.TrimSpace(rec.Body.String()))
	}
}

func TestNormalizeFeishuCardMarkdown_CodeFenceAndTruncate(t *testing.T) {
	got := normalizeFeishuCardMarkdown("## 标题\r\n~~~go\r\nfmt.Println(1)\r\n")
	if !strings.Contains(got, "```go") {
		t.Fatalf("expected code fence marker converted to ```go, got: %q", got)
	}
	if !strings.HasSuffix(got, "```") {
		t.Fatalf("expected markdown to auto-close code fence, got: %q", got)
	}
}

func TestAppendFeishuProgressLine_CapsHistory(t *testing.T) {
	got := appendFeishuProgressLine([]string{"a", "b"}, "   ", 2)
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("blank line should be ignored, got=%v", got)
	}

	got = appendFeishuProgressLine(got, " c ", 2)
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("history should keep latest 2 lines, got=%v", got)
	}

	got = appendFeishuProgressLine(got, "d", 0)
	if len(got) != 1 || got[0] != "d" {
		t.Fatalf("max<=0 should fallback to 1 line, got=%v", got)
	}
}

func TestResolveFeishuCardProjectName_UsesRepoBaseName(t *testing.T) {
	resolver := &staticProjectResolver{
		projects: map[string]*channelsvc.ProjectContext{
			"Users-xiewanpeng-agi-dalek": {
				Name:     "Users-xiewanpeng-agi-dalek",
				RepoRoot: "/Users/xiewanpeng/agi/dalek",
				Runtime:  &echoProjectRuntime{},
			},
		},
	}
	got := resolveFeishuCardProjectName("Users-xiewanpeng-agi-dalek", resolver)
	if got != "dalek" {
		t.Fatalf("project title should use repo basename, got=%q", got)
	}
}

func TestFeishuHTTPSender_SendCard_UsesInteractiveAndLarkMD(t *testing.T) {
	type capturedRequest struct {
		ReceiveID string `json:"receive_id"`
		MsgType   string `json:"msg_type"`
		Content   string `json:"content"`
	}
	var (
		mu         sync.Mutex
		capture    capturedRequest
		handlerErr error
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/open-apis/auth/v3/tenant_access_token/internal":
			writeGatewayJSON(w, http.StatusOK, map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "token-1",
				"expire":              7200,
			})
		case "/open-apis/im/v1/messages":
			raw, _ := io.ReadAll(r.Body)
			var req capturedRequest
			if err := json.Unmarshal(raw, &req); err != nil {
				mu.Lock()
				handlerErr = fmt.Errorf("decode message request failed: %w", err)
				mu.Unlock()
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			mu.Lock()
			capture = req
			mu.Unlock()
			writeGatewayJSON(w, http.StatusOK, map[string]any{"code": 0, "msg": "ok"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sender := newFeishuSender(feishuSenderConfig{
		AppID:     "app-id",
		AppSecret: "app-secret",
		BaseURL:   srv.URL,
	})
	err := sender.SendCard(context.Background(), "chat-1", "  demo project  ", "~~~go\nfmt.Println(1)\n")
	if err != nil {
		t.Fatalf("SendCard failed: %v", err)
	}

	mu.Lock()
	got := capture
	gotHandlerErr := handlerErr
	mu.Unlock()
	if gotHandlerErr != nil {
		t.Fatalf("server handler failed: %v", gotHandlerErr)
	}
	if got.ReceiveID != "chat-1" {
		t.Fatalf("receive_id mismatch: %q", got.ReceiveID)
	}
	if got.MsgType != "interactive" {
		t.Fatalf("msg_type should be interactive, got=%q", got.MsgType)
	}
	var card struct {
		Schema string `json:"schema"`
		Header struct {
			Title struct {
				Tag     string `json:"tag"`
				Content string `json:"content"`
			} `json:"title"`
		} `json:"header"`
		Body struct {
			Elements []struct {
				Tag     string `json:"tag"`
				Content string `json:"content"`
			} `json:"elements"`
		} `json:"body"`
	}
	if err := json.Unmarshal([]byte(got.Content), &card); err != nil {
		t.Fatalf("decode card content failed: %v", err)
	}
	if card.Schema != "2.0" {
		t.Fatalf("card schema should be 2.0, got=%q", card.Schema)
	}
	if card.Header.Title.Tag != "plain_text" {
		t.Fatalf("card title tag mismatch: %q", card.Header.Title.Tag)
	}
	if card.Header.Title.Content != "demo project" {
		t.Fatalf("card title mismatch: %q", card.Header.Title.Content)
	}
	if len(card.Body.Elements) != 1 || card.Body.Elements[0].Tag != "markdown" {
		t.Fatalf("card markdown element missing: %+v", card.Body.Elements)
	}
	if !strings.Contains(card.Body.Elements[0].Content, "```go") {
		t.Fatalf("card markdown should use code fence ```go, got=%q", card.Body.Elements[0].Content)
	}
}

func TestFeishuHTTPSender_GetUserName_UsesCache(t *testing.T) {
	var (
		mu        sync.Mutex
		tokenHits int
		userHits  int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/open-apis/auth/v3/tenant_access_token/internal":
			mu.Lock()
			tokenHits++
			mu.Unlock()
			writeGatewayJSON(w, http.StatusOK, map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "token-1",
				"expire":              7200,
			})
		case strings.HasPrefix(r.URL.Path, "/open-apis/contact/v3/users/"):
			mu.Lock()
			userHits++
			mu.Unlock()
			if r.URL.Query().Get("user_id_type") != "open_id" {
				http.Error(w, "invalid user_id_type", http.StatusBadRequest)
				return
			}
			writeGatewayJSON(w, http.StatusOK, map[string]any{
				"code": 0,
				"msg":  "ok",
				"data": map[string]any{
					"user": map[string]any{
						"name": "Alice",
					},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sender := newFeishuSender(feishuSenderConfig{
		AppID:     "app-id",
		AppSecret: "app-secret",
		BaseURL:   srv.URL,
	})
	name1, err := sender.GetUserName(context.Background(), "ou_cache_test")
	if err != nil {
		t.Fatalf("first GetUserName failed: %v", err)
	}
	name2, err := sender.GetUserName(context.Background(), "ou_cache_test")
	if err != nil {
		t.Fatalf("second GetUserName failed: %v", err)
	}
	if name1 != "Alice" || name2 != "Alice" {
		t.Fatalf("unexpected names: name1=%q name2=%q", name1, name2)
	}

	mu.Lock()
	gotTokenHits := tokenHits
	gotUserHits := userHits
	mu.Unlock()
	if gotTokenHits != 1 {
		t.Fatalf("tenant token should be fetched once, got=%d", gotTokenHits)
	}
	if gotUserHits != 1 {
		t.Fatalf("user profile should be fetched once due to cache, got=%d", gotUserHits)
	}
}
