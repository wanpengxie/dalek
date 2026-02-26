package worker

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/infra"
)

type InterruptResult struct {
	TicketID uint
	WorkerID uint

	TmuxSocket  string
	TmuxSession string
	TargetPane  string
}

func (s *Service) InterruptTicket(ctx context.Context, ticketID uint) (InterruptResult, error) {
	w, err := s.LatestWorker(ctx, ticketID)
	if err != nil {
		return InterruptResult{}, err
	}
	if w == nil {
		return InterruptResult{}, fmt.Errorf("该 ticket 还没有 worker")
	}
	return s.InterruptWorker(ctx, w.ID)
}

// InterruptWorker 软中断：向目标 pane 发送 Ctrl+C（SIGINT），不 kill tmux session。
func (s *Service) InterruptWorker(ctx context.Context, workerID uint) (InterruptResult, error) {
	p, err := s.require()
	if err != nil {
		return InterruptResult{}, err
	}
	db := p.DB
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()

	var w contracts.Worker
	if err := db.First(&w, workerID).Error; err != nil {
		return InterruptResult{}, err
	}
	if strings.TrimSpace(w.TmuxSession) == "" {
		return InterruptResult{}, fmt.Errorf("worker 缺少 tmux_session: w%d", workerID)
	}

	cfg, err := s.cfg()
	if err != nil {
		return InterruptResult{}, err
	}
	socket := strings.TrimSpace(w.TmuxSocket)
	if socket == "" {
		socket = strings.TrimSpace(cfg.TmuxSocket)
	}

	// session 是否还活着
	listCtx, listCancel := context.WithTimeout(ctx, 5*time.Second)
	sessions, lerr := p.Tmux.ListSessions(listCtx, socket)
	listCancel()
	if lerr != nil {
		return InterruptResult{}, lerr
	}
	if !sessions[strings.TrimSpace(w.TmuxSession)] {
		return InterruptResult{}, fmt.Errorf("tmux session 不存在/已停止：%s", strings.TrimSpace(w.TmuxSession))
	}

	tmuxCtx, tmuxCancel := context.WithTimeout(ctx, 5*time.Second)
	defer tmuxCancel()

	target, pane, _ := infra.PickObservationTarget(p.Tmux, tmuxCtx, socket, w.TmuxSession)
	if strings.TrimSpace(target) == "" {
		target = strings.TrimSpace(w.TmuxSession) + ":0.0"
	}

	if pane.InputOff {
		return InterruptResult{}, fmt.Errorf("目标 pane input_off=1，无法注入 Ctrl+C（pane=%s）", strings.TrimSpace(target))
	}
	if pane.InMode {
		return InterruptResult{}, fmt.Errorf("目标 pane 处于 mode=%s，请退出后再中断（pane=%s）", strings.TrimSpace(pane.Mode), strings.TrimSpace(target))
	}

	if err := p.Tmux.SendKeys(tmuxCtx, socket, target, "C-c"); err != nil {
		_ = s.appendWorkerTaskEvent(ctx, w.ID, "interrupt_error", fmt.Sprintf("send-keys C-c 失败: %v", err), map[string]any{
			"target": strings.TrimSpace(target),
		}, now)
		return InterruptResult{}, err
	}
	_ = s.appendWorkerTaskEvent(ctx, w.ID, "interrupt_sent", fmt.Sprintf("target=%s", strings.TrimSpace(target)), map[string]any{
		"target": strings.TrimSpace(target),
	}, now)
	// 事件触发：尽快做一次语义观测（例如中断后可能马上回到 prompt / 报错 / 等待输入）。
	_ = s.RequestWorkerSemanticWatch(ctx, w.ID, time.Now())

	return InterruptResult{
		TicketID:    w.TicketID,
		WorkerID:    w.ID,
		TmuxSocket:  socket,
		TmuxSession: strings.TrimSpace(w.TmuxSession),
		TargetPane:  strings.TrimSpace(target),
	}, nil
}
