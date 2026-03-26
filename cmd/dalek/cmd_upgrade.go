package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"dalek/internal/app"
)

func cmdUpgrade(args []string) {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		printSubcommandUsage(
			fs,
			"升级当前项目（DB migration + config schema + control plane + 版本记录）",
			"dalek upgrade [--scope kernel|control] [--project <name>] [--dry-run] [--force] [--output text|json]",
			"dalek upgrade --dry-run",
			"dalek upgrade --scope kernel",
			"dalek upgrade --scope control",
			"dalek upgrade -p demo",
		)
	}
	home := fs.String("home", globalHome, "dalek Home 目录（默认 ~/.dalek）")
	project := fs.String("project", globalProject, "项目名（可选，默认从当前目录推断）")
	projectShort := fs.String("p", globalProject, "项目名（可选，默认从当前目录推断）")
	scope := fs.String("scope", "", "升级范围: kernel（入口文件+内核）| control（控制平面模板）。留空=全量升级")
	dryRun := fs.Bool("dry-run", false, "仅预览，不执行写入")
	force := fs.Bool("force", false, "即使记录版本与当前 binary 相同也强制执行")
	output := addOutputFlag(fs, "输出格式: text|json（默认 text）")
	parseFlagSetOrExit(fs, args, globalOutput, "upgrade 参数解析失败", "运行 dalek upgrade --help 查看参数")
	if strings.TrimSpace(*projectShort) != "" {
		*project = strings.TrimSpace(*projectShort)
	}
	out := parseOutputOrExit(*output, true)

	homeDir, err := app.ResolveHomeDir(*home)
	if err != nil {
		exitRuntimeError(out, "解析 Home 目录失败", err.Error(), "通过 --home 指定有效目录，或设置 DALEK_HOME")
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		exitRuntimeError(out, "打开 Home 失败", err.Error(), "检查 Home 目录权限与文件完整性")
	}

	wd, err := os.Getwd()
	if err != nil {
		exitRuntimeError(out, "获取当前目录失败", err.Error(), "切换到可读目录后重试")
	}

	scopeVal := strings.TrimSpace(*scope)
	if scopeVal != "" && scopeVal != app.UpgradeScopeKernel && scopeVal != app.UpgradeScopeControl {
		exitRuntimeError(out, "无效 --scope 值", fmt.Sprintf("%q 不是合法选项", scopeVal), "支持: kernel, control")
	}

	res, err := h.UpgradeProject(context.Background(), app.UpgradeOptions{
		ProjectName:   strings.TrimSpace(*project),
		StartDir:      wd,
		BinaryVersion: version,
		DryRun:        *dryRun,
		Force:         *force,
		Scope:         scopeVal,
	})
	if err != nil {
		cause := strings.TrimSpace(err.Error())
		fix := "检查错误信息并根据备份文件恢复后重试"
		if errors.Is(err, app.ErrNotInitialized) {
			fix = "先在项目目录执行 `dalek init` 完成初始化"
		}
		var upgradeErr *app.UpgradeFailure
		if errors.As(err, &upgradeErr) && len(upgradeErr.Result.Backups) > 0 {
			cause = cause + "\n已创建备份:\n  " + strings.Join(upgradeErr.Result.Backups, "\n  ")
		}
		exitRuntimeError(out, "dalek upgrade 失败", cause, fix)
	}

	printUpgradeResult(out, res)
}

func printUpgradeResult(out cliOutputFormat, res app.UpgradeResult) {
	if out == outputJSON {
		printJSONOrExit(map[string]any{
			"schema":                   "dalek.upgrade.v1",
			"project":                  strings.TrimSpace(res.Project),
			"repo_root":                strings.TrimSpace(res.RepoRoot),
			"dry_run":                  res.DryRun,
			"force":                    res.Force,
			"scope":                    res.Scope,
			"already_latest":           res.AlreadyLatest,
			"previous_version":         strings.TrimSpace(res.PreviousVersion),
			"target_version":           strings.TrimSpace(res.TargetVersion),
			"applied_version":          strings.TrimSpace(res.AppliedVersion),
			"daemon_running":           res.DaemonRunning,
			"running_workers":          res.RunningWorkers,
			"config_schema_version":    res.ConfigSchemaVersion,
			"migration_version":        res.MigrationVersion,
			"latest_migration_version": res.LatestMigrationVersion,
			"changes":                  res.Changes,
			"backups":                  res.Backups,
			"warnings":                 res.Warnings,
		})
		return
	}

	mode := "apply"
	if res.DryRun {
		mode = "dry-run"
	}
	fmt.Printf("upgrade mode: %s\n", mode)
	if res.Scope != "" {
		fmt.Printf("scope: %s\n", res.Scope)
	}
	fmt.Printf("project: %s\n", strings.TrimSpace(res.Project))
	fmt.Printf("repo: %s\n", strings.TrimSpace(res.RepoRoot))
	if res.Scope == "" {
		fmt.Printf("version: %s -> %s\n", fallbackVersion(res.PreviousVersion), fallbackVersion(preferVersion(res.AppliedVersion, res.TargetVersion)))
		if res.AlreadyLatest {
			fmt.Println("status: already latest (skip)")
			return
		}
		fmt.Printf("daemon_running: %t\n", res.DaemonRunning)
		fmt.Printf("running_workers: %d\n", res.RunningWorkers)
		if !res.DryRun {
			fmt.Printf("db_migration: %d/%d\n", res.MigrationVersion, res.LatestMigrationVersion)
			fmt.Printf("config_schema: %d\n", res.ConfigSchemaVersion)
		}
	}
	fmt.Printf("changes: %d\n", len(res.Changes))
	for _, item := range res.Changes {
		fmt.Printf("  - [%s] %s\n", strings.TrimSpace(item.Action), strings.TrimSpace(item.Path))
	}
	if len(res.Backups) > 0 {
		fmt.Printf("backups: %d\n", len(res.Backups))
		for _, b := range res.Backups {
			fmt.Printf("  - %s\n", strings.TrimSpace(b))
		}
	}
	if len(res.Warnings) > 0 {
		fmt.Printf("warnings: %d\n", len(res.Warnings))
		for _, w := range res.Warnings {
			fmt.Printf("  - %s\n", strings.TrimSpace(w))
		}
	}
	if res.DryRun {
		if res.Scope != "" {
			fmt.Printf("next: 运行 `dalek upgrade --scope %s` 执行实际升级。\n", res.Scope)
		} else {
			fmt.Println("next: 运行 `dalek upgrade` 执行实际升级。")
		}
	} else if res.Scope != "" {
		fmt.Println("done.")
	} else {
		fmt.Println("next: 如 daemon 在运行，建议执行 `dalek daemon restart`。")
	}
}

func preferVersion(primary, fallback string) string {
	primary = strings.TrimSpace(primary)
	if primary != "" {
		return primary
	}
	return strings.TrimSpace(fallback)
}

func fallbackVersion(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "(none)"
	}
	return v
}
