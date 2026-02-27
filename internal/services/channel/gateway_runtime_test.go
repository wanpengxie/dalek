package channel

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type fakeProjectResolver struct {
	ctx *ProjectContext
}

func (r *fakeProjectResolver) Resolve(name string) (*ProjectContext, error) {
	if r == nil || r.ctx == nil {
		return nil, fmt.Errorf("resolver empty")
	}
	if strings.TrimSpace(name) != strings.TrimSpace(r.ctx.Name) {
		return nil, fmt.Errorf("project not found: %s", name)
	}
	return r.ctx, nil
}

func (r *fakeProjectResolver) ListProjects() ([]string, error) {
	if r == nil || r.ctx == nil {
		return nil, nil
	}
	return []string{strings.TrimSpace(r.ctx.Name)}, nil
}

type fakeProjectRuntime struct{}

func (f *fakeProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (ProcessResult, error) {
	_ = ctx
	reply := strings.TrimSpace(env.Text)
	if reply == "" {
		reply = "empty"
	}
	runID := "run-fake-1"
	return ProcessResult{
		RunID:         runID,
		JobStatus:     contracts.ChannelTurnSucceeded,
		ReplyText:     "reply: " + reply,
		AgentProvider: "fake",
		AgentModel:    "test",
		AgentEvents: []AgentEvent{
			{
				RunID:  runID,
				Seq:    1,
				Stream: StreamLifecycle,
				Ts:     time.Now().UnixMilli(),
				Data: AgentEventData{
					Phase: "start",
				},
			},
			{
				RunID:  runID,
				Seq:    2,
				Stream: StreamAssistant,
				Ts:     time.Now().UnixMilli(),
				Data:   AgentEventData{Text: "thinking"},
			},
			{
				RunID:  runID,
				Seq:    3,
				Stream: StreamLifecycle,
				Ts:     time.Now().UnixMilli(),
				Data:   AgentEventData{Phase: "end"},
			},
		},
	}, nil
}

func (f *fakeProjectRuntime) GatewayTurnTimeout() time.Duration {
	return 3 * time.Second
}

type streamingProjectRuntime struct{}

func (r *streamingProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (ProcessResult, error) {
	runID := "run-stream-1"
	emitStreamAgentEvent(ctx, AgentEvent{
		RunID:  runID,
		Seq:    1,
		Stream: StreamLifecycle,
		Ts:     time.Now().UnixMilli(),
		Data: AgentEventData{
			Phase: "start",
		},
	})
	emitStreamAgentEvent(ctx, AgentEvent{
		RunID:  runID,
		Seq:    2,
		Stream: StreamAssistant,
		Ts:     time.Now().UnixMilli(),
		Data: AgentEventData{
			Text: "streaming: " + strings.TrimSpace(env.Text),
		},
	})
	return ProcessResult{
		RunID:         runID,
		JobStatus:     contracts.ChannelTurnSucceeded,
		ReplyText:     "final: " + strings.TrimSpace(env.Text),
		AgentProvider: "claude",
		AgentModel:    "stream-test",
	}, nil
}

func (r *streamingProjectRuntime) GatewayTurnTimeout() time.Duration { return 3 * time.Second }

type interruptProjectRuntime struct {
	interruptOK  bool
	interruptErr error
	resetOK      bool
	resetErr     error

	mu                 sync.Mutex
	interruptCalls     int
	lastChannelType    contracts.ChannelType
	lastAdapter        string
	lastConversationID string
	resetCalls         int
	lastResetConvID    string
}

func (r *interruptProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (ProcessResult, error) {
	_ = ctx
	_ = env
	return ProcessResult{
		RunID:     "run-interrupt-runtime",
		JobStatus: contracts.ChannelTurnSucceeded,
		ReplyText: "ok",
	}, nil
}

func (r *interruptProjectRuntime) GatewayTurnTimeout() time.Duration { return 3 * time.Second }

func (r *interruptProjectRuntime) InterruptConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (InterruptResult, error) {
	_ = ctx
	r.mu.Lock()
	r.interruptCalls++
	r.lastChannelType = contracts.ChannelType(strings.TrimSpace(string(channelType)))
	r.lastAdapter = strings.TrimSpace(adapter)
	r.lastConversationID = strings.TrimSpace(peerConversationID)
	r.mu.Unlock()
	if r.interruptErr != nil {
		return InterruptResult{}, r.interruptErr
	}
	if r.interruptOK {
		return InterruptResult{
			Status:            InterruptStatusHit,
			RunnerInterrupted: true,
		}, nil
	}
	return InterruptResult{Status: InterruptStatusMiss}, nil
}

func (r *interruptProjectRuntime) ResetConversationSession(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (bool, error) {
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

type mapProjectResolver struct {
	ctx map[string]*ProjectContext
}

func (r *mapProjectResolver) Resolve(name string) (*ProjectContext, error) {
	if r == nil || r.ctx == nil {
		return nil, fmt.Errorf("resolver empty")
	}
	key := strings.TrimSpace(name)
	project, ok := r.ctx[key]
	if !ok || project == nil {
		return nil, fmt.Errorf("project not found: %s", key)
	}
	return project, nil
}

func (r *mapProjectResolver) ListProjects() ([]string, error) {
	if r == nil || r.ctx == nil {
		return nil, nil
	}
	out := make([]string, 0, len(r.ctx))
	for k := range r.ctx {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		out = append(out, k)
	}
	return out, nil
}

type gatedProjectRuntime struct {
	name string

	mu      sync.Mutex
	calls   int
	entered chan int
	release chan struct{}
}

func newGatedProjectRuntime(name string) *gatedProjectRuntime {
	return &gatedProjectRuntime{
		name:    strings.TrimSpace(name),
		entered: make(chan int, 8),
		release: make(chan struct{}, 8),
	}
}

func (r *gatedProjectRuntime) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (ProcessResult, error) {
	r.mu.Lock()
	r.calls++
	callNo := r.calls
	r.mu.Unlock()

	select {
	case r.entered <- callNo:
	case <-ctx.Done():
		return ProcessResult{}, ctx.Err()
	}
	select {
	case <-r.release:
	case <-ctx.Done():
		return ProcessResult{}, ctx.Err()
	}

	runID := fmt.Sprintf("%s-run-%d", r.name, callNo)
	return ProcessResult{
		RunID:         runID,
		JobStatus:     contracts.ChannelTurnSucceeded,
		ReplyText:     fmt.Sprintf("%s-reply-%d:%s", r.name, callNo, strings.TrimSpace(env.Text)),
		AgentProvider: "fake",
		AgentModel:    "test",
	}, nil
}

func (r *gatedProjectRuntime) GatewayTurnTimeout() time.Duration {
	return 5 * time.Second
}

func (r *gatedProjectRuntime) waitEntered(t *testing.T, timeout time.Duration) int {
	t.Helper()
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	select {
	case callNo := <-r.entered:
		return callNo
	case <-time.After(timeout):
		t.Fatalf("wait runtime entered timeout: %s", r.name)
		return 0
	}
}

func (r *gatedProjectRuntime) releaseOne() {
	r.release <- struct{}{}
}

func TestGateway_PersistInboundAccepted_DuplicatePendingJobReused(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  &fakeProjectRuntime{},
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 4})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}

	item, err := gw.normalizeInboundRequest(GatewayInboundRequest{
		ProjectName:    "alpha",
		PeerProjectKey: "chat-alpha",
		Envelope: contracts.InboundEnvelope{
			Schema:             contracts.ChannelInboundSchemaV1,
			ChannelType:        contracts.ChannelTypeIM,
			Adapter:            "im.feishu",
			PeerConversationID: "chat-alpha",
			PeerMessageID:      "msg-pending-dup",
			SenderID:           "u1",
			Text:               "hello",
			ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("normalize inbound failed: %v", err)
	}

	firstState, firstCached, err := gw.persistInboundAccepted(context.Background(), item)
	if err != nil {
		t.Fatalf("first persistInboundAccepted failed: %v", err)
	}
	if firstCached != nil {
		t.Fatalf("first persist should not hit cache")
	}
	if firstState.job.ID == 0 || firstState.inbound.ID == 0 {
		t.Fatalf("first persist should create inbound+job, inbound=%d job=%d", firstState.inbound.ID, firstState.job.ID)
	}
	if firstState.job.Status != contracts.ChannelTurnPending {
		t.Fatalf("first job should be pending, got=%s", firstState.job.Status)
	}

	secondState, secondCached, err := gw.persistInboundAccepted(context.Background(), item)
	if err != nil {
		t.Fatalf("second persistInboundAccepted failed: %v", err)
	}
	if secondCached == nil {
		t.Fatalf("duplicate pending job should return cached processing result")
	}
	if secondCached.JobID != firstState.job.ID {
		t.Fatalf("cached job id mismatch: got=%d want=%d", secondCached.JobID, firstState.job.ID)
	}
	if secondCached.JobStatus != contracts.ChannelTurnPending {
		t.Fatalf("cached status should remain pending, got=%s", secondCached.JobStatus)
	}
	if secondState.job.ID != firstState.job.ID {
		t.Fatalf("state job should be reused, got=%d want=%d", secondState.job.ID, firstState.job.ID)
	}

	var inboundCount int64
	if err := db.Model(&contracts.ChannelMessage{}).Where("direction = ?", contracts.ChannelMessageIn).Count(&inboundCount).Error; err != nil {
		t.Fatalf("count inbound failed: %v", err)
	}
	if inboundCount != 1 {
		t.Fatalf("duplicate pending path should not create extra inbound, got=%d", inboundCount)
	}

	var jobCount int64
	if err := db.Model(&contracts.ChannelTurnJob{}).Count(&jobCount).Error; err != nil {
		t.Fatalf("count jobs failed: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("duplicate pending path should not create extra job, got=%d", jobCount)
	}
}

func TestGateway_SubmitPersistsAndPublishes(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  &fakeProjectRuntime{},
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 4})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}

	sub, unsubscribe := gw.EventBus().Subscribe("alpha", "conv-1", 8)
	defer unsubscribe()

	resultCh := make(chan ProcessResult, 1)
	errCh := make(chan error, 1)
	env := contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeWeb,
		Adapter:            "web.ws",
		PeerConversationID: "conv-1",
		PeerMessageID:      "msg-1",
		SenderID:           "u1",
		Text:               "hello",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	if err := gw.Submit(context.Background(), GatewayInboundRequest{
		ProjectName: "alpha",
		Envelope:    env,
		Callback: func(res ProcessResult, runErr error) {
			if runErr != nil {
				errCh <- runErr
				return
			}
			resultCh <- res
		},
	}); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	var got ProcessResult
	select {
	case runErr := <-errCh:
		t.Fatalf("callback returned error: %v", runErr)
	case got = <-resultCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("wait callback timeout")
	}
	if got.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("expected succeeded, got=%s err=%s", got.JobStatus, got.JobError)
	}
	if !strings.Contains(got.ReplyText, "reply:") {
		t.Fatalf("reply text unexpected: %q", got.ReplyText)
	}
	if got.OutboundMessageID == 0 {
		t.Fatalf("outbound message should be persisted")
	}

	foundFinal := false
	deadline := time.After(3 * time.Second)
	for !foundFinal {
		select {
		case ev := <-sub:
			if strings.TrimSpace(ev.Type) == "assistant_message" {
				foundFinal = true
			}
		case <-deadline:
			t.Fatalf("did not receive assistant_message event")
		}
	}

	var msgCount int64
	if err := db.Model(&contracts.ChannelMessage{}).Count(&msgCount).Error; err != nil {
		t.Fatalf("count channel messages failed: %v", err)
	}
	if msgCount < 2 {
		t.Fatalf("expected inbound+outbound in gateway db, got=%d", msgCount)
	}

	var job contracts.ChannelTurnJob
	if err := db.First(&job, got.JobID).Error; err != nil {
		t.Fatalf("query turn job failed: %v", err)
	}
	if job.Status != contracts.ChannelTurnSucceeded {
		t.Fatalf("job status mismatch: %s", job.Status)
	}
}

func TestGateway_MarkOutboxDelivery(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  &fakeProjectRuntime{},
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 4})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}

	resultCh := make(chan ProcessResult, 1)
	errCh := make(chan error, 1)
	env := contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeIM,
		Adapter:            "im.feishu",
		PeerConversationID: "chat-mark",
		PeerMessageID:      "msg-mark-1",
		SenderID:           "u1",
		Text:               "hello",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	if err := gw.Submit(context.Background(), GatewayInboundRequest{
		ProjectName: "alpha",
		Envelope:    env,
		Callback: func(res ProcessResult, runErr error) {
			if runErr != nil {
				errCh <- runErr
				return
			}
			resultCh <- res
		},
	}); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	var got ProcessResult
	select {
	case runErr := <-errCh:
		t.Fatalf("callback returned error: %v", runErr)
	case got = <-resultCh:
	case <-time.After(3 * time.Second):
		t.Fatalf("wait callback timeout")
	}
	if got.OutboxID == 0 || got.OutboundMessageID == 0 {
		t.Fatalf("outbox/outbound should be persisted, outbox=%d outbound=%d", got.OutboxID, got.OutboundMessageID)
	}

	if err := gw.MarkOutboxDelivery(context.Background(), got.OutboxID, false, errors.New("feishu send failed")); err != nil {
		t.Fatalf("MarkOutboxDelivery failed status failed: %v", err)
	}

	var outbox contracts.ChannelOutbox
	if err := db.First(&outbox, got.OutboxID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxFailed {
		t.Fatalf("outbox status should be failed, got=%s", outbox.Status)
	}
	if !strings.Contains(outbox.LastError, "feishu send failed") {
		t.Fatalf("outbox last_error unexpected: %q", outbox.LastError)
	}

	var outbound contracts.ChannelMessage
	if err := db.First(&outbound, got.OutboundMessageID).Error; err != nil {
		t.Fatalf("query outbound message failed: %v", err)
	}
	if outbound.Status != contracts.ChannelMessageFailed {
		t.Fatalf("outbound status should be failed, got=%s", outbound.Status)
	}

	if err := gw.MarkOutboxDelivery(context.Background(), got.OutboxID, true, nil); err != nil {
		t.Fatalf("MarkOutboxDelivery sent failed: %v", err)
	}
	if err := db.First(&outbox, got.OutboxID).Error; err != nil {
		t.Fatalf("query outbox after sent failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxSent {
		t.Fatalf("outbox status should be sent, got=%s", outbox.Status)
	}
	if strings.TrimSpace(outbox.LastError) != "" {
		t.Fatalf("outbox last_error should be empty after sent, got=%q", outbox.LastError)
	}

	if err := db.First(&outbound, got.OutboundMessageID).Error; err != nil {
		t.Fatalf("query outbound message after sent failed: %v", err)
	}
	if outbound.Status != contracts.ChannelMessageSent {
		t.Fatalf("outbound status should be sent, got=%s", outbound.Status)
	}

	var conv contracts.ChannelConversation
	if err := db.First(&conv, outbound.ConversationID).Error; err != nil {
		t.Fatalf("query conversation failed: %v", err)
	}
	if conv.LastMessageAt == nil {
		t.Fatalf("conversation last_message_at should be updated after sent")
	}
}

func TestGateway_SubmitPublishesStreamEventsAndFinalMessage(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  &streamingProjectRuntime{},
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 4})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}

	sub, unsubscribe := gw.EventBus().Subscribe("alpha", "conv-stream", 32)
	defer unsubscribe()

	done := make(chan struct{})
	if err := gw.Submit(context.Background(), GatewayInboundRequest{
		ProjectName: "alpha",
		Envelope: contracts.InboundEnvelope{
			Schema:             contracts.ChannelInboundSchemaV1,
			ChannelType:        contracts.ChannelTypeWeb,
			Adapter:            "web.ws",
			PeerConversationID: "conv-stream",
			PeerMessageID:      "msg-stream-1",
			SenderID:           "u1",
			Text:               "hello",
			ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
		},
		Callback: func(res ProcessResult, runErr error) {
			if runErr != nil {
				t.Errorf("callback err: %v", runErr)
			}
			close(done)
		},
	}); err != nil {
		t.Fatalf("submit failed: %v", err)
	}

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("wait callback timeout")
	}

	var (
		gotStream bool
		gotFinal  bool
	)
	deadline := time.After(3 * time.Second)
	for !(gotStream && gotFinal) {
		select {
		case ev := <-sub:
			if strings.TrimSpace(ev.Type) == "assistant_event" && strings.TrimSpace(ev.Stream) == "assistant" {
				gotStream = true
			}
			if strings.TrimSpace(ev.Type) == "assistant_message" {
				gotFinal = true
			}
		case <-deadline:
			t.Fatalf("stream/final event timeout, gotStream=%v gotFinal=%v", gotStream, gotFinal)
		}
	}
}

