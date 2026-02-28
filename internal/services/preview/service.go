package preview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/infra"
	"dalek/internal/repo"
	"dalek/internal/services/core"
)

// WorkerLookup 收口 preview 仅需的 worker 查询能力。
type WorkerLookup interface {
	LatestWorker(ctx context.Context, ticketID uint) (*contracts.Worker, error)
}

// Service 提供 ticket worker 输出尾部预览能力。
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
	if s.p.WorkerRuntime == nil {
		return nil, fmt.Errorf("preview service 缺少 worker runtime")
	}
	if s.worker == nil {
		return nil, fmt.Errorf("preview service 缺少 worker lookup")
	}
	return s.p, nil
}

// CaptureTicketTail 抓取该 ticket 最新 worker 的日志尾部输出。
func (s *Service) CaptureTicketTail(ctx context.Context, ticketID uint, lastLines int) (contracts.TailPreview, error) {
	p, err := s.require()
	if err != nil {
		return contracts.TailPreview{}, err
	}
	a, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return contracts.TailPreview{}, err
	}
	if a == nil {
		return contracts.TailPreview{}, fmt.Errorf("该 ticket 没有可抓取的 worker")
	}
	logPath := strings.TrimSpace(a.LogPath)
	if logPath == "" && strings.TrimSpace(p.WorkersDir) != "" && a.ID != 0 {
		logPath = repo.WorkerStreamLogPath(p.WorkersDir, a.ID)
	}
	if logPath == "" {
		return contracts.TailPreview{}, fmt.Errorf("该 ticket 没有可抓取的日志路径")
	}

	if lastLines <= 0 {
		lastLines = 20
	}
	handle := infra.WorkerProcessHandle{
		PID:     a.ProcessPID,
		LogPath: logPath,
	}
	if a.StartedAt != nil {
		handle.StartedAt = *a.StartedAt
	}
	out, err := p.WorkerRuntime.CaptureOutput(ctx, handle, lastLines)
	if err != nil {
		return contracts.TailPreview{}, err
	}
	lines := infra.SplitLines(out)
	lines = infra.TrimTrailingEmpty(lines)
	if len(lines) > lastLines {
		lines = lines[len(lines)-lastLines:]
	}

	return contracts.TailPreview{
		TicketID:   ticketID,
		WorkerID:   a.ID,
		Source:     "worker_log",
		LogPath:    logPath,
		CapturedAt: time.Now(),
		Lines:      lines,
	}, nil
}
