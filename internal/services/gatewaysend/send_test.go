package gatewaysend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

type capturedCall struct {
	chatID string
	title  string
	text   string
}

type captureSender struct {
	mu    sync.Mutex
	calls []capturedCall
}

func (s *captureSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	s.mu.Lock()
	s.calls = append(s.calls, capturedCall{
		chatID: strings.TrimSpace(chatID),
		title:  strings.TrimSpace(title),
		text:   strings.TrimSpace(markdown),
	})
	s.mu.Unlock()
	return nil
}

func (s *captureSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	s.mu.Lock()
	s.calls = append(s.calls, capturedCall{
		chatID: strings.TrimSpace(chatID),
		title:  "",
		text:   strings.TrimSpace(text),
	})
	s.mu.Unlock()
	return nil
}

func (s *captureSender) snapshot() []capturedCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capturedCall(nil), s.calls...)
}

type interactiveCaptureSender struct {
	captureSender
	mu               sync.Mutex
	interactiveCalls int
	lastCardJSON     string
	overrideMid      *string
	overrideErr      error
}

func (s *interactiveCaptureSender) SendCardInteractive(ctx context.Context, chatID, cardJSON string) (string, error) {
	_ = ctx
	_ = chatID
	s.mu.Lock()
	s.interactiveCalls++
	s.lastCardJSON = strings.TrimSpace(cardJSON)
	s.mu.Unlock()
	if s.overrideMid != nil {
		return *s.overrideMid, s.overrideErr
	}
	return "om_interactive_1", nil
}

func (s *interactiveCaptureSender) interactiveSnapshot() (int, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.interactiveCalls, s.lastCardJSON
}

type failingSender struct {
	err     error
	textErr error
}

func (s *failingSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	_ = chatID
	_ = title
	_ = markdown
	if s == nil || s.err == nil {
		return fmt.Errorf("send failed")
	}
	return s.err
}

func (s *failingSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	_ = chatID
	_ = text
	if s == nil {
		return fmt.Errorf("send failed")
	}
	if s.textErr != nil {
		return s.textErr
	}
	if s.err == nil {
		return fmt.Errorf("send failed")
	}
	return s.err
}

type staticProjectResolver struct {
	projects map[string]*contracts.ProjectMeta
}

func (r *staticProjectResolver) ResolveProjectMeta(name string) (*contracts.ProjectMeta, error) {
	if r == nil {
		return nil, fmt.Errorf("project not found: %s", name)
	}
	project := r.projects[strings.TrimSpace(name)]
	if project == nil {
		return nil, fmt.Errorf("project not found: %s", name)
	}
	return project, nil
}

func TestHandler_RequiresAuth(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}

	handler := NewHandler(
		NewServiceWithDB(db, nil, &captureSender{}, nil),
		HandlerConfig{AuthToken: "send-token"},
	)
	req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(`{"project":"demo","text":"hello"}`))
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("missing token should be unauthorized: got=%d body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

func TestHandler_Success(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}

	binding := contracts.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        AdapterFeishu,
		PeerProjectKey: "chat-demo-1",
		RolePolicyJSON: contracts.JSONMap{},
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	sender := &captureSender{}
	handler := NewHandler(
		NewServiceWithDB(db, nil, sender, nil),
		HandlerConfig{AuthToken: "send-token"},
	)

	body := map[string]string{"project": "demo", "text": "deploy done"}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, Path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer send-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.Schema != ResponseSchemaV1 {
		t.Fatalf("unexpected schema: %q", resp.Schema)
	}
	if resp.Delivered != 1 || resp.Failed != 0 {
		t.Fatalf("unexpected delivery stats: delivered=%d failed=%d", resp.Delivered, resp.Failed)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("unexpected results len: %d", len(resp.Results))
	}
	if resp.Results[0].Status != string(contracts.ChannelOutboxSent) {
		t.Fatalf("unexpected result status: %q", resp.Results[0].Status)
	}

	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("sender call count mismatch: %d", len(calls))
	}
	if calls[0].chatID != "chat-demo-1" || calls[0].text != "deploy done" {
		t.Fatalf("sender call mismatch: %+v", calls[0])
	}
	if !strings.Contains(calls[0].title, "demo") {
		t.Fatalf("card title should include project: %+v", calls[0])
	}

	var msg contracts.ChannelMessage
	if err := db.First(&msg, resp.Results[0].MessageID).Error; err != nil {
		t.Fatalf("query outbound message failed: %v", err)
	}
	if msg.Status != contracts.ChannelMessageSent {
		t.Fatalf("message status should be sent, got=%s", msg.Status)
	}
	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, resp.Results[0].OutboxID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxSent {
		t.Fatalf("outbox status should be sent, got=%s", outbox.Status)
	}
}

