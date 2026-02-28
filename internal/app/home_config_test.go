package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadHomeConfig_MissingUsesDefaults(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	cfg, exists, needsRewrite, err := LoadHomeConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadHomeConfig failed: %v", err)
	}
	if exists {
		t.Fatalf("config should not exist")
	}
	if needsRewrite {
		t.Fatalf("missing config should not need rewrite")
	}
	if cfg.SchemaVersion != CurrentHomeConfigSchemaVersion {
		t.Fatalf("unexpected schema version: %d", cfg.SchemaVersion)
	}
	if cfg.Gateway.Listen != "127.0.0.1:18080" {
		t.Fatalf("unexpected default gateway.listen: %q", cfg.Gateway.Listen)
	}
	if cfg.Gateway.InternalListen != "127.0.0.1:18081" {
		t.Fatalf("unexpected default gateway.internal_listen: %q", cfg.Gateway.InternalListen)
	}
	if len(cfg.Gateway.InternalAllowCIDRs) != 2 ||
		cfg.Gateway.InternalAllowCIDRs[0] != "127.0.0.1/32" ||
		cfg.Gateway.InternalAllowCIDRs[1] != "::1/128" {
		t.Fatalf("unexpected default gateway.internal_allow_cidrs: %#v", cfg.Gateway.InternalAllowCIDRs)
	}
	if cfg.Gateway.QueueDepth != 32 {
		t.Fatalf("unexpected default gateway.queue_depth: %d", cfg.Gateway.QueueDepth)
	}
	if cfg.Gateway.AuthToken != "" {
		t.Fatalf("unexpected default gateway.auth_token: %q", cfg.Gateway.AuthToken)
	}
	if cfg.Gateway.Feishu.WebhookSecretPath != "" {
		t.Fatalf("unexpected default gateway.feishu.webhook_secret_path: %q", cfg.Gateway.Feishu.WebhookSecretPath)
	}
	if cfg.Gateway.Feishu.BaseURL != "https://open.feishu.cn" {
		t.Fatalf("unexpected default gateway.feishu.base_url: %q", cfg.Gateway.Feishu.BaseURL)
	}
	if cfg.Gateway.Tunnel.Name != "" {
		t.Fatalf("unexpected default gateway.tunnel.name: %q", cfg.Gateway.Tunnel.Name)
	}
	if cfg.Gateway.Tunnel.Hostname != "" {
		t.Fatalf("unexpected default gateway.tunnel.hostname: %q", cfg.Gateway.Tunnel.Hostname)
	}
	if cfg.Gateway.Tunnel.CloudflaredBin != "" {
		t.Fatalf("unexpected default gateway.tunnel.cloudflared_bin: %q", cfg.Gateway.Tunnel.CloudflaredBin)
	}
	if cfg.Agent.Provider != "" {
		t.Fatalf("unexpected default agent.provider: %q", cfg.Agent.Provider)
	}
	if cfg.Agent.Model != "" {
		t.Fatalf("unexpected default agent.model: %q", cfg.Agent.Model)
	}
	if cfg.Daemon.PIDFile != "daemon.pid" {
		t.Fatalf("unexpected default daemon.pid_file: %q", cfg.Daemon.PIDFile)
	}
	if cfg.Daemon.LockFile != "daemon.lock" {
		t.Fatalf("unexpected default daemon.lock_file: %q", cfg.Daemon.LockFile)
	}
	if cfg.Daemon.LogFile != "daemon.log" {
		t.Fatalf("unexpected default daemon.log_file: %q", cfg.Daemon.LogFile)
	}
	if cfg.Daemon.MaxConcurrent != defaultDaemonMaxConcurrent {
		t.Fatalf("unexpected default daemon.max_concurrent: %d", cfg.Daemon.MaxConcurrent)
	}
	if cfg.Daemon.Internal.Listen != "127.0.0.1:18081" {
		t.Fatalf("unexpected default daemon.internal.listen: %q", cfg.Daemon.Internal.Listen)
	}
	if cfg.Daemon.Public.Listen != "127.0.0.1:18080" {
		t.Fatalf("unexpected default daemon.public.listen: %q", cfg.Daemon.Public.Listen)
	}
	if !cfg.Daemon.Public.Feishu.Enabled {
		t.Fatalf("unexpected default daemon.public.feishu.enabled: false")
	}
	if cfg.Daemon.Public.Feishu.BaseURL != "https://open.feishu.cn" {
		t.Fatalf("unexpected default daemon.public.feishu.base_url: %q", cfg.Daemon.Public.Feishu.BaseURL)
	}
	if cfg.Daemon.Public.Feishu.UseSystemProxy {
		t.Fatalf("unexpected default daemon.public.feishu.use_system_proxy: true")
	}
	if cfg.Daemon.Public.Ingress.Provider != "cloudflare_tunnel" {
		t.Fatalf("unexpected default daemon.public.ingress.provider: %q", cfg.Daemon.Public.Ingress.Provider)
	}
	if !cfg.Daemon.Public.Ingress.Enabled {
		t.Fatalf("unexpected default daemon.public.ingress.enabled: false")
	}
	if cfg.Daemon.Public.Ingress.TunnelName != "dalek" {
		t.Fatalf("unexpected default daemon.public.ingress.tunnel_name: %q", cfg.Daemon.Public.Ingress.TunnelName)
	}
	if cfg.Daemon.Public.Ingress.CloudflaredBin != "cloudflared" {
		t.Fatalf("unexpected default daemon.public.ingress.cloudflared_bin: %q", cfg.Daemon.Public.Ingress.CloudflaredBin)
	}
	if cfg.Daemon.Notebook.WorkerCount != defaultNotebookWorkerCount {
		t.Fatalf("unexpected default daemon.notebook.worker_count: %d", cfg.Daemon.Notebook.WorkerCount)
	}
}

