package gatewaysend

import (
	"context"
	"errors"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

const (
	Path             = "/api/send"
	AdapterFeishu    = "im.feishu"
	ResponseSchemaV1 = "dalek.gateway_send.response.v1"
	payloadSchemaV1  = "dalek.gateway_send.payload.v1"
	sendDedupWindow  = 30 * time.Second
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

type MessageSender interface {
	SendCard(ctx context.Context, chatID, title, markdown string) error
}

type NoopSender struct{}

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
	conversation store.ChannelConversation
	message      store.ChannelMessage
	outbox       store.ChannelOutbox
}

type Repository interface {
	FindEnabledBindings(ctx context.Context, projectName string, channelType contracts.ChannelType, adapter string) ([]store.ChannelBinding, error)
	FindRecentDuplicateDelivery(ctx context.Context, binding store.ChannelBinding, text string) (Delivery, bool, error)
	CreatePending(ctx context.Context, binding store.ChannelBinding, projectName, text string) (persistState, error)
	MarkSending(ctx context.Context, outboxID uint) error
	MarkSent(ctx context.Context, state persistState) error
	MarkFailed(ctx context.Context, state persistState, cause error) error
}

var ErrBindingNotFound = errors.New("project 未绑定飞书 chat_id")
