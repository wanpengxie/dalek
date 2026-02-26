package config

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dalek/internal/app"
	"dalek/internal/repo"
)

func TestLoadPresence(t *testing.T) {
	t.Parallel()
	homePath := filepath.Join(t.TempDir(), "home.json")
	homeJSON := `{
  "daemon": {
    "internal": {"listen": "127.0.0.1:19081"},
    "max_concurrent": 9
  },
  "agent": {"provider": "claude"}
}`
	if err := os.WriteFile(homePath, []byte(homeJSON), 0o644); err != nil {
		t.Fatalf("write home json failed: %v", err)
	}
	hp, err := LoadHomeConfigPresence(homePath)
	if err != nil {
		t.Fatalf("LoadHomeConfigPresence failed: %v", err)
	}
	if !hp.DaemonInternalListen || hp.DaemonPublicListen || !hp.DaemonMaxConcurrent || !hp.AgentProvider || hp.AgentModel {
		t.Fatalf("unexpected home presence: %+v", hp)
	}

	localPath := filepath.Join(t.TempDir(), "config.json")
	localJSON := `{
  "worker_agent": {"provider": "codex", "model": "gpt-5.3-codex"},
  "pm_agent": {"model": "gpt-5.3-codex"}
}`
	if err := os.WriteFile(localPath, []byte(localJSON), 0o644); err != nil {
		t.Fatalf("write local json failed: %v", err)
	}
	lp, err := LoadLocalConfigPresence(localPath)
	if err != nil {
		t.Fatalf("LoadLocalConfigPresence failed: %v", err)
	}
	if !lp.AgentProvider || !lp.AgentModel {
		t.Fatalf("unexpected local presence: %+v", lp)
	}
}

func TestResolveValue(t *testing.T) {
	t.Parallel()
	defaultHome := app.DefaultHomeConfig()

	t.Run("daemon.max_concurrent source from default/global", func(t *testing.T) {
		t.Parallel()
		eval := &EvalContext{HomeCfg: defaultHome}
		v, src, err := ResolveValue(ConfigKeyDaemonMaxConcurrent, eval)
		if err != nil {
			t.Fatalf("ResolveValue failed: %v", err)
		}
		if v != "4" || src != ScopeDefault {
			t.Fatalf("unexpected default value/src: value=%s src=%s", v, src)
		}

		eval.HomePresence = HomePresence{DaemonMaxConcurrent: true}
		v, src, err = ResolveValue(ConfigKeyDaemonMaxConcurrent, eval)
		if err != nil {
			t.Fatalf("ResolveValue failed: %v", err)
		}
		if v != "4" || src != ScopeGlobal {
			t.Fatalf("unexpected global value/src: value=%s src=%s", v, src)
		}
	})

	t.Run("agent.provider source precedence", func(t *testing.T) {
		t.Parallel()
		homeCfg := app.DefaultHomeConfig()
		homeCfg.Agent.Provider = "claude"
		homeCfg.Agent.Model = "claude-3-7-sonnet"
		localCfg := repo.Config{}.WithDefaults()
		localCfg.WorkerAgent.Provider = "claude"
		localCfg.PMAgent.Provider = "claude"
		localCfg.WorkerAgent.Model = "claude-3-7-sonnet"
		localCfg.PMAgent.Model = "claude-3-7-sonnet"
		eval := &EvalContext{
			HomeCfg:      homeCfg,
			HomePresence: HomePresence{AgentProvider: true},
			LocalCfg:     localCfg,
		}

		v, src, err := ResolveValue(ConfigKeyAgentProvider, eval)
		if err != nil {
			t.Fatalf("ResolveValue failed: %v", err)
		}
		if v != "claude" || src != ScopeGlobal {
			t.Fatalf("unexpected global provider/src: value=%s src=%s", v, src)
		}

		eval.LocalPresence = LocalPresence{AgentProvider: true}
		eval.LocalCfg.WorkerAgent.Provider = "codex"
		eval.LocalCfg.PMAgent.Provider = "codex"
		v, src, err = ResolveValue(ConfigKeyAgentProvider, eval)
		if err != nil {
			t.Fatalf("ResolveValue failed: %v", err)
		}
		if v != "codex" || src != ScopeLocal {
			t.Fatalf("unexpected local provider/src: value=%s src=%s", v, src)
		}
	})
}

