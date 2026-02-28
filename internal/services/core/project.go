package core

import (
	"dalek/internal/infra"
	"dalek/internal/repo"
	"fmt"
	"log/slog"
	"strings"

	"gorm.io/gorm"
)

// Project 是 services 层的“项目上下文载体”（只承载资源与路径，不承载业务流程）。
// 业务流程必须落在 internal/services/* 的函数中。
type Project struct {
	Name string
	Key  string

	RepoRoot string
	Layout   repo.Layout

	// WorktreesDir: <home>/worktrees/<name>/tickets
	WorktreesDir string
	WorkersDir   string

	Config repo.Config
	DB     *gorm.DB
	Logger *slog.Logger

	Tmux          infra.TmuxClient
	WorkerRuntime infra.WorkerRuntime
	Git           infra.GitClient
	TaskRuntime   TaskRuntimeFactory
}

type NewProjectInput struct {
	Name string
	Key  string

	RepoRoot string
	Layout   repo.Layout

	WorktreesDir string
	WorkersDir   string

	Config repo.Config
	DB     *gorm.DB
	Logger *slog.Logger

	Tmux          infra.TmuxClient
	WorkerRuntime infra.WorkerRuntime
	Git           infra.GitClient
	TaskRuntime   TaskRuntimeFactory
}

func NewProject(in NewProjectInput) (*Project, error) {
	p := &Project{
		Name:          strings.TrimSpace(in.Name),
		Key:           strings.TrimSpace(in.Key),
		RepoRoot:      strings.TrimSpace(in.RepoRoot),
		Layout:        in.Layout,
		WorktreesDir:  strings.TrimSpace(in.WorktreesDir),
		WorkersDir:    strings.TrimSpace(in.WorkersDir),
		Config:        in.Config,
		DB:            in.DB,
		Logger:        in.Logger,
		Tmux:          in.Tmux,
		WorkerRuntime: in.WorkerRuntime,
		Git:           in.Git,
		TaskRuntime:   in.TaskRuntime,
	}
	p.Layout = normalizeLayout(p.Layout, p.RepoRoot)
	if strings.TrimSpace(p.RepoRoot) == "" {
		p.RepoRoot = strings.TrimSpace(p.Layout.RepoRoot)
	}
	if strings.TrimSpace(p.WorkersDir) == "" {
		p.WorkersDir = strings.TrimSpace(p.Layout.RuntimeWorkersDir)
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Project) Validate() error {
	if p == nil {
		return fmt.Errorf("project 不能为空")
	}
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("project name 不能为空")
	}
	if strings.TrimSpace(p.Key) == "" {
		return fmt.Errorf("project key 不能为空")
	}
	if strings.TrimSpace(p.RepoRoot) == "" {
		return fmt.Errorf("project repo_root 不能为空")
	}
	layout := p.effectiveLayout()
	if strings.TrimSpace(layout.ProjectDir) == "" ||
		strings.TrimSpace(layout.ConfigPath) == "" ||
		strings.TrimSpace(layout.DBPath) == "" {
		return fmt.Errorf("project layout 不完整")
	}
	if p.DB == nil {
		return fmt.Errorf("project DB 不能为空")
	}
	if p.Logger == nil {
		return fmt.Errorf("project Logger 不能为空")
	}
	if p.Tmux == nil {
		return fmt.Errorf("project Tmux 不能为空")
	}
	if p.WorkerRuntime == nil {
		return fmt.Errorf("project WorkerRuntime 不能为空")
	}
	if p.Git == nil {
		return fmt.Errorf("project Git 不能为空")
	}
	if p.TaskRuntime == nil {
		return fmt.Errorf("project TaskRuntime 不能为空")
	}
	return nil
}

func (p *Project) ProjectDir() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.effectiveLayout().ProjectDir)
}

func (p *Project) ConfigPath() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.effectiveLayout().ConfigPath)
}

func (p *Project) DBPath() string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(p.effectiveLayout().DBPath)
}

func (p *Project) effectiveLayout() repo.Layout {
	if p == nil {
		return repo.Layout{}
	}
	return normalizeLayout(p.Layout, p.RepoRoot)
}

func normalizeLayout(layout repo.Layout, repoRoot string) repo.Layout {
	root := strings.TrimSpace(layout.RepoRoot)
	if root == "" {
		root = strings.TrimSpace(repoRoot)
	}
	if root == "" {
		return layout
	}
	base := repo.NewLayout(root)
	if strings.TrimSpace(layout.RepoRoot) == "" {
		layout.RepoRoot = base.RepoRoot
	}
	if strings.TrimSpace(layout.ProjectDir) == "" {
		layout.ProjectDir = base.ProjectDir
	}
	if strings.TrimSpace(layout.ConfigPath) == "" {
		layout.ConfigPath = base.ConfigPath
	}
	if strings.TrimSpace(layout.DBPath) == "" {
		layout.DBPath = base.DBPath
	}
	if strings.TrimSpace(layout.RuntimeWorkersDir) == "" {
		layout.RuntimeWorkersDir = base.RuntimeWorkersDir
	}
	return layout
}
