package app

import (
	"context"
	"dalek/internal/contracts"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	daemonsvc "dalek/internal/services/daemon"
	pmsvc "dalek/internal/services/pm"
)

const defaultDaemonManagerTickInterval = 30 * time.Second

const (
	plannerPromptPlanMaxBytes = 24 * 1024
	plannerPromptListLimit    = 200
)

type managerDispatchHost interface {
	SubmitWorkerRun(ctx context.Context, req daemonsvc.WorkerRunSubmitRequest) (daemonsvc.WorkerRunSubmitReceipt, error)
	SubmitPlannerRun(ctx context.Context, req daemonsvc.PlannerSubmitRequest) (daemonsvc.PlannerSubmitReceipt, error)
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
	PlannerOps     int
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
		if recovered, err := p.pm.RecoverPlannerOps(ctx, now); err != nil {
			m.logf("recovery planner ops failed: project=%s err=%v", name, err)
		} else {
			summary.PlannerOps = recovered
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
			"recovery summary: project=%s dispatch_jobs=%d task_runs=%d planner_ops=%d reopened_notes=%d fixed_workers=%d autopilot=%v queued=%d blocked=%d",
			name,
			summary.DispatchJobs,
			summary.TaskRuns,
			summary.PlannerOps,
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
	m.submitPlannerRunIfScheduled(parent, p, projectName, res)
	m.logf("manager tick ok: source=%s project=%s running=%d blocked=%d capacity=%d started=%d dispatched=%d planner_scheduled=%v", strings.TrimSpace(source), projectName, res.Running, res.RunningBlocked, res.Capacity, len(res.StartedTickets), len(res.DispatchedTickets), res.PlannerRunScheduled)
}

func (m *daemonManagerComponent) submitPlannerRunIfScheduled(parent context.Context, p *Project, projectName string, res pmsvc.ManagerTickResult) {
	if m == nil || m.host == nil || p == nil || !res.PlannerRunScheduled {
		return
	}
	if parent == nil {
		parent = context.Background()
	}
	submitCtx, cancel := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancel()
	req, err := m.buildPlannerSubmitRequest(submitCtx, p, projectName, res)
	if err != nil {
		m.logf("manager planner submit skipped: project=%s err=%v", strings.TrimSpace(projectName), err)
		return
	}
	if _, err := m.host.SubmitPlannerRun(submitCtx, req); err != nil {
		m.logf("manager planner submit failed: project=%s run_id=%d request_id=%s err=%v", strings.TrimSpace(projectName), req.TaskRunID, req.RequestID, err)
		return
	}
	m.logf("manager planner submit accepted: project=%s run_id=%d request_id=%s", strings.TrimSpace(projectName), req.TaskRunID, req.RequestID)
}

func (m *daemonManagerComponent) buildPlannerSubmitRequest(ctx context.Context, p *Project, projectName string, res pmsvc.ManagerTickResult) (daemonsvc.PlannerSubmitRequest, error) {
	if p == nil || p.core == nil || p.core.DB == nil {
		return daemonsvc.PlannerSubmitRequest{}, fmt.Errorf("project db 为空")
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		projectName = strings.TrimSpace(p.Name())
	}
	if projectName == "" {
		return daemonsvc.PlannerSubmitRequest{}, fmt.Errorf("project 不能为空")
	}
	pmState, err := p.GetPMState(ctx)
	if err != nil {
		return daemonsvc.PlannerSubmitRequest{}, err
	}
	if pmState.PlannerActiveTaskRunID == nil || *pmState.PlannerActiveTaskRunID == 0 {
		return daemonsvc.PlannerSubmitRequest{}, fmt.Errorf("planner active task run id 为空")
	}
	runID := *pmState.PlannerActiveTaskRunID
	var run contracts.TaskRun
	if err := p.core.DB.WithContext(ctx).
		Select("id", "request_id", "owner_type", "task_type").
		First(&run, runID).Error; err != nil {
		return daemonsvc.PlannerSubmitRequest{}, err
	}
	if run.OwnerType != contracts.TaskOwnerPM || run.TaskType != contracts.TaskTypePMPlannerRun {
		return daemonsvc.PlannerSubmitRequest{}, fmt.Errorf("planner active run 类型不匹配: run_id=%d owner=%s type=%s", runID, run.OwnerType, run.TaskType)
	}
	requestID := strings.TrimSpace(run.RequestID)
	if requestID == "" {
		requestID = fmt.Sprintf("pln_run_%d", runID)
	}
	prompt, err := m.buildPlannerPrompt(ctx, p, projectName, runID, requestID, res)
	if err != nil {
		return daemonsvc.PlannerSubmitRequest{}, err
	}
	return daemonsvc.PlannerSubmitRequest{
		Project:   projectName,
		RequestID: requestID,
		TaskRunID: runID,
		Prompt:    prompt,
	}, nil
}

func (m *daemonManagerComponent) buildPlannerPrompt(ctx context.Context, p *Project, projectName string, runID uint, requestID string, res pmsvc.ManagerTickResult) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	repoRoot := strings.TrimSpace(p.RepoRoot())
	planPath := filepath.Join(repoRoot, ".dalek", "pm", "plan.md")
	planText := readPlannerPlanMarkdown(planPath, plannerPromptPlanMaxBytes)
	pmWorkspaceState, pmWorkspaceErr := p.SyncPMWorkspaceState(ctx)

	ticketViews, ticketErr := p.ListTicketViews(ctx)
	mergeItems, mergeErr := p.ListMergeItems(ctx, ListMergeOptions{Limit: plannerPromptListLimit})
	inboxOpen, inboxOpenErr := p.ListInbox(ctx, ListInboxOptions{
		Status: contracts.InboxOpen,
		Limit:  plannerPromptListLimit,
	})
	inboxSnoozed, inboxSnoozedErr := p.ListInbox(ctx, ListInboxOptions{
		Status: contracts.InboxSnoozed,
		Limit:  plannerPromptListLimit,
	})
	plannerRecovery := contracts.JSONMap{}
	var plannerRecoveryErr error
	if p.pm == nil {
		plannerRecoveryErr = fmt.Errorf("pm service 为空")
	} else {
		plannerRecovery, plannerRecoveryErr = p.pm.PlannerRecoverySnapshot(ctx, plannerPromptListLimit)
	}

	snapshot := map[string]any{
		"generated_at":       time.Now().UTC().Format(time.RFC3339),
		"project":            strings.TrimSpace(projectName),
		"repo_root":          repoRoot,
		"planner_task_run":   runID,
		"planner_request_id": strings.TrimSpace(requestID),
		"pm_state":           plannerListSnapshot("dalek pm state sync", pmWorkspaceState, pmWorkspaceErr),
		"ticket_ls":          plannerListSnapshot("dalek ticket ls", ticketViews, ticketErr),
		"merge_ls":           plannerListSnapshot("dalek merge ls", mergeItems, mergeErr),
		"inbox_ls": map[string]any{
			"open":    plannerListSnapshot("dalek inbox ls --status open", inboxOpen, inboxOpenErr),
			"snoozed": plannerListSnapshot("dalek inbox ls --status snoozed", inboxSnoozed, inboxSnoozedErr),
		},
		"planner_recovery": plannerListSnapshot("pm planner recovery context", plannerRecovery, plannerRecoveryErr),
		"surface_conflicts": map[string]any{
			"source":          "manager_tick",
			"items":           res.SurfaceConflicts,
			"serial_deferred": res.SerialDeferred,
		},
	}
	snapshotJSON, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return "", fmt.Errorf("planner prompt 构建失败: %w", err)
	}

	prompt := strings.TrimSpace(fmt.Sprintf(`你是 dalek 的 PM planner agent。请先理解项目计划和当前状态，然后仅输出结构化 PMOps 决策；实际执行由系统 executor 串行处理。

		必须遵守：
		1. 不要在本轮直接执行任何 dalek CLI 或 shell 命令；只能输出 PMOps。
		2. 你是 PM，不是 worker。不要直接修改产品源码、测试或功能实现文件；需求实现必须通过 ticket/worker 完成。
		3. 输出必须且仅必须包含一个 <pmops>...</pmops> JSON 块，JSON 顶层为 {"ops":[...]}。
		4. 每个 op 必须包含：kind、idempotency_key、arguments。推荐同时给出 op_id、critical、preconditions。
		5. 避免重复动作，优先收敛阻塞项与高优先级事项；结合 planner_recovery 上下文避免重复执行已完成 op。
		6. 对 approval_required / needs_user / incident 先自行判断并吸收，只有确实缺少用户独有信息时才允许请求人工介入。
		7. 如果 git merge 在产品文件上产生冲突，先执行 git merge --abort，再改为 create_integration_ticket；禁止手工解决产品文件冲突。
		8. 可用 kind：write_requirement_doc, write_design_doc, create_ticket, start_ticket, create_integration_ticket, close_inbox, run_acceptance, set_feature_status。

		输出格式示例（严格遵守）：
		<pmops>
		{
		  "ops": [
		    {
		      "op_id": "op-1",
		      "kind": "create_ticket",
		      "idempotency_key": "create_ticket:feature-x:hash",
		      "critical": true,
		      "arguments": {
		        "title": "实现 xxx",
		        "description": "..."
		      },
		      "preconditions": ["feature_x status=planned"]
		    }
		  ]
		}
		</pmops>

	【PLAN 文档：%s】
	%s

【项目快照（对应 dalek ticket/merge/inbox ls）】
%s
`, strings.TrimSpace(planPath), planText, strings.TrimSpace(string(snapshotJSON))))
	if prompt == "" {
		return "", fmt.Errorf("planner prompt 为空")
	}
	return prompt, nil
}

