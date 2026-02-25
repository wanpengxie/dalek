package infra

import (
	"context"
	"fmt"
	"strings"
)

// PickObservationTarget 尽量选择“用户 attach 进去看到的活动 pane”，以便：
// - capture-pane 与 UI tail 预览一致
// - 注入输入与人眼一致
//
// 失败时回退到 session:0.0。
func PickObservationTarget(tmuxc TmuxClient, ctx context.Context, socket, session string) (string, PaneInfo, error) {
	socket = strings.TrimSpace(socket)
	if socket == "" {
		socket = "dalek"
	}
	session = strings.TrimSpace(session)
	if session == "" {
		return "", PaneInfo{}, fmt.Errorf("session 不能为空")
	}

	// Fast path：直接取 session 当前活动 pane（一条 tmux 命令）。
	if ap, err := tmuxc.ActivePane(ctx, socket, session); err == nil {
		if strings.TrimSpace(ap.PaneID) != "" {
			return strings.TrimSpace(ap.PaneID), ap, nil
		}
	}

	// 退化：list-panes 取第一个 pane。
	panes, err := tmuxc.ListPanes(ctx, socket, session)
	if err != nil || len(panes) == 0 {
		return session + ":0.0", PaneInfo{}, fmt.Errorf("无法枚举 panes: %v", err)
	}
	for _, p := range panes {
		if strings.TrimSpace(p.PaneID) != "" {
			return strings.TrimSpace(p.PaneID), p, nil
		}
	}
	return session + ":0.0", PaneInfo{}, fmt.Errorf("找不到 pane_id")
}
