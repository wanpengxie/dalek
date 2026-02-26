package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type testDaemonFeishuRuntime struct {
	received chan contracts.InboundEnvelope
	reply    string
}

func (r *testDaemonFeishuRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	select {
	case r.received <- env:
	default:
	}
	return channelsvc.ProcessResult{
		ReplyText: strings.TrimSpace(r.reply),
		JobStatus: contracts.ChannelTurnSucceeded,
	}, nil
}

func (r *testDaemonFeishuRuntime) GatewayTurnTimeout() time.Duration {
	return 0
}

type testDaemonFeishuNoEventRuntime struct{}

func (r *testDaemonFeishuNoEventRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	return channelsvc.ProcessResult{
		ReplyText: "done: " + strings.TrimSpace(env.Text),
		JobStatus: contracts.ChannelTurnSucceeded,
	}, nil
}

func (r *testDaemonFeishuNoEventRuntime) GatewayTurnTimeout() time.Duration {
	return 0
}

type testDaemonFeishuCountingRuntime struct {
	mu    sync.Mutex
	calls int
}

func (r *testDaemonFeishuCountingRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	_ = env
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return channelsvc.ProcessResult{
		ReplyText: "ok",
		JobStatus: contracts.ChannelTurnSucceeded,
	}, nil
}

func (r *testDaemonFeishuCountingRuntime) GatewayTurnTimeout() time.Duration {
	return 0
}

func (r *testDaemonFeishuCountingRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type testDaemonFeishuScriptedEventRuntime struct {
	delay time.Duration
}

func (r *testDaemonFeishuScriptedEventRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
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
		RunID:     runID,
		ReplyText: "done " + msgID,
		JobStatus: contracts.ChannelTurnSucceeded,
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

func (r *testDaemonFeishuScriptedEventRuntime) GatewayTurnTimeout() time.Duration {
	return 5 * time.Second
}

type testDaemonFeishuDelayedNoEventRuntime struct {
	delay time.Duration
}

func (r *testDaemonFeishuDelayedNoEventRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	wait := 200 * time.Millisecond
	if r != nil && r.delay > 0 {
		wait = r.delay
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return channelsvc.ProcessResult{}, ctx.Err()
	case <-timer.C:
	}
	return channelsvc.ProcessResult{
		ReplyText: "done slow " + strings.TrimSpace(env.PeerMessageID),
		JobStatus: contracts.ChannelTurnSucceeded,
	}, nil
}

func (r *testDaemonFeishuDelayedNoEventRuntime) GatewayTurnTimeout() time.Duration {
	return 5 * time.Second
}

type testDaemonFeishuApprovalRuntime struct {
	reply         string
	pending       []channelsvc.PendingActionView
	decisionError error
	decisionReply channelsvc.PendingActionDecisionResult

	mu           sync.Mutex
	decisionReqs []channelsvc.PendingActionDecisionRequest
}

func (r *testDaemonFeishuApprovalRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	_ = env
	reply := "请审批后继续"
	if r != nil && strings.TrimSpace(r.reply) != "" {
		reply = strings.TrimSpace(r.reply)
	}
	pending := []channelsvc.PendingActionView{
		{
			ID:             1,
			ConversationID: 1,
			JobID:          1,
			Action: contracts.TurnAction{
				Name: contracts.ActionCreateTicket,
				Args: map[string]any{"title": "approval-ticket"},
			},
			Status: contracts.ChannelPendingActionPending,
		},
	}
	if r != nil && len(r.pending) > 0 {
		pending = append([]channelsvc.PendingActionView(nil), r.pending...)
	}
	return channelsvc.ProcessResult{
		ReplyText:      reply,
		JobStatus:      contracts.ChannelTurnSucceeded,
		PendingActions: pending,
	}, nil
}

func (r *testDaemonFeishuApprovalRuntime) GatewayTurnTimeout() time.Duration {
	return 5 * time.Second
}

func (r *testDaemonFeishuApprovalRuntime) DecidePendingAction(ctx context.Context, req channelsvc.PendingActionDecisionRequest) (channelsvc.PendingActionDecisionResult, error) {
	_ = ctx
	if r == nil {
		return channelsvc.PendingActionDecisionResult{}, fmt.Errorf("runtime nil")
	}
	r.mu.Lock()
	r.decisionReqs = append(r.decisionReqs, req)
	r.mu.Unlock()
	if r.decisionError != nil {
		return channelsvc.PendingActionDecisionResult{}, r.decisionError
	}
	if r.decisionReply.Action.ID != 0 {
		return r.decisionReply, nil
	}
	finalStatus := contracts.ChannelPendingActionExecuted
	msg := "已执行审批操作"
	if req.Decision == channelsvc.PendingActionReject {
		finalStatus = contracts.ChannelPendingActionRejected
		msg = "已拒绝审批操作"
	}
	return channelsvc.PendingActionDecisionResult{
		Action: channelsvc.PendingActionView{
			ID:     req.PendingActionID,
			Status: finalStatus,
			Action: contracts.TurnAction{
				Name: contracts.ActionCreateTicket,
				Args: map[string]any{"title": "approval-ticket"},
			},
		},
		Decision: req.Decision,
		Message:  msg,
	}, nil
}

func (r *testDaemonFeishuApprovalRuntime) decisionRequests() []channelsvc.PendingActionDecisionRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]channelsvc.PendingActionDecisionRequest, 0, len(r.decisionReqs))
	out = append(out, r.decisionReqs...)
	return out
}

