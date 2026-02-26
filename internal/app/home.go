package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dalek/internal/agent/provider"
	"dalek/internal/infra"
	"dalek/internal/repo"
	channelsvc "dalek/internal/services/channel"
	"dalek/internal/services/core"
	logssvc "dalek/internal/services/logs"
	"dalek/internal/services/notebook"
	"dalek/internal/services/pm"
	subagentsvc "dalek/internal/services/subagent"
	"dalek/internal/services/task"
	"dalek/internal/services/ticket"
	"dalek/internal/services/worker"
	"dalek/internal/store"

	"gorm.io/gorm"
)

// Home 是 dalek 的全局运行目录（默认 ~/.dalek）。
//
// 说明（v0）：
// - Home 只负责：registry.json（repo <-> project 映射）+ worktrees 根目录。
// - 项目自身状态存放在 repo 内：<repoRoot>/.dalek/（sqlite/config/workers/manager 引导文件等）。
// - 每个 Project 的 git worktrees 统一存放在 Home/worktrees/<name>/tickets/ 下（不放进 repo）。
type Home struct {
	Root          string
	RegistryPath  string
	WorktreesDir  string
	ConfigPath    string
	Config        HomeConfig
	GatewayDBPath string
}

func ResolveHomeDir(cliHome string) (string, error) {
	home := strings.TrimSpace(cliHome)
	if home == "" {
		home = strings.TrimSpace(os.Getenv("DALEK_HOME"))
	}
	if home == "" {
		uh, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法获取用户 HOME: %w", err)
		}
		home = filepath.Join(uh, ".dalek")
	}

	// 支持 ~ 前缀
	if strings.HasPrefix(home, "~"+string(os.PathSeparator)) || home == "~" {
		uh, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("无法展开 ~: %w", err)
		}
		if home == "~" {
			home = uh
		} else {
			home = filepath.Join(uh, strings.TrimPrefix(home, "~"+string(os.PathSeparator)))
		}
	}

	abs, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func OpenHome(root string) (*Home, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		var err error
		root, err = ResolveHomeDir("")
		if err != nil {
			return nil, err
		}
	}
	h := &Home{
		Root:          root,
		RegistryPath:  filepath.Join(root, "registry.json"),
		WorktreesDir:  filepath.Join(root, "worktrees"),
		ConfigPath:    filepath.Join(root, "config.json"),
		Config:        DefaultHomeConfig(),
		GatewayDBPath: filepath.Join(root, "gateway.db"),
	}
	if err := os.MkdirAll(h.WorktreesDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}
	// 确保 registry 文件存在（空也行）
	if _, err := os.Stat(h.RegistryPath); err != nil {
		if os.IsNotExist(err) {
			r := Registry{Schema: "dalek.registry.v0", UpdatedAt: time.Now(), Projects: []RegisteredProject{}}
			if err := writeRegistryAtomic(h.RegistryPath, r); err != nil {
				return nil, err
			}
		} else {
			return nil, err
		}
	}
	cfg, exists, needsRewrite, err := LoadHomeConfig(h.ConfigPath)
	if err != nil {
		return nil, fmt.Errorf("读取 Home 配置失败: %w", err)
	}
	h.Config = cfg
	if exists && needsRewrite {
		if err := WriteHomeConfigAtomic(h.ConfigPath, cfg); err != nil {
			return nil, err
		}
	}
	if cfgPath := resolveHomeConfigPath(h.Root, h.Config.Gateway.DBPath); cfgPath != "" {
		h.GatewayDBPath = cfgPath
	}
	return h, nil
}

func (h *Home) LoadRegistry() (Registry, error) {
	return loadRegistry(h.RegistryPath)
}

func (h *Home) SaveRegistry(r Registry) error {
	r.UpdatedAt = time.Now()
	return writeRegistryAtomic(h.RegistryPath, r)
}

func (h *Home) OpenGatewayDB() (*gorm.DB, error) {
	if h == nil {
		return nil, fmt.Errorf("home 为空")
	}
	dbPath := strings.TrimSpace(h.GatewayDBPath)
	if dbPath == "" {
		return nil, fmt.Errorf("gateway db path 不能为空")
	}
	return store.OpenGatewayDB(dbPath)
}

