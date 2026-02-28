package app

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	daemonsvc "dalek/internal/services/daemon"
	pmsvc "dalek/internal/services/pm"
)

const defaultDaemonManagerTickInterval = 30 * time.Second

type managerDispatchHost interface {
	SubmitDispatch(ctx context.Context, req daemonsvc.DispatchSubmitRequest) (daemonsvc.DispatchSubmitReceipt, error)
}

type managerRunProjectIndexWarmer interface {
	WarmupRunProjectIndex(project string, runIDs []uint) int
}

type daemonManagerComponent struct {
	home     *Home
	registry *ProjectRegistry
	logger   *slog.Logger
	interval time.Duration
	host     managerDispatchHost

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}
	wakeCh   chan string

	recoveryOnce sync.Once

	statusHookFactory func(projectName string, p *Project) pmsvc.WorkflowStatusChangeHook
}

type recoveryProjectSummary struct {
	DispatchJobs   int
	TaskRuns       int
	Notes          int
	Workers        int
	TicketsQueued  int
	TicketsBlocked int
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

func (m *daemonManagerComponent) setDispatchHost(host managerDispatchHost) {
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
		if err != nil || p == nil || p.core == nil || p.core.DB == nil || p.pm == nil || p.worker == nil {
			if err != nil {
				m.logf("recovery open project failed: project=%s err=%v", name, err)
			}
			continue
		}
		pmState, pmErr := p.GetPMState(ctx)
		autopilotEnabled := false
		if pmErr != nil {
			m.logf("recovery read pm state failed: project=%s err=%v", name, pmErr)
		} else {
			autopilotEnabled = pmState.AutopilotEnabled
		}
		summary := recoveryProjectSummary{}

		recoveredRunIDs := map[uint]struct{}{}
		if recovered, recoveredRuns, err := p.pm.RecoverStuckDispatchJobs(ctx, name, now, autopilotEnabled); err != nil {
			m.logf("recovery dispatch jobs failed: project=%s err=%v", name, err)
		} else {
			summary.DispatchJobs = recovered.DispatchJobs
			summary.TaskRuns += recovered.TaskRuns
			summary.TicketsQueued = recovered.TicketsQueued
			summary.TicketsBlocked = recovered.TicketsBlocked
			recoveredRunIDs = recoveredRuns
		}

		if recovered, err := p.pm.RecoverActiveTaskRuns(ctx, name, now, recoveredRunIDs); err != nil {
			m.logf("recovery active runs failed: project=%s err=%v", name, err)
		} else {
			summary.TaskRuns += recovered
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
			if err := p.pm.UpdateRecoverySummary(ctx, pmState.ID, now, summary.DispatchJobs, summary.TaskRuns, summary.Notes, summary.Workers); err != nil {
				m.logf("recovery summary persist failed: project=%s err=%v", name, err)
			}
		}
		m.logf(
			"recovery summary: project=%s dispatch_jobs=%d task_runs=%d reopened_notes=%d fixed_workers=%d autopilot=%v queued=%d blocked=%d",
			name,
			summary.DispatchJobs,
			summary.TaskRuns,
			summary.Notes,
			summary.Workers,
			autopilotEnabled,
			summary.TicketsQueued,
			summary.TicketsBlocked,
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

func (m *daemonManagerComponent) checkExpiredDispatchLeases(ctx context.Context, p *Project, projectName string) {
	if m == nil || p == nil || p.pm == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName = strings.TrimSpace(projectName)
	now := time.Now()

	pmState, pmErr := p.GetPMState(ctx)
	autopilotEnabled := false
	if pmErr != nil {
		m.logf("lease check read pm state failed: project=%s err=%v", projectName, pmErr)
	} else {
		autopilotEnabled = pmState.AutopilotEnabled
	}
	summary, err := p.pm.CheckExpiredDispatchLeases(ctx, projectName, now, autopilotEnabled)
	if err != nil {
		m.logf("lease check recover failed: project=%s err=%v", projectName, err)
		return
	}
	if summary.DispatchJobs > 0 || summary.TaskRuns > 0 {
		m.logf(
			"lease check summary: project=%s recovered_jobs=%d recovered_runs=%d queued=%d blocked=%d autopilot=%v",
			projectName,
			summary.DispatchJobs,
			summary.TaskRuns,
			summary.TicketsQueued,
			summary.TicketsBlocked,
			autopilotEnabled,
		)
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
		p.pm.SetDispatchSubmitter(daemonManagerDispatchSubmitter{
			projectName: projectName,
			host:        m.host,
		})
	}
	if m.statusHookFactory != nil && p != nil && p.pm != nil {
		p.pm.SetStatusChangeHook(m.statusHookFactory(projectName, p))
	}
	m.checkExpiredDispatchLeases(parent, p, projectName)
	tickCtx, cancel := context.WithTimeout(parent, 2*time.Minute)
	res, err := p.ManagerTick(tickCtx, ManagerTickOptions{})
	cancel()
	if err != nil {
		m.logf("manager tick failed: source=%s project=%s err=%v", strings.TrimSpace(source), projectName, err)
		return
	}
	m.logf("manager tick ok: source=%s project=%s running=%d blocked=%d capacity=%d started=%d dispatched=%d", strings.TrimSpace(source), projectName, res.Running, res.RunningBlocked, res.Capacity, len(res.StartedTickets), len(res.DispatchedTickets))
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

type daemonManagerDispatchSubmitter struct {
	projectName string
	host        managerDispatchHost
}

func (s daemonManagerDispatchSubmitter) SubmitTicketDispatch(ctx context.Context, ticketID uint) error {
	if s.host == nil {
		return fmt.Errorf("dispatch host 未初始化")
	}
	projectName := strings.TrimSpace(s.projectName)
	if projectName == "" {
		return fmt.Errorf("project 不能为空")
	}
	requestID := fmt.Sprintf("mgr_t%d_%s", ticketID, strings.TrimSpace(daemonsvc.NewRequestID("mgr")))
	_, err := s.host.SubmitDispatch(ctx, daemonsvc.DispatchSubmitRequest{
		Project:   projectName,
		TicketID:  ticketID,
		RequestID: requestID,
	})
	return err
}
