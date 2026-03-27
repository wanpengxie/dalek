package app

import (
	"dalek/internal/repo"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultGatewayListenAddr         = "127.0.0.1:18080"
	defaultGatewayInternalListenAddr = "127.0.0.1:18081"
	defaultGatewayQueueDepth         = 32
	defaultFeishuBaseURL             = "https://open.feishu.cn"
	defaultDaemonPIDFile             = "daemon.pid"
	defaultDaemonLockFile            = "daemon.lock"
	defaultDaemonLogFile             = "daemon.log"

	defaultDaemonInternalListenAddr    = "127.0.0.1:18081"
	defaultDaemonPublicListenAddr      = "127.0.0.1:18080"
	defaultDaemonWebListenAddr         = "127.0.0.1:18082"
	defaultDaemonMaxConcurrent         = 4
	defaultDaemonPublicIngressProvider = "cloudflare_tunnel"
	defaultDaemonPublicIngressName     = "dalek"
	defaultDaemonCloudflaredBin        = "cloudflared"
)

var defaultGatewayInternalAllowCIDRs = []string{
	"127.0.0.1/32",
	"::1/128",
}

// CurrentHomeConfigSchemaVersion 是 Home 全局配置版本号（非 binary semver）。
// v3: 新增 providers map（全局 provider 定义）。
const CurrentHomeConfigSchemaVersion = 3

var homeConfigDeprecationWarnf = func(format string, args ...any) {
	log.Printf(format, args...)
}

type HomeConfig struct {
	SchemaVersion int               `json:"schema_version"`
	Gateway       HomeGatewayConfig `json:"gateway"`
	Agent         HomeAgentConfig   `json:"agent"`
	Daemon        HomeDaemonConfig  `json:"daemon"`

	// v3 新增：全局 providers map + 角色默认
	Providers    map[string]repo.ProviderConfig `json:"providers,omitempty"`
	WorkerAgent  repo.RoleConfig                `json:"worker_agent,omitempty"`
	PMAgent      repo.RoleConfig                `json:"pm_agent,omitempty"`
	GatewayAgent repo.GatewayRoleConfig         `json:"gateway_agent,omitempty"`
}

type HomeAgentConfig struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

type HomeGatewayConfig struct {
	Listen             string   `json:"listen"`
	InternalListen     string   `json:"internal_listen,omitempty"`
	InternalAllowCIDRs []string `json:"internal_allow_cidrs,omitempty"`
	QueueDepth         int      `json:"queue_depth"`
	DBPath             string   `json:"db_path"`
	// AuthToken 为保留字段，不用于 daemon internal。
	AuthToken string `json:"auth_token"`
	// Feishu 为保留字段，runtime 以 daemon.public.feishu 为准。
	Feishu HomeGatewayFeishuConfig `json:"feishu"`
	// Tunnel 为保留字段，runtime 以 daemon.public.ingress 为准。
	Tunnel HomeGatewayTunnelConfig `json:"tunnel"`
}

type HomeGatewayFeishuConfig struct {
	AppID             string `json:"app_id"`
	AppSecret         string `json:"app_secret"`
	VerificationToken string `json:"verification_token"`
	WebhookSecretPath string `json:"webhook_secret_path"`
	WebhookPath       string `json:"webhook_path,omitempty"`
	BaseURL           string `json:"base_url"`
}

type HomeGatewayTunnelConfig struct {
	Name           string `json:"name"`
	Hostname       string `json:"hostname"`
	CloudflaredBin string `json:"cloudflared_bin"`
}

type HomeDaemonConfig struct {
	PIDFile       string                   `json:"pid_file"`
	LockFile      string                   `json:"lock_file"`
	LogFile       string                   `json:"log_file"`
	MaxConcurrent int                      `json:"max_concurrent,omitempty"`
	Internal      HomeDaemonInternalConfig `json:"internal"`
	Public        HomeDaemonPublicConfig   `json:"public"`
	Web           HomeDaemonWebConfig      `json:"web"`
	Notebook      HomeDaemonNotebookConfig `json:"notebook,omitempty"`
}

type HomeDaemonInternalConfig struct {
	Listen string `json:"listen"`
}

type HomeDaemonWebConfig struct {
	Listen string `json:"listen"`
}

type HomeDaemonPublicConfig struct {
	Listen  string                        `json:"listen"`
	Feishu  HomeDaemonPublicFeishuConfig  `json:"feishu"`
	Ingress HomeDaemonPublicIngressConfig `json:"ingress"`
}

