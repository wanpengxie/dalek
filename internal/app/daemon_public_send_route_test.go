package app

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
)

func TestDaemonPublicGatewayComponent_NonWebhookRoutesNotExposed(t *testing.T) {
	homeDir := t.TempDir()
	cfg := DefaultHomeConfig()
	cfg.Daemon.Public.Listen = "127.0.0.1:0"
	cfg.Daemon.Public.Ingress.Enabled = false
	cfg.Daemon.Public.Feishu.Enabled = false
	cfg.Gateway.Feishu = HomeGatewayFeishuConfig{}
	if err := WriteHomeConfigAtomic(filepath.Join(homeDir, "config.json"), cfg); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}

	home, err := OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	comp := newDaemonPublicGatewayComponent(home, nil)
	if err := comp.Start(context.Background()); err != nil {
		t.Fatalf("component start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = comp.Stop(context.Background())
	})

	baseURL := "http://" + comp.listener.Addr().String()
	paths := []string{
		"/api/send",
		"/api/tickets/start",
		"/api/dispatch/submit",
		"/api/worker-run/submit",
		"/api/notes",
		"/api/runs/1",
		"/ws",
		"/health",
		"/random-not-found",
	}
	for _, p := range paths {
		req, err := http.NewRequest(http.MethodGet, baseURL+p, nil)
		if err != nil {
			t.Fatalf("new request failed: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("http do failed for path %s: %v", p, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("public path should return 404, path=%s got=%d", p, resp.StatusCode)
		}
	}
}
