package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"

	channelsvc "dalek/internal/services/channel"
	feishusvc "dalek/internal/services/channel/feishu"
)

type gatewayFeishuHandlerOptions = feishusvc.HandlerOptions

type feishuMessageSender = feishusvc.MessageSender

type noopFeishuSender = feishusvc.NoopSender

type feishuSenderConfig struct {
	AppID     string
	AppSecret string
	BaseURL   string
}

func newFeishuSenderFromEnv() feishuMessageSender {
	return newFeishuSender(feishuSenderConfig{
		AppID:     os.Getenv("FEISHU_APP_ID"),
		AppSecret: os.Getenv("FEISHU_APP_SECRET"),
		BaseURL:   os.Getenv("DALEK_FEISHU_BASE_URL"),
	})
}

func newFeishuSender(raw feishuSenderConfig) feishuMessageSender {
	return feishusvc.NewSender(feishusvc.SenderConfig{
		Enabled:   true,
		AppID:     raw.AppID,
		AppSecret: raw.AppSecret,
		BaseURL:   raw.BaseURL,
		Logger:    slog.Default(),
	})
}

func newGatewayFeishuWebhookHandler(gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender feishuMessageSender, rawOpt gatewayFeishuHandlerOptions) http.HandlerFunc {
	return feishusvc.NewWebhookHandler(gateway, resolver, sender, rawOpt)
}

func tryHandleFeishuBindCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender feishuMessageSender, adapter, chatID, text string) bool {
	return feishusvc.TryHandleBindCommand(ctx, gateway, resolver, sender, adapter, chatID, text)
}

func tryHandleFeishuUnbindCommand(ctx context.Context, gateway *channelsvc.Gateway, sender feishuMessageSender, adapter, chatID, text string) bool {
	return feishusvc.TryHandleUnbindCommand(ctx, gateway, sender, adapter, chatID, text)
}

func tryHandleFeishuInterruptCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender feishuMessageSender, adapter, chatID, text string) bool {
	return feishusvc.TryHandleInterruptCommand(ctx, gateway, resolver, sender, adapter, chatID, text)
}

func tryHandleFeishuNewCommand(ctx context.Context, gateway *channelsvc.Gateway, resolver channelsvc.ProjectResolver, sender feishuMessageSender, adapter, chatID, text string) bool {
	return feishusvc.TryHandleNewCommand(ctx, gateway, resolver, sender, adapter, chatID, text)
}

func buildFeishuUnboundHint(resolver channelsvc.ProjectResolver) string {
	return feishusvc.BuildUnboundHint(resolver)
}

func appendFeishuProgressLine(lines []string, line string, maxLines int) []string {
	return feishusvc.AppendProgressLine(lines, line, maxLines)
}

func normalizeFeishuCardMarkdown(markdown string) string {
	return feishusvc.NormalizeCardMarkdown(markdown)
}

func resolveFeishuCardProjectName(projectName string, resolver channelsvc.ProjectResolver) string {
	return feishusvc.ResolveCardProjectName(projectName, resolver)
}

func buildFeishuWebhookPath(secretPath string) string {
	return feishusvc.BuildWebhookPath(secretPath)
}

func normalizeFeishuWebhookSecretPath(raw string) string {
	return feishusvc.NormalizeWebhookSecretPath(raw)
}

func writeGatewayJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
