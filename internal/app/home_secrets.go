package app

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
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
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	s = strings.TrimPrefix(s, "/")
	s = strings.TrimPrefix(s, "feishu/webhook/")
	s = strings.TrimPrefix(s, "/feishu/webhook/")
	s = strings.Trim(s, "/")
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, ch := range s {
		if ch >= 'a' && ch <= 'z' ||
			ch >= 'A' && ch <= 'Z' ||
			ch >= '0' && ch <= '9' ||
			ch == '-' || ch == '_' {
			b.WriteRune(ch)
		}
	}
	return strings.TrimSpace(b.String())
}