type HomeDaemonPublicFeishuConfig struct {
	Enabled           bool   `json:"enabled"`
	AppID             string `json:"app_id,omitempty"`
	AppSecret         string `json:"app_secret,omitempty"`
	VerificationToken string `json:"verification_token,omitempty"`
	WebhookSecretPath string `json:"webhook_secret_path,omitempty"`
	WebhookPath       string `json:"webhook_path,omitempty"`
	BaseURL           string `json:"base_url,omitempty"`
	UseSystemProxy    bool   `json:"use_system_proxy,omitempty"`
}

type HomeDaemonPublicIngressConfig struct {
	Provider       string `json:"provider"`
	Enabled        bool   `json:"enabled"`
	TunnelName     string `json:"tunnel_name,omitempty"`
	Hostname       string `json:"hostname,omitempty"`
	CloudflaredBin string `json:"cloudflared_bin,omitempty"`
}

type HomeDaemonNotebookConfig struct {
	WorkerCount int `json:"worker_count,omitempty"`
}

func DefaultHomeConfig() HomeConfig {
	return HomeConfig{
		SchemaVersion: CurrentHomeConfigSchemaVersion,
		Gateway: HomeGatewayConfig{
			Listen:             defaultGatewayListenAddr,
			InternalListen:     defaultGatewayInternalListenAddr,
			InternalAllowCIDRs: append([]string(nil), defaultGatewayInternalAllowCIDRs...),
			QueueDepth:         defaultGatewayQueueDepth,
			Feishu: HomeGatewayFeishuConfig{
				BaseURL: defaultFeishuBaseURL,
			},
		},
		Daemon: HomeDaemonConfig{
			PIDFile:       defaultDaemonPIDFile,
			LockFile:      defaultDaemonLockFile,
			LogFile:       defaultDaemonLogFile,
			MaxConcurrent: defaultDaemonMaxConcurrent,
			Internal: HomeDaemonInternalConfig{
				Listen: defaultDaemonInternalListenAddr,
			},
			Public: HomeDaemonPublicConfig{
				Listen: defaultDaemonPublicListenAddr,
				Feishu: HomeDaemonPublicFeishuConfig{
					Enabled: true,
					BaseURL: defaultFeishuBaseURL,
				},
				Ingress: HomeDaemonPublicIngressConfig{
					Provider:       defaultDaemonPublicIngressProvider,
					Enabled:        true,
					TunnelName:     defaultDaemonPublicIngressName,
					CloudflaredBin: defaultDaemonCloudflaredBin,
				},
			},
			Web: HomeDaemonWebConfig{
				Listen: defaultDaemonWebListenAddr,
			},
			Notebook: HomeDaemonNotebookConfig{
				WorkerCount: defaultNotebookWorkerCount,
			},
		},
	}
}

