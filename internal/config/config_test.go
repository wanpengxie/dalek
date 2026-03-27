package config

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"dalek/internal/repo"
)

type fakeProject struct {
	configPath string
	maxRunning int
}

func (p *fakeProject) ConfigPath() string {
	if p == nil {
		return ""
	}
	return p.configPath
}

func (p *fakeProject) GetMaxRunningWorkers(ctx context.Context) (int, error) {
	_ = ctx
	if p == nil {
		return 0, nil
	}
	return p.maxRunning, nil
}

func (p *fakeProject) SetMaxRunningWorkers(ctx context.Context, n int) (int, error) {
	_ = ctx
	if p != nil {
		p.maxRunning = n
	}
	return n, nil
}

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
  "worker_agent": {"provider": "codex"},
  "pm_agent": {"provider": "claude"}
}`
	if err := os.WriteFile(localPath, []byte(localJSON), 0o644); err != nil {
		t.Fatalf("write local json failed: %v", err)
	}
	lp, err := LoadLocalConfigPresence(localPath)
	if err != nil {
		t.Fatalf("LoadLocalConfigPresence failed: %v", err)
	}
	// v3: model 不再是 role-level 字段，AgentModel 始终 false
	if !lp.AgentProvider || lp.AgentModel {
		t.Fatalf("unexpected local presence: %+v", lp)
	}
}

func TestResolveValue(t *testing.T) {
	t.Parallel()
	defaultHome := HomeConfig{}

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
		homeCfg := HomeConfig{}
		homeCfg.Agent.Provider = "claude"
		homeCfg.Agent.Model = "claude-3-7-sonnet"
		localCfg := repo.Config{}.WithDefaults()
		localCfg.WorkerAgent.Provider = "claude"
		localCfg.PMAgent.Provider = "claude"
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
	var written HomeConfig
	ctx := &SetContext{
		HomeConfigPath: path,
		HomeCfg:        HomeConfig{},
		WriteHomeConfig: func(path string, cfg HomeConfig) error {
			written = cfg.WithDefaults()
			return nil
		},
	}
	v, err := SetValue(ctx, ConfigKeyDaemonMaxConcurrent, ScopeGlobal, "8")
	if err != nil {
		t.Fatalf("SetValue failed: %v", err)
	}
	if v != "8" {
		t.Fatalf("unexpected normalized value: %s", v)
	}
	if got := written.WithDefaults().Daemon.MaxConcurrent; got != 8 {
		t.Fatalf("unexpected daemon.max_concurrent=%d", got)
	}
}

func TestSetValue_LocalWritesProjectConfig(t *testing.T) {
	repoRoot := initGitRepo(t)
	cfgPath := filepath.Join(repoRoot, ".dalek", "config.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := repo.WriteConfigAtomic(cfgPath, repo.Config{}.WithDefaults()); err != nil {
		t.Fatalf("WriteConfigAtomic failed: %v", err)
	}
	p := &fakeProject{configPath: cfgPath, maxRunning: 4}
	localCfg, err := LoadProjectConfigFromProject(p)
	if err != nil {
		t.Fatalf("LoadProjectConfigFromProject failed: %v", err)
	}

	ctx := &SetContext{
		Project:  p,
		LocalCfg: localCfg,
	}
	v, err := SetValue(ctx, ConfigKeyAgentProvider, ScopeLocal, "claude")
	if err != nil {
		t.Fatalf("SetValue local failed: %v", err)
	}
	if v != "claude" {
		t.Fatalf("unexpected normalized value: %s", v)
	}

	cfg, _, err := repo.LoadConfig(cfgPath)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	cfg = cfg.WithDefaults()
	if cfg.WorkerAgent.Provider != "claude" || cfg.PMAgent.Provider != "claude" {
		t.Fatalf("unexpected local providers: worker=%s pm=%s", cfg.WorkerAgent.Provider, cfg.PMAgent.Provider)
	}
}

func TestBuildEffectiveProjectConfig(t *testing.T) {
	t.Parallel()
	homeCfg := HomeConfig{}
	homeCfg.Agent.Provider = "claude"
	homeCfg.Agent.Model = "home-model"

	localCfg := repo.Config{}.WithDefaults()
	localCfg.WorkerAgent.Provider = "codex"
	localCfg.PMAgent.Provider = "codex"

	got := BuildEffectiveProjectConfig(homeCfg, localCfg)
	if got.WorkerAgent.Provider != "codex" || got.PMAgent.Provider != "codex" {
		t.Fatalf("unexpected provider merge result: worker=%s pm=%s", got.WorkerAgent.Provider, got.PMAgent.Provider)
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
