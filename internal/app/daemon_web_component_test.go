package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestDaemonWebComponent_StartAndServeShell(t *testing.T) {
	home, err := OpenHome(t.TempDir())
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	home.Config.Daemon.Web.Listen = "127.0.0.1:0"

	comp := newDaemonWebComponent(home, nil)
	if err := comp.Start(context.Background()); err != nil {
		t.Fatalf("component start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = comp.Stop(context.Background())
	})

	resp, err := http.Get("http://" + comp.listener.Addr().String() + "/")
	if err != nil {
		t.Fatalf("http get failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected web root 200, got=%d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body failed: %v", err)
	}
	if !strings.Contains(string(body), "Dalek Web Console") {
		t.Fatalf("unexpected web shell response body")
	}
}

func TestDaemonWebComponent_MissingAssetReturnsNotFound(t *testing.T) {
	home, err := OpenHome(t.TempDir())
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	home.Config.Daemon.Web.Listen = "127.0.0.1:0"

	comp := newDaemonWebComponent(home, nil)
	if err := comp.Start(context.Background()); err != nil {
		t.Fatalf("component start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = comp.Stop(context.Background())
	})

	resp, err := http.Get("http://" + comp.listener.Addr().String() + "/missing.js")
	if err != nil {
		t.Fatalf("http get failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for missing static file, got=%d", resp.StatusCode)
	}
}
