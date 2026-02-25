package main

import (
	"net/url"
	"path/filepath"
	"testing"

	"dalek/internal/app"
)

func TestResolveGatewayDaemonWSURL_HomeConfigFallback(t *testing.T) {
	t.Setenv("DALEK_GATEWAY_WS_URL", "")
	home := t.TempDir()
	if err := app.WriteHomeConfigAtomic(filepath.Join(home, "config.json"), app.HomeConfig{
		Daemon: app.HomeDaemonConfig{
			Internal: app.HomeDaemonInternalConfig{
				Listen: "127.0.0.1:29091",
			},
		},
	}); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}

	got := resolveGatewayDaemonWSURL("", home)
	if got != "ws://127.0.0.1:29091/ws" {
		t.Fatalf("unexpected ws url from home config: %q", got)
	}
}

func TestResolveGatewayDaemonWSURL_EnvOverridesHomeConfig(t *testing.T) {
	t.Setenv("DALEK_GATEWAY_WS_URL", "ws://127.0.0.1:39090/ws")
	home := t.TempDir()
	if err := app.WriteHomeConfigAtomic(filepath.Join(home, "config.json"), app.HomeConfig{
		Daemon: app.HomeDaemonConfig{
			Internal: app.HomeDaemonInternalConfig{
				Listen: "127.0.0.1:29091",
			},
		},
	}); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}

	got := resolveGatewayDaemonWSURL("", home)
	if got != "ws://127.0.0.1:39090/ws" {
		t.Fatalf("env ws url should override home config: %q", got)
	}
}

func TestResolveGatewayDaemonWSURL_CLIOverridesAll(t *testing.T) {
	t.Setenv("DALEK_GATEWAY_WS_URL", "ws://127.0.0.1:39090/ws")
	home := t.TempDir()
	if err := app.WriteHomeConfigAtomic(filepath.Join(home, "config.json"), app.HomeConfig{
		Daemon: app.HomeDaemonConfig{
			Internal: app.HomeDaemonInternalConfig{
				Listen: "127.0.0.1:29091",
			},
		},
	}); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}

	got := resolveGatewayDaemonWSURL("ws://127.0.0.1:49090/ws", home)
	if got != "ws://127.0.0.1:49090/ws" {
		t.Fatalf("cli ws url should override env/home config: %q", got)
	}
}

func TestResolveGatewayDaemonWSURL_HomeListenHTTPS(t *testing.T) {
	t.Setenv("DALEK_GATEWAY_WS_URL", "")
	home := t.TempDir()
	if err := app.WriteHomeConfigAtomic(filepath.Join(home, "config.json"), app.HomeConfig{
		Daemon: app.HomeDaemonConfig{
			Internal: app.HomeDaemonInternalConfig{
				Listen: "https://gateway.example.com",
			},
		},
	}); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}

	got := resolveGatewayDaemonWSURL("", home)
	if got != "wss://gateway.example.com/ws" {
		t.Fatalf("home https listen should convert to wss ws endpoint: %q", got)
	}
}

func TestBuildGatewayChatWSURL_PreservesBaseQuery(t *testing.T) {
	got, err := buildGatewayChatWSURL("ws://127.0.0.1:18081/ws?trace=1", "demo", "conv1", "sender1")
	if err != nil {
		t.Fatalf("buildGatewayChatWSURL failed: %v", err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url parse failed: %v", err)
	}
	q := u.Query()
	if q.Get("trace") != "1" {
		t.Fatalf("base ws url query should be preserved, got=%q", q.Get("trace"))
	}
}