func TestSendProjectText_DedupRecentContent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}

	binding := contracts.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        AdapterFeishu,
		PeerProjectKey: "chat-demo-dedup",
		RolePolicyJSON: contracts.JSONMap{},
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	sender := &captureSender{}
	first, err := SendProjectText(context.Background(), db, nil, sender, "demo", "deploy done")
	if err != nil {
		t.Fatalf("first SendProjectText failed: %v", err)
	}
	if first.Delivered != 1 || first.Failed != 0 || len(first.Results) != 1 {
		t.Fatalf("first response unexpected: %+v", first)
	}

	second, err := SendProjectText(context.Background(), db, nil, sender, "demo", "deploy done")
	if err != nil {
		t.Fatalf("second SendProjectText failed: %v", err)
	}
	if second.Delivered != 1 || second.Failed != 0 || len(second.Results) != 1 {
		t.Fatalf("second response unexpected: %+v", second)
	}
	if second.Results[0].MessageID != first.Results[0].MessageID {
		t.Fatalf("dedup should reuse message id, got=%d want=%d", second.Results[0].MessageID, first.Results[0].MessageID)
	}
	if second.Results[0].OutboxID != first.Results[0].OutboxID {
		t.Fatalf("dedup should reuse outbox id, got=%d want=%d", second.Results[0].OutboxID, first.Results[0].OutboxID)
	}
	if second.Results[0].Status != string(contracts.ChannelOutboxSent) {
		t.Fatalf("dedup result status should be sent, got=%s", second.Results[0].Status)
	}

	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("dedup should avoid second send call, got=%d", len(calls))
	}

	var outboundCount int64
	if err := db.Model(&contracts.ChannelMessage{}).Where("direction = ?", contracts.ChannelMessageOut).Count(&outboundCount).Error; err != nil {
		t.Fatalf("count outbound failed: %v", err)
	}
	if outboundCount != 1 {
		t.Fatalf("dedup should avoid second outbound record, got=%d", outboundCount)
	}

	var outboxCount int64
	if err := db.Model(&contracts.ChannelOutbox{}).Count(&outboxCount).Error; err != nil {
		t.Fatalf("count outbox failed: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("dedup should avoid second outbox record, got=%d", outboxCount)
	}
}

func TestHandler_UsesRepoBaseNameInCardTitle(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}

	projectName := "Users-xiewanpeng-agi-dalek"
	binding := contracts.ChannelBinding{
		ProjectName:    projectName,
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        AdapterFeishu,
		PeerProjectKey: "chat-demo-3",
		RolePolicyJSON: contracts.JSONMap{},
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	resolver := &staticProjectResolver{
		projects: map[string]*contracts.ProjectMeta{
			projectName: {
				Name:     projectName,
				RepoRoot: "/Users/xiewanpeng/agi/dalek",
			},
		},
	}
	sender := &captureSender{}
	handler := NewHandler(
		NewServiceWithDB(db, resolver, sender, nil),
		HandlerConfig{AuthToken: "send-token"},
	)

	body := map[string]string{"project": projectName, "text": "deploy done"}
	raw, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, Path, bytes.NewReader(raw))
	req.Header.Set("Authorization", "Bearer send-token")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("sender call count mismatch: %d", len(calls))
	}
	if calls[0].title != "dalek" {
		t.Fatalf("card title should use repo basename, got=%q", calls[0].title)
	}
}

func TestHandler_ProjectNotBound(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}

	handler := NewHandler(
		NewServiceWithDB(db, nil, &captureSender{}, nil),
		HandlerConfig{AuthToken: "send-token"},
	)
	req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(`{"project":"demo","text":"hello"}`))
	req.Header.Set("Authorization", "Bearer send-token")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("unbound project should return 404, got=%d body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
}

