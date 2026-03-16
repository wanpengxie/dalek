package app

import "testing"

func TestEnsureHomeSecrets_GenerateAndPersist(t *testing.T) {
	homeDir := t.TempDir()
	h, err := OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}

	if got := h.Config.Gateway.AuthToken; got != "" {
		t.Fatalf("precondition failed: auth token should be empty, got=%q", got)
	}
	if got := h.Config.Daemon.Internal.NodeAgentToken; got != "" {
		t.Fatalf("precondition failed: node agent token should be empty, got=%q", got)
	}
	if got := h.Config.Daemon.Public.Feishu.WebhookSecretPath; got != "" {
		t.Fatalf("precondition failed: webhook secret path should be empty, got=%q", got)
	}

	if err := EnsureHomeSecrets(h); err != nil {
		t.Fatalf("EnsureHomeSecrets failed: %v", err)
	}
	if h.Config.Gateway.AuthToken != "" {
		t.Fatalf("gateway.auth_token should stay empty, got=%q", h.Config.Gateway.AuthToken)
	}
	if h.Config.Daemon.Internal.NodeAgentToken == "" {
		t.Fatalf("daemon.internal.node_agent_token should be generated")
	}
	if h.Config.Daemon.Public.Feishu.WebhookSecretPath == "" {
		t.Fatalf("webhook secret path should be generated")
	}

	firstNodeAgentToken := h.Config.Daemon.Internal.NodeAgentToken
	firstSecretPath := h.Config.Daemon.Public.Feishu.WebhookSecretPath
	if err := EnsureHomeSecrets(h); err != nil {
		t.Fatalf("EnsureHomeSecrets second call failed: %v", err)
	}
	if h.Config.Gateway.AuthToken != "" {
		t.Fatalf("gateway.auth_token should stay empty after second ensure, got=%q", h.Config.Gateway.AuthToken)
	}
	if h.Config.Daemon.Internal.NodeAgentToken != firstNodeAgentToken {
		t.Fatalf("node agent token should stay stable after second ensure")
	}
	if h.Config.Daemon.Public.Feishu.WebhookSecretPath != firstSecretPath {
		t.Fatalf("webhook secret path should stay stable after second ensure")
	}

	reopened, err := OpenHome(homeDir)
	if err != nil {
		t.Fatalf("reopen home failed: %v", err)
	}
	if reopened.Config.Gateway.AuthToken != "" {
		t.Fatalf("gateway.auth_token should remain empty after reopen, got=%q", reopened.Config.Gateway.AuthToken)
	}
	if reopened.Config.Daemon.Internal.NodeAgentToken != firstNodeAgentToken {
		t.Fatalf("node agent token not persisted: got=%q want=%q", reopened.Config.Daemon.Internal.NodeAgentToken, firstNodeAgentToken)
	}
	if reopened.Config.Daemon.Public.Feishu.WebhookSecretPath != firstSecretPath {
		t.Fatalf("webhook secret path not persisted: got=%q want=%q", reopened.Config.Daemon.Public.Feishu.WebhookSecretPath, firstSecretPath)
	}
}

func TestNormalizeWebhookSecretPath(t *testing.T) {
	got := NormalizeWebhookSecretPath(" /feishu/webhook/abc-123_/ ")
	if got != "abc-123_" {
		t.Fatalf("unexpected normalized secret path: %q", got)
	}
}