func TestLoadHomeConfig_ExistingConfigWithDefaults(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	raw := `{
  "gateway": {
    "listen": "0.0.0.0:19090",
    "auth_token": "  test-auth-token  ",
    "feishu": {
      "app_id": "app-id",
      "app_secret": "app-secret",
      "verification_token": "verify-token",
      "webhook_secret_path": "  hook-secret  ",
      "base_url": "https://feishu.example.com/"
    },
    "tunnel": {
      "name": "  gw-prod  ",
      "hostname": "  gw.example.com  ",
      "cloudflared_bin": "  /usr/local/bin/cloudflared  "
    }
  },
  "agent": {
    "provider": "  CLAUDE  ",
    "model": " claude-3-7-sonnet "
  },
  "daemon": {
    "pid_file": " runtime/daemon.pid ",
    "lock_file": " runtime/daemon.lock ",
    "log_file": " runtime/daemon.log ",
    "internal": {
      "listen": " 0.0.0.0:18081 "
    },
    "public": {
      "listen": " 0.0.0.0:18080 ",
      "feishu": {
        "use_system_proxy": true
      }
    }
  }
}
`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfg, exists, needsRewrite, err := LoadHomeConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadHomeConfig failed: %v", err)
	}
	if !exists {
		t.Fatalf("config should exist")
	}
	if !needsRewrite {
		t.Fatalf("config without schema_version should need rewrite")
	}
	if cfg.SchemaVersion != CurrentHomeConfigSchemaVersion {
		t.Fatalf("unexpected schema version: %d", cfg.SchemaVersion)
	}
	if cfg.Gateway.Listen != "0.0.0.0:19090" {
		t.Fatalf("unexpected gateway.listen: %q", cfg.Gateway.Listen)
	}
	if cfg.Gateway.InternalListen != "127.0.0.1:18081" {
		t.Fatalf("gateway.internal_listen should fallback to default: got=%q", cfg.Gateway.InternalListen)
	}
	if len(cfg.Gateway.InternalAllowCIDRs) != 2 ||
		cfg.Gateway.InternalAllowCIDRs[0] != "127.0.0.1/32" ||
		cfg.Gateway.InternalAllowCIDRs[1] != "::1/128" {
		t.Fatalf("gateway.internal_allow_cidrs should fallback to defaults: %#v", cfg.Gateway.InternalAllowCIDRs)
	}
	if cfg.Gateway.QueueDepth != 32 {
		t.Fatalf("gateway.queue_depth should fallback to default: got=%d", cfg.Gateway.QueueDepth)
	}
	if cfg.Gateway.AuthToken != "test-auth-token" {
		t.Fatalf("gateway.auth_token should be normalized: got=%q", cfg.Gateway.AuthToken)
	}
	if cfg.Gateway.Feishu.WebhookSecretPath != "hook-secret" {
		t.Fatalf("gateway.feishu.webhook_secret_path should be normalized: got=%q", cfg.Gateway.Feishu.WebhookSecretPath)
	}
	if cfg.Gateway.Feishu.BaseURL != "https://feishu.example.com" {
		t.Fatalf("gateway.feishu.base_url should be normalized: got=%q", cfg.Gateway.Feishu.BaseURL)
	}
	if cfg.Gateway.Tunnel.Name != "gw-prod" {
		t.Fatalf("gateway.tunnel.name should be normalized: got=%q", cfg.Gateway.Tunnel.Name)
	}
	if cfg.Gateway.Tunnel.Hostname != "gw.example.com" {
		t.Fatalf("gateway.tunnel.hostname should be normalized: got=%q", cfg.Gateway.Tunnel.Hostname)
	}
	if cfg.Gateway.Tunnel.CloudflaredBin != "/usr/local/bin/cloudflared" {
		t.Fatalf("gateway.tunnel.cloudflared_bin should be normalized: got=%q", cfg.Gateway.Tunnel.CloudflaredBin)
	}
	if cfg.Agent.Provider != "claude" {
		t.Fatalf("agent.provider should be normalized: got=%q", cfg.Agent.Provider)
	}
	if cfg.Agent.Model != "claude-3-7-sonnet" {
		t.Fatalf("agent.model should be normalized: got=%q", cfg.Agent.Model)
	}
	if cfg.Daemon.PIDFile != "runtime/daemon.pid" {
		t.Fatalf("daemon.pid_file should be normalized: got=%q", cfg.Daemon.PIDFile)
	}
	if cfg.Daemon.LockFile != "runtime/daemon.lock" {
		t.Fatalf("daemon.lock_file should be normalized: got=%q", cfg.Daemon.LockFile)
	}
	if cfg.Daemon.LogFile != "runtime/daemon.log" {
		t.Fatalf("daemon.log_file should be normalized: got=%q", cfg.Daemon.LogFile)
	}
	if cfg.Daemon.MaxConcurrent != defaultDaemonMaxConcurrent {
		t.Fatalf("daemon.max_concurrent should fallback to default: got=%d", cfg.Daemon.MaxConcurrent)
	}
	if cfg.Daemon.Internal.Listen != "0.0.0.0:18081" {
		t.Fatalf("daemon.internal.listen should be normalized: got=%q", cfg.Daemon.Internal.Listen)
	}
	if cfg.Daemon.Public.Listen != "0.0.0.0:18080" {
		t.Fatalf("daemon.public.listen should be normalized: got=%q", cfg.Daemon.Public.Listen)
	}
	if cfg.Daemon.Public.Feishu.Enabled {
		t.Fatalf("daemon.public.feishu.enabled should keep explicit false")
	}
	if cfg.Daemon.Public.Feishu.AppID != "" {
		t.Fatalf("daemon.public.feishu.app_id should not be auto-filled: got=%q", cfg.Daemon.Public.Feishu.AppID)
	}
	if cfg.Daemon.Public.Feishu.AppSecret != "" {
		t.Fatalf("daemon.public.feishu.app_secret should not be auto-filled: got=%q", cfg.Daemon.Public.Feishu.AppSecret)
	}
	if cfg.Daemon.Public.Feishu.VerificationToken != "" {
		t.Fatalf("daemon.public.feishu.verification_token should not be auto-filled: got=%q", cfg.Daemon.Public.Feishu.VerificationToken)
	}
	if cfg.Daemon.Public.Feishu.WebhookSecretPath != "" {
		t.Fatalf("daemon.public.feishu.webhook_secret_path should not be auto-filled: got=%q", cfg.Daemon.Public.Feishu.WebhookSecretPath)
	}
	if cfg.Daemon.Public.Feishu.BaseURL != "https://open.feishu.cn" {
		t.Fatalf("daemon.public.feishu.base_url should fallback to default: got=%q", cfg.Daemon.Public.Feishu.BaseURL)
	}
	if !cfg.Daemon.Public.Feishu.UseSystemProxy {
		t.Fatalf("daemon.public.feishu.use_system_proxy should keep explicit value")
	}
	if cfg.Daemon.Public.Ingress.Provider != "cloudflare_tunnel" {
		t.Fatalf("daemon.public.ingress.provider should fallback to default: got=%q", cfg.Daemon.Public.Ingress.Provider)
	}
	if cfg.Daemon.Public.Ingress.Enabled {
		t.Fatalf("daemon.public.ingress.enabled should keep explicit false")
	}
	if cfg.Daemon.Public.Ingress.TunnelName != "" {
		t.Fatalf("daemon.public.ingress.tunnel_name should not be auto-filled: got=%q", cfg.Daemon.Public.Ingress.TunnelName)
	}
	if cfg.Daemon.Public.Ingress.Hostname != "" {
		t.Fatalf("daemon.public.ingress.hostname should not be auto-filled: got=%q", cfg.Daemon.Public.Ingress.Hostname)
	}
	if cfg.Daemon.Public.Ingress.CloudflaredBin != "cloudflared" {
		t.Fatalf("daemon.public.ingress.cloudflared_bin should fallback to default: got=%q", cfg.Daemon.Public.Ingress.CloudflaredBin)
	}
	if cfg.Daemon.Notebook.WorkerCount != defaultNotebookWorkerCount {
		t.Fatalf("daemon.notebook.worker_count should fallback to default: got=%d", cfg.Daemon.Notebook.WorkerCount)
	}
}