func TestGateway_Submit_ContextCanceled(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  &fakeProjectRuntime{},
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 2})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = gw.Submit(ctx, GatewayInboundRequest{
		ProjectName: "alpha",
		Envelope: contracts.InboundEnvelope{
			Schema:             contracts.ChannelInboundSchemaV1,
			ChannelType:        contracts.ChannelTypeCLI,
			Adapter:            "cli.local",
			PeerConversationID: "conv-ctx",
			PeerMessageID:      "msg-ctx",
			SenderID:           "u1",
			Text:               "hello",
			ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Submit should return context canceled, got=%v", err)
	}
	if got := gw.queue.Len("alpha"); got != 0 {
		t.Fatalf("canceled submit should not enqueue item, queue len=%d", got)
	}
}

func TestGateway_InterruptBoundConversation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	runtime := &interruptProjectRuntime{interruptOK: true}
	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  runtime,
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 4})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}
	if _, err := gw.BindProject(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-1", "alpha"); err != nil {
		t.Fatalf("bind project failed: %v", err)
	}

	projectName, result, err := gw.InterruptBoundConversation(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-1", "chat-1")
	if err != nil {
		t.Fatalf("InterruptBoundConversation failed: %v", err)
	}
	if strings.TrimSpace(projectName) != "alpha" {
		t.Fatalf("unexpected project: %q", projectName)
	}
	if result.Status != InterruptStatusHit {
		t.Fatalf("expected status=hit, got=%s", result.Status)
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
	if runtime.lastConversationID != "chat-1" {
		t.Fatalf("conversation id mismatch: %q", runtime.lastConversationID)
	}
}

func TestGateway_ResetBoundConversationSession(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	runtime := &interruptProjectRuntime{resetOK: true}
	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  runtime,
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 4})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}
	if _, err := gw.BindProject(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-r", "alpha"); err != nil {
		t.Fatalf("bind project failed: %v", err)
	}

	projectName, reset, err := gw.ResetBoundConversationSession(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-r", "chat-r")
	if err != nil {
		t.Fatalf("ResetBoundConversationSession failed: %v", err)
	}
	if strings.TrimSpace(projectName) != "alpha" {
		t.Fatalf("unexpected project: %q", projectName)
	}
	if !reset {
		t.Fatalf("expected reset=true")
	}

	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	if runtime.resetCalls != 1 {
		t.Fatalf("reset calls mismatch: %d", runtime.resetCalls)
	}
	if runtime.lastResetConvID != "chat-r" {
		t.Fatalf("reset conversation id mismatch: %q", runtime.lastResetConvID)
	}
}