type testDaemonFeishuInterruptRuntime struct {
	interruptOK  bool
	interruptErr error

	mu                 sync.Mutex
	interruptCalls     int
	lastChannelType    contracts.ChannelType
	lastAdapter        string
	lastConversationID string
}

func (r *testDaemonFeishuInterruptRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error) {
	_ = ctx
	return channelsvc.ProcessResult{
		RunID:     "run-feishu-interrupt",
		ReplyText: "echo: " + strings.TrimSpace(env.Text),
		JobStatus: contracts.ChannelTurnSucceeded,
	}, nil
}

func (r *testDaemonFeishuInterruptRuntime) GatewayTurnTimeout() time.Duration {
	return 3 * time.Second
}

func (r *testDaemonFeishuInterruptRuntime) InterruptConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (channelsvc.InterruptResult, error) {
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

type testDaemonFeishuResolver struct {
	projectName string
	runtime     channelsvc.ProjectRuntime
}

func (r *testDaemonFeishuResolver) Resolve(name string) (*channelsvc.ProjectContext, error) {
	if strings.TrimSpace(name) != strings.TrimSpace(r.projectName) {
		return nil, fmt.Errorf("project not found: %s", name)
	}
	return &channelsvc.ProjectContext{
		Name:     r.projectName,
		RepoRoot: "/tmp/demo",
		Runtime:  r.runtime,
	}, nil
}

func (r *testDaemonFeishuResolver) ListProjects() ([]string, error) {
	return []string{r.projectName}, nil
}

type testDaemonFeishuSender struct {
	mu       sync.Mutex
	messages []testDaemonFeishuSenderMessage
	msgIDSeq int

	userNames        map[string]string
	getUserNameErr   error
	getUserNameCalls int

	failInteractiveTimes int
	failPatchTimes       int
	failTextTimes        int
}

type testDaemonFeishuSenderMessage struct {
	ChatID string
	Kind   string
	Title  string
	Text   string
	MsgID  string
}

func (s *testDaemonFeishuSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failTextTimes > 0 {
		s.failTextTimes--
		return errors.New("mock text send failed")
	}
	s.messages = append(s.messages, testDaemonFeishuSenderMessage{
		ChatID: strings.TrimSpace(chatID),
		Kind:   "text",
		Text:   strings.TrimSpace(text),
	})
	return nil
}

func (s *testDaemonFeishuSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, testDaemonFeishuSenderMessage{
		ChatID: strings.TrimSpace(chatID),
		Kind:   "card",
		Title:  strings.TrimSpace(title),
		Text:   strings.TrimSpace(markdown),
	})
	return nil
}

func (s *testDaemonFeishuSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failInteractiveTimes > 0 {
		s.failInteractiveTimes--
		return "", errors.New("mock send failed")
	}
	s.msgIDSeq++
	mid := fmt.Sprintf("om_%d", s.msgIDSeq)
	s.messages = append(s.messages, testDaemonFeishuSenderMessage{
		ChatID: strings.TrimSpace(chatID),
		Kind:   "card_interactive",
		Text:   strings.TrimSpace(cardJSON),
		MsgID:  mid,
	})
	return mid, nil
}

