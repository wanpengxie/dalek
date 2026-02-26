package pm

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"dalek/internal/services/core"
	"dalek/internal/services/worker"

	"gorm.io/gorm"
)

type Service struct {
	p                 *core.Project
	worker            *worker.Service
	logger            *slog.Logger
	mu                sync.RWMutex
	dispatchSubmitter DispatchSubmitter
	statusChangeHook  WorkflowStatusChangeHook

	workerReadyTimeout      time.Duration
	workerReadyPollInterval time.Duration
}

func New(p *core.Project, workerSvc *worker.Service) *Service {
	logger := core.DiscardLogger()
	if p != nil {
		logger = core.EnsureLogger(p.Logger).With("service", "pm")
	}
	return &Service{
		p:                       p,
		worker:                  workerSvc,
		logger:                  logger,
		workerReadyTimeout:      8 * time.Second,
		workerReadyPollInterval: 200 * time.Millisecond,
	}
}

func (s *Service) SetDispatchSubmitter(submitter DispatchSubmitter) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dispatchSubmitter = submitter
}

func (s *Service) SetStatusChangeHook(hook WorkflowStatusChangeHook) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusChangeHook = hook
}

func (s *Service) getDispatchSubmitter() DispatchSubmitter {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.dispatchSubmitter
}

func (s *Service) getStatusChangeHook() WorkflowStatusChangeHook {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusChangeHook
}

func (s *Service) require() (*core.Project, *gorm.DB, error) {
	if s == nil || s.p == nil {
		return nil, nil, fmt.Errorf("pm service 缺少 project 上下文")
	}
	if s.p.DB == nil {
		return nil, nil, fmt.Errorf("pm service 缺少 DB")
	}
	if s.p.Tmux == nil {
		return nil, nil, fmt.Errorf("pm service 缺少 tmux client")
	}
	if s.worker == nil {
		return nil, nil, fmt.Errorf("pm service 缺少 worker service")
	}
	if s.p.TaskRuntime == nil {
		return nil, nil, fmt.Errorf("pm service 缺少 task runtime")
	}
	return s.p, s.p.DB, nil
}

func (s *Service) slog() *slog.Logger {
	if s == nil || s.logger == nil {
		return core.DiscardLogger()
	}
	return s.logger
}
