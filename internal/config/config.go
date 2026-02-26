package config

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	agentprovider "dalek/internal/agent/provider"
	"dalek/internal/app"
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
		DefaultScope:  ScopeLocal,
		AllowedScopes: []Scope{ScopeGlobal, ScopeLocal},
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
	AgentProvider bool
	AgentModel    bool
}

type EvalContext struct {
	HomeCfg       app.HomeConfig
	HomePresence  HomePresence
	Project       *app.Project
	LocalCfg      repo.Config
	LocalPresence LocalPresence
}

type SetContext struct {
	Home     *app.Home
	HomeCfg  app.HomeConfig
	Project  *app.Project
	LocalCfg repo.Config
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
	switch NormalizeKey(key) {
	case ConfigKeyDaemonInternalListen:
		value := strings.TrimSpace(eval.HomeCfg.WithDefaults().Daemon.Internal.Listen)
		if eval.HomePresence.DaemonInternalListen {
			return value, ScopeGlobal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyDaemonPublicListen:
		value := strings.TrimSpace(eval.HomeCfg.WithDefaults().Daemon.Public.Listen)
		if eval.HomePresence.DaemonPublicListen {
			return value, ScopeGlobal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyDaemonMaxConcurrent:
		value := strconv.Itoa(eval.HomeCfg.WithDefaults().Daemon.MaxConcurrent)
		if eval.HomePresence.DaemonMaxConcurrent {
			return value, ScopeGlobal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyProjectMaxRunningWorkers:
		if eval.Project == nil {
			return "", ScopeDB, fmt.Errorf("project 为空")
		}
		st, err := eval.Project.GetPMState(context.Background())
		if err != nil {
			return "", ScopeDB, err
		}
		return strconv.Itoa(st.MaxRunningWorkers), ScopeDB, nil
	case ConfigKeyAgentProvider:
		effective := BuildEffectiveProjectConfig(eval.HomeCfg, eval.LocalCfg)
		value := strings.TrimSpace(strings.ToLower(effective.WorkerAgent.Provider))
		if eval.LocalPresence.AgentProvider {
			return value, ScopeLocal, nil
		}
		if eval.HomePresence.AgentProvider {
			return value, ScopeGlobal, nil
		}
		return value, ScopeDefault, nil
	case ConfigKeyAgentModel:
		effective := BuildEffectiveProjectConfig(eval.HomeCfg, eval.LocalCfg)
		value := strings.TrimSpace(effective.WorkerAgent.Model)
		if eval.LocalPresence.AgentModel {
			return value, ScopeLocal, nil
		}
		if eval.HomePresence.AgentModel {
			return value, ScopeGlobal, nil
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
		if ctx.Home == nil {
			return "", fmt.Errorf("home 为空")
		}
		if err := app.WriteHomeConfigAtomic(ctx.Home.ConfigPath, ctx.HomeCfg); err != nil {
			return "", err
		}
		return ctx.HomeCfg.WithDefaults().Daemon.Internal.Listen, nil
	case ConfigKeyDaemonPublicListen:
		if scope != ScopeGlobal {
			return "", fmt.Errorf("%s 仅支持 global 层", key)
		}
		ctx.HomeCfg.Daemon.Public.Listen = strings.TrimSpace(rawValue)
		if strings.TrimSpace(ctx.HomeCfg.Daemon.Public.Listen) == "" {
			return "", fmt.Errorf("daemon.public.listen 不能为空")
		}
		if ctx.Home == nil {
			return "", fmt.Errorf("home 为空")
		}
		if err := app.WriteHomeConfigAtomic(ctx.Home.ConfigPath, ctx.HomeCfg); err != nil {
			return "", err
		}
		return ctx.HomeCfg.WithDefaults().Daemon.Public.Listen, nil
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
		if ctx.Home == nil {
			return "", fmt.Errorf("home 为空")
		}
		if err := app.WriteHomeConfigAtomic(ctx.Home.ConfigPath, ctx.HomeCfg); err != nil {
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
		st, err := ctx.Project.SetMaxRunningWorkers(context.Background(), n)
		if err != nil {
			return "", err
		}
		return strconv.Itoa(st.MaxRunningWorkers), nil
	case ConfigKeyAgentProvider:
		provider := agentprovider.NormalizeProvider(rawValue)
		if !agentprovider.IsSupportedProvider(provider) {
			return "", fmt.Errorf("agent.provider 仅支持 codex 或 claude")
		}
		switch scope {
		case ScopeGlobal:
			ctx.HomeCfg.Agent.Provider = provider
			if ctx.Home == nil {
				return "", fmt.Errorf("home 为空")
			}
			if err := app.WriteHomeConfigAtomic(ctx.Home.ConfigPath, ctx.HomeCfg); err != nil {
				return "", err
			}
			return strings.TrimSpace(strings.ToLower(ctx.HomeCfg.WithDefaults().Agent.Provider)), nil
		case ScopeLocal:
			if ctx.Project == nil {
				return "", fmt.Errorf("project 为空")
			}
			pc := app.ProjectConfig(ctx.LocalCfg)
			app.ApplyAgentProviderModelOverride(&pc, provider, "")
			next := repo.Config(pc).WithDefaults()
			if err := repo.WriteConfigAtomic(repo.NewLayout(ctx.Project.RepoRoot()).ConfigPath, next); err != nil {
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
			if ctx.Home == nil {
				return "", fmt.Errorf("home 为空")
			}
			if err := app.WriteHomeConfigAtomic(ctx.Home.ConfigPath, ctx.HomeCfg); err != nil {
				return "", err
			}
			return strings.TrimSpace(ctx.HomeCfg.WithDefaults().Agent.Model), nil
		case ScopeLocal:
			if ctx.Project == nil {
				return "", fmt.Errorf("project 为空")
			}
			next := ctx.LocalCfg.WithDefaults()
			next.WorkerAgent.Model = model
			next.PMAgent.Model = model
			if err := repo.WriteConfigAtomic(repo.NewLayout(ctx.Project.RepoRoot()).ConfigPath, next); err != nil {
				return "", err
			}
			return strings.TrimSpace(next.WorkerAgent.Model), nil
		default:
			return "", fmt.Errorf("agent.model 不支持 scope=%s", scope)
		}
	default:
		return "", fmt.Errorf("未知配置键: %s", key)
	}
}

func LoadProjectConfigFromProject(p *app.Project) (repo.Config, error) {
	if p == nil {
		return repo.Config{}, fmt.Errorf("project 为空")
	}
	cfgPath := repo.NewLayout(p.RepoRoot()).ConfigPath
	cfg, _, err := repo.LoadConfig(cfgPath)
	if err != nil {
		return repo.Config{}, err
	}
	return cfg.WithDefaults(), nil
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
		AgentProvider: jsonPathExists(root, "worker_agent", "provider") || jsonPathExists(root, "pm_agent", "provider"),
		AgentModel:    jsonPathExists(root, "worker_agent", "model") || jsonPathExists(root, "pm_agent", "model"),
	}, nil
}

func LoadLocalConfigPresenceFromProject(p *app.Project) (LocalPresence, error) {
	if p == nil {
		return LocalPresence{}, fmt.Errorf("project 为空")
	}
	cfgPath := repo.NewLayout(p.RepoRoot()).ConfigPath
	return LoadLocalConfigPresence(cfgPath)
}

func BuildEffectiveProjectConfig(homeCfg app.HomeConfig, localCfg repo.Config) repo.Config {
	merged := repo.Config{}.WithDefaults()
	pc := app.ProjectConfig(merged)
	app.ApplyAgentProviderModelOverride(&pc, homeCfg.Agent.Provider, homeCfg.Agent.Model)
	merged = repo.Config(pc)
	merged = repo.MergeConfig(merged, localCfg)
	return merged.WithDefaults()
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