func TestHandler_SenderFailed_MarksOutboxFailed(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}

	binding := contracts.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        AdapterFeishu,
		PeerProjectKey: "chat-demo-2",
		RolePolicyJSON: contracts.JSONMap{},
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	handler := NewHandler(
		NewServiceWithDB(db, nil, &failingSender{err: fmt.Errorf("mock send failed")}, nil),
		HandlerConfig{AuthToken: "send-token"},
	)
	req := httptest.NewRequest(http.MethodPost, Path, strings.NewReader(`{"project":"demo","text":"hello"}`))
	req.Header.Set("Authorization", "Bearer send-token")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("partial failure should still return 200 with details, got=%d body=%s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}

	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if resp.Delivered != 0 || resp.Failed != 1 {
		t.Fatalf("unexpected delivery stats: delivered=%d failed=%d", resp.Delivered, resp.Failed)
	}
	if len(resp.Results) != 1 {
		t.Fatalf("unexpected results len: %d", len(resp.Results))
	}
	if resp.Results[0].Status != string(contracts.ChannelOutboxFailed) {
		t.Fatalf("unexpected result status: %q", resp.Results[0].Status)
	}
	if strings.TrimSpace(resp.Results[0].Error) == "" {
		t.Fatalf("failed result should carry error message")
	}

	var msg contracts.ChannelMessage
	if err := db.First(&msg, resp.Results[0].MessageID).Error; err != nil {
		t.Fatalf("query outbound message failed: %v", err)
	}
	if msg.Status != contracts.ChannelMessageFailed {
		t.Fatalf("message status should be failed, got=%s", msg.Status)
	}
	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, resp.Results[0].OutboxID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxFailed {
		t.Fatalf("outbox status should be failed, got=%s", outbox.Status)
	}
}

func TestService_SendChatReply_DisablesDedup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}

	binding := contracts.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        AdapterFeishu,
		PeerProjectKey: "chat-demo-reply",
		RolePolicyJSON: contracts.JSONMap{},
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	sender := &captureSender{}
	svc := NewServiceWithDB(db, nil, sender, nil)
	if err := svc.SendChatReply(context.Background(), "demo", "chat-demo-reply", "same reply", ""); err != nil {
		t.Fatalf("first SendChatReply failed: %v", err)
	}
	if err := svc.SendChatReply(context.Background(), "demo", "chat-demo-reply", "same reply", ""); err != nil {
		t.Fatalf("second SendChatReply failed: %v", err)
	}

	calls := sender.snapshot()
	if len(calls) != 2 {
		t.Fatalf("chat reply should not dedup by content, got calls=%d", len(calls))
	}
}

func TestService_SendChatReply_UsesInteractiveCardAndPersistsPayload(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}

	binding := contracts.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        AdapterFeishu,
		PeerProjectKey: "chat-demo-interactive",
		RolePolicyJSON: contracts.JSONMap{},
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	sender := &interactiveCaptureSender{}
	svc := NewServiceWithDB(db, nil, sender, nil)
	cardJSON := `{"type":"template","data":{"x":"y"}}`
	if err := svc.SendChatReply(context.Background(), "demo", "chat-demo-interactive", "fallback text", cardJSON); err != nil {
		t.Fatalf("SendChatReply failed: %v", err)
	}

	interactiveCalls, gotCardJSON := sender.interactiveSnapshot()
	if interactiveCalls != 1 {
		t.Fatalf("interactive sender calls mismatch: %d", interactiveCalls)
	}
	if gotCardJSON != cardJSON {
		t.Fatalf("interactive card json mismatch: got=%q want=%q", gotCardJSON, cardJSON)
	}
	if calls := sender.snapshot(); len(calls) != 0 {
		t.Fatalf("interactive success should not fallback to SendCard/SendText, got=%d", len(calls))
	}

	var message contracts.ChannelMessage
	if err := db.Where("sender_id = ?", "gateway.send").Order("id DESC").First(&message).Error; err != nil {
		t.Fatalf("query outbound message failed: %v", err)
	}
	payload := contracts.JSONMapFromAny(message.PayloadJSON)
	gotPayloadCardJSON, _ := payload[payloadKeyCardJSON].(string)
	if strings.TrimSpace(gotPayloadCardJSON) != cardJSON {
		t.Fatalf("persisted payload card_json mismatch: got=%q want=%q", gotPayloadCardJSON, cardJSON)
	}
}

func TestService_SendChatReply_EmptyMessageID_NoFallback(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenGatewayDB(dbPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}

	binding := contracts.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        AdapterFeishu,
		PeerProjectKey: "chat-empty-mid",
		RolePolicyJSON: contracts.JSONMap{},
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}

	emptyMid := ""
	sender := &interactiveCaptureSender{overrideMid: &emptyMid}
	svc := NewServiceWithDB(db, nil, sender, nil)
	cardJSON := `{"schema":"2.0","body":{"elements":[{"tag":"markdown","content":"hello"}]}}`
	if err := svc.SendChatReply(context.Background(), "demo", "chat-empty-mid", "fallback text", cardJSON); err != nil {
		t.Fatalf("SendChatReply failed: %v", err)
	}

	interactiveCalls, _ := sender.interactiveSnapshot()
	if interactiveCalls != 1 {
		t.Fatalf("interactive sender should be called once: got=%d", interactiveCalls)
	}
	if calls := sender.snapshot(); len(calls) != 0 {
		t.Fatalf("empty message_id with nil error should NOT fallback to SendCard/SendText, got=%d calls", len(calls))
	}
}
