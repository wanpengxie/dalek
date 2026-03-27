package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/repo"
)

type Scope string

const (
	ScopeDefault Scope = "default"
	ScopeGlobal  Scope = "global"
	ScopeLocal   Scope = "local"
	ScopeDB      Scope = "db"
)

const (
	ConfigKeyDaemonInternalListen     = "daemon.internal.listen"
	ConfigKeyDaemonPublicListen       = "daemon.public.listen"
	ConfigKeyDaemonMaxConcurrent      = "daemon.max_concurrent"
	ConfigKeyProjectMaxRunningWorkers = "project.max_running_workers"
	ConfigKeyAgentProvider            = "agent.provider"
	ConfigKeyAgentModel               = "agent.model"
	ConfigKeyWorkerAgentProvider      = "worker_agent.provider"
	ConfigKeyPMAgentProvider          = "pm_agent.provider"
	ConfigKeyGatewayAgentProvider     = "gateway_agent.provider"
)

const (
	defaultDaemonInternalListen = "127.0.0.1:18081"
	defaultDaemonPublicListen   = "127.0.0.1:18080"
	defaultDaemonMaxConcurrent  = 4
)

type KeyMeta struct {
	Key           string
	DefaultScope  Scope
	AllowedScopes []Scope
}

var KeyOrder = []KeyMeta{
	{
		Key:           ConfigKeyDaemonInternalListen,
		DefaultScope:  ScopeGlobal,
		AllowedScopes: []Scope{ScopeGlobal},
	},
	{
		Key:           ConfigKeyDaemonPublicListen,
		DefaultScope:  ScopeGlobal,
		AllowedScopes: []Scope{ScopeGlobal},
	},
	{
		Key:           ConfigKeyDaemonMaxConcurrent,
		DefaultScope:  ScopeGlobal,
		AllowedScopes: []Scope{ScopeGlobal},
	},
	{
		Key:           ConfigKeyProjectMaxRunningWorkers,
		DefaultScope:  ScopeDB,
		AllowedScopes: []Scope{ScopeDB},
	},
	{
		Key:           ConfigKeyAgentProvider,
		DefaultScope:  ScopeLocal,
		AllowedScopes: []Scope{ScopeGlobal, ScopeLocal},
	},
	{
		Key:           ConfigKeyAgentModel,
		DefaultScope:  ScopeGlobal,
		AllowedScopes: []Scope{ScopeGlobal},
	},
	{
		Key:           ConfigKeyWorkerAgentProvider,
		DefaultScope:  ScopeLocal,
		AllowedScopes: []Scope{ScopeLocal},
	},
	{
		Key:           ConfigKeyPMAgentProvider,
		DefaultScope:  ScopeLocal,
		AllowedScopes: []Scope{ScopeLocal},
	},
	{
		Key:           ConfigKeyGatewayAgentProvider,
		DefaultScope:  ScopeLocal,
		AllowedScopes: []Scope{ScopeLocal},
	},
}

type HomePresence struct {
	DaemonInternalListen bool
	DaemonPublicListen   bool
	DaemonMaxConcurrent  bool
	AgentProvider        bool
	AgentModel           bool
}

type LocalPresence struct {
	AgentProvider        bool
	AgentModel           bool
	WorkerAgentProvider  bool
	PMAgentProvider      bool
	GatewayAgentProvider bool
}

type HomeConfig struct {
	Agent  HomeAgentConfig
	Daemon HomeDaemonConfig
}

type HomeAgentConfig struct {
	Provider string
	Model    string
}

type HomeDaemonConfig struct {
	MaxConcurrent int
	Internal      HomeDaemonInternalConfig
	Public        HomeDaemonPublicConfig
}

type HomeDaemonInternalConfig struct {
	Listen string
}

type HomeDaemonPublicConfig struct {
	Listen string
}

