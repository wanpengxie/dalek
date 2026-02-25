package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	daemonsvc "dalek/internal/services/daemon"
	pmsvc "dalek/internal/services/pm"
	"dalek/internal/store"

	"gorm.io/gorm"
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
	logger   *log.Logger
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

func newDaemonManagerComponent(home *Home, logger *log.Logger, registries ...*ProjectRegistry) *daemonManagerComponent {
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
		if err != nil || p == nil || p.core == nil || p.core.DB == nil {
			if err != nil {
				m.logf("recovery open project failed: project=%s err=%v", name, err)
			}
			continue
		}
		db := p.core.DB.WithContext(ctx)
		pmState, pmErr := p.GetPMState(ctx)
		autopilotEnabled := false
		if pmErr != nil {
			m.logf("recovery read pm state failed: project=%s err=%v", name, pmErr)
		} else {
			autopilotEnabled = pmState.AutopilotEnabled
		}
		summary := recoveryProjectSummary{}

		recoveredRunIDs := map[uint]struct{}{}
		if recovered, recoveredRuns, err := m.recoverStuckDispatchJobs(ctx, db, name, now, autopilotEnabled); err != nil {
			m.logf("recovery dispatch jobs failed: project=%s err=%v", name, err)
		} else {
			summary.DispatchJobs = recovered.DispatchJobs
			summary.TaskRuns += recovered.TaskRuns
			summary.TicketsQueued = recovered.TicketsQueued
			summary.TicketsBlocked = recovered.TicketsBlocked
			recoveredRunIDs = recoveredRuns
		}

		var active []store.TaskRun
		activeQuery := db.Where("orchestration_state IN ?", []store.TaskOrchestrationState{store.TaskPending, store.TaskRunning})
		if excluded := sortedRunIDsFromSet(recoveredRunIDs); len(excluded) > 0 {
			activeQuery = activeQuery.Where("id NOT IN ?", excluded)
		}
		if err := activeQuery.Find(&active).Error; err != nil {
			m.logf("recovery query active runs failed: project=%s err=%v", name, err)
		} else {
			for _, run := range active {
				errMsg := "daemon restart recovery: previous run marked failed"
				if err := db.Model(&store.TaskRun{}).Where("id = ?", run.ID).Updates(map[string]any{
					"orchestration_state": store.TaskFailed,
					"error_code":          "daemon_recovered",
					"error_message":       errMsg,
					"finished_at":         now,
					"updated_at":          now,
				}).Error; err != nil {
					m.logf("recovery mark failed error: project=%s run=%d err=%v", name, run.ID, err)
					continue
				}
				summary.TaskRuns++
				_ = db.Create(&store.TaskEvent{
					TaskRunID:   run.ID,
					EventType:   "daemon_recovery_failed",
					ToStateJSON: `{"orchestration_state":"failed"}`,
					Note:        errMsg,
					PayloadJSON: `{"source":"daemon_recovery"}`,
					CreatedAt:   now,
				}).Error
				_ = db.Create(&store.InboxItem{
					Key:      fmt.Sprintf("daemon_recovery_run_%d", run.ID),
					Status:   store.InboxOpen,
					Severity: store.InboxWarn,
					Reason:   store.InboxIncident,
					Title:    fmt.Sprintf("daemon recovery: run %d 已标记失败", run.ID),
					Body:     fmt.Sprintf("project=%s owner=%s task=%s ticket=%d worker=%d", name, string(run.OwnerType), strings.TrimSpace(run.TaskType), run.TicketID, run.WorkerID),
					TicketID: run.TicketID,
					WorkerID: run.WorkerID,
				}).Error
			}
		}
		if rolled, err := p.RecoverStuckShapingNotes(ctx, 5*time.Minute); err != nil {
			m.logf("recovery note shaping failed: project=%s err=%v", name, err)
		} else {
			summary.Notes = rolled
			if rolled > 0 {
				m.logf("recovery note shaping summary: project=%s reopened_notes=%d", name, rolled)
			}
		}
		if fixed, err := m.reconcileWorkerSessions(ctx, p); err != nil {
			m.logf("recovery worker reconcile failed: project=%s err=%v", name, err)
		} else {
			summary.Workers = fixed
		}
		if pmErr == nil && pmState.ID != 0 {
			if err := db.Model(&store.PMState{}).Where("id = ?", pmState.ID).Updates(map[string]any{
				"last_recovery_at":            &now,
				"last_recovery_dispatch_jobs": summary.DispatchJobs,
				"last_recovery_task_runs":     summary.TaskRuns,
				"last_recovery_notes":         summary.Notes,
				"last_recovery_workers":       summary.Workers,
				"updated_at":                  now,
			}).Error; err != nil {
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
		if err != nil || p == nil || p.core == nil || p.core.DB == nil {
			if err != nil {
				m.logf("warmup run index open project failed: project=%s err=%v", projectName, err)
			}
			continue
		}
		var runIDs []uint
		if err := p.core.DB.WithContext(ctx).
			Model(&store.TaskRun{}).
			Where("orchestration_state IN ?", []store.TaskOrchestrationState{store.TaskPending, store.TaskRunning}).
			Pluck("id", &runIDs).Error; err != nil {
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

func (m *daemonManagerComponent) recoverStuckDispatchJobs(ctx context.Context, db *gorm.DB, projectName string, now time.Time, autopilotEnabled bool) (recoveryProjectSummary, map[uint]struct{}, error) {
	out := recoveryProjectSummary{}
	recoveredRunIDs := map[uint]struct{}{}
	if db == nil {
		return out, recoveredRunIDs, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var jobs []store.PMDispatchJob
	if err := db.
		Where("status IN ?", []store.PMDispatchJobStatus{store.PMDispatchPending, store.PMDispatchRunning}).
		Order("id asc").
		Find(&jobs).Error; err != nil {
		return out, recoveredRunIDs, err
	}
	if len(jobs) == 0 {
		return out, recoveredRunIDs, nil
	}

	targetStatus := store.TicketBlocked
	retryAction := "blocked"
	retryReason := "daemon recovery: dispatch interrupted, ticket moved to blocked"
	if autopilotEnabled {
		targetStatus = store.TicketQueued
		retryAction = "queued"
		retryReason = "daemon recovery: dispatch interrupted, ticket queued for redispatch"
	}

	for _, job := range jobs {
		errMsg := "daemon restart recovery: dispatch job marked failed"
		recovered := false
		taskRunRecovered := false
		queued := false
		blocked := false
		err := db.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&store.PMDispatchJob{}).
				Where("id = ? AND status IN ?", job.ID, []store.PMDispatchJobStatus{store.PMDispatchPending, store.PMDispatchRunning}).
				Updates(map[string]any{
					"status":            store.PMDispatchFailed,
					"error":             errMsg,
					"runner_id":         "",
					"lease_expires_at":  nil,
					"active_ticket_key": nil,
					"finished_at":       &now,
					"updated_at":        now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return nil
			}
			recovered = true

			if job.TaskRunID != 0 {
				changed, err := markTaskRunFailedTx(tx, job.TaskRunID, "daemon_recovered", errMsg, now)
				if err != nil {
					return err
				}
				if changed {
					taskRunRecovered = true
					eventPayload := marshalRecoveryJSON(map[string]any{
						"source":          "daemon_recovery",
						"dispatch_job_id": job.ID,
						"ticket_id":       job.TicketID,
						"worker_id":       job.WorkerID,
						"request_id":      strings.TrimSpace(job.RequestID),
						"retry_action":    retryAction,
					})
					_ = tx.Create(&store.TaskEvent{
						TaskRunID:   job.TaskRunID,
						EventType:   "daemon_recovery_dispatch_failed",
						ToStateJSON: `{"orchestration_state":"failed"}`,
						Note:        errMsg,
						PayloadJSON: eventPayload,
						CreatedAt:   now,
					}).Error
				}
			}

			changed, err := m.applyTicketStatusTx(ctx, tx, job, targetStatus, retryReason, "daemon.recovery", now)
			if err != nil {
				return err
			}
			if changed {
				if targetStatus == store.TicketQueued {
					queued = true
				} else if targetStatus == store.TicketBlocked {
					blocked = true
				}
			}

			_ = tx.Create(&store.InboxItem{
				Key:      fmt.Sprintf("daemon_recovery_dispatch_%d", job.ID),
				Status:   store.InboxOpen,
				Severity: store.InboxWarn,
				Reason:   store.InboxIncident,
				Title:    fmt.Sprintf("daemon recovery: dispatch %d 已标记失败", job.ID),
				Body:     fmt.Sprintf("project=%s ticket=%d worker=%d request=%s action=%s", projectName, job.TicketID, job.WorkerID, strings.TrimSpace(job.RequestID), retryAction),
				TicketID: job.TicketID,
				WorkerID: job.WorkerID,
			}).Error
			return nil
		})
		if err != nil {
			m.logf("recovery dispatch job failed: project=%s job=%d err=%v", projectName, job.ID, err)
			continue
		}
		if recovered {
			out.DispatchJobs++
		}
		if taskRunRecovered {
			out.TaskRuns++
			recoveredRunIDs[job.TaskRunID] = struct{}{}
		}
		if queued {
			out.TicketsQueued++
		}
		if blocked {
			out.TicketsBlocked++
		}
	}
	return out, recoveredRunIDs, nil
}

func markTaskRunFailedTx(tx *gorm.DB, runID uint, errorCode, errMsg string, now time.Time) (bool, error) {
	if tx == nil || runID == 0 {
		return false, nil
	}
	errorCode = strings.TrimSpace(errorCode)
	if errorCode == "" {
		errorCode = "daemon_recovered"
	}
	res := tx.Model(&store.TaskRun{}).
		Where("id = ? AND orchestration_state IN ?", runID, []store.TaskOrchestrationState{store.TaskPending, store.TaskRunning}).
		Updates(map[string]any{
			"orchestration_state": store.TaskFailed,
			"error_code":          errorCode,
			"error_message":       errMsg,
			"finished_at":         now,
			"updated_at":          now,
		})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func sortedRunIDsFromSet(runIDs map[uint]struct{}) []uint {
	if len(runIDs) == 0 {
		return nil
	}
	ids := make([]uint, 0, len(runIDs))
	for id := range runIDs {
		if id == 0 {
			continue
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (m *daemonManagerComponent) applyTicketStatusTx(ctx context.Context, tx *gorm.DB, job store.PMDispatchJob, target store.TicketWorkflowStatus, reason, source string, now time.Time) (bool, error) {
	if tx == nil || job.TicketID == 0 {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	source = strings.TrimSpace(source)
	if source == "" {
		source = "daemon.recovery"
	}
	var ticket store.Ticket
	if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&ticket, job.TicketID).Error; err != nil {
		return false, err
	}
	from := store.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus)
	if from == store.TicketDone || from == store.TicketArchived || from == target {
		return false, nil
	}
	if err := tx.WithContext(ctx).
		Model(&store.Ticket{}).
		Where("id = ?", ticket.ID).
		Updates(map[string]any{
			"workflow_status": target,
			"updated_at":      now,
		}).Error; err != nil {
		return false, err
	}
	ev := store.TicketWorkflowEvent{
		CreatedAt:  now,
		TicketID:   ticket.ID,
		FromStatus: from,
		ToStatus:   target,
		Source:     source,
		Reason:     strings.TrimSpace(reason),
		PayloadJSON: marshalRecoveryJSON(map[string]any{
			"ticket_id":       ticket.ID,
			"worker_id":       job.WorkerID,
			"dispatch_id":     job.ID,
			"request_id":      strings.TrimSpace(job.RequestID),
			"target_workflow": string(target),
		}),
	}
	if err := tx.WithContext(ctx).Create(&ev).Error; err != nil {
		return false, err
	}
	return true, nil
}

func marshalRecoveryJSON(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func (m *daemonManagerComponent) reconcileWorkerSessions(ctx context.Context, p *Project) (int, error) {
	if p == nil {
		return 0, fmt.Errorf("project 为空")
	}
	workers, err := p.worker.ListRunningWorkers(ctx)
	if err != nil {
		return 0, err
	}
	if len(workers) == 0 {
		return 0, nil
	}

	defaultSocket := strings.TrimSpace(p.TmuxSocket())
	sessionsBySocket := map[string]map[string]bool{}
	probeFailedBySocket := map[string]error{}
	for _, w := range workers {
		socket := strings.TrimSpace(w.TmuxSocket)
		if socket == "" {
			socket = defaultSocket
		}
		if socket == "" {
			continue
		}
		if _, ok := sessionsBySocket[socket]; ok {
			continue
		}
		if _, failed := probeFailedBySocket[socket]; failed {
			continue
		}
		sessions, serr := p.core.Tmux.ListSessions(ctx, socket)
		if serr != nil {
			probeFailedBySocket[socket] = serr
			continue
		}
		sessionsBySocket[socket] = sessions
	}

	now := time.Now()
	fixed := 0
	for _, w := range workers {
		socket := strings.TrimSpace(w.TmuxSocket)
		if socket == "" {
			socket = defaultSocket
		}
		if _, failed := probeFailedBySocket[socket]; failed {
			continue
		}
		session := strings.TrimSpace(w.TmuxSession)
		if socket != "" && session != "" && sessionsBySocket[socket][session] {
			continue
		}
		if err := p.worker.MarkWorkerSessionNotAlive(ctx, w, now); err != nil {
			m.logf("recovery mark worker session not alive failed: worker=%d err=%v", w.ID, err)
			continue
		}
		fixed++
		socketLabel := socket
		if socketLabel == "" {
			socketLabel = "(empty)"
		}
		sessionLabel := session
		if sessionLabel == "" {
			sessionLabel = "(empty)"
		}
		item := store.InboxItem{
			Key:      fmt.Sprintf("worker_session_recover_%d", w.ID),
			Status:   store.InboxOpen,
			Severity: store.InboxWarn,
			Reason:   store.InboxIncident,
			Title:    fmt.Sprintf("worker session 丢失：w%d", w.ID),
			Body:     fmt.Sprintf("ticket=t%d worker=w%d 在 recovery 对账中发现 session 不在线（socket=%s session=%s），已自动回收状态。", w.TicketID, w.ID, socketLabel, sessionLabel),
			TicketID: w.TicketID,
			WorkerID: w.ID,
		}
		_ = p.core.DB.WithContext(ctx).Create(&item).Error
	}
	for socket, serr := range probeFailedBySocket {
		m.logf("recovery worker session probe failed: project=%s socket=%s err=%v", strings.TrimSpace(p.Name()), socket, serr)
	}
	if fixed > 0 || len(probeFailedBySocket) > 0 {
		m.logf("recovery worker reconcile summary: project=%s running_workers=%d fixed_workers=%d probe_failed_sockets=%d", strings.TrimSpace(p.Name()), len(workers), fixed, len(probeFailedBySocket))
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
	if m == nil || p == nil || p.core == nil || p.core.DB == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectName = strings.TrimSpace(projectName)
	now := time.Now()
	db := p.core.DB.WithContext(ctx)

	pmState, pmErr := p.GetPMState(ctx)
	autopilotEnabled := false
	if pmErr != nil {
		m.logf("lease check read pm state failed: project=%s err=%v", projectName, pmErr)
	} else {
		autopilotEnabled = pmState.AutopilotEnabled
	}

	var jobs []store.PMDispatchJob
	if err := db.
		Where("status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < ?", store.PMDispatchRunning, now).
		Order("id asc").
		Find(&jobs).Error; err != nil {
		m.logf("lease check query failed: project=%s err=%v", projectName, err)
		return
	}
	if len(jobs) == 0 {
		return
	}

	targetStatus := store.TicketBlocked
	retryAction := "blocked"
	retryReason := "dispatch lease expired: ticket moved to blocked"
	if autopilotEnabled {
		targetStatus = store.TicketQueued
		retryAction = "queued"
		retryReason = "dispatch lease expired: ticket queued for redispatch"
	}

	recoveredJobs := 0
	recoveredRuns := 0
	ticketsQueued := 0
	ticketsBlocked := 0
	for _, job := range jobs {
		errMsg := "dispatch lease expired: dispatch job marked failed"
		recovered := false
		taskRunRecovered := false
		queued := false
		blocked := false
		err := db.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&store.PMDispatchJob{}).
				Where("id = ? AND status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at < ?", job.ID, store.PMDispatchRunning, now).
				Updates(map[string]any{
					"status":            store.PMDispatchFailed,
					"error":             errMsg,
					"runner_id":         "",
					"lease_expires_at":  nil,
					"active_ticket_key": nil,
					"finished_at":       &now,
					"updated_at":        now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return nil
			}
			recovered = true

			if job.TaskRunID != 0 {
				changed, err := markTaskRunFailedTx(tx, job.TaskRunID, "lease_expired", errMsg, now)
				if err != nil {
					return err
				}
				if changed {
					taskRunRecovered = true
					payload := marshalRecoveryJSON(map[string]any{
						"source":          "lease_expired",
						"dispatch_job_id": job.ID,
						"ticket_id":       job.TicketID,
						"worker_id":       job.WorkerID,
						"request_id":      strings.TrimSpace(job.RequestID),
						"retry_action":    retryAction,
					})
					_ = tx.Create(&store.TaskEvent{
						TaskRunID:   job.TaskRunID,
						EventType:   "lease_expired_dispatch_failed",
						ToStateJSON: `{"orchestration_state":"failed"}`,
						Note:        errMsg,
						PayloadJSON: payload,
						CreatedAt:   now,
					}).Error
				}
			}

			changed, err := m.applyTicketStatusTx(ctx, tx, job, targetStatus, retryReason, "daemon.lease_expired", now)
			if err != nil {
				return err
			}
			if changed {
				if targetStatus == store.TicketQueued {
					queued = true
				} else if targetStatus == store.TicketBlocked {
					blocked = true
				}
			}

			_ = tx.Create(&store.InboxItem{
				Key:      fmt.Sprintf("lease_expired_dispatch_%d", job.ID),
				Status:   store.InboxOpen,
				Severity: store.InboxWarn,
				Reason:   store.InboxIncident,
				Title:    fmt.Sprintf("lease expired: dispatch %d 已标记失败", job.ID),
				Body:     fmt.Sprintf("project=%s ticket=%d worker=%d request=%s action=%s", projectName, job.TicketID, job.WorkerID, strings.TrimSpace(job.RequestID), retryAction),
				TicketID: job.TicketID,
				WorkerID: job.WorkerID,
			}).Error
			return nil
		})
		if err != nil {
			m.logf("lease check recover failed: project=%s job=%d err=%v", projectName, job.ID, err)
			continue
		}
		if recovered {
			recoveredJobs++
		}
		if taskRunRecovered {
			recoveredRuns++
		}
		if queued {
			ticketsQueued++
		}
		if blocked {
			ticketsBlocked++
		}
	}
	if recoveredJobs > 0 || recoveredRuns > 0 {
		m.logf(
			"lease check summary: project=%s recovered_jobs=%d recovered_runs=%d queued=%d blocked=%d autopilot=%v",
			projectName,
			recoveredJobs,
			recoveredRuns,
			ticketsQueued,
			ticketsBlocked,
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
	m.logger.Printf(format, args...)
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
