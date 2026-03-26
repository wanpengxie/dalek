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
	if _, err := ensureControlWorkerTemplates(layout, false); err != nil {
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
		"backup/",
		".dalek_project_name",
		".dalek_repo_path",
		".dalek_bin_path",
		".dalek_project.json",
	} {
		if err := infra.EnsureLineInFile(layout.ProjectGitignorePath, line); err != nil {
			return err
		}
	}
	// 将 .dalek/ 添加到 repo 根目录 .gitignore，防止 worktree checkout 带入 PM kernel
	repoGitignore := filepath.Join(layout.RepoRoot, ".gitignore")
	if err := infra.EnsureLineInFile(repoGitignore, ".dalek/"); err != nil {
		return err
	}
	return nil
}

func PlanControlPlaneSeedUpdate(layout Layout, projectName string, force bool) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return nil, fmt.Errorf("project_dir 为空")
	}
	changes := make([]ControlPlaneChange, 0)

	workerChanges, err := planControlWorkerTemplateChanges(layout)
	if err != nil {
		return nil, err
	}
	changes = append(changes, workerChanges...)

	skillChanges, err := planControlSkillsTemplateChanges(layout)
	if err != nil {
		return nil, err
	}
	changes = append(changes, skillChanges...)

	planSpecs := []struct {
		path        string
		content     string
		allowUpdate bool
	}{
		{path: layout.ProjectAgentKernelPath, content: defaultControlProjectAgentKernelTemplate(layout, projectName), allowUpdate: true},
		{path: layout.ProjectAgentUserPath, content: defaultControlProjectAgentUserTemplate(layout, projectName), allowUpdate: force},
		{path: layout.ProjectBootstrapPath, content: defaultProjectBootstrapTemplate(), allowUpdate: force},
	}
	for _, spec := range planSpecs {
		change, err := planTemplateFileChange(spec.path, spec.content, spec.allowUpdate)
		if err != nil {
			return nil, err
		}
		if change != nil {
			changes = append(changes, *change)
		}
	}
	return changes, nil
}

func UpdateControlPlaneSeed(layout Layout, projectName string, force bool) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return nil, fmt.Errorf("project_dir 为空")
	}
	if err := ensureControlPlaneDirs(layout); err != nil {
		return nil, err
	}
	changes, err := ensureControlWorkerTemplates(layout, true)
	if err != nil {
		return nil, err
	}
	skillChanges, err := seedControlSkillsTemplates(layout, true)
	if err != nil {
		return nil, err
	}
	changes = append(changes, skillChanges...)

	forceSpecs := []struct {
		path        string
		content     string
		mode        os.FileMode
		allowUpdate bool
	}{
		{path: layout.ProjectAgentKernelPath, content: defaultControlProjectAgentKernelTemplate(layout, projectName), mode: 0o644, allowUpdate: true},
		{path: layout.ProjectAgentUserPath, content: defaultControlProjectAgentUserTemplate(layout, projectName), mode: 0o644, allowUpdate: force},
		{path: layout.ProjectBootstrapPath, content: defaultProjectBootstrapTemplate(), mode: 0o755, allowUpdate: force},
	}
	for _, spec := range forceSpecs {
		change, writeErr := applyTemplateFileChange(spec.path, spec.content, spec.mode, spec.allowUpdate)
		if writeErr != nil {
			return nil, writeErr
		}
		if change != nil {
			changes = append(changes, *change)
		}
	}

	for _, line := range []string{
		"runtime/",
		"backup/",
		".dalek_project_name",
		".dalek_repo_path",
		".dalek_bin_path",
		".dalek_project.json",
	} {
		if err := infra.EnsureLineInFile(layout.ProjectGitignorePath, line); err != nil {
			return nil, err
		}
	}
	// 将 .dalek/ 添加到 repo 根目录 .gitignore，防止 worktree checkout 带入 PM kernel
	repoGitignore := filepath.Join(layout.RepoRoot, ".gitignore")
	if err := infra.EnsureLineInFile(repoGitignore, ".dalek/"); err != nil {
		return nil, err
	}
	return changes, nil
}

// UpdateKernelSeed updates only agent-kernel.md (always force).
func UpdateKernelSeed(layout Layout, projectName string) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return nil, fmt.Errorf("project_dir 为空")
	}
	change, err := applyTemplateFileChange(
		layout.ProjectAgentKernelPath,
		defaultControlProjectAgentKernelTemplate(layout, projectName),
		0o644,
		true,
	)
	if err != nil {
		return nil, err
	}
	if change != nil {
		return []ControlPlaneChange{*change}, nil
	}
	return nil, nil
}

// PlanKernelSeedUpdate previews changes for kernel scope.
func PlanKernelSeedUpdate(layout Layout, projectName string) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return nil, fmt.Errorf("project_dir 为空")
	}
	change, err := planTemplateFileChange(
		layout.ProjectAgentKernelPath,
		defaultControlProjectAgentKernelTemplate(layout, projectName),
		true,
	)
	if err != nil {
		return nil, err
	}
	if change != nil {
		return []ControlPlaneChange{*change}, nil
	}
	return nil, nil
}

// UpdateControlTemplates updates only control/worker and control/skills (always force).
func UpdateControlTemplates(layout Layout) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return nil, fmt.Errorf("project_dir 为空")
	}
	if err := ensureControlPlaneDirs(layout); err != nil {
		return nil, err
	}
	changes, err := ensureControlWorkerTemplates(layout, true)
	if err != nil {
		return nil, err
	}
	skillChanges, err := seedControlSkillsTemplates(layout, true)
	if err != nil {
		return nil, err
	}
	return append(changes, skillChanges...), nil
}

// PlanControlTemplatesUpdate previews changes for control scope.
func PlanControlTemplatesUpdate(layout Layout) ([]ControlPlaneChange, error) {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return nil, fmt.Errorf("project_dir 为空")
	}
	changes, err := planControlWorkerTemplateChanges(layout)
	if err != nil {
		return nil, err
	}
	skillChanges, err := planControlSkillsTemplateChanges(layout)
	if err != nil {
		return nil, err
	}
	return append(changes, skillChanges...), nil
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
		layout.PMDir,
		layout.RuntimeDir,
		layout.RuntimeWorkersDir,
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

func planTemplateFileChange(path, content string, allowUpdate bool) (*ControlPlaneChange, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &ControlPlaneChange{Path: path, Action: "create"}, nil
		}
		return nil, fmt.Errorf("读取文件失败(%s): %w", path, err)
	}
	if string(raw) == content {
		return nil, nil
	}
	if !allowUpdate {
		return nil, nil
	}
	return &ControlPlaneChange{Path: path, Action: "update"}, nil
}

func applyTemplateFileChange(path, content string, perm os.FileMode, allowUpdate bool) (*ControlPlaneChange, error) {
	existed := true
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			existed = false
		} else {
			return nil, err
		}
	}
	var (
		changed bool
		err     error
	)
	if existed {
		if !allowUpdate {
			return nil, nil
		}
		changed, err = writeFileForce(path, content, perm)
	} else {
		changed, err = writeFileIfMissing(path, content, perm)
	}
	if err != nil {
		return nil, err
	}
	if !changed {
		return nil, nil
	}
	action := "create"
	if existed {
		action = "update"
	}
	return &ControlPlaneChange{Path: path, Action: action}, nil
}