func (c HomeConfig) WithDefaults() HomeConfig {
	out := c
	out.Daemon.Internal.Listen = strings.TrimSpace(out.Daemon.Internal.Listen)
	if out.Daemon.Internal.Listen == "" {
		out.Daemon.Internal.Listen = defaultDaemonInternalListen
	}
	out.Daemon.Public.Listen = strings.TrimSpace(out.Daemon.Public.Listen)
	if out.Daemon.Public.Listen == "" {
		out.Daemon.Public.Listen = defaultDaemonPublicListen
	}
	if out.Daemon.MaxConcurrent <= 0 {
		out.Daemon.MaxConcurrent = defaultDaemonMaxConcurrent
	}
	out.Agent.Provider = strings.TrimSpace(strings.ToLower(out.Agent.Provider))
	switch out.Agent.Provider {
	case "", agentprovider.ProviderCodex, agentprovider.ProviderClaude, agentprovider.ProviderGemini:
	default:
		out.Agent.Provider = ""
	}
	out.Agent.Model = strings.TrimSpace(out.Agent.Model)
	return out
}

type ProjectConfigPathProvider interface {
	ConfigPath() string
}

type ProjectConfigAccessor interface {
	ProjectConfigPathProvider
	GetMaxRunningWorkers(ctx context.Context) (int, error)
	SetMaxRunningWorkers(ctx context.Context, n int) (int, error)
}

type EvalContext struct {
	HomeCfg       HomeConfig
	HomePresence  HomePresence
	Project       ProjectConfigAccessor
	LocalCfg      repo.Config
	LocalPresence LocalPresence
	Providers     map[string]repo.ProviderConfig // 全局 providers map
}

type SetContext struct {
	HomeConfigPath  string
	HomeCfg         HomeConfig
	WriteHomeConfig func(path string, cfg HomeConfig) error
	Project         ProjectConfigAccessor
	LocalCfg        repo.Config
	Providers       map[string]repo.ProviderConfig // 全局 providers map
}