func (s *testDaemonFeishuSender) PatchCard(ctx context.Context, messageID, cardJSON string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failPatchTimes > 0 {
		s.failPatchTimes--
		return errors.New("mock patch failed")
	}
	s.messages = append(s.messages, testDaemonFeishuSenderMessage{
		Kind:  "patch",
		Text:  strings.TrimSpace(cardJSON),
		MsgID: strings.TrimSpace(messageID),
	})
	return nil
}

func (s *testDaemonFeishuSender) GetUserName(ctx context.Context, userID string) (string, error) {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getUserNameCalls++
	if s.getUserNameErr != nil {
		return "", s.getUserNameErr
	}
	if s.userNames == nil {
		return "", nil
	}
	return strings.TrimSpace(s.userNames[strings.TrimSpace(userID)]), nil
}

func (s *testDaemonFeishuSender) waitMessages(t *testing.T, want int, timeout time.Duration) []testDaemonFeishuSenderMessage {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := append([]testDaemonFeishuSenderMessage(nil), s.messages...)
		s.mu.Unlock()
		if len(got) >= want {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	got := append([]testDaemonFeishuSenderMessage(nil), s.messages...)
	s.mu.Unlock()
	t.Fatalf("wait sender messages timeout: want>=%d got=%d", want, len(got))
	return nil
}

func (s *testDaemonFeishuSender) snapshot() []testDaemonFeishuSenderMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]testDaemonFeishuSenderMessage(nil), s.messages...)
}

func (s *testDaemonFeishuSender) getUserNameCallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getUserNameCalls
}

func newTestDaemonFeishuGatewayWithRuntime(t *testing.T, runtime channelsvc.ProjectRuntime) (*gorm.DB, *channelsvc.Gateway, *testDaemonFeishuResolver) {
	t.Helper()

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

	resolver := &testDaemonFeishuResolver{
		projectName: "demo",
		runtime:     runtime,
	}
	gateway, err := channelsvc.NewGateway(db, resolver, channelsvc.GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("NewGateway failed: %v", err)
	}
	return db, gateway, resolver
}

func newTestDaemonFeishuGateway(t *testing.T) (*channelsvc.Gateway, *testDaemonFeishuResolver, *testDaemonFeishuRuntime) {
	t.Helper()
	runtime := &testDaemonFeishuRuntime{
		received: make(chan contracts.InboundEnvelope, 8),
		reply:    "daemon-reply",
	}
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	return gateway, resolver, runtime
}

func TestDaemonFeishuWebhookHandler_URLVerification(t *testing.T) {
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, &testDaemonFeishuRuntime{})
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, &daemonFeishuNoopSender{}, daemonFeishuWebhookOptions{
		VerifyToken: "token-ok",
	}, nil)

	body := `{"type":"url_verification","token":"token-ok","challenge":"abc123"}`
	req := httptest.NewRequest(http.MethodPost, "/feishu/webhook", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d want=%d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response failed: %v", err)
	}
	if got := strings.TrimSpace(fmt.Sprintf("%v", payload["challenge"])); got != "abc123" {
		t.Fatalf("unexpected challenge: got=%q", got)
	}
}