func (h *Home) ListProjects() ([]RegisteredProject, error) {
	r, err := h.LoadRegistry()
	if err != nil {
		return nil, err
	}
	ps := append([]RegisteredProject(nil), r.Projects...)
	sort.Slice(ps, func(i, j int) bool { return ps[i].Name < ps[j].Name })
	return ps, nil
}

func (h *Home) FindProjectByName(name string) (*RegisteredProject, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name 不能为空")
	}
	r, err := h.LoadRegistry()
	if err != nil {
		return nil, err
	}
	for _, p := range r.Projects {
		if p.Name == name {
			pp := p
			return &pp, nil
		}
	}
	return nil, ErrNotInitialized
}

func (h *Home) FindProjectByRepoRoot(repoRoot string) (*RegisteredProject, error) {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return nil, fmt.Errorf("repoRoot 不能为空")
	}
	r, err := h.LoadRegistry()
	if err != nil {
		return nil, err
	}
	for _, p := range r.Projects {
		if samePath(p.RepoRoot, repoRoot) {
			pp := p
			return &pp, nil
		}
	}
	return nil, ErrNotInitialized
}

func (h *Home) InitProjectFromDir(startDir, name string, cfg ProjectConfig) (*Project, error) {
	repoRoot, err := repo.FindRepoRoot(startDir)
	if err != nil {
		return nil, err
	}
	return h.AddOrUpdateProject(name, repoRoot, cfg)
}

func (h *Home) AddOrUpdateProject(name, repoRoot string, cfg ProjectConfig) (*Project, error) {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return nil, fmt.Errorf("repoRoot 不能为空")
	}
	absRepo, err := filepath.Abs(repoRoot)
	if err == nil && absRepo != "" {
		repoRoot = absRepo
	}

	name = strings.TrimSpace(name)
	if name == "" {
		name = repo.DeriveProjectName(repoRoot)
	}

	r, err := h.LoadRegistry()
	if err != nil {
		return nil, err
	}

	// name 冲突检查
	for _, p := range r.Projects {
		if p.Name == name && !samePath(p.RepoRoot, repoRoot) {
			return nil, fmt.Errorf("project 名称已存在但指向另一个 repo: name=%s repo=%s", name, p.RepoRoot)
		}
	}

	now := time.Now()
	found := false
	for i := range r.Projects {
		if samePath(r.Projects[i].RepoRoot, repoRoot) {
			// 已注册：允许更新 name（若一致则无变化）
			r.Projects[i].Name = name
			r.Projects[i].RepoRoot = repoRoot
			r.Projects[i].UpdatedAt = now
			found = true
			break
		}
	}
	if !found {
		r.Projects = append(r.Projects, RegisteredProject{
			Name:      name,
			RepoRoot:  repoRoot,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	if err := h.SaveRegistry(r); err != nil {
		return nil, err
	}

	// 打开/初始化 project
	p, err := h.openProject(RegisteredProject{Name: name, RepoRoot: repoRoot})
	if err == nil {
		base, _, lerr := repo.LoadConfig(p.core.ConfigPath())
		if lerr != nil {
			base = p.core.Config
		}
		merged := repo.MergeConfig(base, cfg)
		if err := repo.WriteConfigAtomic(p.core.ConfigPath(), merged); err != nil {
			return nil, err
		}
		p.core.Config = merged
		return p, nil
	}
	// 可能尚未创建目录；走 init
	p, err = h.initProjectFiles(name, repoRoot, cfg)
	if err != nil {
		return nil, err
	}
	return p, nil
}

func (h *Home) OpenProjectFromDir(startDir string) (*Project, error) {
	repoRoot, err := repo.FindRepoRoot(startDir)
	if err != nil {
		return nil, err
	}
	rp, err := h.FindProjectByRepoRoot(repoRoot)
	if err != nil {
		return nil, err
	}
	return h.OpenProjectByName(rp.Name)
}

func (h *Home) OpenProjectByName(name string) (*Project, error) {
	rp, err := h.FindProjectByName(name)
	if err != nil {
		return nil, err
	}
	return h.openProject(*rp)
}

func (h *Home) openProject(rp RegisteredProject) (*Project, error) {
	name := strings.TrimSpace(rp.Name)
	if name == "" {
		return nil, fmt.Errorf("registry 里的 project name 为空")
	}
	repoRoot := strings.TrimSpace(rp.RepoRoot)
	if repoRoot == "" {
		return nil, fmt.Errorf("registry 里的 repo_root 为空: %s", name)
	}
	layout := repo.NewLayout(repoRoot)

	if _, err := os.Stat(layout.ConfigPath); err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotInitialized
		}
		return nil, err
	}

	repoRawCfg, err := loadProjectConfigRaw(layout.ConfigPath)
	if err != nil {
		return nil, err
	}
	cfg := composeProjectConfigLayers(repo.Config{}, h.Config.Agent.Provider, h.Config.Agent.Model, repoRawCfg)
	cfgNeedsRewrite := repoRawCfg.SchemaVersion <= 0 ||
		repoRawCfg.SchemaVersion < repo.CurrentProjectSchemaVersion ||
		strings.TrimSpace(repoRawCfg.WorkerAgent.Provider) == "" ||
		strings.TrimSpace(repoRawCfg.PMAgent.Provider) == "" ||
		repoRawCfg.Notebook.ShapeIntervalSec <= 0
	if cfgNeedsRewrite {
		if err := repo.WriteConfigAtomic(layout.ConfigPath, repoRawCfg.WithDefaults()); err != nil {
			return nil, err
		}
	}
	cfg.BranchPrefix = strings.TrimSpace(cfg.BranchPrefix)
	if cfg.BranchPrefix == "" {
		cfg.BranchPrefix = fmt.Sprintf("ts/%s/", repo.ProjectKey(repoRoot))
	}

	if err := os.MkdirAll(layout.RuntimeDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}
	db, err := store.OpenAndMigrate(layout.DBPath)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(layout.RuntimeWorkersDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}
	worktreesDir := filepath.Join(h.WorktreesDir, name, "tickets")
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}

	// 控制面 seed：缺失补齐；不写入运行时策略。
	if err := repo.EnsureControlPlaneSeed(layout, name); err != nil {
		return nil, err
	}
	// manager bootstrap：缺失补齐（尽量不让旧 repo 因缺文件而不可用）。
	if err := repo.EnsureManagerBootstrap(layout, name); err != nil {
		return nil, err
	}

	cp, err := buildCoreProject(name, repo.ProjectKey(repoRoot), repoRoot, layout, cfg, db, worktreesDir)
	if err != nil {
		return nil, err
	}
	return assembleProject(cp), nil
}

