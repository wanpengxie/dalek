package core

import (
	"dalek/internal/infra"
	"dalek/internal/repo"

	"gorm.io/gorm"
)

// Project 是 services 层的“项目上下文载体”（只承载资源与路径，不承载业务流程）。
// 业务流程必须落在 internal/services/* 的函数中。
type Project struct {
	Name string
	Key  string

	RepoRoot   string
	Layout     repo.Layout
	ProjectDir string
	ConfigPath string
	DBPath     string

	// WorktreesDir: <home>/worktrees/<name>/tickets
	WorktreesDir string
	WorkersDir   string

	Config repo.Config
	DB     *gorm.DB

	Tmux        infra.TmuxClient
	Git         infra.GitClient
	TaskRuntime TaskRuntimeFactory
}
