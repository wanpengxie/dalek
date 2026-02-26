package app

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	channelsvc "dalek/internal/services/channel"
	feishusvc "dalek/internal/services/channel/feishu"
)

const defaultDaemonFeishuAdapter = feishusvc.DefaultAdapter

var (
	daemonFeishuRelayTimeout     = 10 * time.Minute
	daemonFeishuRelayIdleTimeout = 5 * time.Minute
)

type daemonFeishuMessageSender = feishusvc.MessageSender

type daemonFeishuNoopSender = feishusvc.NoopSender

type daemonFeishuWebhookOptions = feishusvc.HandlerOptions

func newDaemonFeishuSender(cfg HomeDaemonPublicFeishuConfig, logger *slog.Logger) daemonFeishuMessageSender {
	return feishusvc.NewSender(feishusvc.SenderConfig{
		Enabled:   cfg.Enabled,
		AppID:     cfg.AppID,
		AppSecret: cfg.AppSecret,
		BaseURL:   cfg.BaseURL,
		Logger:    logger,
	})
}

func newDaemonFeishuWebhookHandler(gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender daemonFeishuMessageSender, rawOpt daemonFeishuWebhookOptions, logger *slog.Logger) http.HandlerFunc {
	opt := rawOpt
	if strings.TrimSpace(opt.Adapter) == "" {
		opt.Adapter = defaultDaemonFeishuAdapter
	}
	opt.Logger = logger
	opt.RelayTimeout = daemonFeishuRelayTimeout
	opt.RelayIdleTimeout = daemonFeishuRelayIdleTimeout
	return feishusvc.NewWebhookHandler(gateway, resolver, sender, opt)
}

func resolveDaemonFeishuWebhookPath(cfg HomeConfig) string {
	override := strings.TrimSpace(cfg.Daemon.Public.Feishu.WebhookPath)
	if override != "" {
		if !strings.HasPrefix(override, "/") {
			override = "/" + override
		}
		return override
	}
	return feishusvc.BuildWebhookPath(cfg.Daemon.Public.Feishu.WebhookSecretPath)
}

func buildDaemonFeishuWebhookPath(secretPath string) string {
	return feishusvc.BuildWebhookPath(secretPath)
}

func normalizeDaemonFeishuWebhookSecretPath(raw string) string {
	return feishusvc.NormalizeWebhookSecretPath(raw)
}
