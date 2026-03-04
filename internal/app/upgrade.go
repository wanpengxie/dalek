package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
	"dalek/internal/store"
)

type UpgradeOptions struct {
	ProjectName   string
	StartDir      string
	BinaryVersion string
	DryRun        bool
	Force         bool
}

type UpgradeChange struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

type UpgradeResult struct {
	Project                string          `json:"project"`
	RepoRoot               string          `json:"repo_root"`
	DryRun                 bool            `json:"dry_run"`
	Force                  bool            `json:"force"`
	AlreadyLatest          bool            `json:"already_latest"`
	PreviousVersion        string          `json:"previous_version"`
	TargetVersion          string          `json:"target_version"`
	AppliedVersion         string          `json:"applied_version"`
	DaemonRunning          bool            `json:"daemon_running"`
	RunningWorkers         int             `json:"running_workers"`
	ConfigSchemaVersion    int             `json:"config_schema_version"`
	MigrationVersion       int             `json:"migration_version"`
	LatestMigrationVersion int             `json:"latest_migration_version"`
	Changes                []UpgradeChange `json:"changes"`
	Backups                []string        `json:"backups"`
	Warnings               []string        `json:"warnings"`
}

type UpgradeFailure struct {
	Result UpgradeResult
	Err    error
}

func (e *UpgradeFailure) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *UpgradeFailure) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (h *Home) UpgradeProject(ctx context.Context, opt UpgradeOptions) (UpgradeResult, error) {
	if h == nil {
		return UpgradeResult{}, fmt.Errorf("home 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectRef, err := h.resolveUpgradeProject(opt.ProjectName, opt.StartDir)
	if err != nil {
		return UpgradeResult{}, err
	}
	layout := repo.NewLayout(projectRef.RepoRoot)
	if _, err := os.Stat(layout.ConfigPath); err != nil {
		if os.IsNotExist(err) {
			return UpgradeResult{}, ErrNotInitialized
		}
		return UpgradeResult{}, err
	}

	metaPath := repo.ProjectMetaPath(layout)
	meta, _, err := repo.LoadProjectMeta(metaPath)
	if err != nil {
		return UpgradeResult{}, err
	}

	res := UpgradeResult{
		Project:         projectRef.Name,
		RepoRoot:        projectRef.RepoRoot,
		DryRun:          opt.DryRun,
		Force:           opt.Force,
		PreviousVersion: strings.TrimSpace(meta.DalekVersion),
		TargetVersion:   repo.NormalizeDalekVersion(opt.BinaryVersion),
	}

	if !repo.ShouldUpgradeProject(res.PreviousVersion, res.TargetVersion, opt.Force) {
		res.AlreadyLatest = true
		res.AppliedVersion = strings.TrimSpace(res.PreviousVersion)
		return res, nil
	}

	if paths, err := h.ResolveDaemonPaths(); err == nil {
		if st, inspectErr := InspectDaemon(paths); inspectErr != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("读取 daemon 状态失败: %v", inspectErr))
		} else {
			res.DaemonRunning = st.Running
			if st.Running {
				res.Warnings = append(res.Warnings, "检测到 daemon 正在运行，建议升级后执行 `dalek daemon restart`。")
			}
		}
	} else {
		res.Warnings = append(res.Warnings, fmt.Sprintf("解析 daemon 配置失败: %v", err))
	}

	runningWorkers, err := countRunningWorkers(layout.DBPath)
	if err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("读取 running workers 失败: %v", err))
	} else {
		res.RunningWorkers = runningWorkers
		if runningWorkers > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("当前仍有 %d 个 running worker，升级不阻塞但建议先确认任务状态。", runningWorkers))
		}
	}

	controlChanges, err := repo.PlanControlPlaneSeedUpdate(layout, projectRef.Name)
	if err != nil {
		return res, &UpgradeFailure{Result: res, Err: err}
	}
	res.Changes = convertControlChanges(controlChanges)

	backupSources := collectBackupSources(layout, res.Changes)
	if opt.DryRun {
		res.Backups = previewBackupTargets(backupSources)
		sort.Strings(res.Backups)
		return res, nil
	}

	backups, err := createBackups(backupSources)
	if err != nil {
		res.Backups = backups
		return res, &UpgradeFailure{Result: res, Err: err}
	}
	res.Backups = backups

	db, err := store.OpenAndMigrate(layout.DBPath)
	if err != nil {
		return res, &UpgradeFailure{Result: res, Err: fmt.Errorf("执行 DB migration 失败: %w", err)}
	}
	defer func() {
		sqlDB, derr := db.DB()
		if derr == nil {
			_ = sqlDB.Close()
		}
	}()

	cfg, _, err := repo.LoadConfig(layout.ConfigPath)
	if err != nil {
		return res, &UpgradeFailure{Result: res, Err: fmt.Errorf("读取项目配置失败: %w", err)}
	}
	if err := repo.WriteConfigAtomic(layout.ConfigPath, cfg.WithDefaults()); err != nil {
		return res, &UpgradeFailure{Result: res, Err: fmt.Errorf("写入项目配置失败: %w", err)}
	}

	appliedControlChanges, err := repo.UpdateControlPlaneSeed(layout, projectRef.Name)
	if err != nil {
		return res, &UpgradeFailure{Result: res, Err: fmt.Errorf("更新 control plane 失败: %w", err)}
	}
	res.Changes = convertControlChanges(appliedControlChanges)

	if err := repo.EnsureManagerBootstrap(layout, projectRef.Name); err != nil {
		return res, &UpgradeFailure{Result: res, Err: fmt.Errorf("更新 manager bootstrap 失败: %w", err)}
	}
	if err := repo.OverwriteRepoAgentEntryPoints(projectRef.RepoRoot); err != nil {
		return res, &UpgradeFailure{Result: res, Err: fmt.Errorf("覆写入口文件失败: %w", err)}
	}

	recorded, err := repo.RecordProjectDalekVersion(
		metaPath,
		projectRef.Name,
		repo.ProjectKey(projectRef.RepoRoot),
		projectRef.RepoRoot,
		res.TargetVersion,
		time.Now(),
	)
	if err != nil {
		return res, &UpgradeFailure{Result: res, Err: fmt.Errorf("写入项目版本记录失败: %w", err)}
	}
	res.AppliedVersion = strings.TrimSpace(recorded.DalekVersion)

	currentMigration, err := store.CurrentMigrationVersion(db)
	if err != nil {
		return res, &UpgradeFailure{Result: res, Err: fmt.Errorf("读取 schema_migrations 失败: %w", err)}
	}
	res.MigrationVersion = currentMigration
	res.LatestMigrationVersion = store.LatestMigrationVersion()
	if res.MigrationVersion < res.LatestMigrationVersion {
		return res, &UpgradeFailure{
			Result: res,
			Err:    fmt.Errorf("migration 版本未对齐: got=%d want=%d", res.MigrationVersion, res.LatestMigrationVersion),
		}
	}

	postCfg, _, err := repo.LoadConfig(layout.ConfigPath)
	if err != nil {
		return res, &UpgradeFailure{Result: res, Err: fmt.Errorf("升级后读取配置失败: %w", err)}
	}
	res.ConfigSchemaVersion = postCfg.SchemaVersion
	if postCfg.SchemaVersion != repo.CurrentProjectSchemaVersion {
		return res, &UpgradeFailure{
			Result: res,
			Err:    fmt.Errorf("config schema 版本未对齐: got=%d want=%d", postCfg.SchemaVersion, repo.CurrentProjectSchemaVersion),
		}
	}

	if err := verifyControlPlaneIntegrity(layout); err != nil {
		return res, &UpgradeFailure{Result: res, Err: err}
	}

	if res.AppliedVersion == "" {
		res.AppliedVersion = res.TargetVersion
	}
	sort.Strings(res.Backups)
	sort.Slice(res.Changes, func(i, j int) bool {
		if res.Changes[i].Action == res.Changes[j].Action {
			return res.Changes[i].Path < res.Changes[j].Path
		}
		return res.Changes[i].Action < res.Changes[j].Action
	})
	return res, nil
}

