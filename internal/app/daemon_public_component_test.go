package app

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonPublicGatewayComponent_DefaultConfigFallsBackToLocalOnly(t *testing.T) {
	home, err := OpenHome(t.TempDir())
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	home.Config.Daemon.Public.Listen = "127.0.0.1:0"
	comp := newDaemonPublicGatewayComponent(home, nil)
	if comp.feishuEnabled {
		t.Fatalf("default config should disable feishu runtime when credentials are missing")
	}
	if comp.tunnelEnabled {
		t.Fatalf("default config should disable tunnel runtime when ingress config is incomplete")
	}
	if strings.TrimSpace(comp.feishuDisabled) == "" {
		t.Fatalf("expected feishu disable reason")
	}
	if strings.TrimSpace(comp.tunnelDisabled) == "" {
		t.Fatalf("expected tunnel disable reason")
	}
	if err := comp.Start(context.Background()); err != nil {
		t.Fatalf("component start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = comp.Stop(context.Background())
	})
	if comp.tunnelSupervisor != nil {
		t.Fatalf("local-only fallback should not start tunnel supervisor")
	}

	resp, err := http.Get("http://" + comp.listener.Addr().String() + comp.webhookPath)
	if err != nil {
		t.Fatalf("http get failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected disabled feishu webhook to return 404, got=%d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if got := payload["error"]; got != "feishu_disabled" {
		t.Fatalf("unexpected error payload: %#v", payload)
	}
}

func TestDaemonPublicGatewayComponent_InvalidTunnelConfigDoesNotBlockStart(t *testing.T) {
	homeDir := t.TempDir()
	cfg := DefaultHomeConfig()
	cfg.Daemon.Public.Listen = "127.0.0.1:0"
	cfg.Daemon.Public.Feishu.AppID = "app-id"
	cfg.Daemon.Public.Feishu.AppSecret = "app-secret"
	cfg.Daemon.Public.Feishu.VerificationToken = "verify-token"
	cfg.Daemon.Public.Feishu.WebhookSecretPath = "hook-test"
	cfg.Daemon.Public.Ingress.Enabled = true
	cfg.Daemon.Public.Ingress.TunnelName = "gw-prod"
	cfg.Daemon.Public.Ingress.Hostname = ""
	if err := WriteHomeConfigAtomic(filepath.Join(homeDir, "config.json"), cfg); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}

	home, err := OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	comp := newDaemonPublicGatewayComponent(home, nil)
	if !comp.feishuEnabled {
		t.Fatalf("feishu should stay enabled when credentials are complete")
	}
	if comp.tunnelEnabled {
		t.Fatalf("invalid tunnel config should be downgraded before start")
	}
	if strings.TrimSpace(comp.tunnelDisabled) == "" {
		t.Fatalf("expected tunnel disable reason")
	}
	if err := comp.Start(context.Background()); err != nil {
		t.Fatalf("component start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = comp.Stop(context.Background())
	})
	if comp.tunnelSupervisor != nil {
		t.Fatalf("invalid tunnel config should not start tunnel supervisor")
	}

	req, err := http.NewRequest(http.MethodPost, "http://"+comp.listener.Addr().String()+comp.webhookPath, strings.NewReader(`{"token":"bad"}`))
	if err != nil {
		t.Fatalf("new request failed: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("http do failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected enabled feishu webhook to enforce token validation, got=%d", resp.StatusCode)
	}
}
