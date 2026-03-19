package app

import (
	"os"
	"path/filepath"
	"testing"

	"dalek/internal/repo"
)

func TestCollectBackupSources_NonForceSkipsUserOwnedPaths(t *testing.T) {
	repoRoot := t.TempDir()
	layout := repo.NewLayout(repoRoot)

	mustWrite := func(path, content string, perm os.FileMode) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir failed(%s): %v", path, err)
		}
		if err := os.WriteFile(path, []byte(content), perm); err != nil {
			t.Fatalf("write failed(%s): %v", path, err)
		}
	}

	mustWrite(layout.DBPath, "db", 0o644)
	mustWrite(layout.ConfigPath, "{}", 0o644)
	mustWrite(repo.ProjectMetaPath(layout), "{}", 0o644)
	mustWrite(filepath.Join(repoRoot, "AGENTS.md"), "agents", 0o644)
	mustWrite(filepath.Join(repoRoot, "CLAUDE.md"), "claude", 0o644)
	mustWrite(layout.ProjectGitignorePath, ".dalek_project.json\n", 0o644)
	mustWrite(filepath.Join(repoRoot, ".gitignore"), ".dalek/\n", 0o644)
	mustWrite(filepath.Join(layout.ControlWorkerDir, "worker-kernel.md"), "worker", 0o644)
	mustWrite(filepath.Join(layout.ControlSkillsDir, "project-init", "SKILL.md"), "skill", 0o644)
	mustWrite(filepath.Join(layout.ControlKnowledgeDir, "custom.md"), "knowledge", 0o644)
	mustWrite(filepath.Join(layout.PMDir, "plan.md"), "pm", 0o644)
	mustWrite(layout.ProjectAgentKernelPath, "kernel", 0o644)
	mustWrite(layout.ProjectAgentUserPath, "user", 0o644)
	mustWrite(layout.ProjectBootstrapPath, "#!/usr/bin/env bash\n", 0o755)

	nonForce := collectBackupSources(layout, false)
	force := collectBackupSources(layout, true)

	assertContains := func(paths []string, want string) {
		t.Helper()
		for _, path := range paths {
			if path == want {
				return
			}
		}
		t.Fatalf("expected path %s in %+v", want, paths)
	}
	assertNotContains := func(paths []string, want string) {
		t.Helper()
		for _, path := range paths {
			if path == want {
				t.Fatalf("did not expect path %s in %+v", want, paths)
			}
		}
	}

	assertContains(nonForce, layout.ProjectAgentKernelPath)
	assertContains(nonForce, filepath.Join(layout.ControlWorkerDir, "worker-kernel.md"))
	assertContains(nonForce, filepath.Join(layout.ControlSkillsDir, "project-init", "SKILL.md"))
	assertNotContains(nonForce, layout.ProjectAgentUserPath)
	assertNotContains(nonForce, layout.ProjectBootstrapPath)
	assertNotContains(nonForce, filepath.Join(layout.ControlKnowledgeDir, "custom.md"))
	assertNotContains(nonForce, filepath.Join(layout.PMDir, "plan.md"))

	assertContains(force, layout.ProjectAgentUserPath)
	assertContains(force, layout.ProjectBootstrapPath)
	assertNotContains(force, filepath.Join(layout.ControlKnowledgeDir, "custom.md"))
	assertNotContains(force, filepath.Join(layout.PMDir, "plan.md"))
}
