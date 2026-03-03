package main

import (
	"testing"

	"dalek/internal/app"
)

func TestResolveFeishuClientConfig(t *testing.T) {
	got := resolveFeishuClientConfig(app.HomeConfig{
		Daemon: app.HomeDaemonConfig{
			Public: app.HomeDaemonPublicConfig{
				Feishu: app.HomeDaemonPublicFeishuConfig{
					AppID:     " app-id ",
					AppSecret: " app-secret ",
					BaseURL:   " https://open.feishu.cn/ ",
				},
			},
		},
	})

	if got.AppID != "app-id" {
		t.Fatalf("unexpected app id: %q", got.AppID)
	}
	if got.AppSecret != "app-secret" {
		t.Fatalf("unexpected app secret: %q", got.AppSecret)
	}
	if got.BaseURL != "https://open.feishu.cn" {
		t.Fatalf("unexpected base url: %q", got.BaseURL)
	}
}

func TestResolveFeishuClientConfig_DefaultBaseURL(t *testing.T) {
	got := resolveFeishuClientConfig(app.HomeConfig{})
	if got.BaseURL != "https://open.feishu.cn" {
		t.Fatalf("default base url mismatch: got=%q", got.BaseURL)
	}
}
