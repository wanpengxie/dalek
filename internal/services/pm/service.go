package pm

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
	"dalek/internal/services/agentexec"
	"dalek/internal/services/core"
	"dalek/internal/services/worker"

	"gorm.io/gorm"
)

// workerSDKHandleLauncherFunc 是 launchWorkerSDKHandle 的函数签名，用于测试注入。
type workerSDKHandleLauncherFunc func(ctx context.Context, t contracts.Ticket, w contracts.Worker, entryPrompt string) (agentexec.AgentRunHandle, error)

type Service struct {
	p                  *core.Project
	worker             *worker.Service
	logger             *slog.Logger
	mu                 sync.RWMutex
	workerRunSubmitter WorkerRunSubmitter
	statusChangeHook   WorkflowStatusChangeHook
	statusHookWG       sync.WaitGroup

	workerReadyTimeout      time.Duration
	workerReadyPollInterval time.Duration

	// sdkHandleLauncher 用于测试注入，生产环境保持 nil（使用真实的 launchWorkerSDKHandle）。
	sdkHandleLauncher workerSDKHandleLauncherFunc
	runner            sdkrunner.TaskRunner
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
		workerReadyTimeout:      defaultWorkerReadyTimeout,
		workerReadyPollInterval: defaultWorkerReadyPollInterval,
		runner:                  sdkrunner.DefaultTaskRunner{},
	}
}

func (s *Service) SetWorkerRunSubmitter(submitter WorkerRunSubmitter) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.workerRunSubmitter = submitter
}

func (s *Service) SetStatusChangeHook(hook WorkflowStatusChangeHook) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusChangeHook = hook
}

func (s *Service) getWorkerRunSubmitter() WorkerRunSubmitter {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workerRunSubmitter
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
	if s.worker == nil {
		return nil, nil, fmt.Errorf("pm service 缺少 worker service")
	}
	if s.p.TaskRuntime == nil {
		return nil, nil, fmt.Errorf("pm service 缺少 task runtime")
	}
	return s.p, s.p.DB, nil
}

func (s *Service) SetTaskRunner(runner sdkrunner.TaskRunner) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.runner = runner
}

func (s *Service) taskRunner() sdkrunner.TaskRunner {
	if s == nil {
		return sdkrunner.DefaultTaskRunner{}
	}
	s.mu.RLock()
	runner := s.runner
	s.mu.RUnlock()
	if runner == nil {
		return sdkrunner.DefaultTaskRunner{}
	}
	return runner
}

func (s *Service) plannerRunTimeout() time.Duration {
	if s == nil || s.p == nil {
		return defaultPlannerRunTimeout
	}
	ms := s.p.Config.WithDefaults().PMPlannerTimeoutMS
	if ms <= 0 {
		return defaultPlannerRunTimeout
	}
	return time.Duration(ms) * time.Millisecond
}

func (s *Service) slog() *slog.Logger {
	if s == nil || s.logger == nil {
		return core.DiscardLogger()
	}
	return s.logger
}