func (h *Home) initProjectFiles(name, repoRoot string, cfg ProjectConfig) (*Project, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("project name 不能为空")
	}
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return nil, fmt.Errorf("repoRoot 不能为空")
	}
	layout := repo.NewLayout(repoRoot)
	worktreesDir := filepath.Join(h.WorktreesDir, name, "tickets")

	if err := os.MkdirAll(layout.ProjectDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.MkdirAll(layout.RuntimeDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.MkdirAll(layout.RuntimeWorkersDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建目录失败: %w", err)
	}

	key := repo.ProjectKey(repoRoot)
	cfg.BranchPrefix = strings.TrimSpace(cfg.BranchPrefix)
	if cfg.BranchPrefix == "" {
		cfg.BranchPrefix = fmt.Sprintf("ts/%s/", key)
	}
	cfg = composeProjectConfigLayers(repo.Config{}, h.Config.Agent.Provider, h.Config.Agent.Model, cfg)

	db, err := store.OpenAndMigrate(layout.DBPath)
	if err != nil {
		return nil, err
	}

	// 控制面 seed + manager bootstrap
	if err := repo.EnsureControlPlaneSeed(layout, name); err != nil {
		return nil, err
	}
	if err := repo.EnsureManagerBootstrap(layout, name); err != nil {
		return nil, err
	}
	// 入口文件（CLAUDE.md/AGENTS.md）在项目初始化阶段处理一次。
	if err := repo.EnsureRepoAgentEntryPoints(repoRoot); err != nil {
		return nil, err
	}
	if err := repo.EnsureRepoAgentEntryPointsVersioned(repoRoot); err != nil {
		return nil, err
	}
	// config.json 作为“项目初始化成功锚点”在最后阶段写入，避免半初始化状态伪成功。
	if err := repo.WriteConfigAtomic(layout.ConfigPath, cfg); err != nil {
		return nil, err
	}

	cp, err := buildCoreProject(name, key, repoRoot, layout, cfg, db, worktreesDir)
	if err != nil {
		return nil, err
	}
	return assembleProject(cp), nil
}

func buildCoreProject(name, key, repoRoot string, layout repo.Layout, cfg repo.Config, db *gorm.DB, worktreesDir string) (*core.Project, error) {
	return core.NewProject(core.NewProjectInput{
		Name:         strings.TrimSpace(name),
		Key:          strings.TrimSpace(key),
		RepoRoot:     strings.TrimSpace(repoRoot),
		Layout:       layout,
		WorktreesDir: strings.TrimSpace(worktreesDir),
		WorkersDir:   strings.TrimSpace(layout.RuntimeWorkersDir),
		Config:       cfg,
		DB:           db,
		Logger:       core.DefaultLogger(),
		Tmux:         infra.NewTmuxExecClient(),
		Git:          infra.NewGitExecClient(),
		TaskRuntime:  task.NewRuntimeFactory(),
	})
}

func loadProjectConfigRaw(path string) (repo.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return repo.Config{}, err
	}
	var cfg repo.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return repo.Config{}, err
	}
	return cfg, nil
}

