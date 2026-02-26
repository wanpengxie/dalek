package gatewaysend

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

type serviceMockRepo struct {
	findBindingsFn func(ctx context.Context, projectName string, channelType contracts.ChannelType, adapter string) ([]store.ChannelBinding, error)
	findDupFn      func(ctx context.Context, binding store.ChannelBinding, text string) (Delivery, bool, error)
	createPending  func(ctx context.Context, binding store.ChannelBinding, projectName, text string) (persistState, error)
	markSending    func(ctx context.Context, outboxID uint) error
	markSent       func(ctx context.Context, state persistState) error
	markFailed     func(ctx context.Context, state persistState, cause error) error
}

func (m *serviceMockRepo) FindEnabledBindings(ctx context.Context, projectName string, channelType contracts.ChannelType, adapter string) ([]store.ChannelBinding, error) {
	if m != nil && m.findBindingsFn != nil {
		return m.findBindingsFn(ctx, projectName, channelType, adapter)
	}
	return nil, nil
}

func (m *serviceMockRepo) FindRecentDuplicateDelivery(ctx context.Context, binding store.ChannelBinding, text string) (Delivery, bool, error) {
	if m != nil && m.findDupFn != nil {
		return m.findDupFn(ctx, binding, text)
	}
	return Delivery{}, false, nil
}

func (m *serviceMockRepo) CreatePending(ctx context.Context, binding store.ChannelBinding, projectName, text string) (persistState, error) {
	if m != nil && m.createPending != nil {
		return m.createPending(ctx, binding, projectName, text)
	}
	return persistState{}, nil
}

func (m *serviceMockRepo) MarkSending(ctx context.Context, outboxID uint) error {
	if m != nil && m.markSending != nil {
		return m.markSending(ctx, outboxID)
	}
	return nil
}

func (m *serviceMockRepo) MarkSent(ctx context.Context, state persistState) error {
	if m != nil && m.markSent != nil {
		return m.markSent(ctx, state)
	}
	return nil
}

func (m *serviceMockRepo) MarkFailed(ctx context.Context, state persistState, cause error) error {
	if m != nil && m.markFailed != nil {
		return m.markFailed(ctx, state, cause)
	}
	return nil
}

func TestService_Send_UsesRepositoryAndSender(t *testing.T) {
	binding := store.ChannelBinding{ID: 11, Adapter: AdapterFeishu, PeerProjectKey: "chat-service-1"}
	state := persistState{
		conversation: store.ChannelConversation{ID: 21},
		message:      store.ChannelMessage{ID: 31},
		outbox:       store.ChannelOutbox{ID: 41},
	}

	markSendingCalled := false
	markSentCalled := false
	repo := &serviceMockRepo{
		findBindingsFn: func(ctx context.Context, projectName string, channelType contracts.ChannelType, adapter string) ([]store.ChannelBinding, error) {
			if projectName != "demo" {
				t.Fatalf("unexpected projectName: %q", projectName)
			}
			if channelType != contracts.ChannelTypeIM || adapter != AdapterFeishu {
				t.Fatalf("unexpected query args: channelType=%s adapter=%s", channelType, adapter)
			}
			return []store.ChannelBinding{binding}, nil
		},
		findDupFn: func(ctx context.Context, gotBinding store.ChannelBinding, text string) (Delivery, bool, error) {
			if gotBinding.ID != binding.ID || text != "hello" {
				t.Fatalf("unexpected dedup input: binding=%d text=%q", gotBinding.ID, text)
			}
			return Delivery{}, false, nil
		},
		createPending: func(ctx context.Context, gotBinding store.ChannelBinding, projectName, text string) (persistState, error) {
			if gotBinding.ID != binding.ID || projectName != "demo" || text != "hello" {
				t.Fatalf("unexpected create pending input: binding=%d project=%q text=%q", gotBinding.ID, projectName, text)
			}
			return state, nil
		},
		markSending: func(ctx context.Context, outboxID uint) error {
			markSendingCalled = true
			if outboxID != state.outbox.ID {
				t.Fatalf("unexpected outbox id: %d", outboxID)
			}
			return nil
		},
		markSent: func(ctx context.Context, gotState persistState) error {
			markSentCalled = true
			if gotState.message.ID != state.message.ID || gotState.outbox.ID != state.outbox.ID {
				t.Fatalf("unexpected mark sent state: %+v", gotState)
			}
			return nil
		},
		markFailed: func(ctx context.Context, state persistState, cause error) error {
			t.Fatalf("markFailed should not be called, state=%+v cause=%v", state, cause)
			return nil
		},
	}

	sender := &captureSender{}
	svc := NewService(repo, sender, nil, nil)

	resp, err := svc.Send(context.Background(), " demo ", " hello ")
	if err != nil {
		t.Fatalf("Service.Send failed: %v", err)
	}
	if resp.Delivered != 1 || resp.Failed != 0 || len(resp.Results) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Results[0].Status != string(contracts.ChannelOutboxSent) {
		t.Fatalf("unexpected result status: %s", resp.Results[0].Status)
	}
	if resp.Results[0].MessageID != state.message.ID || resp.Results[0].OutboxID != state.outbox.ID {
		t.Fatalf("unexpected result ids: %+v", resp.Results[0])
	}
	if !markSendingCalled || !markSentCalled {
		t.Fatalf("expected markSending/markSent called, got markSending=%v markSent=%v", markSendingCalled, markSentCalled)
	}

	calls := sender.snapshot()
	if len(calls) != 1 {
		t.Fatalf("sender call count mismatch: %d", len(calls))
	}
	if calls[0].chatID != binding.PeerProjectKey || calls[0].text != "hello" {
		t.Fatalf("unexpected sender call: %+v", calls[0])
	}
	if calls[0].title != "demo" {
		t.Fatalf("unexpected sender title: %q", calls[0].title)
	}
}

