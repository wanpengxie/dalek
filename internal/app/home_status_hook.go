package app

import (
	"strings"

	"dalek/internal/services/core"
	pmsvc "dalek/internal/services/pm"
)

// wireStatusChangeHook 为 CLI 路径打开的 Project 设置 outbox 入队 notifier（best-effort）。
//
// daemon 的 managerTickProject 会在每次 tick 时覆盖此 hook；CLI 路径则依赖本方法。
// 任何环节失败（gateway DB 不可用等）都静默跳过，不影响项目打开。
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
	logger := core.EnsureLogger(p.core.Logger).With("project", projectName, "service", "pm_status_outbox_notifier")
	hook := pmsvc.NewOutboxEnqueueStatusNotifier(projectName, p.core.DB, gatewayDB, logger)
	p.pm.SetStatusChangeHook(hook)

	convergentLogger := core.EnsureLogger(p.core.Logger).With("project", projectName, "service", "convergent_outbox_notifier")
	p.pm.SetConvergentNotifier(pmsvc.NewOutboxConvergentNotifier(projectName, gatewayDB, convergentLogger))
}
