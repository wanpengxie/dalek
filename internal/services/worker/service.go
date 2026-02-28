package worker

import (
	"context"
	"fmt"

	"dalek/internal/contracts"
	"dalek/internal/infra"
	"dalek/internal/repo"
	"dalek/internal/services/core"

	"gorm.io/gorm"
)

// Service 是 worker 相关业务流程的权威实现：
// - worktree + tmux session 生命周期（start/stop/interrupt）
// - dispatch 注入执行（在 PM 已完成 entrypoint/验收后：选择 pane + 注入 worker_agent）
// - report 融合、runtime watcher 状态写回、事件审计
//
// 注意：Service 不负责“项目初始化/播种”，这属于 internal/repo + internal/app。
type TicketReader interface {
	GetByID(ctx context.Context, id uint) (*contracts.Ticket, error)
	List(ctx context.Context, includeArchived bool) ([]contracts.Ticket, error)
}

type Service struct {
	p       *core.Project
	tickets TicketReader
}

func New(p *core.Project, tickets TicketReader) *Service {
	return &Service{p: p, tickets: tickets}
}

func (s *Service) require() (*core.Project, error) {
	if s == nil || s.p == nil {
		return nil, fmt.Errorf("worker service 缺少 project 上下文")
	}
	if s.p.DB == nil {
		return nil, fmt.Errorf("worker service 缺少 DB")
	}
	if s.p.Tmux == nil {
		return nil, fmt.Errorf("worker service 缺少 tmux client")
	}
	if s.p.WorkerRuntime == nil {
		return nil, fmt.Errorf("worker service 缺少 worker runtime")
	}
	if s.p.Git == nil {
		return nil, fmt.Errorf("worker service 缺少 git client")
	}
	if s.p.TaskRuntime == nil {
		return nil, fmt.Errorf("worker service 缺少 task runtime")
	}
	if s.tickets == nil {
		return nil, fmt.Errorf("worker service 缺少 ticket reader")
	}
	return s.p, nil
}

func (s *Service) ticketSvc() (TicketReader, error) {
	if _, err := s.require(); err != nil {
		return nil, err
	}
	return s.tickets, nil
}

func (s *Service) db() (*gorm.DB, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	return p.DB, nil
}

func (s *Service) cfg() (repo.Config, error) {
	p, err := s.require()
	if err != nil {
		return repo.Config{}, err
	}
	return p.Config.WithDefaults(), nil
}

func (s *Service) tmux() (infra.TmuxClient, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	return p.Tmux, nil
}

func (s *Service) runtime() (infra.WorkerRuntime, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	return p.WorkerRuntime, nil
}

func (s *Service) git() (infra.GitClient, error) {
	p, err := s.require()
	if err != nil {
		return nil, err
	}
	return p.Git, nil
}