func TestDaemonFeishuWebhookHandler_URLVerificationInvalidTokenRejected(t *testing.T) {
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, &testDaemonFeishuRuntime{})
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, &daemonFeishuNoopSender{}, daemonFeishuWebhookOptions{
		VerifyToken: "token-ok",
	}, nil)

	body := `{"type":"url_verification","token":"token-bad","challenge":"abc123"}`
	req := httptest.NewRequest(http.MethodPost, "/feishu/webhook", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: got=%d want=%d body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestDaemonFeishuWebhookHandler_VerifyTokenRejected(t *testing.T) {
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, &testDaemonFeishuRuntime{})
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, &daemonFeishuNoopSender{}, daemonFeishuWebhookOptions{
		VerifyToken: "token-ok",
	}, nil)

	body := `{"header":{"event_type":"im.message.receive_v1","token":"token-bad"},"event":{"sender":{"sender_type":"user","sender_id":{"open_id":"ou_test"}},"message":{"message_id":"om_x","chat_id":"oc_x","message_type":"text","content":"{\"text\":\"hello\"}"}}}`
	req := httptest.NewRequest(http.MethodPost, "/feishu/webhook", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: got=%d want=%d body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestDaemonFeishuWebhookHandler_VerifyTokenRequiredForBusinessEvents(t *testing.T) {
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, &testDaemonFeishuRuntime{})
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, &daemonFeishuNoopSender{}, daemonFeishuWebhookOptions{}, nil)

	body := `{"header":{"event_type":"im.message.receive_v1","token":"any-token"},"event":{"sender":{"sender_type":"user","sender_id":{"open_id":"ou_test"}},"message":{"message_id":"om_x","chat_id":"oc_x","message_type":"text","content":"{\"text\":\"hello\"}"}}}`
	req := httptest.NewRequest(http.MethodPost, "/feishu/webhook", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: got=%d want=%d body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
}

