package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"dalek/internal/app"
	"dalek/internal/repo"
)

func openRemoteProject(homeFlag, projectFlag string) (*app.Home, app.RemoteProject, error) {
	homeDir, err := app.ResolveHomeDir(homeFlag)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 Home 目录失败: %w", err)
	}
	h, err := app.OpenHome(homeDir)
	if err != nil {
		return nil, nil, fmt.Errorf("打开 Home 失败: %w", err)
	}

	projectName := strings.TrimSpace(projectFlag)
	if projectName == "" {
		wd, _ := os.Getwd()
		repoRoot, err := repo.FindRepoRoot(wd)
		if err != nil {
			return nil, nil, fmt.Errorf("无法识别当前目录的项目: %w", err)
		}
		rp, err := h.FindProjectByRepoRoot(repoRoot)
		if err != nil {
			return nil, nil, err
		}
		projectName = strings.TrimSpace(rp.Name)
	}
	if projectName == "" {
		return nil, nil, fmt.Errorf("project name 不能为空")
	}

	remote, err := app.NewDaemonRemoteProjectFromHome(h, projectName)
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := remote.Health(ctx); err != nil {
		return nil, nil, err
	}
	return h, remote, nil
}

func mustOpenRemoteProject(out cliOutputFormat, homeFlag, projectFlag string) (*app.Home, app.RemoteProject) {
	h, remote, err := openRemoteProject(homeFlag, projectFlag)
	if err != nil {
		cause := strings.TrimSpace(err.Error())
		fix := "通过 --home 指定有效目录，或检查 ~/.dalek/config.json 的 daemon.internal 配置"
		switch {
		case strings.Contains(cause, "解析 Home 目录失败"):
			fix = "通过 --home 指定有效目录，或设置 DALEK_HOME"
		case strings.Contains(cause, "打开 Home 失败"):
			fix = "检查 Home 目录权限与文件完整性"
		case strings.Contains(cause, "无法识别当前目录的项目"):
			fix = "使用 --project 指定项目名，或切换到已注册项目目录，或先运行 dalek init"
		}
		exitRuntimeError(out, "打开 remote project 失败", cause, fix)
	}
	return h, remote
}