func composeProjectConfigLayers(base repo.Config, provider, model string, repoOverride repo.Config) repo.Config {
	merged := base.WithDefaults()
	merged = applyAgentProviderModel(merged, provider, model)
	merged = repo.MergeConfig(merged, repoOverride)
	return merged.WithDefaults()
}

func applyAgentProviderModel(cfg repo.Config, providerRaw, model string) repo.Config {
	providerName := provider.NormalizeProvider(providerRaw)
	model = strings.TrimSpace(model)
	if providerName != "" {
		prevWorkerProvider := strings.TrimSpace(strings.ToLower(cfg.WorkerAgent.Provider))
		prevPMProvider := strings.TrimSpace(strings.ToLower(cfg.PMAgent.Provider))
		cfg.WorkerAgent.Provider = providerName
		cfg.PMAgent.Provider = providerName
		if model == "" {
			if prevWorkerProvider != providerName {
				cfg.WorkerAgent.Model = ""
			}
			if prevPMProvider != providerName {
				cfg.PMAgent.Model = ""
			}
			if providerName == provider.ProviderCodex {
				defaultCodexModel := provider.DefaultModel(provider.ProviderCodex)
				if strings.TrimSpace(cfg.WorkerAgent.Model) == "" {
					cfg.WorkerAgent.Model = defaultCodexModel
				}
				if strings.TrimSpace(cfg.PMAgent.Model) == "" {
					cfg.PMAgent.Model = defaultCodexModel
				}
			}
		}
		if providerName == provider.ProviderClaude {
			cfg.WorkerAgent.ReasoningEffort = ""
			cfg.PMAgent.ReasoningEffort = ""
		}
	}
	if model != "" {
		cfg.WorkerAgent.Model = model
		cfg.PMAgent.Model = model
	}
	return cfg
}

func assembleProject(cp *core.Project) *Project {
	ticketSvc := ticket.New(cp.DB)
	ticketQuerySvc := ticket.NewQueryService(cp)
	workerSvc := worker.New(cp, ticketSvc)
	logsSvc := logssvc.New(cp, workerSvc)
	notebookSvc := notebook.New(cp)
	pmSvc := pm.New(cp, workerSvc)
	taskSvc := task.New(cp.DB)
	subagentSvc := subagentsvc.New(cp, taskSvc, cp.Logger)
	channelSvc := channelsvc.New(cp)
	return &Project{
		core:        cp,
		ticket:      ticketSvc,
		ticketQuery: ticketQuerySvc,
		worker:      workerSvc,
		logs:        logsSvc,
		notebook:    notebookSvc,
		pm:          pmSvc,
		subagent:    subagentSvc,
		task:        taskSvc,
		channel:     channelSvc,
	}
}

func samePath(a, b string) bool {
	aa := strings.TrimSpace(a)
	bb := strings.TrimSpace(b)
	if aa == "" || bb == "" {
		return aa == bb
	}
	absA, errA := filepath.Abs(aa)
	absB, errB := filepath.Abs(bb)
	if errA == nil && absA != "" {
		aa = absA
	}
	if errB == nil && absB != "" {
		bb = absB
	}
	return aa == bb
}