func (c HomeConfig) WithDefaults() HomeConfig {
	out := c
	if out.SchemaVersion <= 0 || out.SchemaVersion < CurrentHomeConfigSchemaVersion {
		out.SchemaVersion = CurrentHomeConfigSchemaVersion
	}

	out.Gateway.Listen = strings.TrimSpace(out.Gateway.Listen)
	if out.Gateway.Listen == "" {
		out.Gateway.Listen = defaultGatewayListenAddr
	}

	out.Gateway.InternalListen = strings.TrimSpace(out.Gateway.InternalListen)
	if out.Gateway.InternalListen == "" {
		out.Gateway.InternalListen = defaultGatewayInternalListenAddr
	}

	out.Gateway.InternalAllowCIDRs = normalizeGatewayInternalAllowCIDRs(out.Gateway.InternalAllowCIDRs)
	if len(out.Gateway.InternalAllowCIDRs) == 0 {
		out.Gateway.InternalAllowCIDRs = append([]string(nil), defaultGatewayInternalAllowCIDRs...)
	}

	if out.Gateway.QueueDepth <= 0 {
		out.Gateway.QueueDepth = defaultGatewayQueueDepth
	}

	out.Gateway.DBPath = strings.TrimSpace(out.Gateway.DBPath)
	out.Gateway.AuthToken = strings.TrimSpace(out.Gateway.AuthToken)
	out.Gateway.Feishu.AppID = strings.TrimSpace(out.Gateway.Feishu.AppID)
	out.Gateway.Feishu.AppSecret = strings.TrimSpace(out.Gateway.Feishu.AppSecret)
	out.Gateway.Feishu.VerificationToken = strings.TrimSpace(out.Gateway.Feishu.VerificationToken)
	out.Gateway.Feishu.WebhookSecretPath = strings.TrimSpace(out.Gateway.Feishu.WebhookSecretPath)

	out.Gateway.Feishu.WebhookPath = strings.TrimSpace(out.Gateway.Feishu.WebhookPath)
	out.Gateway.Feishu.BaseURL = strings.TrimSpace(out.Gateway.Feishu.BaseURL)
	if out.Gateway.Feishu.BaseURL == "" {
		out.Gateway.Feishu.BaseURL = defaultFeishuBaseURL
	}
	out.Gateway.Feishu.BaseURL = strings.TrimRight(out.Gateway.Feishu.BaseURL, "/")
	if out.Gateway.Feishu.BaseURL == "" {
		out.Gateway.Feishu.BaseURL = defaultFeishuBaseURL
	}
	out.Gateway.Tunnel.Name = strings.TrimSpace(out.Gateway.Tunnel.Name)
	out.Gateway.Tunnel.Hostname = strings.TrimSpace(out.Gateway.Tunnel.Hostname)
	out.Gateway.Tunnel.CloudflaredBin = strings.TrimSpace(out.Gateway.Tunnel.CloudflaredBin)
	out.Gateway.Feishu.BaseURL = normalizeDaemonFeishuBaseURL(out.Gateway.Feishu.BaseURL)

	out.Daemon.PIDFile = strings.TrimSpace(out.Daemon.PIDFile)
	if out.Daemon.PIDFile == "" {
		out.Daemon.PIDFile = defaultDaemonPIDFile
	}
	out.Daemon.LockFile = strings.TrimSpace(out.Daemon.LockFile)
	if out.Daemon.LockFile == "" {
		out.Daemon.LockFile = defaultDaemonLockFile
	}
	out.Daemon.LogFile = strings.TrimSpace(out.Daemon.LogFile)
	if out.Daemon.LogFile == "" {
		out.Daemon.LogFile = defaultDaemonLogFile
	}
	if out.Daemon.MaxConcurrent <= 0 {
		out.Daemon.MaxConcurrent = defaultDaemonMaxConcurrent
	}

	out.Daemon.Internal.Listen = strings.TrimSpace(out.Daemon.Internal.Listen)
	if out.Daemon.Internal.Listen == "" {
		out.Daemon.Internal.Listen = defaultDaemonInternalListenAddr
	}

	out.Daemon.Public.Listen = strings.TrimSpace(out.Daemon.Public.Listen)
	if out.Daemon.Public.Listen == "" {
		out.Daemon.Public.Listen = defaultDaemonPublicListenAddr
	}
	out.Daemon.Web.Listen = strings.TrimSpace(out.Daemon.Web.Listen)
	if out.Daemon.Web.Listen == "" {
		out.Daemon.Web.Listen = defaultDaemonWebListenAddr
	}
	out.Daemon.Public.Feishu.AppID = strings.TrimSpace(out.Daemon.Public.Feishu.AppID)
	out.Daemon.Public.Feishu.AppSecret = strings.TrimSpace(out.Daemon.Public.Feishu.AppSecret)
	out.Daemon.Public.Feishu.VerificationToken = strings.TrimSpace(out.Daemon.Public.Feishu.VerificationToken)
	out.Daemon.Public.Feishu.WebhookSecretPath = strings.TrimSpace(out.Daemon.Public.Feishu.WebhookSecretPath)
	out.Daemon.Public.Feishu.WebhookPath = strings.TrimSpace(out.Daemon.Public.Feishu.WebhookPath)
	out.Daemon.Public.Feishu.BaseURL = normalizeDaemonFeishuBaseURL(out.Daemon.Public.Feishu.BaseURL)

	out.Daemon.Public.Ingress.Provider = strings.TrimSpace(strings.ToLower(out.Daemon.Public.Ingress.Provider))
	if out.Daemon.Public.Ingress.Provider == "" {
		out.Daemon.Public.Ingress.Provider = defaultDaemonPublicIngressProvider
	}
	out.Daemon.Public.Ingress.TunnelName = strings.TrimSpace(out.Daemon.Public.Ingress.TunnelName)
	out.Daemon.Public.Ingress.Hostname = strings.TrimSpace(out.Daemon.Public.Ingress.Hostname)
	out.Daemon.Public.Ingress.CloudflaredBin = strings.TrimSpace(out.Daemon.Public.Ingress.CloudflaredBin)
	if out.Daemon.Public.Ingress.CloudflaredBin == "" {
		out.Daemon.Public.Ingress.CloudflaredBin = defaultDaemonCloudflaredBin
	}
	if out.Daemon.Public.Ingress.Enabled && out.Daemon.Public.Ingress.TunnelName == "" {
		out.Daemon.Public.Ingress.TunnelName = defaultDaemonPublicIngressName
	}
	if out.Daemon.Notebook.WorkerCount <= 0 {
		out.Daemon.Notebook.WorkerCount = defaultNotebookWorkerCount
	}

	out.Agent.Provider = strings.TrimSpace(strings.ToLower(out.Agent.Provider))
	out.Agent.Model = strings.TrimSpace(out.Agent.Model)
	switch out.Agent.Provider {
	case "", "codex", "claude", "gemini":
	default:
		out.Agent.Provider = ""
	}

	// v3: providers map 默认
	if len(out.Providers) == 0 {
		out.Providers = repo.DefaultProviders()
	}
	out.WorkerAgent.Provider = strings.TrimSpace(out.WorkerAgent.Provider)
	out.PMAgent.Provider = strings.TrimSpace(out.PMAgent.Provider)
	out.GatewayAgent.Provider = strings.TrimSpace(out.GatewayAgent.Provider)

	return out
}

