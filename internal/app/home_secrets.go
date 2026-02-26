package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	feishusvc "dalek/internal/services/channel/feishu"
)

// EnsureHomeSecrets 确保 daemon/public 所需的密钥配置存在并持久化。
func EnsureHomeSecrets(h *Home) error {
	if h == nil {
		return fmt.Errorf("home 为空")
	}

	cfg := h.Config.WithDefaults()
	changed := false

	normalizedPath := NormalizeWebhookSecretPath(cfg.Daemon.Public.Feishu.WebhookSecretPath)
	if normalizedPath == "" {
		secretPath, err := GenerateWebhookSecretPath()
		if err != nil {
			return err
		}
		normalizedPath = secretPath
		changed = true
	}
	if normalizedPath != strings.TrimSpace(cfg.Daemon.Public.Feishu.WebhookSecretPath) {
		changed = true
	}
	cfg.Daemon.Public.Feishu.WebhookSecretPath = normalizedPath

	if !changed {
		h.Config = cfg
		return nil
	}
	if err := WriteHomeConfigAtomic(h.ConfigPath, cfg); err != nil {
		return err
	}
	h.Config = cfg
	return nil
}

func GenerateSecretToken(size int) (string, error) {
	if size <= 0 {
		size = 24
	}
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func GenerateWebhookSecretPath() (string, error) {
	seg, err := GenerateSecretToken(18)
	if err != nil {
		return "", err
	}
	return "hook_" + seg, nil
}

func NormalizeWebhookSecretPath(raw string) string {
	return feishusvc.NormalizeWebhookSecretPath(raw)
}