func TestGateway_BindingQuietMode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  &fakeProjectRuntime{},
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 2})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}

	projectName, quietMode, err := gw.LookupBindingDetail(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-q")
	if err != nil {
		t.Fatalf("LookupBindingDetail on unbound chat should not fail: %v", err)
	}
	if projectName != "" {
		t.Fatalf("unbound project should be empty, got %q", projectName)
	}
	if quietMode {
		t.Fatalf("unbound quiet mode should default to false")
	}

	err = gw.SetBindingQuietMode(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-q", true)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("SetBindingQuietMode on unbound chat should return not found, got=%v", err)
	}

	if _, err := gw.BindProject(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-q", "alpha"); err != nil {
		t.Fatalf("BindProject failed: %v", err)
	}

	quietMode, err = gw.GetBindingQuietMode(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-q")
	if err != nil {
		t.Fatalf("GetBindingQuietMode failed: %v", err)
	}
	if quietMode {
		t.Fatalf("quiet mode should default to false after bind")
	}

	if err := gw.SetBindingQuietMode(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-q", true); err != nil {
		t.Fatalf("SetBindingQuietMode true failed: %v", err)
	}
	projectName, quietMode, err = gw.LookupBindingDetail(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-q")
	if err != nil {
		t.Fatalf("LookupBindingDetail after enable quiet failed: %v", err)
	}
	if projectName != "alpha" {
		t.Fatalf("unexpected project after bind: %q", projectName)
	}
	if !quietMode {
		t.Fatalf("quiet mode should be true after set")
	}

	if err := gw.SetBindingQuietMode(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-q", false); err != nil {
		t.Fatalf("SetBindingQuietMode false failed: %v", err)
	}
	quietMode, err = gw.GetBindingQuietMode(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-q")
	if err != nil {
		t.Fatalf("GetBindingQuietMode after disable failed: %v", err)
	}
	if quietMode {
		t.Fatalf("quiet mode should be false after disable")
	}
}

func TestGateway_Submit_ProjectParallelAndSerialOrder(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	alphaRuntime := newGatedProjectRuntime("alpha")
	betaRuntime := newGatedProjectRuntime("beta")
	resolver := &mapProjectResolver{
		ctx: map[string]*ProjectContext{
			"alpha": {Name: "alpha", RepoRoot: "/tmp/alpha", Runtime: alphaRuntime},
			"beta":  {Name: "beta", RepoRoot: "/tmp/beta", Runtime: betaRuntime},
		},
	}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 8})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}

	alphaRes1 := make(chan ProcessResult, 1)
	alphaRes2 := make(chan ProcessResult, 1)
	betaRes := make(chan ProcessResult, 1)
	errCh := make(chan error, 3)
	submit := func(project, conv, msgID, text string, out chan ProcessResult) {
		t.Helper()
		runErr := gw.Submit(context.Background(), GatewayInboundRequest{
			ProjectName: project,
			Envelope: contracts.InboundEnvelope{
				Schema:             contracts.ChannelInboundSchemaV1,
				ChannelType:        contracts.ChannelTypeCLI,
				Adapter:            "cli.local",
				PeerConversationID: conv,
				PeerMessageID:      msgID,
				SenderID:           "u1",
				Text:               text,
				ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
			},
			Callback: func(res ProcessResult, cbErr error) {
				if cbErr != nil {
					errCh <- cbErr
					return
				}
				out <- res
			},
		})
		if runErr != nil {
			t.Fatalf("submit failed for project=%s: %v", project, runErr)
		}
	}

	submit("alpha", "conv-alpha", "msg-a1", "alpha-1", alphaRes1)
	if got := alphaRuntime.waitEntered(t, time.Second); got != 1 {
		t.Fatalf("alpha first call number mismatch: got=%d want=1", got)
	}

	submit("alpha", "conv-alpha", "msg-a2", "alpha-2", alphaRes2)
	select {
	case got := <-alphaRuntime.entered:
		t.Fatalf("alpha second call should wait for first completion, got entered=%d", got)
	case <-time.After(200 * time.Millisecond):
		// 预期：同 project 串行，第二条不能提前进入 runtime。
	}

	submit("beta", "conv-beta", "msg-b1", "beta-1", betaRes)
	if got := betaRuntime.waitEntered(t, time.Second); got != 1 {
		t.Fatalf("beta first call number mismatch: got=%d want=1", got)
	}

	betaRuntime.releaseOne()
	alphaRuntime.releaseOne()
	if got := alphaRuntime.waitEntered(t, time.Second); got != 2 {
		t.Fatalf("alpha second call number mismatch: got=%d want=2", got)
	}
	alphaRuntime.releaseOne()

	waitResult := func(ch chan ProcessResult, label string) ProcessResult {
		t.Helper()
		select {
		case cbErr := <-errCh:
			t.Fatalf("%s callback error: %v", label, cbErr)
		case res := <-ch:
			return res
		case <-time.After(2 * time.Second):
			t.Fatalf("wait %s callback timeout", label)
			return ProcessResult{}
		}
		return ProcessResult{}
	}

	alphaFirst := waitResult(alphaRes1, "alpha-1")
	alphaSecond := waitResult(alphaRes2, "alpha-2")
	betaFirst := waitResult(betaRes, "beta-1")

	if alphaFirst.JobStatus != contracts.ChannelTurnSucceeded || alphaSecond.JobStatus != contracts.ChannelTurnSucceeded || betaFirst.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("unexpected job status: alpha1=%s alpha2=%s beta=%s", alphaFirst.JobStatus, alphaSecond.JobStatus, betaFirst.JobStatus)
	}
	if !strings.Contains(alphaFirst.ReplyText, "alpha-reply-1") {
		t.Fatalf("alpha first reply unexpected: %q", alphaFirst.ReplyText)
	}
	if !strings.Contains(alphaSecond.ReplyText, "alpha-reply-2") {
		t.Fatalf("alpha second reply unexpected: %q", alphaSecond.ReplyText)
	}
	if !strings.Contains(betaFirst.ReplyText, "beta-reply-1") {
		t.Fatalf("beta reply unexpected: %q", betaFirst.ReplyText)
	}
}