func NormalizeKey(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func FindKeyMeta(key string) (KeyMeta, bool) {
	key = NormalizeKey(key)
	for _, meta := range KeyOrder {
		if meta.Key == key {
			return meta, true
		}
	}
	return KeyMeta{}, false
}

func ContainsScope(scopes []Scope, target Scope) bool {
	for _, s := range scopes {
		if s == target {
			return true
		}
	}
	return false
}

func JoinScopes(scopes []Scope) string {
	if len(scopes) == 0 {
		return "-"
	}
	out := make([]string, 0, len(scopes))
	for _, s := range scopes {
		out = append(out, string(s))
	}
	return strings.Join(out, ", ")
}

func ResolveValue(key string, eval *EvalContext) (string, Scope, error) {
	if eval == nil {
		return "", ScopeDefault, fmt.Errorf("config eval context 为空")
	}
	home := eval.HomeCfg.WithDefaults()
	switch NormalizeKey(key) {
	case ConfigKeyDaemonInternalListen:
		value := strings.TrimSpace(home.Daemon.Internal.Listen)
		if eval.HomePresence.DaemonInternalListen {
			return value, ScopeGlobal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyDaemonPublicListen:
		value := strings.TrimSpace(home.Daemon.Public.Listen)
		if eval.HomePresence.DaemonPublicListen {
			return value, ScopeGlobal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyDaemonMaxConcurrent:
		value := strconv.Itoa(home.Daemon.MaxConcurrent)
		if eval.HomePresence.DaemonMaxConcurrent {
			return value, ScopeGlobal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyProjectMaxRunningWorkers:
		if eval.Project == nil {
			return "", ScopeDB, fmt.Errorf("project 为空")
		}
		n, err := eval.Project.GetMaxRunningWorkers(context.Background())
		if err != nil {
			return "", ScopeDB, err
		}
		return strconv.Itoa(n), ScopeDB, nil
	case ConfigKeyAgentProvider:
		effective := BuildEffectiveProjectConfig(home, eval.LocalCfg)
		value := strings.TrimSpace(strings.ToLower(effective.WorkerAgent.Provider))
		if eval.LocalPresence.AgentProvider {
			return value, ScopeLocal, nil
		}
		if eval.HomePresence.AgentProvider {
			return value, ScopeGlobal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyAgentModel:
		effective := BuildEffectiveProjectConfig(home, eval.LocalCfg)
		providers := eval.Providers
		if len(providers) == 0 {
			providers = repo.DefaultProviders()
		}
		resolved, err := repo.ResolveAgentConfig(effective.WorkerAgent.Provider, providers)
		if err != nil {
			return "", ScopeDefault, nil
		}
		value := strings.TrimSpace(resolved.Model)
		if eval.LocalPresence.AgentModel {
			return value, ScopeLocal, nil
		}
		if eval.HomePresence.AgentModel {
			return value, ScopeGlobal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyWorkerAgentProvider:
		effective := BuildEffectiveProjectConfig(home, eval.LocalCfg)
		value := strings.TrimSpace(effective.WorkerAgent.Provider)
		if eval.LocalPresence.WorkerAgentProvider {
			return value, ScopeLocal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyPMAgentProvider:
		effective := BuildEffectiveProjectConfig(home, eval.LocalCfg)
		value := strings.TrimSpace(effective.PMAgent.Provider)
		if eval.LocalPresence.PMAgentProvider {
			return value, ScopeLocal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyGatewayAgentProvider:
		effective := BuildEffectiveProjectConfig(home, eval.LocalCfg)
		value := strings.TrimSpace(effective.GatewayAgent.Provider)
		if eval.LocalPresence.GatewayAgentProvider {
			return value, ScopeLocal, nil
		}
		return value, ScopeDefault, nil
	default:
		return "", ScopeDefault, fmt.Errorf("未知配置键: %s", key)
	}
}

func SetValue(ctx *SetContext, key string, scope Scope, rawValue string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("config set context 为空")
	}
	switch NormalizeKey(key) {
	case ConfigKeyDaemonInternalListen:
		if scope != ScopeGlobal {
			return "", fmt.Errorf("%s 仅支持 global 层", key)
		}
		ctx.HomeCfg.Daemon.Internal.Listen = strings.TrimSpace(rawValue)
		if strings.TrimSpace(ctx.HomeCfg.Daemon.Internal.Listen) == "" {
			return "", fmt.Errorf("daemon.internal.listen 不能为空")
		}
		if err := persistHomeConfig(ctx); err != nil {
			return "", err
		}
		return strings.TrimSpace(ctx.HomeCfg.WithDefaults().Daemon.Internal.Listen), nil
	case ConfigKeyDaemonPublicListen:
		if scope != ScopeGlobal {
			return "", fmt.Errorf("%s 仅支持 global 层", key)
		}
		ctx.HomeCfg.Daemon.Public.Listen = strings.TrimSpace(rawValue)
		if strings.TrimSpace(ctx.HomeCfg.Daemon.Public.Listen) == "" {
			return "", fmt.Errorf("daemon.public.listen 不能为空")
		}
		if err := persistHomeConfig(ctx); err != nil {
			return "", err
		}
		return strings.TrimSpace(ctx.HomeCfg.WithDefaults().Daemon.Public.Listen), nil
	case ConfigKeyDaemonMaxConcurrent:
		if scope != ScopeGlobal {
			return "", fmt.Errorf("%s 仅支持 global 层", key)
		}
		n, err := strconv.Atoi(strings.TrimSpace(rawValue))
		if err != nil {
			return "", fmt.Errorf("daemon.max_concurrent 必须是正整数: %w", err)
		}
		if n <= 0 {
			return "", fmt.Errorf("daemon.max_concurrent 必须大于 0")
		}
		ctx.HomeCfg.Daemon.MaxConcurrent = n
		if err := persistHomeConfig(ctx); err != nil {
			return "", err
		}
		return strconv.Itoa(ctx.HomeCfg.WithDefaults().Daemon.MaxConcurrent), nil
	case ConfigKeyProjectMaxRunningWorkers:
		if scope != ScopeDB {
			return "", fmt.Errorf("%s 仅支持 db 层", key)
		}
		if ctx.Project == nil {
			return "", fmt.Errorf("project 为空")
		}
		n, err := strconv.Atoi(strings.TrimSpace(rawValue))
		if err != nil {
			return "", fmt.Errorf("project.max_running_workers 必须是整数: %w", err)
		}
		if n < 1 || n > 32 {
			return "", fmt.Errorf("project.max_running_workers 取值范围为 1-32")
		}
		got, err := ctx.Project.SetMaxRunningWorkers(context.Background(), n)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(got), nil
	case ConfigKeyAgentProvider:
		provider := agentprovider.NormalizeProvider(rawValue)
		if !agentprovider.IsSupportedProvider(provider) {
			return "", fmt.Errorf("agent.provider 仅支持 codex、claude 或 gemini")
		}
		switch scope {
		case ScopeGlobal:
			ctx.HomeCfg.Agent.Provider = provider
			if err := persistHomeConfig(ctx); err != nil {
				return "", err
			}
			return strings.TrimSpace(strings.ToLower(ctx.HomeCfg.WithDefaults().Agent.Provider)), nil
		case ScopeLocal:
			if ctx.Project == nil {
				return "", fmt.Errorf("project 为空")
			}
			next := ctx.LocalCfg.WithDefaults()
			applyAgentProviderModelOverride(&next, provider, "")
			if err := repo.WriteConfigAtomic(strings.TrimSpace(ctx.Project.ConfigPath()), next); err != nil {
				return "", err
			}
			return strings.TrimSpace(strings.ToLower(next.WorkerAgent.Provider)), nil
		default:
			return "", fmt.Errorf("agent.provider 不支持 scope=%s", scope)
		}
	case ConfigKeyAgentModel:
		model := strings.TrimSpace(rawValue)
		if model == "" {
			return "", fmt.Errorf("agent.model 不能为空")
		}
		switch scope {
		case ScopeGlobal:
			ctx.HomeCfg.Agent.Model = model
			if err := persistHomeConfig(ctx); err != nil {
				return "", err
			}
			return strings.TrimSpace(ctx.HomeCfg.WithDefaults().Agent.Model), nil
		case ScopeLocal:
			// v3: model 不再是 role-level 字段，不支持 local scope 设置
			return "", fmt.Errorf("agent.model 在 v3 中不再支持 local scope 设置，请通过 providers map 配置")
		default:
			return "", fmt.Errorf("agent.model 不支持 scope=%s", scope)
		}
	case ConfigKeyWorkerAgentProvider:
		return setRoleProvider(ctx, "worker_agent", rawValue, scope)
	case ConfigKeyPMAgentProvider:
		return setRoleProvider(ctx, "pm_agent", rawValue, scope)
	case ConfigKeyGatewayAgentProvider:
		return setRoleProvider(ctx, "gateway_agent", rawValue, scope)
	default:
		return "", fmt.Errorf("未知配置键: %s", key)
	}
}

// setRoleProvider 校验 provider key 存在于全局 providers map，写入项目配置，返回最终值。
func setRoleProvider(ctx *SetContext, role, rawValue string, scope Scope) (string, error) {
	if scope != ScopeLocal {
		return "", fmt.Errorf("%s.provider 仅支持 local 层", role)
	}
	if ctx.Project == nil {
		return "", fmt.Errorf("project 为空")
	}
	providerKey := strings.TrimSpace(rawValue)
	if providerKey == "" {
		return "", fmt.Errorf("%s.provider 不能为空", role)
	}
	// 校验 provider key 在全局 providers map 中存在
	providers := ctx.Providers
	if len(providers) == 0 {
		providers = repo.DefaultProviders()
	}
	if _, ok := providers[providerKey]; !ok {
		// 也接受合法的 provider 类型名作为 fallback
		normalized := agentprovider.NormalizeProvider(providerKey)
		if !agentprovider.IsSupportedProvider(normalized) {
			keys := make([]string, 0, len(providers))
			for k := range providers {
				keys = append(keys, k)
			}
			return "", fmt.Errorf("provider key %q 不存在，可用: %s", providerKey, strings.Join(keys, ", "))
		}
	}
	next := ctx.LocalCfg.WithDefaults()
	switch role {
	case "worker_agent":
		next.WorkerAgent.Provider = providerKey
	case "pm_agent":
		next.PMAgent.Provider = providerKey
	case "gateway_agent":
		next.GatewayAgent.Provider = providerKey
	}
	if err := repo.WriteConfigAtomic(strings.TrimSpace(ctx.Project.ConfigPath()), next); err != nil {
		return "", err
	}
	ctx.LocalCfg = next // 同步内存，确保同一 session 内后续操作使用新配置
	return providerKey, nil
}

func LoadProjectConfigFromPath(path string) (repo.Config, error) {
	cfg, _, err := repo.LoadConfig(strings.TrimSpace(path))
	if err != nil {
		return repo.Config{}, err
	}
	return cfg.WithDefaults(), nil
}

func LoadProjectConfigFromProject(p ProjectConfigPathProvider) (repo.Config, error) {
	if p == nil {
		return repo.Config{}, fmt.Errorf("project 为空")
	}
	return LoadProjectConfigFromPath(p.ConfigPath())
}

func LoadHomeConfigPresence(path string) (HomePresence, error) {
	root, err := loadJSONRoot(path)
	if err != nil {
		return HomePresence{}, err
	}
	return HomePresence{
		DaemonInternalListen: jsonPathExists(root, "daemon", "internal", "listen"),
		DaemonPublicListen:   jsonPathExists(root, "daemon", "public", "listen"),
		DaemonMaxConcurrent:  jsonPathExists(root, "daemon", "max_concurrent"),
		AgentProvider:        jsonPathExists(root, "agent", "provider"),
		AgentModel:           jsonPathExists(root, "agent", "model"),
	}, nil
}

func LoadLocalConfigPresence(path string) (LocalPresence, error) {
	root, err := loadJSONRoot(path)
	if err != nil {
		return LocalPresence{}, err
	}
	return LocalPresence{
		AgentProvider:        jsonPathExists(root, "worker_agent", "provider") || jsonPathExists(root, "pm_agent", "provider"),
		AgentModel:           false, // v3: model 不再是 role-level 字段
		WorkerAgentProvider:  jsonPathExists(root, "worker_agent", "provider"),
		PMAgentProvider:      jsonPathExists(root, "pm_agent", "provider"),
		GatewayAgentProvider: jsonPathExists(root, "gateway_agent", "provider"),
	}, nil
}

func LoadLocalConfigPresenceFromProject(p ProjectConfigPathProvider) (LocalPresence, error) {
	if p == nil {
		return LocalPresence{}, fmt.Errorf("project 为空")
	}
	return LoadLocalConfigPresence(p.ConfigPath())
}

func BuildEffectiveProjectConfig(homeCfg HomeConfig, localCfg repo.Config) repo.Config {
	merged := repo.Config{}.WithDefaults()
	applyAgentProviderModelOverride(&merged, homeCfg.Agent.Provider, homeCfg.Agent.Model)
	merged = repo.MergeConfig(merged, localCfg)
	return merged.WithDefaults()
}

func persistHomeConfig(ctx *SetContext) error {
	if ctx == nil {
		return fmt.Errorf("config set context 为空")
	}
	if ctx.WriteHomeConfig == nil {
		return fmt.Errorf("home config 写入器未注入")
	}
	path := strings.TrimSpace(ctx.HomeConfigPath)
	if path == "" {
		return fmt.Errorf("home config path 为空")
	}
	return ctx.WriteHomeConfig(path, ctx.HomeCfg)
}

func applyAgentProviderModelOverride(cfg *repo.Config, providerRaw, model string) {
	if cfg == nil {
		return
	}
	providerName := agentprovider.NormalizeProvider(providerRaw)
	_ = strings.TrimSpace(model) // v3: model 不再是 role-level 字段
	if providerName != "" {
		cfg.WorkerAgent.Provider = providerName
		cfg.PMAgent.Provider = providerName
	}
}

func loadJSONRoot(path string) (map[string]any, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return map[string]any{}, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, nil
		}
		return nil, err
	}
	var root map[string]any
	if len(strings.TrimSpace(string(b))) == 0 {
		return map[string]any{}, nil
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, err
	}
	if root == nil {
		return map[string]any{}, nil
	}
	return root, nil
}

func jsonPathExists(root map[string]any, path ...string) bool {
	if len(path) == 0 {
		return false
	}
	var cur any = root
	for _, seg := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		next, ok := obj[strings.TrimSpace(seg)]
		if !ok {
			return false
		}
		cur = next
	}
	return true
}
