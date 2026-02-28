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

func EnsureControlPlaneSeed(layout Layout, projectName string) error {
	if strings.TrimSpace(layout.ProjectDir) == "" {
		return fmt.Errorf("project_dir 为空")
	}

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
	if err := seedControlSkillsTemplates(layout); err != nil {
		return err
	}

	if _, err := writeFileIfMissing(layout.ProjectAgentKernelPath, defaultControlProjectAgentKernelTemplate(layout, projectName), 0o644); err != nil {
		return err
	}
	if _, err := writeFileIfMissing(layout.ProjectAgentUserPath, defaultControlProjectAgentUserTemplate(layout, projectName), 0o644); err != nil {
		return err
	}
	if err := ensureProjectBootstrap(layout); err != nil {
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

func ensureProjectBootstrap(layout Layout) error {
	current := strings.TrimSpace(layout.ProjectBootstrapPath)
	if current == "" {
		return fmt.Errorf("project_bootstrap_path 为空")
	}
	if st, err := os.Stat(current); err == nil {
		if st.IsDir() {
			return fmt.Errorf("bootstrap 路径是目录: %s", current)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("检查 bootstrap 脚本失败(%s): %w", current, err)
	}

	legacy := strings.TrimSpace(layout.ProjectLegacyBootstrapPath)
	if legacy != "" {
		if st, err := os.Stat(legacy); err == nil {
			if !st.IsDir() {
				raw, readErr := os.ReadFile(legacy)
				if readErr != nil {
					return fmt.Errorf("读取 legacy bootstrap 失败(%s): %w", legacy, readErr)
				}
				mode := st.Mode() & os.ModePerm
				if mode == 0 {
					mode = 0o755
				}
				if _, writeErr := writeFileIfMissing(current, string(raw), mode); writeErr != nil {
					return fmt.Errorf("迁移 legacy bootstrap 失败(%s -> %s): %w", legacy, current, writeErr)
				}
				return nil
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("检查 legacy bootstrap 失败(%s): %w", legacy, err)
		}
	}

	if _, err := writeFileIfMissing(current, defaultProjectBootstrapTemplate(), 0o755); err != nil {
		return err
	}
	return nil
}

func seedControlSkillsTemplates(layout Layout) error {
	if strings.TrimSpace(layout.ControlSkillsDir) == "" {
		return fmt.Errorf("control_skills_dir 为空")
	}
	const templateRoot = "templates/project/control/skills"
	return fs.WalkDir(seedTemplateFS, templateRoot, func(path string, d fs.DirEntry, walkErr error) error {
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

		// embed.FS 对所有文件一律返回 0444 权限（只读文件系统），
		// 不能直接信任。根据文件后缀判断：.sh/.py 给可执行权限，其余给 0644。
		mode := os.FileMode(0o644)
		if strings.HasSuffix(rel, ".sh") || strings.HasSuffix(rel, ".py") {
			mode = 0o755
		}
		raw, err := seedTemplateFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("读取 skills 模板失败(%s): %w", path, err)
		}
		if _, err := writeFileIfMissing(target, string(raw), mode); err != nil {
			return fmt.Errorf("写入 skills 模板失败(%s): %w", target, err)
		}
		return nil
	})
}