func TestDaemonFeishuWebhookHandler_SubmitAndReplyUsesInteractiveCard(t *testing.T) {
	runtime := &testDaemonFeishuRuntime{
		received: make(chan contracts.InboundEnvelope, 8),
		reply:    "daemon-reply",
	}
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-1", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	postDaemonFeishuTextEvent(t, handler, "token-ok", "chat-1", "om_1", "open-user-1", "你好 daemon")

	select {
	case env := <-runtime.received:
		if strings.TrimSpace(env.Text) != "你好 daemon" {
			t.Fatalf("unexpected inbound text: got=%q", env.Text)
		}
		if strings.TrimSpace(env.PeerConversationID) != "chat-1" {
			t.Fatalf("unexpected peer conversation: got=%q", env.PeerConversationID)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for runtime inbound")
	}

	msgs := sender.waitMessages(t, 1, 3*time.Second)
	foundFinalCard := false
	for _, msg := range msgs {
		if strings.TrimSpace(msg.Kind) == "card_interactive" && strings.Contains(msg.Text, "daemon-reply") {
			foundFinalCard = true
			break
		}
	}
	if !foundFinalCard {
		t.Fatalf("final interactive card not delivered, messages=%+v", msgs)
	}
	if sender.getUserNameCallCount() == 0 {
		t.Fatalf("expected GetUserName to be called")
	}
}

func TestDaemonFeishuWebhookHandler_PendingActionsSendApprovalCard(t *testing.T) {
	runtime := &testDaemonFeishuApprovalRuntime{
		reply: "需要审批",
		pending: []channelsvc.PendingActionView{
			{
				ID:     42,
				Status: contracts.ChannelPendingActionPending,
				Action: contracts.TurnAction{
					Name: contracts.ActionCreateTicket,
					Args: map[string]any{"title": "approval ticket"},
				},
			},
		},
	}
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-approval", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	postDaemonFeishuTextEvent(t, handler, "token-ok", "chat-approval", "msg-approval", "open-user-1", "请执行")

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		for _, msg := range msgs {
			if strings.TrimSpace(msg.Kind) != "card_interactive" {
				continue
			}
			if strings.Contains(msg.Text, "\"pending_action_id\":\"42\"") &&
				strings.Contains(msg.Text, "\"decision\":\"approve\"") &&
				strings.Contains(msg.Text, "\"decision\":\"reject\"") &&
				strings.Contains(msg.Text, "\"tag\":\"button\"") &&
				strings.Contains(msg.Text, "\"type\":\"callback\"") &&
				!strings.Contains(msg.Text, "\"tag\":\"action_group\"") &&
				!strings.Contains(msg.Text, "\"tag\":\"action\"") {
				return
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	msgs := sender.snapshot()
	t.Fatalf("approval card not delivered, messages=%+v", msgs)
}

func TestDaemonFeishuWebhookHandler_CardActionTriggerDecideAndPatch(t *testing.T) {
	runtime := &testDaemonFeishuApprovalRuntime{
		decisionReply: channelsvc.PendingActionDecisionResult{
			Action: channelsvc.PendingActionView{
				ID:     42,
				Status: contracts.ChannelPendingActionExecuted,
				Action: contracts.TurnAction{
					Name: contracts.ActionCreateTicket,
				},
			},
			Decision: channelsvc.PendingActionApprove,
			Message:  "已执行审批操作",
		},
	}
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-callback", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	rec := postDaemonFeishuCardActionEvent(t, handler, "token-ok", "chat-callback", "om-card-1", "evt-card-1", "open-user-2", 42, "approve")

	// Response body should contain the updated card JSON (Feishu replaces the original card).
	respBody := rec.Body.String()
	if !strings.Contains(respBody, "审批结果") {
		t.Fatalf("response body should contain card with 审批结果, got=%s", respBody)
	}
	if !strings.Contains(respBody, `"type":"raw"`) {
		t.Fatalf("response body should contain card type raw, got=%s", respBody)
	}
	if !strings.Contains(respBody, `"toast"`) {
		t.Fatalf("response body should contain toast, got=%s", respBody)
	}
	if !strings.Contains(respBody, `"type":"success"`) {
		t.Fatalf("response body toast type should be success for approve, got=%s", respBody)
	}

	reqs := runtime.decisionRequests()
	if len(reqs) != 1 {
		t.Fatalf("decision request count mismatch: %d", len(reqs))
	}
	if reqs[0].PendingActionID != 42 {
		t.Fatalf("pending action id mismatch: %d", reqs[0].PendingActionID)
	}
	if reqs[0].Decision != channelsvc.PendingActionApprove {
		t.Fatalf("decision mismatch: %s", reqs[0].Decision)
	}
	if strings.TrimSpace(reqs[0].PeerConversationID) != "chat-callback" {
		t.Fatalf("peer conversation mismatch: %q", reqs[0].PeerConversationID)
	}
}

func TestDaemonFeishuWebhookHandler_CardActionTriggerV1EventTypeFallback(t *testing.T) {
	runtime := &testDaemonFeishuApprovalRuntime{
		decisionReply: channelsvc.PendingActionDecisionResult{
			Action: channelsvc.PendingActionView{
				ID:     42,
				Status: contracts.ChannelPendingActionExecuted,
				Action: contracts.TurnAction{
					Name: contracts.ActionCreateTicket,
				},
			},
			Decision: channelsvc.PendingActionApprove,
			Message:  "已执行审批操作",
		},
	}
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-callback-v1", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	rec := postDaemonFeishuCardActionEventLegacyV1(t, handler, "token-ok", "chat-callback-v1", "om-card-v1", "evt-card-v1", "open-user-2", 42, "approve")

	// Response body should contain the updated card JSON.
	respBody := rec.Body.String()
	if !strings.Contains(respBody, "审批结果") {
		t.Fatalf("response body should contain card with 审批结果, got=%s", respBody)
	}
	if !strings.Contains(respBody, `"type":"raw"`) {
		t.Fatalf("response body should contain card type raw, got=%s", respBody)
	}

	reqs := runtime.decisionRequests()
	if len(reqs) != 1 {
		t.Fatalf("decision request count mismatch: %d", len(reqs))
	}
	if reqs[0].PendingActionID != 42 {
		t.Fatalf("pending action id mismatch: %d", reqs[0].PendingActionID)
	}
	if reqs[0].Decision != channelsvc.PendingActionApprove {
		t.Fatalf("decision mismatch: %s", reqs[0].Decision)
	}
	if strings.TrimSpace(reqs[0].PeerConversationID) != "chat-callback-v1" {
		t.Fatalf("peer conversation mismatch: %q", reqs[0].PeerConversationID)
	}
}

func TestDaemonFeishuWebhookHandler_ForwardIncludesSenderName(t *testing.T) {
	runtime := &testDaemonFeishuRuntime{
		received: make(chan contracts.InboundEnvelope, 8),
		reply:    "ok",
	}
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-sender", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{
		userNames: map[string]string{
			"open-user-1": "Alice",
		},
	}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	postDaemonFeishuTextEvent(t, handler, "token-ok", "chat-sender", "om_sender", "open-user-1", "hello")

	select {
	case env := <-runtime.received:
		if strings.TrimSpace(env.SenderID) != "open-user-1" {
			t.Fatalf("sender id mismatch: %q", env.SenderID)
		}
		if strings.TrimSpace(env.SenderName) != "Alice" {
			t.Fatalf("sender name mismatch: %q", env.SenderName)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout waiting for runtime inbound")
	}
	if sender.getUserNameCallCount() == 0 {
		t.Fatalf("expected GetUserName to be called")
	}
}

func TestDaemonFeishuWebhookHandler_InterruptCommandsTriggerInterrupt(t *testing.T) {
	commands := []string{"/interrupt", "/stop"}
	for _, cmd := range commands {
		cmd := cmd
		t.Run(strings.TrimPrefix(cmd, "/"), func(t *testing.T) {
			runtime := &testDaemonFeishuInterruptRuntime{interruptOK: true}
			_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
			chatID := "chat-" + strings.TrimPrefix(cmd, "/")
			msgID := "msg-" + strings.TrimPrefix(cmd, "/")
			if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, chatID, "demo"); err != nil {
				t.Fatalf("BindProject failed: %v", err)
			}

			sender := &testDaemonFeishuSender{}
			handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
				Adapter:     defaultDaemonFeishuAdapter,
				VerifyToken: "token-ok",
			}, nil)

			postDaemonFeishuTextEvent(t, handler, "token-ok", chatID, msgID, "open-user-1", cmd)

			msgs := sender.waitMessages(t, 1, 2*time.Second)
			if !strings.Contains(msgs[0].Text, "已发送中断信号") {
				t.Fatalf("interrupt reply unexpected: %q", msgs[0].Text)
			}

			runtime.mu.Lock()
			defer runtime.mu.Unlock()
			if runtime.interruptCalls != 1 {
				t.Fatalf("interrupt calls mismatch: %d", runtime.interruptCalls)
			}
			if runtime.lastChannelType != contracts.ChannelTypeIM {
				t.Fatalf("channel type mismatch: %q", runtime.lastChannelType)
			}
			if runtime.lastAdapter != defaultDaemonFeishuAdapter {
				t.Fatalf("adapter mismatch: %q", runtime.lastAdapter)
			}
			if runtime.lastConversationID != chatID {
				t.Fatalf("conversation id mismatch: %q", runtime.lastConversationID)
			}
		})
	}
}

