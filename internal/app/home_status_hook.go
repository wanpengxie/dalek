package app

import (
	"strings"

	"dalek/internal/contracts"
	feishusvc "dalek/internal/services/channel/feishu"
	"dalek/internal/services/core"
	pmsvc "dalek/internal/services/pm"
)

// wireStatusChangeHook 为 CLI 路径打开的 Project 设置 GatewayStatusNotifier（best-effort）。
//
// daemon 的 managerTickProject 会在每次 tick 时覆盖此 hook；CLI 路径则依赖本方法。
// 任何环节失败（gateway DB 不可用、feishu 配置缺失等）都静默跳过，不影响项目打开。
func (h *Home) wireStatusChangeHook(p *Project, projectName string) {
	if h == nil || p == nil || p.pm == nil || p.core == nil || p.core.DB == nil {
		return
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return
	}
	gatewayDB, err := h.OpenGatewayDB()
	if err != nil {
		return
	}
	cfg := h.Config.WithDefaults()
	sender := feishusvc.NewSender(feishusvc.SenderConfig{
		Enabled:   cfg.Daemon.Public.Feishu.Enabled,
		AppID:     cfg.Daemon.Public.Feishu.AppID,
		AppSecret: cfg.Daemon.Public.Feishu.AppSecret,
		BaseURL:   cfg.Daemon.Public.Feishu.BaseURL,
		Logger:    p.core.Logger,
	})
	resolver := &staticProjectMetaResolver{
		name:     projectName,
		repoRoot: strings.TrimSpace(p.core.RepoRoot),
	}
	logger := core.EnsureLogger(p.core.Logger).With("project", projectName, "service", "pm_status_notifier")
	hook := pmsvc.NewGatewayStatusNotifier(projectName, p.core.DB, gatewayDB, resolver, sender, logger)
	p.pm.SetStatusChangeHook(hook)
}

// staticProjectMetaResolver 是一个简单的 ProjectMetaResolver，用于已知 project 的 CLI 场景。
type staticProjectMetaResolver struct {
	name     string
	repoRoot string
}

func (r *staticProjectMetaResolver) ResolveProjectMeta(name string) (*contracts.ProjectMeta, error) {
	if r == nil {
		return nil, nil
	}
	return &contracts.ProjectMeta{
		Name:     r.name,
		RepoRoot: r.repoRoot,
	}, nil
}