func (h *Home) resolveUpgradeProject(projectName, startDir string) (RegisteredProject, error) {
	projectName = strings.TrimSpace(projectName)
	if projectName != "" {
		ref, err := h.FindProjectByName(projectName)
		if err != nil {
			return RegisteredProject{}, err
		}
		return *ref, nil
	}

	baseDir := strings.TrimSpace(startDir)
	if baseDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return RegisteredProject{}, err
		}
		baseDir = wd
	}
	repoRoot, err := repo.FindRepoRoot(baseDir)
	if err != nil {
		return RegisteredProject{}, err
	}
	ref, err := h.FindProjectByRepoRoot(repoRoot)
	if err != nil {
		return RegisteredProject{}, err
	}
	return *ref, nil
}

func countRunningWorkers(dbPath string) (int, error) {
	dbPath = strings.TrimSpace(dbPath)
	if dbPath == "" {
		return 0, nil
	}
	if _, err := os.Stat(dbPath); err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return 0, err
	}
	defer func() {
		sqlDB, derr := db.DB()
		if derr == nil {
			_ = sqlDB.Close()
		}
	}()
	var count int64
	if err := db.Model(&contracts.Worker{}).Where("status = ?", contracts.WorkerRunning).Count(&count).Error; err != nil {
		return 0, err
	}
	return int(count), nil
}