func TestDaemonFeishuWebhookHandler_EventBusRelayFiltersByPeerMessageID(t *testing.T) {
	runtime := &testDaemonFeishuScriptedEventRuntime{delay: 200 * time.Millisecond}
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-filter", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	postDaemonFeishuTextEvent(t, handler, "token-ok", "chat-filter", "msg-a", "open-user-1", "A")
	postDaemonFeishuTextEvent(t, handler, "token-ok", "chat-filter", "msg-b", "open-user-1", "B")

	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		doneA := 0
		doneB := 0
		for _, m := range msgs {
			switch strings.TrimSpace(m.Kind) {
			case "card", "card_interactive", "patch":
				if strings.Contains(m.Text, "done msg-a") {
					doneA++
				}
				if strings.Contains(m.Text, "done msg-b") {
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

func TestDaemonFeishuWebhookHandler_DedupByEventID(t *testing.T) {
	runtime := &testDaemonFeishuCountingRuntime{}
	db, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-dedup", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	postDaemonFeishuTextEventWithEventID(t, handler, "token-ok", "chat-dedup", "msg-dedup", "evt-dedup", "open-user-1", "hello")

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

	postDaemonFeishuTextEventWithEventID(t, handler, "token-ok", "chat-dedup", "msg-dedup", "evt-dedup", "open-user-1", "hello")
	time.Sleep(250 * time.Millisecond)

	if runtime.callCount() != 1 {
		t.Fatalf("duplicate event should be skipped, got runtime calls=%d", runtime.callCount())
	}

	var inboundCount int64
	if err := db.Model(&store.ChannelMessage{}).Where("direction = ?", contracts.ChannelMessageIn).Count(&inboundCount).Error; err != nil {
		t.Fatalf("count inbound message failed: %v", err)
	}
	if inboundCount != 1 {
		t.Fatalf("duplicate event should not create new inbound record, got=%d", inboundCount)
	}

	var jobCount int64
	if err := db.Model(&store.ChannelTurnJob{}).Count(&jobCount).Error; err != nil {
		t.Fatalf("count turn jobs failed: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("duplicate event should not create new turn job, got=%d", jobCount)
	}
}

func TestDaemonFeishuWebhookHandler_RelayTimeoutSendsTimeoutReply(t *testing.T) {
	setDaemonFeishuRelayTimeoutsForTest(t, 220*time.Millisecond, 90*time.Millisecond)

	runtime := &testDaemonFeishuDelayedNoEventRuntime{delay: 700 * time.Millisecond}
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-timeout", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	postDaemonFeishuTextEvent(t, handler, "token-ok", "chat-timeout", "msg-timeout", "open-user-1", "hello")

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		for _, msg := range msgs {
			if strings.Contains(msg.Text, "处理超时") {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}

	msgs := sender.snapshot()
	t.Fatalf("timeout reply not delivered, messages=%+v", msgs)
}

func TestDaemonFeishuWebhookHandler_RelayIdleTimerResetByEvents(t *testing.T) {
	setDaemonFeishuRelayTimeoutsForTest(t, 900*time.Millisecond, 120*time.Millisecond)

	runtime := &testDaemonFeishuDelayedNoEventRuntime{delay: 500 * time.Millisecond}
	_, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, runtime)
	chatID := "chat-keepalive"
	msgID := "msg-keepalive"
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, chatID, "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	postDaemonFeishuTextEvent(t, handler, "token-ok", chatID, msgID, "open-user-1", "hello")

	go func() {
		for i := 0; i < 8; i++ {
			time.Sleep(60 * time.Millisecond)
			gateway.EventBus().Publish(channelsvc.GatewayEvent{
				ProjectName:    "demo",
				ConversationID: chatID,
				PeerMessageID:  msgID,
				Type:           "assistant_event",
				Stream:         "assistant",
				Text:           fmt.Sprintf("keepalive %d", i),
				At:             time.Now(),
			})
		}
	}()

	wantDone := "done slow " + msgID
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		hasDone := false
		for _, msg := range msgs {
			if strings.Contains(msg.Text, "处理超时") {
				t.Fatalf("should not timeout when keepalive events arrive, messages=%+v", msgs)
			}
			if strings.Contains(msg.Text, wantDone) {
				hasDone = true
			}
		}
		if hasDone {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	msgs := sender.snapshot()
	t.Fatalf("final reply not delivered, messages=%+v", msgs)
}

func TestDaemonFeishuWebhookHandler_FinalReplyCardFailedFallbackToTextAndOutboxSent(t *testing.T) {
	db, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, &testDaemonFeishuNoEventRuntime{})
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-fallback", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{
		failInteractiveTimes: 8,
	}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	postDaemonFeishuTextEvent(t, handler, "token-ok", "chat-fallback", "msg-fallback", "open-user-1", "hello")

	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		msgs := sender.snapshot()
		for _, msg := range msgs {
			if strings.TrimSpace(msg.Kind) == "text" && strings.Contains(msg.Text, "done: hello") {
				outbox := waitDaemonFeishuOutboxStatus(t, db, contracts.ChannelOutboxSent, 4*time.Second)
				if strings.TrimSpace(outbox.LastError) != "" {
					t.Fatalf("outbox last_error should be empty when text fallback succeeded, got=%q", outbox.LastError)
				}
				return
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	msgs := sender.snapshot()
	t.Fatalf("final text fallback not delivered, messages=%+v", msgs)
}

func TestDaemonFeishuWebhookHandler_FinalReplyFailedMarksOutboxFailed(t *testing.T) {
	db, gateway, resolver := newTestDaemonFeishuGatewayWithRuntime(t, &testDaemonFeishuNoEventRuntime{})
	if _, err := gateway.BindProject(context.Background(), contracts.ChannelTypeIM, defaultDaemonFeishuAdapter, "chat-failed", "demo"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	sender := &testDaemonFeishuSender{
		failInteractiveTimes: 8,
		failTextTimes:        8,
	}
	handler := newDaemonFeishuWebhookHandler(gateway, resolver, sender, daemonFeishuWebhookOptions{
		Adapter:     defaultDaemonFeishuAdapter,
		VerifyToken: "token-ok",
	}, nil)

	postDaemonFeishuTextEvent(t, handler, "token-ok", "chat-failed", "msg-failed", "open-user-1", "hello")

	outbox := waitDaemonFeishuOutboxStatus(t, db, contracts.ChannelOutboxFailed, 8*time.Second)
	if !strings.Contains(strings.TrimSpace(outbox.LastError), "mock send failed") {
		t.Fatalf("outbox last_error should include send error, got=%q", outbox.LastError)
	}
}

func waitDaemonFeishuOutboxStatus(t *testing.T, db *gorm.DB, want contracts.ChannelOutboxStatus, timeout time.Duration) store.ChannelOutbox {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var outbox store.ChannelOutbox
		if err := db.Order("id DESC").First(&outbox).Error; err == nil {
			if outbox.Status == want {
				return outbox
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	var outbox store.ChannelOutbox
	if err := db.Order("id DESC").First(&outbox).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	t.Fatalf("outbox status should become %s, got=%s last_error=%q", want, outbox.Status, outbox.LastError)
	return store.ChannelOutbox{}
}

func setDaemonFeishuRelayTimeoutsForTest(t *testing.T, relayTimeout, idleTimeout time.Duration) {
	t.Helper()
	originRelay := daemonFeishuRelayTimeout
	originIdle := daemonFeishuRelayIdleTimeout
	daemonFeishuRelayTimeout = relayTimeout
	daemonFeishuRelayIdleTimeout = idleTimeout
	t.Cleanup(func() {
		daemonFeishuRelayTimeout = originRelay
		daemonFeishuRelayIdleTimeout = originIdle
	})
}

func postDaemonFeishuTextEvent(t *testing.T, handler http.HandlerFunc, token, chatID, msgID, senderID, text string) {
	t.Helper()
	postDaemonFeishuTextEventWithEventID(t, handler, token, chatID, msgID, msgID, senderID, text)
}

func postDaemonFeishuTextEventWithEventID(t *testing.T, handler http.HandlerFunc, token, chatID, msgID, eventID, senderID, text string) {
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
				"sender_type": "user",
				"sender_id": map[string]any{
					"open_id": strings.TrimSpace(senderID),
				},
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
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d body=%s", rec.Code, rec.Body.String())
	}
}

func postDaemonFeishuCardActionEvent(t *testing.T, handler http.HandlerFunc, token, chatID, messageID, eventID, operatorOpenID string, pendingActionID uint, decision string) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{
		"header": map[string]any{
			"event_type": "card.action.trigger",
			"token":      strings.TrimSpace(token),
			"event_id":   strings.TrimSpace(eventID),
		},
		"event": map[string]any{
			"operator": map[string]any{
				"operator_id": map[string]any{
					"open_id": strings.TrimSpace(operatorOpenID),
				},
			},
			"context": map[string]any{
				"open_chat_id":    strings.TrimSpace(chatID),
				"open_message_id": strings.TrimSpace(messageID),
			},
			"action": map[string]any{
				"value": map[string]any{
					"pending_action_id": fmt.Sprintf("%d", pendingActionID),
					"decision":          strings.TrimSpace(decision),
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/feishu/webhook", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d body=%s", rec.Code, rec.Body.String())
	}
	return rec
}

func postDaemonFeishuCardActionEventLegacyV1(t *testing.T, handler http.HandlerFunc, token, chatID, messageID, eventID, operatorOpenID string, pendingActionID uint, decision string) *httptest.ResponseRecorder {
	t.Helper()
	payload := map[string]any{
		"header": map[string]any{
			"token":    strings.TrimSpace(token),
			"event_id": strings.TrimSpace(eventID),
		},
		"event": map[string]any{
			"type":            "card.action.trigger_v1",
			"token":           strings.TrimSpace(token),
			"event_id":        strings.TrimSpace(eventID),
			"open_chat_id":    strings.TrimSpace(chatID),
			"open_message_id": strings.TrimSpace(messageID),
			"operator": map[string]any{
				"operator_id": map[string]any{
					"open_id": strings.TrimSpace(operatorOpenID),
				},
			},
			"action": map[string]any{
				"value": map[string]any{
					"pending_action_id": fmt.Sprintf("%d", pendingActionID),
					"decision":          strings.TrimSpace(decision),
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/feishu/webhook", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: got=%d body=%s", rec.Code, rec.Body.String())
	}
	return rec
}
