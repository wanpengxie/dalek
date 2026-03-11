package main

import (
	"net/url"
	"strings"
	"testing"

	"dalek/internal/app"
)

func prepareDemoProjectWithOneTicket(t *testing.T, bin, repo, home string) {
	t.Helper()
	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")
	out, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"ticket", "create",
		"-title", "dispatch acceptance",
		"-desc", "dispatch acceptance",
	)
	if strings.TrimSpace(out) != "1" {
		t.Fatalf("expected ticket id 1, got=%q", out)
	}
}

func configureDaemonInternalListenForE2E(t *testing.T, homeDir, daemonURL string) {
	t.Helper()
	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	u, err := url.Parse(strings.TrimSpace(daemonURL))
	if err != nil {
		t.Fatalf("parse daemon url failed: %v", err)
	}
	cfg := h.Config.WithDefaults()
	cfg.Daemon.Internal.Listen = strings.TrimSpace(u.Host)
	if err := app.WriteHomeConfigAtomic(h.ConfigPath, cfg); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}
}