func TestLoadHomeConfig_DaemonNotebookWorkerCountFromJSON(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	raw := `{
  "schema_version": 2,
  "daemon": {
    "notebook": {
      "worker_count": 4
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfg, exists, needsRewrite, err := LoadHomeConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadHomeConfig failed: %v", err)
	}
	if !exists {
		t.Fatalf("config should exist")
	}
	if needsRewrite {
		t.Fatalf("schema_version=2 should not need rewrite")
	}
	if cfg.Daemon.Notebook.WorkerCount != 4 {
		t.Fatalf("daemon.notebook.worker_count should keep explicit value: got=%d", cfg.Daemon.Notebook.WorkerCount)
	}
}

func TestLoadHomeConfig_RejectsRemovedDaemonInternalFields(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	raw := `{
  "schema_version": 2,
  "daemon": {
    "internal": {
      "listen": "127.0.0.1:18081",
      "allow_cidrs": ["127.0.0.1/32"]
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	_, _, _, err := LoadHomeConfig(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "daemon.internal.allow_cidrs 已移除") {
		t.Fatalf("expected removed allow_cidrs error, got=%v", err)
	}

	raw = `{
  "schema_version": 2,
  "daemon": {
    "internal": {
      "listen": "127.0.0.1:18081",
      "auth_token_env": "DALEK_DAEMON_INTERNAL_TOKEN"
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	_, _, _, err = LoadHomeConfig(cfgPath)
	if err == nil || !strings.Contains(err.Error(), "daemon.internal.auth_token_env 已移除") {
		t.Fatalf("expected removed auth_token_env error, got=%v", err)
	}
}

func TestHomeConfigWithDefaults_DaemonNotebookWorkerCountFallback(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "zero fallback", in: 0, want: defaultNotebookWorkerCount},
		{name: "negative fallback", in: -3, want: defaultNotebookWorkerCount},
		{name: "positive keep", in: 3, want: 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := HomeConfig{
				Daemon: HomeDaemonConfig{
					Notebook: HomeDaemonNotebookConfig{
						WorkerCount: tt.in,
					},
				},
			}
			got := cfg.WithDefaults().Daemon.Notebook.WorkerCount
			if got != tt.want {
				t.Fatalf("worker_count mismatch: got=%d want=%d", got, tt.want)
			}
		})
	}
}

func TestHomeConfigWithDefaults_DaemonMaxConcurrentFallback(t *testing.T) {
	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "zero fallback", in: 0, want: defaultDaemonMaxConcurrent},
		{name: "negative fallback", in: -7, want: defaultDaemonMaxConcurrent},
		{name: "positive keep", in: 9, want: 9},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := HomeConfig{
				Daemon: HomeDaemonConfig{
					MaxConcurrent: tt.in,
				},
			}
			got := cfg.WithDefaults().Daemon.MaxConcurrent
			if got != tt.want {
				t.Fatalf("max_concurrent mismatch: got=%d want=%d", got, tt.want)
			}
		})
	}
}

func TestHomeConfigWithDefaults_DoesNotReenableDaemonFeishuWithoutOldValues(t *testing.T) {
	cfg := DefaultHomeConfig()
	cfg.Daemon.Public.Feishu.Enabled = false

	got := cfg.WithDefaults()
	if got.Daemon.Public.Feishu.Enabled {
		t.Fatalf("daemon.public.feishu.enabled should stay false when no old feishu config exists")
	}
}

func TestLoadHomeConfig_InvalidAgentProviderResetsToEmpty(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	raw := `{
  "schema_version": 2,
  "agent": {
    "provider": "unsupported",
    "model": "foo"
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	cfg, exists, needsRewrite, err := LoadHomeConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadHomeConfig failed: %v", err)
	}
	if !exists {
		t.Fatalf("config should exist")
	}
	if needsRewrite {
		t.Fatalf("schema_version=2 should not need rewrite")
	}
	if cfg.Agent.Provider != "" {
		t.Fatalf("invalid agent.provider should reset to empty, got=%q", cfg.Agent.Provider)
	}
	if cfg.Agent.Model != "foo" {
		t.Fatalf("agent.model should keep raw value, got=%q", cfg.Agent.Model)
	}
}

func TestLoadHomeConfig_SchemaV1LogsDeprecation(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, "config.json")
	raw := `{
  "schema_version": 1,
  "gateway": {
    "feishu": {
      "app_id": "old-app"
    }
  }
}`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	var deprecationMsg string
	origWarnf := homeConfigDeprecationWarnf
	homeConfigDeprecationWarnf = func(format string, args ...any) {
		deprecationMsg = fmt.Sprintf(format, args...)
	}
	defer func() { homeConfigDeprecationWarnf = origWarnf }()

	cfg, exists, needsRewrite, err := LoadHomeConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadHomeConfig failed: %v", err)
	}
	if !exists {
		t.Fatalf("config should exist")
	}
	if !needsRewrite {
		t.Fatalf("schema_version=1 should need rewrite")
	}
	if cfg.SchemaVersion != CurrentHomeConfigSchemaVersion {
		t.Fatalf("unexpected schema version: got=%d want=%d", cfg.SchemaVersion, CurrentHomeConfigSchemaVersion)
	}
	if !strings.Contains(deprecationMsg, "已废弃") {
		t.Fatalf("expected deprecation warning, got=%q", deprecationMsg)
	}
}

func TestOpenHome_UsesConfiguredGatewayDBPath(t *testing.T) {
	root := t.TempDir()
	if err := WriteHomeConfigAtomic(filepath.Join(root, "config.json"), HomeConfig{
		Gateway: HomeGatewayConfig{
			DBPath: "data/gateway-custom.db",
		},
	}); err != nil {
		t.Fatalf("WriteHomeConfigAtomic failed: %v", err)
	}

	h, err := OpenHome(root)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}

	want := filepath.Join(root, "data", "gateway-custom.db")
	if h.GatewayDBPath != want {
		t.Fatalf("unexpected GatewayDBPath: got=%q want=%q", h.GatewayDBPath, want)
	}
}