func TestGateway_Stop_IdempotentAndRejectsSubmit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  &fakeProjectRuntime{},
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 4})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}

	done := make(chan struct{}, 1)
	if err := gw.Submit(context.Background(), GatewayInboundRequest{
		ProjectName: "alpha",
		Envelope: contracts.InboundEnvelope{
			Schema:             contracts.ChannelInboundSchemaV1,
			ChannelType:        contracts.ChannelTypeCLI,
			Adapter:            "cli.local",
			PeerConversationID: "conv-stop",
			PeerMessageID:      "msg-stop-1",
			SenderID:           "u1",
			Text:               "hello",
			ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
		},
		Callback: func(res ProcessResult, runErr error) {
			if runErr != nil {
				t.Errorf("callback err: %v", runErr)
			}
			done <- struct{}{}
		},
	}); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("wait callback timeout")
	}

	if err := gw.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop failed: %v", err)
	}
	if err := gw.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop should be idempotent, got=%v", err)
	}

	err = gw.Submit(context.Background(), GatewayInboundRequest{
		ProjectName: "alpha",
		Envelope: contracts.InboundEnvelope{
			Schema:             contracts.ChannelInboundSchemaV1,
			ChannelType:        contracts.ChannelTypeCLI,
			Adapter:            "cli.local",
			PeerConversationID: "conv-stop",
			PeerMessageID:      "msg-stop-2",
			SenderID:           "u1",
			Text:               "after-stop",
			ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
		},
	})
	if !errors.Is(err, ErrGatewayStopped) {
		t.Fatalf("submit after stop should return ErrGatewayStopped, got=%v", err)
	}
}

