package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"dalek/internal/contracts"
	daemonsvc "dalek/internal/services/daemon"
	pmsvc "dalek/internal/services/pm"
	workersvc "dalek/internal/services/worker"
)

const defaultDaemonManagerTickInterval = 30 * time.Second

type managerExecutionHost interface {
	SubmitTicketLoop(ctx context.Context, req daemonsvc.TicketLoopSubmitRequest) (daemonsvc.TicketLoopSubmitReceipt, error)
	CancelTaskRun(ctx context.Context, runID uint) (daemonsvc.CancelResult, error)
	CancelTicketLoop(ctx context.Context, project string, ticketID uint) (daemonsvc.CancelResult, error)
	CancelTicketLoopWithCause(ctx context.Context, project string, ticketID uint, cause contracts.TaskCancelCause) (daemonsvc.CancelResult, error)
}

type managerRunProjectIndexWarmer interface {
	WarmupRunProjectIndex(project string, runIDs []uint) int
}

type daemonManagerComponent struct {
	home     *Home
	registry *ProjectRegistry
	logger   *slog.Logger
	interval time.Duration
	host     managerExecutionHost

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
	wakeCh   chan string

	recoveryOnce sync.Once

	statusHookFactory func(projectName string, p *Project) pmsvc.WorkflowStatusChangeHook
}

type recoveryProjectSummary struct {
	TaskRuns int
	Notes    int
	Workers  int
}

func newDaemonManagerComponent(home *Home, logger *slog.Logger, registries ...*ProjectRegistry) *daemonManagerComponent {
	var registry *ProjectRegistry
	if len(registries) > 0 {
		registry = registries[0]
	}
	if registry == nil && home != nil {
		registry = NewProjectRegistry(home)
	}
	interval := defaultDaemonManagerTickInterval
	return &daemonManagerComponent{
		home:     home,
		registry: registry,
		logger:   logger,
		interval: interval,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
		wakeCh:   make(chan string, 64),
	}
}

func (m *daemonManagerComponent) setExecutionHost(host managerExecutionHost) {
	if m == nil {
		return
	}
	m.host = host
}

func (m *daemonManagerComponent) setStatusChangeHookFactory(factory func(projectName string, p *Project) pmsvc.WorkflowStatusChangeHook) {
	if m == nil {
		return
	}
	m.statusHookFactory = factory
}

func (m *daemonManagerComponent) Name() string {
	return "project_manager"
}

func (m *daemonManagerComponent) Start(ctx context.Context) error {
	if m == nil || m.home == nil || m.registry == nil {
		return fmt.Errorf("daemon manager 未初始化")
	}
	interval := m.interval
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}
	go m.loop(ctx, interval)
	return nil
}

func (m *daemonManagerComponent) Stop(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
	if ctx == nil {
		<-m.doneCh
		return nil
	}
	select {
	case <-m.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *daemonManagerComponent) loop(ctx context.Context, interval time.Duration) {
	defer close(m.doneCh)
	m.recoveryOnce.Do(func() {
		m.runRecovery(ctx)
		m.warmupRunProjectIndex(ctx)
	})
	m.runTick(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ctx.Done():
			return
		case projectName := <-m.wakeCh:
			if strings.TrimSpace(projectName) == "" {
				m.runTick(ctx)
				continue
			}
			m.runTickProject(ctx, strings.TrimSpace(projectName), "event")
		case <-ticker.C:
			m.runTick(ctx)
		}
	}
}

func (m *daemonManagerComponent) runRecovery(ctx context.Context) {
	if m == nil || m.home == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projects, err := m.home.ListProjects()
	if err != nil {
		m.logf("recovery list projects failed: %v", err)
		return
	}
	for _, rp := range projects {
		now := time.Now()
		name := strings.TrimSpace(rp.Name)
		if name == "" {
			continue
		}
		p, err := m.registry.Open(name)
		if err != nil || p == nil || p.core == nil || p.core.DB == nil || p.pm == nil || p.task == nil || p.worker == nil {
			if err != nil {
				m.logf("recovery open project failed: project=%s err=%v", name, err)
			}
			continue
		}
		pmState, pmErr := p.GetPMState(ctx)
		if pmErr != nil {
			m.logf("recovery read pm state failed: project=%s err=%v", name, pmErr)
		}
		summary := recoveryProjectSummary{}

		if reconciled, err := p.task.ReconcileOrphanedExecutionHostRuns(ctx, now); err != nil {
			m.logf("recovery orphaned execution runs failed: project=%s err=%v", name, err)
		} else {
			summary.TaskRuns += reconciled
		}
		if repaired, err := p.pm.RecoverActiveTaskRuns(ctx, name, now, nil); err != nil {
			m.logf("recovery active runs failed: project=%s err=%v", name, err)
		} else {
			summary.TaskRuns += repaired
		}
		if rolled, err := p.RecoverStuckShapingNotes(ctx, 5*time.Minute); err != nil {
			m.logf("recovery note shaping failed: project=%s err=%v", name, err)
		} else {
			summary.Notes = rolled
			if rolled > 0 {
				m.logf("recovery note shaping summary: project=%s reopened_notes=%d", name, rolled)
			}
		}
		if fixed, err := m.reconcileWorkerRuntime(ctx, p); err != nil {
			m.logf("recovery worker runtime reconcile failed: project=%s err=%v", name, err)
		} else {
			summary.Workers = fixed
		}
		if pmErr == nil && pmState.ID != 0 {
			if err := p.pm.UpdateRecoverySummary(ctx, pmState.ID, now, summary.TaskRuns, summary.Notes, summary.Workers); err != nil {
				m.logf("recovery summary persist failed: project=%s err=%v", name, err)
			}
		}
		m.logf(
			"recovery summary: project=%s task_run_repairs=%d reopened_notes=%d fixed_workers=%d",
			name,
			summary.TaskRuns,
			summary.Notes,
			summary.Workers,
		)
	}
}

