package repo

import (
	"path/filepath"
	"strings"
)

type Layout struct {
	RepoRoot   string
	ProjectDir string

	ControlDir             string
	ControlWorkerDir       string
	ControlSkillsDir       string
	ControlKnowledgeDir    string
	ControlToolsDir        string
	PMDir                  string
	PMArchiveDir           string
	BackupDir              string
	RuntimeDir             string
	RuntimeWorkersDir      string
	ConfigPath             string
	DBPath                 string
	ProjectGitignorePath   string
	ProjectAgentKernelPath string
	ProjectAgentUserPath   string
	ProjectBootstrapPath   string
}

func NewLayout(repoRoot string) Layout {
	repoRoot = strings.TrimSpace(repoRoot)
	projectDir := filepath.Join(repoRoot, ".dalek")
	controlDir := filepath.Join(projectDir, "control")
	pmDir := filepath.Join(projectDir, "pm")
	runtimeDir := filepath.Join(projectDir, "runtime")
	return Layout{
		RepoRoot:   repoRoot,
		ProjectDir: projectDir,

		ControlDir:          controlDir,
		ControlWorkerDir:    filepath.Join(controlDir, "worker"),
		ControlSkillsDir:    filepath.Join(controlDir, "skills"),
		ControlKnowledgeDir: filepath.Join(controlDir, "knowledge"),
		ControlToolsDir:     filepath.Join(controlDir, "tools"),

		PMDir:        pmDir,
		PMArchiveDir: filepath.Join(pmDir, "archive"),
		BackupDir:    filepath.Join(projectDir, "backup"),

		RuntimeDir:        runtimeDir,
		RuntimeWorkersDir: filepath.Join(runtimeDir, "workers"),

		ConfigPath:             filepath.Join(projectDir, "config.json"),
		DBPath:                 filepath.Join(runtimeDir, "dalek.sqlite3"),
		ProjectGitignorePath:   filepath.Join(projectDir, ".gitignore"),
		ProjectAgentKernelPath: filepath.Join(projectDir, "agent-kernel.md"),
		ProjectAgentUserPath:   filepath.Join(projectDir, "agent-user.md"),
		ProjectBootstrapPath:   filepath.Join(projectDir, "bootstrap.sh"),
	}
}