func TestSetValue_Global(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "home", "config.json")
	ctx := &SetContext{
		Home:    &app.Home{ConfigPath: path},
		HomeCfg: app.DefaultHomeConfig(),
	}
	v, err := SetValue(ctx, ConfigKeyDaemonMaxConcurrent, ScopeGlobal, "8")
	if err != nil {
		t.Fatalf("SetValue failed: %v", err)
	}
	if v != "8" {
		t.Fatalf("unexpected normalized value: %s", v)
	}
	cfg, exists, _, err := app.LoadHomeConfig(path)
	if err != nil {
		t.Fatalf("LoadHomeConfig failed: %v", err)
	}
	if !exists {
		t.Fatalf("home config should exist after write")
	}
	if got := cfg.WithDefaults().Daemon.MaxConcurrent; got != 8 {
		t.Fatalf("unexpected daemon.max_concurrent=%d", got)
	}
}

func TestSetValue_LocalWritesProjectConfig(t *testing.T) {
	repoRoot := initGitRepo(t)
	homeDir := filepath.Join(t.TempDir(), "home")
	h, err := app.OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	p, err := h.InitProjectFromDir(repoRoot, "demo", repo.Config{})
	if err != nil {
		t.Fatalf("InitProjectFromDir failed: %v", err)
	}
	localCfg, err := LoadProjectConfigFromProject(p)
	if err != nil {
		t.Fatalf("LoadProjectConfigFromProject failed: %v", err)
	}

	ctx := &SetContext{
		Home:     h,
		HomeCfg:  h.Config.WithDefaults(),
		Project:  p,
		LocalCfg: localCfg,
	}
	v, err := SetValue(ctx, ConfigKeyAgentModel, ScopeLocal, "claude-3-7-sonnet")
	if err != nil {
		t.Fatalf("SetValue local failed: %v", err)
	}
	if v != "claude-3-7-sonnet" {
		t.Fatalf("unexpected normalized value: %s", v)
	}

	cfgPath := repo.NewLayout(p.RepoRoot()).ConfigPath
	cfg, _, err := repo.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	cfg = cfg.WithDefaults()
	if cfg.WorkerAgent.Model != "claude-3-7-sonnet" || cfg.PMAgent.Model != "claude-3-7-sonnet" {
		t.Fatalf("unexpected local models: worker=%s pm=%s", cfg.WorkerAgent.Model, cfg.PMAgent.Model)
	}
}

func TestBuildEffectiveProjectConfig(t *testing.T) {
	t.Parallel()
	homeCfg := app.DefaultHomeConfig()
	homeCfg.Agent.Provider = "claude"
	homeCfg.Agent.Model = "home-model"

	localCfg := repo.Config{}.WithDefaults()
	localCfg.WorkerAgent.Provider = "codex"
	localCfg.PMAgent.Provider = "codex"
	localCfg.WorkerAgent.Model = "local-model"
	localCfg.PMAgent.Model = "local-model"

	got := BuildEffectiveProjectConfig(homeCfg, localCfg)
	if got.WorkerAgent.Provider != "codex" || got.PMAgent.Provider != "codex" {
		t.Fatalf("unexpected provider merge result: worker=%s pm=%s", got.WorkerAgent.Provider, got.PMAgent.Provider)
	}
	if got.WorkerAgent.Model != "local-model" || got.PMAgent.Model != "local-model" {
		t.Fatalf("unexpected model merge result: worker=%s pm=%s", got.WorkerAgent.Model, got.PMAgent.Model)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	repoDir := t.TempDir()
	runCmdOK(t, repoDir, "git", "init")
	runCmdOK(t, repoDir, "git", "config", "user.email", "dalek-test@example.com")
	runCmdOK(t, repoDir, "git", "config", "user.name", "dalek-test")
	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README failed: %v", err)
	}
	runCmdOK(t, repoDir, "git", "add", "README.md")
	runCmdOK(t, repoDir, "git", "commit", "-m", "init")
	return repoDir
}

func runCmdOK(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("command failed: %s %v\nstdout:\n%s\nstderr:\n%s\nerr=%v", name, args, stdout.String(), stderr.String(), err)
	}
}
