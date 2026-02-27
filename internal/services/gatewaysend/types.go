package gatewaysend

import (
	"context"
	"errors"
	"time"

	"dalek/internal/contracts"
)

const (
	Path             = "/api/send"
	AdapterFeishu    = "im.feishu"
	ResponseSchemaV1 = "dalek.gateway_send.response.v1"
	payloadSchemaV1  = "dalek.gateway_send.payload.v1"
	sendDedupWindow  = 30 * time.Second

	payloadKeyCardJSON         = "card_json"
	payloadKeySendMode         = "send_mode"
	payloadSendModeInteractive = "interactive"

	defaultRetryMaxRetries     = 5
	defaultRetryInitialBackoff = 10 * time.Second
	defaultRetryMaxBackoff     = 5 * time.Minute
	defaultRetryBackoffFactor  = 2.0
)

type Request struct {
	Project string `json:"project"`
	Text    string `json:"text"`
}

type Delivery struct {
	BindingID      uint   `json:"binding_id"`
	ConversationID uint   `json:"conversation_id"`
	MessageID      uint   `json:"message_id"`
	OutboxID       uint   `json:"outbox_id"`
	ChatID         string `json:"chat_id"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
}

type Response struct {
	Schema    string     `json:"schema"`
	Project   string     `json:"project"`
	Text      string     `json:"text"`
	Delivered int        `json:"delivered"`
	Failed    int        `json:"failed"`
	Results   []Delivery `json:"results,omitempty"`
}

type RetryPolicy struct {
	MaxRetries     int
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	BackoffFactor  float64
}

func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxRetries:     defaultRetryMaxRetries,
		InitialBackoff: defaultRetryInitialBackoff,
		MaxBackoff:     defaultRetryMaxBackoff,
		BackoffFactor:  defaultRetryBackoffFactor,
	}
}

func (p RetryPolicy) normalize() RetryPolicy {
	out := p
	if out.MaxRetries <= 0 {
		out.MaxRetries = defaultRetryMaxRetries
	}
	if out.InitialBackoff <= 0 {
		out.InitialBackoff = defaultRetryInitialBackoff
	}
	if out.MaxBackoff <= 0 {
		out.MaxBackoff = defaultRetryMaxBackoff
	}
	if out.BackoffFactor < 1 {
		out.BackoffFactor = defaultRetryBackoffFactor
	}
	if out.InitialBackoff > out.MaxBackoff {
		out.InitialBackoff = out.MaxBackoff
	}
	return out
}

func (p RetryPolicy) IsExhausted(retryCount int) bool {
	p = p.normalize()
	if retryCount < 0 {
		retryCount = 0
	}
	return retryCount >= p.MaxRetries
}

func (p RetryPolicy) NextBackoff(retryCount int) time.Duration {
	p = p.normalize()
	if retryCount <= 0 {
		retryCount = 1
	}
	delay := p.InitialBackoff
	for i := 1; i < retryCount; i++ {
		next := time.Duration(float64(delay) * p.BackoffFactor)
		if next <= delay || next > p.MaxBackoff {
			delay = p.MaxBackoff
			break
		}
		delay = next
	}
	if delay > p.MaxBackoff {
		delay = p.MaxBackoff
	}
	if delay <= 0 {
		delay = p.InitialBackoff
	}
	return delay
}

func (p RetryPolicy) NextRetryAt(retryCount int, now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	return now.Add(p.NextBackoff(retryCount))
}

type MessageSender interface {
	SendCard(ctx context.Context, chatID, title, markdown string) error
	SendText(ctx context.Context, chatID, text string) error
}

type NoopSender struct{}

func (s *NoopSender) SendText(ctx context.Context, chatID, text string) error {
	_ = ctx
	_ = chatID
	_ = text
	return nil
}

func (s *NoopSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	_ = chatID
	_ = title
	_ = markdown
	return nil
}

type HandlerConfig struct {
	AuthToken string
}

type persistState struct {
	conversation contracts.ChannelConversation
	message      contracts.ChannelMessage
	outbox       contracts.ChannelOutbox
}

type retryableOutbox struct {
	binding  contracts.ChannelBinding
	state    persistState
	project  string
	text     string
	cardJSON string
}

type Repository interface {
	FindEnabledBindings(ctx context.Context, projectName string, channelType contracts.ChannelType, adapter string) ([]contracts.ChannelBinding, error)
	FindRecentDuplicateDelivery(ctx context.Context, binding contracts.ChannelBinding, text string) (Delivery, bool, error)
	CreatePending(ctx context.Context, binding contracts.ChannelBinding, projectName, text string) (persistState, error)
	MarkSending(ctx context.Context, outboxID uint) error
	MarkSent(ctx context.Context, state persistState) error
	MarkFailedRetryable(ctx context.Context, state persistState, cause error, nextRetryAt time.Time) error
	MarkFailed(ctx context.Context, state persistState, cause error) error
	MarkDead(ctx context.Context, state persistState, cause error) error
	FindPendingOutbox(ctx context.Context, limit int) ([]retryableOutbox, error)
	FindRetryableOutbox(ctx context.Context, now time.Time, limit int) ([]retryableOutbox, error)
}

var (
	ErrBindingNotFound   = errors.New("project 未绑定飞书 chat_id")
	ErrOutboxNotSendable = errors.New("outbox 状态不可发送")
)
