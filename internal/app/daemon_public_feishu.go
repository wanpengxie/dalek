package app

import (
	"log/slog"
	"net/http"
	"strings"

	channelsvc "dalek/internal/services/channel"
	feishusvc "dalek/internal/services/channel/feishu"
)

const defaultDaemonFeishuAdapter = feishusvc.DefaultAdapter

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