func LoadHomeConfig(path string) (cfg HomeConfig, exists bool, needsRewrite bool, err error) {
	b, readErr := os.ReadFile(path)
	if readErr != nil {
		if os.IsNotExist(readErr) {
			return DefaultHomeConfig(), false, false, nil
		}
		return HomeConfig{}, false, false, readErr
	}
	if err := validateRemovedDaemonInternalFields(b); err != nil {
		return HomeConfig{}, true, false, err
	}

	var parsed HomeConfig
	if err := json.Unmarshal(b, &parsed); err != nil {
		return HomeConfig{}, true, false, err
	}
	needsRewrite = parsed.SchemaVersion <= 0 || parsed.SchemaVersion < CurrentHomeConfigSchemaVersion
	if parsed.SchemaVersion > 0 && parsed.SchemaVersion < CurrentHomeConfigSchemaVersion {
		homeConfigDeprecationWarnf(
			"[deprecation] home config schema v%d 已废弃，已自动迁移到 v%d",
			parsed.SchemaVersion,
			CurrentHomeConfigSchemaVersion,
		)
	}
	return parsed.WithDefaults(), true, needsRewrite, nil
}

func validateRemovedDaemonInternalFields(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil
	}
	daemonRaw, ok := root["daemon"]
	if !ok {
		return nil
	}
	var daemon map[string]json.RawMessage
	if err := json.Unmarshal(daemonRaw, &daemon); err != nil {
		return nil
	}
	internalRaw, ok := daemon["internal"]
	if !ok {
		return nil
	}
	var internal map[string]json.RawMessage
	if err := json.Unmarshal(internalRaw, &internal); err != nil {
		return nil
	}
	if _, ok := internal["allow_cidrs"]; ok {
		return fmt.Errorf("daemon.internal.allow_cidrs 已移除，请删除该字段")
	}
	if _, ok := internal["auth_token_env"]; ok {
		return fmt.Errorf("daemon.internal.auth_token_env 已移除，请删除该字段")
	}
	return nil
}

func normalizeDaemonFeishuBaseURL(raw string) string {
	base := strings.TrimSpace(raw)
	if base == "" {
		base = defaultFeishuBaseURL
	}
	base = strings.TrimRight(base, "/")
	if base == "" {
		base = defaultFeishuBaseURL
	}
	return base
}

func WriteHomeConfigAtomic(path string, cfg HomeConfig) error {
	cfg = cfg.WithDefaults()
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".home.config.json.*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("写入 Home 配置失败: %w", err)
	}
	return nil
}

func resolveHomeConfigPath(rootDir, rawPath string) string {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return ""
	}
	if filepath.IsAbs(rawPath) {
		return rawPath
	}
	return filepath.Join(rootDir, rawPath)
}

func normalizeGatewayInternalAllowCIDRs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, raw := range values {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			continue
		}
		if _, exists := seen[cidr]; exists {
			continue
		}
		seen[cidr] = struct{}{}
		out = append(out, cidr)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
