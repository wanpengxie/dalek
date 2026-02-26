package ws

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"dalek/internal/contracts"
	channelsvc "dalek/internal/services/channel"
)

type TurnProcessor interface {
	ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (channelsvc.ProcessResult, error)
}

type ListInboxFunc func(ctx context.Context, limit int) ([]contracts.InboxItem, error)

type ServerOptions struct {
	Path               string
	DefaultSender      string
	ConversationPrefix string
	TurnTimeout        time.Duration
	InboxPollInterval  time.Duration
	InboxLimit         int
	Logger             *slog.Logger

	TurnProcessor TurnProcessor
	ListInbox     ListInboxFunc
}

func normalizeServerOptions(raw ServerOptions) ServerOptions {
	opt := raw
	if strings.TrimSpace(opt.Path) == "" {
		opt.Path = "/ws"
	}
	opt.DefaultSender = strings.TrimSpace(opt.DefaultSender)
	if opt.DefaultSender == "" {
		opt.DefaultSender = "ws.user"
	}
	opt.ConversationPrefix = strings.TrimSpace(opt.ConversationPrefix)
	if opt.ConversationPrefix == "" {
		opt.ConversationPrefix = "ws"
	}
	if opt.TurnTimeout < 0 {
		opt.TurnTimeout = 0
	}
	if opt.InboxPollInterval <= 0 {
		opt.InboxPollInterval = 2 * time.Second
	}
	if opt.InboxLimit <= 0 {
		opt.InboxLimit = 20
	}
	if opt.InboxLimit > 200 {
		opt.InboxLimit = 200
	}
	return opt
}