func TestGateway_Stop_ContextTimeoutThenDrain(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "gateway.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}

	runtime := newGatedProjectRuntime("alpha")
	resolver := &fakeProjectResolver{ctx: &ProjectContext{
		Name:     "alpha",
		RepoRoot: "/tmp/alpha",
		Runtime:  runtime,
	}}
	gw, err := NewGateway(db, resolver, GatewayOptions{QueueDepth: 4})
	if err != nil {
		t.Fatalf("new gateway failed: %v", err)
	}

	done := make(chan struct{}, 1)
	if err := gw.Submit(context.Background(), GatewayInboundRequest{
		ProjectName: "alpha",
		Envelope: contracts.InboundEnvelope{
			Schema:             contracts.ChannelInboundSchemaV1,
			ChannelType:        contracts.ChannelTypeCLI,
			Adapter:            "cli.local",
			PeerConversationID: "conv-timeout",
			PeerMessageID:      "msg-timeout-1",
			SenderID:           "u1",
			Text:               "hello",
			ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
		},
		Callback: func(res ProcessResult, runErr error) {
			if runErr != nil {
				t.Errorf("callback err: %v", runErr)
			}
			done <- struct{}{}
		},
	}); err != nil {
		t.Fatalf("submit failed: %v", err)
	}
	if got := runtime.waitEntered(t, time.Second); got != 1 {
		t.Fatalf("runtime enter mismatch: got=%d", got)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if err := gw.Stop(stopCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stop should timeout while worker busy, got=%v", err)
	}

	if err := gw.Submit(context.Background(), GatewayInboundRequest{
		ProjectName: "alpha",
		Envelope: contracts.InboundEnvelope{
			Schema:             contracts.ChannelInboundSchemaV1,
			ChannelType:        contracts.ChannelTypeCLI,
			Adapter:            "cli.local",
			PeerConversationID: "conv-timeout",
			PeerMessageID:      "msg-timeout-2",
			SenderID:           "u1",
			Text:               "should-reject",
			ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
		},
	}); !errors.Is(err, ErrGatewayStopped) {
		t.Fatalf("submit after stop begin should return ErrGatewayStopped, got=%v", err)
	}

	runtime.releaseOne()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("wait callback timeout")
	}

	if err := gw.Stop(context.Background()); err != nil {
		t.Fatalf("stop after worker drained failed: %v", err)
	}
}
