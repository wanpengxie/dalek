package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestControlSeed_ProjectEntrypointInjectTemplateAvailableForTests(t *testing.T) {
	// 防止关键入口注入模板被误删/重命名。
	got := MustReadSeedTemplate("templates/project/ENTRYPOINT_INJECT.md")
	if strings.TrimSpace(got) == "" {
		t.Fatalf("expected non-empty project entrypoint inject template")
	}
}

func TestControlSeed_SkillTemplatesAvailableForTests(t *testing.T) {
	paths := []string{
		"templates/project/control/worker/worker-kernel.md",
		"templates/project/control/worker/state.json",
		"templates/project/control/skills/notebook-shaping/SKILL.md",
		"templates/project/control/skills/project-init/SKILL.md",
	}
	for _, p := range paths {
		got := MustReadSeedTemplate(p)
		if strings.TrimSpace(got) == "" {
			t.Fatalf("expected non-empty template: %s", p)
		}
	}
}

func TestEnsureControlPlaneSeed_RenderProjectIdentityInAgentUserTemplate(t *testing.T) {
	repoRoot := t.TempDir()
	layout := NewLayout(repoRoot)

	projectName := "alpha"
	if err := EnsureControlPlaneSeed(layout, projectName); err != nil {
		t.Fatalf("EnsureControlPlaneSeed failed: %v", err)
	}
	b, err := os.ReadFile(layout.ProjectAgentUserPath)
	if err != nil {
		t.Fatalf("read agent-user.md failed: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "<name>alpha</name>") {
		t.Fatalf("agent-user.md should include rendered project_name, got: %s", got)
	}
	if !strings.Contains(got, "<repo_root>"+repoRoot+"</repo_root>") {
		t.Fatalf("agent-user.md should include rendered repo_root")
	}
	if !strings.Contains(got, "<owner>") {
		t.Fatalf("agent-user.md should include owner field")
	}
	if strings.Contains(got, "{{.ProjectName}}") ||
		strings.Contains(got, "{{.ProjectKey}}") ||
		strings.Contains(got, "{{.ProjectOwner}}") ||
		strings.Contains(got, "{{.RepoRoot}}") {
		t.Fatalf("agent-user.md should not contain unresolved template placeholders")
	}

	if _, err := os.Stat(layout.ProjectAgentKernelPath); err != nil {
		t.Fatalf("agent-kernel.md should exist, err=%v", err)
	}

	bootstrapPath := layout.ProjectBootstrapPath
	info, err := os.Stat(bootstrapPath)
	if err != nil {
		t.Fatalf("bootstrap placeholder should exist, err=%v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("bootstrap placeholder should be executable, mode=%v", info.Mode())
	}

	if _, err := os.Stat(layout.PMDir); err != nil {
		t.Fatalf("pm dir should exist, err=%v", err)
	}
}

func TestPlanControlPlaneSeedUpdate_DetectSkillDrift(t *testing.T) {
	repoRoot := t.TempDir()
	layout := NewLayout(repoRoot)
	if err := EnsureControlPlaneSeed(layout, "alpha"); err != nil {
		t.Fatalf("EnsureControlPlaneSeed failed: %v", err)
	}

	skillPath := filepath.Join(layout.ControlSkillsDir, "project-init", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("custom-skill-content\n"), 0o644); err != nil {
		t.Fatalf("write drift skill failed: %v", err)
	}

	changes, err := PlanControlPlaneSeedUpdate(layout, "alpha", false)
	if err != nil {
		t.Fatalf("PlanControlPlaneSeedUpdate failed: %v", err)
	}
	found := false
	for _, change := range changes {
		if change.Path == skillPath && change.Action == "update" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected drifted skill in plan changes, got=%+v", changes)
	}
}

func TestUpdateControlPlaneSeed_OverwriteSkillsKeepKnowledge(t *testing.T) {
	repoRoot := t.TempDir()
	layout := NewLayout(repoRoot)
	if err := EnsureControlPlaneSeed(layout, "alpha"); err != nil {
		t.Fatalf("EnsureControlPlaneSeed failed: %v", err)
	}

	skillPath := filepath.Join(layout.ControlSkillsDir, "project-init", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("local-drift\n"), 0o644); err != nil {
		t.Fatalf("write local drift failed: %v", err)
	}
	knowledgePath := filepath.Join(layout.ControlKnowledgeDir, "custom.md")
	if err := os.MkdirAll(filepath.Dir(knowledgePath), 0o755); err != nil {
		t.Fatalf("mkdir knowledge dir failed: %v", err)
	}
	if err := os.WriteFile(knowledgePath, []byte("user-knowledge\n"), 0o644); err != nil {
		t.Fatalf("write knowledge failed: %v", err)
	}
	changes, err := UpdateControlPlaneSeed(layout, "alpha", false)
	if err != nil {
		t.Fatalf("UpdateControlPlaneSeed failed: %v", err)
	}

	gotSkill, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill failed: %v", err)
	}
	wantSkill := MustReadSeedTemplate("templates/project/control/skills/project-init/SKILL.md")
	if string(gotSkill) != wantSkill {
		t.Fatalf("skill should be overwritten by template")
	}

	gotKnowledge, err := os.ReadFile(knowledgePath)
	if err != nil {
		t.Fatalf("read knowledge failed: %v", err)
	}
	if string(gotKnowledge) != "user-knowledge\n" {
		t.Fatalf("knowledge should stay untouched, got=%q", string(gotKnowledge))
	}
	foundSkillUpdate := false
	for _, change := range changes {
		if change.Path == skillPath && change.Action == "update" {
			foundSkillUpdate = true
		}
	}
	if !foundSkillUpdate {
		t.Fatalf("expected skill update in changes, got=%+v", changes)
	}
}

func TestPlanControlPlaneSeedUpdate_NonForceSkipsAgentUserAndBootstrapDrift(t *testing.T) {
	repoRoot := t.TempDir()
	layout := NewLayout(repoRoot)
	if err := EnsureControlPlaneSeed(layout, "alpha"); err != nil {
		t.Fatalf("EnsureControlPlaneSeed failed: %v", err)
	}

	if err := os.WriteFile(layout.ProjectAgentUserPath, []byte("local-user\n"), 0o644); err != nil {
		t.Fatalf("write agent-user drift failed: %v", err)
	}
	if err := os.WriteFile(layout.ProjectBootstrapPath, []byte("#!/usr/bin/env bash\necho local\n"), 0o755); err != nil {
		t.Fatalf("write bootstrap drift failed: %v", err)
	}

	nonForceChanges, err := PlanControlPlaneSeedUpdate(layout, "alpha", false)
	if err != nil {
		t.Fatalf("PlanControlPlaneSeedUpdate non-force failed: %v", err)
	}
	for _, change := range nonForceChanges {
		if change.Path == layout.ProjectAgentUserPath || change.Path == layout.ProjectBootstrapPath {
			t.Fatalf("non-force plan should not include agent-user/bootstrap drift, got=%+v", nonForceChanges)
		}
	}

	forceChanges, err := PlanControlPlaneSeedUpdate(layout, "alpha", true)
	if err != nil {
		t.Fatalf("PlanControlPlaneSeedUpdate force failed: %v", err)
	}
	foundUser := false
	foundBootstrap := false
	for _, change := range forceChanges {
		if change.Path == layout.ProjectAgentUserPath && change.Action == "update" {
			foundUser = true
		}
		if change.Path == layout.ProjectBootstrapPath && change.Action == "update" {
			foundBootstrap = true
		}
	}
	if !foundUser || !foundBootstrap {
		t.Fatalf("force plan should include agent-user/bootstrap drift, got=%+v", forceChanges)
	}
}

func TestUpdateControlPlaneSeed_NonForceKeepsAgentUserAndBootstrap(t *testing.T) {
	repoRoot := t.TempDir()
	layout := NewLayout(repoRoot)
	if err := EnsureControlPlaneSeed(layout, "alpha"); err != nil {
		t.Fatalf("EnsureControlPlaneSeed failed: %v", err)
	}

	const wantUser = "local-user\n"
	const wantBootstrap = "#!/usr/bin/env bash\necho local\n"
	if err := os.WriteFile(layout.ProjectAgentUserPath, []byte(wantUser), 0o644); err != nil {
		t.Fatalf("write agent-user drift failed: %v", err)
	}
	if err := os.WriteFile(layout.ProjectBootstrapPath, []byte(wantBootstrap), 0o755); err != nil {
		t.Fatalf("write bootstrap drift failed: %v", err)
	}

	changes, err := UpdateControlPlaneSeed(layout, "alpha", false)
	if err != nil {
		t.Fatalf("UpdateControlPlaneSeed non-force failed: %v", err)
	}
	for _, change := range changes {
		if change.Path == layout.ProjectAgentUserPath || change.Path == layout.ProjectBootstrapPath {
			t.Fatalf("non-force update should not report agent-user/bootstrap changes, got=%+v", changes)
		}
	}

	gotUser, err := os.ReadFile(layout.ProjectAgentUserPath)
	if err != nil {
		t.Fatalf("read agent-user failed: %v", err)
	}
	if string(gotUser) != wantUser {
		t.Fatalf("agent-user should stay untouched in non-force mode, got=%q", string(gotUser))
	}
	gotBootstrap, err := os.ReadFile(layout.ProjectBootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap failed: %v", err)
	}
	if string(gotBootstrap) != wantBootstrap {
		t.Fatalf("bootstrap should stay untouched in non-force mode, got=%q", string(gotBootstrap))
	}

	if _, err := UpdateControlPlaneSeed(layout, "alpha", true); err != nil {
		t.Fatalf("UpdateControlPlaneSeed force failed: %v", err)
	}
	gotUser, err = os.ReadFile(layout.ProjectAgentUserPath)
	if err != nil {
		t.Fatalf("read agent-user after force failed: %v", err)
	}
	if string(gotUser) == wantUser {
		t.Fatalf("force update should overwrite agent-user")
	}
	gotBootstrap, err = os.ReadFile(layout.ProjectBootstrapPath)
	if err != nil {
		t.Fatalf("read bootstrap after force failed: %v", err)
	}
	if string(gotBootstrap) == wantBootstrap {
		t.Fatalf("force update should overwrite bootstrap")
	}
}
