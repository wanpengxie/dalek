package repo

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/infra"
)

type ControlPlaneChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

func EnsureControlPlaneSeed(layout Layout, projectName string) error {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return fmt.Errorf("project_dir 为空")
	}
	if err := ensureControlPlaneDirs(layout); err != nil {
		return err
	}
	if _, err := seedControlSkillsTemplates(layout, false); err != nil {
		return err
	}

	if _, err := writeFileIfMissing(layout.ProjectAgentKernelPath, defaultControlProjectAgentKernelTemplate(layout, projectName), 0o644); err != nil {
		return err
	}
	if _, err := writeFileIfMissing(layout.ProjectAgentUserPath, defaultControlProjectAgentUserTemplate(layout, projectName), 0o644); err != nil {
		return err
	}
	if _, err := ensureProjectBootstrap(layout); err != nil {
		return err
	}
	for _, line := range []string{
		"runtime/",
		".dalek_project_name",
		".dalek_repo_path",
		".dalek_bin_path",
		".dalek_project.json",
	} {
		if err := infra.EnsureLineInFile(layout.ProjectGitignorePath, line); err != nil {
			return err
		}
	}
	return nil
}

func PlanControlPlaneSeedUpdate(layout Layout, projectName string) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return nil, fmt.Errorf("project_dir 为空")
	}
	changes := make([]ControlPlaneChange, 0)

	skillChanges, err := planControlSkillsTemplateChanges(layout)
	if err != nil {
		return nil, err
	}
	changes = append(changes, skillChanges...)

	if differs, err := fileContentDiffers(layout.ProjectAgentKernelPath, defaultControlProjectAgentKernelTemplate(layout, projectName)); err != nil {
		return nil, err
	} else if differs {
		action := "create"
		if _, statErr := os.Stat(layout.ProjectAgentKernelPath); statErr == nil {
			action = "update"
		}
		changes = append(changes, ControlPlaneChange{Path: layout.ProjectAgentKernelPath, Action: action})
	}
	if missing, err := fileMissing(layout.ProjectAgentUserPath); err != nil {
		return nil, err
	} else if missing {
		changes = append(changes, ControlPlaneChange{Path: layout.ProjectAgentUserPath, Action: "create"})
	}
	if missing, err := fileMissing(layout.ProjectBootstrapPath); err != nil {
		return nil, err
	} else if missing {
		changes = append(changes, ControlPlaneChange{Path: layout.ProjectBootstrapPath, Action: "create"})
	}
	return changes, nil
}

func UpdateControlPlaneSeed(layout Layout, projectName string) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return nil, fmt.Errorf("project_dir 为空")
	}
	if err := ensureControlPlaneDirs(layout); err != nil {
		return nil, err
	}
	changes, err := seedControlSkillsTemplates(layout, true)
	if err != nil {
		return nil, err
	}

	{
		kernelExisted := true
		if _, statErr := os.Stat(layout.ProjectAgentKernelPath); statErr != nil {
			kernelExisted = false
		}
		if changed, err := writeFileForce(layout.ProjectAgentKernelPath, defaultControlProjectAgentKernelTemplate(layout, projectName), 0o644); err != nil {
			return nil, err
		} else if changed {
			action := "create"
			if kernelExisted {
				action = "update"
			}
			changes = append(changes, ControlPlaneChange{Path: layout.ProjectAgentKernelPath, Action: action})
		}
	}
	if created, err := writeFileIfMissing(layout.ProjectAgentUserPath, defaultControlProjectAgentUserTemplate(layout, projectName), 0o644); err != nil {
		return nil, err
	} else if created {
		changes = append(changes, ControlPlaneChange{Path: layout.ProjectAgentUserPath, Action: "create"})
	}
	if created, err := ensureProjectBootstrap(layout); err != nil {
		return nil, err
	} else if created {
		changes = append(changes, ControlPlaneChange{Path: layout.ProjectBootstrapPath, Action: "create"})
	}

	for _, line := range []string{
		"runtime/",
		".dalek_project_name",
		".dalek_repo_path",
		".dalek_bin_path",
		".dalek_project.json",
	} {
		if err := infra.EnsureLineInFile(layout.ProjectGitignorePath, line); err != nil {
			return nil, err
		}
	}
	return changes, nil
}

func CurrentControlVersion(ctx context.Context, repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return "unknown"
	}
	if ctx == nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
	}

	sha := "unknown"
	if out, err := infra.Run(ctx, repoRoot, "git", "rev-parse", "HEAD"); err == nil {
		v := strings.TrimSpace(out)
		if v != "" {
			sha = v
		}
	}

	dirty := false
	if out, err := infra.Run(ctx, repoRoot, "git", "status", "--porcelain", "--", ".dalek/control"); err == nil {
		dirty = strings.TrimSpace(out) != ""
	}
	if dirty {
		return sha + "+dirty"
	}
	return sha
}

func defaultControlProjectAgentKernelTemplate(_ Layout, _ string) string {
	return mustReadSeedTemplate("templates/project/agent-kernel.md")
}