func plannerListSnapshot(command string, items any, err error) map[string]any {
	out := map[string]any{
		"command": strings.TrimSpace(command),
	}
	if err != nil {
		out["ok"] = false
		out["error"] = strings.TrimSpace(err.Error())
		return out
	}
	out["ok"] = true
	out["items"] = items
	return out
}

func readPlannerPlanMarkdown(path string, maxBytes int) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return "(repo_root 为空，无法读取 .dalek/pm/plan.md)"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Sprintf("(未找到文件: %s)", path)
		}
		return fmt.Sprintf("(读取失败: %v)", err)
	}
	text := strings.TrimSpace(string(b))
	if text == "" {
		return fmt.Sprintf("(文件为空: %s)", path)
	}
	if maxBytes > 0 && len(text) > maxBytes {
		text = text[:maxBytes] + "\n\n...(truncated)"
	}
	return text
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
		return fmt.Errorf("worker run host 未初始化")
	}
	projectName := strings.TrimSpace(s.projectName)
	if projectName == "" {
		return fmt.Errorf("project 不能为空")
	}
	requestID := fmt.Sprintf("mgr_t%d_%s", ticketID, strings.TrimSpace(daemonsvc.NewRequestID("mgr")))
	_, err := s.host.SubmitWorkerRun(ctx, daemonsvc.WorkerRunSubmitRequest{
		Project:   projectName,
		TicketID:  ticketID,
		RequestID: requestID,
	})
	return err
}
