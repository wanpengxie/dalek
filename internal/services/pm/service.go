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

// workerLoopClosureFallbackApplierFunc 是 closure fallback 的函数签名，用于测试注入。
type workerLoopClosureFallbackApplierFunc func(ctx context.Context, ticketID uint, w contracts.Worker, loopResult WorkerLoopResult, decision workerLoopStageClosureDecision, source string) error

type Service struct {
	p                  *core.Project
	worker             *worker.Service
	logger             *slog.Logger
	mu                 sync.RWMutex
	workerRunSubmitter WorkerRunSubmitter
	focusLoopControl   FocusLoopControl
	statusChangeHook   WorkflowStatusChangeHook
	projectWakeHook    func()
	statusHookWG       sync.WaitGroup

	workerReadyTimeout      time.Duration
	workerReadyPollInterval time.Duration

	// queuedCh / queueWakeCh 是 queued ticket 的调度唤醒通道。
	// ticket 进入 queued 或项目额度变化后，通过 notifyQueued/KickQueueConsumer 唤醒消费者，
	// 由消费者按项目级配额扫描并消费 queued ticket。
	queuedCh          chan uint
	queueWakeCh       chan struct{}
	queueConsumerOnce sync.Once

	// pmSubmitter 用于 convergent 模式的 PM run 提交。
	pmSubmitter PMRunSubmitter
	// sdkHandleLauncher 用于测试注入，生产环境保持 nil（使用真实的 launchWorkerSDKHandle）。
	sdkHandleLauncher workerSDKHandleLauncherFunc
	// workerLoopClosureFallbackApplier 用于测试注入，生产环境保持 nil（使用真实 fallback）。
	workerLoopClosureFallbackApplier workerLoopClosureFallbackApplierFunc
	runner                           sdkrunner.TaskRunner
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
		queuedCh:                make(chan uint, 64),
		queueWakeCh:             make(chan struct{}, 1),
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

func (s *Service) SetFocusLoopControl(ctrl FocusLoopControl) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.focusLoopControl = ctrl
}

func (s *Service) SetStatusChangeHook(hook WorkflowStatusChangeHook) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statusChangeHook = hook
}

func (s *Service) SetProjectWakeHook(hook func()) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectWakeHook = hook
}

func (s *Service) getWorkerRunSubmitter() WorkerRunSubmitter {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.workerRunSubmitter
}

func (s *Service) getFocusLoopControl() FocusLoopControl {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.focusLoopControl
}

func (s *Service) getStatusChangeHook() WorkflowStatusChangeHook {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.statusChangeHook
}

func (s *Service) projectWake() {
	if s == nil {
		return
	}
	s.mu.RLock()
	hook := s.projectWakeHook
	s.mu.RUnlock()
	if hook != nil {
		hook()
	}
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

func (s *Service) slog() *slog.Logger {
	if s == nil || s.logger == nil {
		return core.DiscardLogger()
	}
	return s.logger
}