func defaultControlProjectAgentUserTemplate(layout Layout, projectName string) string {
	name := strings.TrimSpace(projectName)
	if name == "" {
		name = DeriveProjectName(layout.RepoRoot)
	}
	if name == "" {
		name = "-"
	}
	repoRoot := strings.TrimSpace(layout.RepoRoot)
	if repoRoot == "" {
		repoRoot = "-"
	}
	projectKey := "-"
	if strings.TrimSpace(layout.RepoRoot) != "" {
		projectKey = ProjectKey(layout.RepoRoot)
	}
	projectOwner := deriveProjectOwner(layout.RepoRoot)
	return mustRenderSeedTemplate("templates/project/agent-user.md", map[string]string{
		"ProjectName":  name,
		"ProjectKey":   projectKey,
		"ProjectOwner": projectOwner,
		"RepoRoot":     repoRoot,
	})
}

func deriveProjectOwner(repoRoot string) string {
	repoRoot = strings.TrimSpace(repoRoot)
	if repoRoot == "" {
		return "-"
	}
	clean := filepath.Clean(repoRoot)
	parts := strings.Split(clean, string(filepath.Separator))
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "Users" && strings.TrimSpace(parts[i+1]) != "" {
			return strings.TrimSpace(parts[i+1])
		}
	}
	if len(parts) >= 2 && strings.TrimSpace(parts[len(parts)-2]) != "" {
		return strings.TrimSpace(parts[len(parts)-2])
	}
	return "-"
}

func defaultProjectBootstrapTemplate() string {
	return mustReadSeedTemplate("templates/project/bootstrap.sh")
}

func ensureProjectBootstrap(layout Layout) (bool, error) {
	current := strings.TrimSpace(layout.ProjectBootstrapPath)
	if current == "" {
		return false, fmt.Errorf("project_bootstrap_path 为空")
	}
	if st, err := os.Stat(current); err == nil {
		if st.IsDir() {
			return false, fmt.Errorf("bootstrap 路径是目录: %s", current)
		}
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("检查 bootstrap 脚本失败(%s): %w", current, err)
	}

	changed, err := writeFileIfMissing(current, defaultProjectBootstrapTemplate(), 0o755)
	if err != nil {
		return false, err
	}
	return changed, nil
}

func ensureControlPlaneDirs(layout Layout) error {
	dirs := []string{
		layout.ControlWorkerDir,
		layout.ControlSkillsDir,
		layout.ControlKnowledgeDir,
		layout.ControlToolsDir,
		layout.RuntimeDir,
		layout.RuntimeWorkersDir,
		filepath.Join(layout.ControlSkillsDir, "dispatch-new-ticket"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建目录失败(%s): %w", dir, err)
		}
	}
	return nil
}

func seedControlSkillsTemplates(layout Layout, force bool) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ControlSkillsDir) == "" {
		return nil, fmt.Errorf("control_skills_dir 为空")
	}
	changes := make([]ControlPlaneChange, 0)
	const templateRoot = "templates/project/control/skills"
	err := fs.WalkDir(seedTemplateFS, templateRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == templateRoot {
			return nil
		}
		rel := strings.TrimPrefix(path, templateRoot+"/")
		target := filepath.Join(layout.ControlSkillsDir, filepath.FromSlash(rel))
		if d.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("创建 skills 目录失败(%s): %w", target, err)
			}
			return nil
		}

		mode := controlTemplateFileMode(rel)
		raw, err := seedTemplateFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 skills 模板失败(%s): %w", path, err)
		}
		existed := true
		if _, err := os.Stat(target); err != nil {
			if os.IsNotExist(err) {
				existed = false
			} else {
				return fmt.Errorf("检查 skills 目标文件失败(%s): %w", target, err)
			}
		}

		var changed bool
		if force {
			changed, err = writeFileForce(target, string(raw), mode)
		} else {
			changed, err = writeFileIfMissing(target, string(raw), mode)
		}
		if err != nil {
			return fmt.Errorf("写入 skills 模板失败(%s): %w", target, err)
		}
		if !changed {
			return nil
		}
		action := "create"
		if existed {
			action = "update"
		}
		changes = append(changes, ControlPlaneChange{Path: target, Action: action})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return changes, nil
}

func planControlSkillsTemplateChanges(layout Layout) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ControlSkillsDir) == "" {
		return nil, fmt.Errorf("control_skills_dir 为空")
	}
	changes := make([]ControlPlaneChange, 0)
	const templateRoot = "templates/project/control/skills"
	err := fs.WalkDir(seedTemplateFS, templateRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == templateRoot || d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, templateRoot+"/")
		target := filepath.Join(layout.ControlSkillsDir, filepath.FromSlash(rel))
		raw, err := seedTemplateFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 skills 模板失败(%s): %w", path, err)
		}
		local, exists, err := readFileStringIfExists(target)
		if err != nil {
			return err
		}
		if !exists {
			changes = append(changes, ControlPlaneChange{Path: target, Action: "create"})
			return nil
		}
		if local != string(raw) {
			changes = append(changes, ControlPlaneChange{Path: target, Action: "update"})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return changes, nil
}

func controlTemplateFileMode(rel string) os.FileMode {
	if strings.HasSuffix(rel, ".sh") || strings.HasSuffix(rel, ".py") {
		return 0o755
	}
	return 0o644
}

func fileMissing(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return false, nil
	}
	if os.IsNotExist(err) {
		return true, nil
	}
	return false, err
}

func fileContentDiffers(path, expected string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("读取文件失败(%s): %w", path, err)
	}
	return string(raw) != expected, nil
}

func readFileStringIfExists(path string) (string, bool, error) {
	raw, err := os.ReadFile(path)
	if err == nil {
		return string(raw), true, nil
	}
	if os.IsNotExist(err) {
		return "", false, nil
	}
	return "", false, fmt.Errorf("读取文件失败(%s): %w", path, err)
}