func TestService_Send_DedupSkipsSender(t *testing.T) {
	binding := store.ChannelBinding{ID: 12, Adapter: AdapterFeishu, PeerProjectKey: "chat-service-dedup"}
	reused := Delivery{
		BindingID:      binding.ID,
		ConversationID: 22,
		MessageID:      32,
		OutboxID:       42,
		ChatID:         binding.PeerProjectKey,
		Status:         string(contracts.ChannelOutboxSent),
	}
	repo := &serviceMockRepo{
		findBindingsFn: func(ctx context.Context, projectName string, channelType contracts.ChannelType, adapter string) ([]store.ChannelBinding, error) {
			return []store.ChannelBinding{binding}, nil
		},
		findDupFn: func(ctx context.Context, gotBinding store.ChannelBinding, text string) (Delivery, bool, error) {
			return reused, true, nil
		},
		createPending: func(ctx context.Context, binding store.ChannelBinding, projectName, text string) (persistState, error) {
			t.Fatalf("dedup hit should skip createPending")
			return persistState{}, nil
		},
		markSending: func(ctx context.Context, outboxID uint) error {
			t.Fatalf("dedup hit should skip markSending")
			return nil
		},
		markSent: func(ctx context.Context, state persistState) error {
			t.Fatalf("dedup hit should skip markSent")
			return nil
		},
	}

	sender := &captureSender{}
	svc := NewService(repo, sender, nil, nil)

	resp, err := svc.Send(context.Background(), "demo", "hello")
	if err != nil {
		t.Fatalf("Service.Send failed: %v", err)
	}
	if resp.Delivered != 1 || resp.Failed != 0 || len(resp.Results) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Results[0].MessageID != reused.MessageID || resp.Results[0].OutboxID != reused.OutboxID {
		t.Fatalf("expected dedup ids reused: %+v", resp.Results[0])
	}
	if len(sender.snapshot()) != 0 {
		t.Fatalf("dedup hit should skip sender call")
	}
}

func TestService_Send_SenderFailureMarksFailed(t *testing.T) {
	binding := store.ChannelBinding{ID: 13, Adapter: AdapterFeishu, PeerProjectKey: "chat-service-fail"}
	state := persistState{
		conversation: store.ChannelConversation{ID: 23},
		message:      store.ChannelMessage{ID: 33},
		outbox:       store.ChannelOutbox{ID: 43},
	}
	markFailedCalled := false
	repo := &serviceMockRepo{
		findBindingsFn: func(ctx context.Context, projectName string, channelType contracts.ChannelType, adapter string) ([]store.ChannelBinding, error) {
			return []store.ChannelBinding{binding}, nil
		},
		findDupFn: func(ctx context.Context, binding store.ChannelBinding, text string) (Delivery, bool, error) {
			return Delivery{}, false, nil
		},
		createPending: func(ctx context.Context, binding store.ChannelBinding, projectName, text string) (persistState, error) {
			return state, nil
		},
		markSending: func(ctx context.Context, outboxID uint) error {
			return nil
		},
		markSent: func(ctx context.Context, state persistState) error {
			t.Fatalf("markSent should not be called when sender failed")
			return nil
		},
		markFailed: func(ctx context.Context, gotState persistState, cause error) error {
			markFailedCalled = true
			if gotState.outbox.ID != state.outbox.ID {
				t.Fatalf("unexpected markFailed state: %+v", gotState)
			}
			if !strings.Contains(fmt.Sprint(cause), "mock send failed") {
				t.Fatalf("unexpected cause: %v", cause)
			}
			return nil
		},
	}

	svc := NewService(repo, &failingSender{err: fmt.Errorf("mock send failed")}, nil, nil)
	resp, err := svc.Send(context.Background(), "demo", "hello")
	if err != nil {
		t.Fatalf("Service.Send failed: %v", err)
	}
	if resp.Delivered != 0 || resp.Failed != 1 || len(resp.Results) != 1 {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if resp.Results[0].Status != string(contracts.ChannelOutboxFailed) {
		t.Fatalf("unexpected result status: %s", resp.Results[0].Status)
	}
	if !strings.Contains(resp.Results[0].Error, "mock send failed") {
		t.Fatalf("unexpected result error: %q", resp.Results[0].Error)
	}
	if !markFailedCalled {
		t.Fatalf("expected markFailed called")
	}
}