func convertControlChanges(in []repo.ControlPlaneChange) []UpgradeChange {
	if len(in) == 0 {
		return nil
	}
	out := make([]UpgradeChange, 0, len(in))
	for _, item := range in {
		out = append(out, UpgradeChange{
			Path:   strings.TrimSpace(item.Path),
			Action: strings.TrimSpace(item.Action),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Action == out[j].Action {
			return out[i].Path < out[j].Path
		}
		return out[i].Action < out[j].Action
	})
	return out
}

func collectBackupSources(layout repo.Layout, changes []UpgradeChange) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(changes)+2)
	appendIfExists := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		if _, err := os.Stat(path); err != nil {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	appendIfExists(layout.DBPath)
	appendIfExists(layout.ConfigPath)
	appendIfExists(layout.ProjectAgentKernelPath)
	appendIfExists(filepath.Join(layout.RepoRoot, "CLAUDE.md"))
	appendIfExists(filepath.Join(layout.RepoRoot, "AGENTS.md"))
	for _, change := range changes {
		appendIfExists(change.Path)
	}
	sort.Strings(out)
	return out
}

func previewBackupTargets(sources []string) []string {
	if len(sources) == 0 {
		return nil
	}
	out := make([]string, 0, len(sources))
	for _, src := range sources {
		out = append(out, fmt.Sprintf("%s -> %s.bak.<timestamp>", src, src))
	}
	return out
}

func createBackups(sources []string) ([]string, error) {
	if len(sources) == 0 {
		return nil, nil
	}
	tag := time.Now().UTC().Format("20060102-150405")
	backups := make([]string, 0, len(sources))
	for _, src := range sources {
		raw, err := os.ReadFile(src)
		if err != nil {
			return backups, fmt.Errorf("读取备份源失败(%s): %w", src, err)
		}
		info, err := os.Stat(src)
		if err != nil {
			return backups, fmt.Errorf("读取备份源权限失败(%s): %w", src, err)
		}
		dst := fmt.Sprintf("%s.bak.%s", src, tag)
		if err := os.WriteFile(dst, raw, info.Mode().Perm()); err != nil {
			return backups, fmt.Errorf("写入备份失败(%s): %w", dst, err)
		}
		backups = append(backups, dst)
	}
	return backups, nil
}

func verifyControlPlaneIntegrity(layout repo.Layout) error {
	requiredFiles := []string{
		layout.ProjectAgentKernelPath,
		layout.ProjectAgentUserPath,
		layout.ProjectBootstrapPath,
	}
	for _, file := range requiredFiles {
		info, err := os.Stat(file)
		if err != nil {
			return fmt.Errorf("control 文件缺失: %s: %w", file, err)
		}
		if info.IsDir() {
			return fmt.Errorf("control 文件路径异常（目录）: %s", file)
		}
	}
	requiredDirs := []string{
		layout.ControlSkillsDir,
		filepath.Join(layout.ControlSkillsDir, "dispatch-new-ticket"),
	}
	for _, dir := range requiredDirs {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("control 目录缺失: %s: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("control 目录路径异常（非目录）: %s", dir)
		}
	}
	return nil
}
