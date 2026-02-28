package repo

import (
	"os"
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
		"templates/project/control/skills/dispatch-new-ticket/SKILL.md",
		"templates/project/control/skills/dispatch-new-ticket/references/output-contract.md",
		"templates/project/control/skills/dispatch-new-ticket/assets/worker-agents.md.template",
		"templates/project/control/skills/dispatch-new-ticket/scripts/initialize_copy.py",
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
}