func (m *daemonManagerComponent) warmupRunProjectIndex(ctx context.Context) {
	if m == nil || m.home == nil || m.host == nil {
		return
	}
	warmer, ok := m.host.(managerRunProjectIndexWarmer)
	if !ok {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projects, err := m.home.ListProjects()
	if err != nil {
		m.logf("warmup run index list projects failed: %v", err)
		return
	}

	totalRuns := 0
	activeProjects := 0
	for _, rp := range projects {
		projectName := strings.TrimSpace(rp.Name)
		if projectName == "" {
			continue
		}
		p, err := m.registry.Open(projectName)
		if err != nil || p == nil || p.pm == nil {
			if err != nil {
				m.logf("warmup run index open project failed: project=%s err=%v", projectName, err)
			}
			continue
		}
		runIDs, err := p.pm.ListActiveTaskRunIDs(ctx)
		if err != nil {
			m.logf("warmup run index query active runs failed: project=%s err=%v", projectName, err)
			continue
		}
		if len(runIDs) == 0 {
			continue
		}
		activeProjects++
		totalRuns += warmer.WarmupRunProjectIndex(projectName, runIDs)
	}
	if activeProjects > 0 || totalRuns > 0 {
		m.logf("warmup run index summary: projects=%d indexed_runs=%d", activeProjects, totalRuns)
	}
}

func (m *daemonManagerComponent) reconcileWorkerRuntime(ctx context.Context, p *Project) (int, error) {
	if p == nil {
		return 0, fmt.Errorf("project 为空")
	}
	if p.task == nil {
		return 0, fmt.Errorf("task service 为空")
	}
	workers, err := p.worker.ListRunningWorkers(ctx)
	if err != nil {
		return 0, err
	}
	if len(workers) == 0 {
		return 0, nil
	}

	now := time.Now()
	fixed := 0
	probeFailed := 0
	for _, w := range workers {
		run, rerr := p.task.LatestActiveWorkerRun(ctx, w.ID)
		if rerr != nil {
			probeFailed++
			m.logf("recovery worker runtime probe failed: project=%s worker=%d err=%v", strings.TrimSpace(p.Name()), w.ID, rerr)
			continue
		}
		if run != nil {
			continue
		}
		if err := p.worker.MarkWorkerRuntimeNotAlive(ctx, w, now); err != nil {
			m.logf("recovery mark worker runtime not alive failed: worker=%d err=%v", w.ID, err)
			continue
		}
		fixed++
		logPath := strings.TrimSpace(w.LogPath)
		if logPath == "" {
			logPath = "(empty)"
		}
		item := contracts.InboxItem{
			Key:      fmt.Sprintf("worker_runtime_recover_%d", w.ID),
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxWarn,
			Reason:   contracts.InboxIncident,
			Title:    fmt.Sprintf("worker runtime 丢失：w%d", w.ID),
			Body:     fmt.Sprintf("ticket=t%d worker=w%d 在 recovery 对账中发现 runtime 不在线（log_path=%s），已自动回收状态。", w.TicketID, w.ID, logPath),
			TicketID: w.TicketID,
			WorkerID: w.ID,
		}
		if p.pm != nil {
			_, _ = p.pm.UpsertOpenInbox(ctx, item)
		}
	}
	if fixed > 0 || probeFailed > 0 {
		m.logf("recovery worker runtime reconcile summary: project=%s running_workers=%d fixed_workers=%d probe_failed_workers=%d", strings.TrimSpace(p.Name()), len(workers), fixed, probeFailed)
	}
	return fixed, nil
}

func (m *daemonManagerComponent) runTick(parent context.Context) {
	if m == nil || m.home == nil {
		return
	}
	projects, err := m.home.ListProjects()
	if err != nil {
		m.logf("manager tick list projects failed: %v", err)
		return
	}
	for _, rp := range projects {
		m.runTickProject(parent, strings.TrimSpace(rp.Name), "periodic")
	}
}

func (m *daemonManagerComponent) runTickProject(parent context.Context, projectName, source string) {
	if m == nil || m.home == nil {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return
	}
	p, err := m.registry.Open(projectName)
	if err != nil {
		m.logf("manager tick open project failed: source=%s project=%s err=%v", strings.TrimSpace(source), projectName, err)
		return
	}
	if m.host != nil && p != nil && p.pm != nil {
		p.worker.SetTicketLoopControl(daemonManagerWorkerLoopControl{
			projectName: projectName,
			host:        m.host,
		})
		p.pm.SetWorkerRunSubmitter(daemonManagerWorkerRunSubmitter{
			projectName: projectName,
			host:        m.host,
		})
		p.pm.SetFocusLoopControl(daemonManagerFocusLoopControl{
			projectName: projectName,
			host:        m.host,
		})
		p.pm.SetProjectWakeHook(func() {
			m.NotifyProject(projectName)
		})
		p.pm.StartQueueConsumer(parent)
		p.pm.KickQueueConsumer()
	}
	if m.statusHookFactory != nil && p != nil && p.pm != nil {
		p.pm.SetStatusChangeHook(m.statusHookFactory(projectName, p))
	}
	tickCtx, cancel := context.WithTimeout(parent, 2*time.Minute)
	res, err := p.ManagerTick(tickCtx, ManagerTickOptions{})
	cancel()
	if err != nil {
		m.logf("manager tick failed: source=%s project=%s err=%v", strings.TrimSpace(source), projectName, err)
		return
	}
	m.logf("manager tick ok: source=%s project=%s running=%d blocked=%d capacity=%d started=%d activated=%d", strings.TrimSpace(source), projectName, res.Running, res.RunningBlocked, res.Capacity, len(res.StartedTickets), len(res.ActivatedTickets))
}

func (m *daemonManagerComponent) NotifyProject(projectName string) {
	if m == nil {
		return
	}
	projectName = strings.TrimSpace(projectName)
	select {
	case m.wakeCh <- projectName:
	default:
		m.logf("manager notify dropped: channel full project=%s", projectName)
	}
}

func (m *daemonManagerComponent) logf(format string, args ...any) {
	if m == nil || m.logger == nil {
		return
	}
	m.logger.Info(fmt.Sprintf(format, args...))
}

type daemonManagerWorkerRunSubmitter struct {
	projectName string
	host        managerExecutionHost
}

type daemonManagerWorkerLoopControl struct {
	projectName string
	host        managerExecutionHost
}

type daemonManagerFocusLoopControl struct {
	projectName string
	host        managerExecutionHost
}

func (s daemonManagerWorkerRunSubmitter) SubmitTicketWorkerRun(ctx context.Context, ticketID uint, opt pmsvc.WorkerRunSubmitOptions) (pmsvc.WorkerRunSubmission, error) {
	if s.host == nil {
		return pmsvc.WorkerRunSubmission{}, fmt.Errorf("worker run host 未初始化")
	}
	projectName := strings.TrimSpace(s.projectName)
	if projectName == "" {
		return pmsvc.WorkerRunSubmission{}, fmt.Errorf("project 不能为空")
	}
	requestID := fmt.Sprintf("mgr_t%d_%s", ticketID, strings.TrimSpace(daemonsvc.NewRequestID("mgr")))
	receipt, err := s.host.SubmitTicketLoop(ctx, daemonsvc.TicketLoopSubmitRequest{
		Project:    projectName,
		TicketID:   ticketID,
		RequestID:  requestID,
		BaseBranch: strings.TrimSpace(opt.BaseBranch),
		Prompt:     strings.TrimSpace(opt.Prompt),
	})
	if err != nil {
		return pmsvc.WorkerRunSubmission{}, err
	}
	return pmsvc.WorkerRunSubmission{
		TaskRunID: receipt.TaskRunID,
		WorkerID:  receipt.WorkerID,
		RequestID: strings.TrimSpace(receipt.RequestID),
	}, nil
}

func (c daemonManagerWorkerLoopControl) CancelTicketLoop(ctx context.Context, ticketID uint, cause contracts.TaskCancelCause) (workersvc.TicketLoopCancelResult, error) {
	if c.host == nil || ticketID == 0 {
		return workersvc.TicketLoopCancelResult{}, nil
	}
	projectName := strings.TrimSpace(c.projectName)
	if projectName == "" {
		return workersvc.TicketLoopCancelResult{}, nil
	}
	res, err := c.host.CancelTicketLoopWithCause(ctx, projectName, ticketID, cause)
	if err != nil {
		return workersvc.TicketLoopCancelResult{}, err
	}
	return workersvc.TicketLoopCancelResult{
		Found:    res.Found,
		Canceled: res.Canceled,
		Reason:   strings.TrimSpace(res.Reason),
	}, nil
}

func (c daemonManagerFocusLoopControl) CancelTaskRun(ctx context.Context, runID uint) error {
	if c.host == nil || runID == 0 {
		return nil
	}
	_, err := c.host.CancelTaskRun(ctx, runID)
	return err
}

func (c daemonManagerFocusLoopControl) CancelTicketLoop(ctx context.Context, ticketID uint) error {
	if c.host == nil || ticketID == 0 {
		return nil
	}
	projectName := strings.TrimSpace(c.projectName)
	if projectName == "" {
		return nil
	}
	_, err := c.host.CancelTicketLoopWithCause(ctx, projectName, ticketID, contracts.TaskCancelCauseFocusCancel)
	return err
}
