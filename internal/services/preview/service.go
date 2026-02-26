package preview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/infra"
	"dalek/internal/services/core"
)

// WorkerLookup 收口 preview 仅需的 worker 查询能力。
type WorkerLookup interface {
	LatestWorker(ctx context.Context, ticketID uint) (*contracts.Worker, error)
}

// Service 提供 tmux pane 输出尾部预览能力。
//
// 说明：
// - 当前实现仍以 tmux capture-pane 作为最小可用的 tail 预览来源（用于 TUI 快速查看输出）。
// - 后续将替换为 SDK executor 的结构化日志流（见 docs/STATUS_MODEL_V2.md、docs/WATCHER_REMOVAL_AND_SDK_EXECUTOR.md）。
type Service struct {
	p      *core.Project
	worker WorkerLookup
}

func New(p *core.Project, workerSvc WorkerLookup) *Service {
	return &Service{p: p, worker: workerSvc}
}

func (s *Service) require() (*core.Project, error) {
	if s == nil || s.p == nil {
		return nil, fmt.Errorf("preview service 缺少 project 上下文")
	}
	if s.p.Tmux == nil {
		return nil, fmt.Errorf("preview service 缺少 tmux client")
	}
	if s.worker == nil {
		return nil, fmt.Errorf("preview service 缺少 worker lookup")
	}
	return s.p, nil
}

// CaptureTicketTail 抓取该 ticket 最新 worker 对应 tmux session 的活动 pane 输出尾部。
//
// 用途：UI 在不 attach 的情况下，让用户快速了解该 session 正在输出什么。
func (s *Service) CaptureTicketTail(ctx context.Context, ticketID uint, lastLines int) (contracts.TailPreview, error) {
	p, err := s.require()
	if err != nil {
		return contracts.TailPreview{}, err
	}
	a, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return contracts.TailPreview{}, err
	}
	if a == nil || strings.TrimSpace(a.TmuxSession) == "" {
		return contracts.TailPreview{}, fmt.Errorf("该 ticket 没有可抓取的 tmux session")
	}

	socket := strings.TrimSpace(a.TmuxSocket)
	if socket == "" {
		socket = strings.TrimSpace(p.Config.WithDefaults().TmuxSocket)
	}
	if socket == "" {
		socket = "dalek"
	}

	session := strings.TrimSpace(a.TmuxSession)
	target := session + ":0.0"
	paneID := ""

	// 尽量选中当前“人眼看到”的 pane（活动 pane）。
	if t, pinfo, err := infra.PickObservationTarget(p.Tmux, ctx, socket, session); err == nil && strings.TrimSpace(t) != "" {
		target = strings.TrimSpace(t)
		paneID = strings.TrimSpace(pinfo.PaneID)
	}

	if lastLines <= 0 {
		lastLines = 20
	}

	out, err := p.Tmux.CapturePane(ctx, socket, target, lastLines)
	if err != nil {
		return contracts.TailPreview{}, err
	}
	lines := infra.SplitLines(out)
	lines = infra.TrimTrailingEmpty(lines)
	if len(lines) > lastLines {
		lines = lines[len(lines)-lastLines:]
	}

	return contracts.TailPreview{
		TicketID:    ticketID,
		WorkerID:    a.ID,
		TmuxSocket:  socket,
		TmuxSession: session,
		PaneID:      paneID,
		Target:      target,
		CapturedAt:  time.Now(),
		Lines:       lines,
	}, nil
}
