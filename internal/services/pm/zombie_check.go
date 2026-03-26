package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/services/core"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

type zombieCheckResult struct {
	Checked   int
	Recovered int
	Blocked   int
	Illegal   int
	Undefined int
	Errors    []string
}

func (s *Service) checkZombieWorkers(ctx context.Context, db *gorm.DB, taskRuntime core.TaskRuntime) zombieCheckResult {
	out := zombieCheckResult{}
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		out.Errors = append(out.Errors, "zombie 检查失败：db 为空")
		return out
	}
	if taskRuntime == nil {
		out.Errors = append(out.Errors, "zombie 检查失败：task runtime 为空")
		return out
	}

	if _, _, err := s.require(); err != nil {
		out.Errors = append(out.Errors, err.Error())
		return out
	}

	var running []contracts.Worker
	if err := db.WithContext(ctx).
		Where("status = ?", contracts.WorkerRunning).
		Order("id asc").
		Find(&running).Error; err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("zombie 检查查询 running workers 失败: %v", err))
		return out
	}
	ticketWorkflowByID, ticketErr := ticketWorkflowStatusesByWorker(ctx, db, running)
	if ticketErr != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("zombie 检查加载 ticket workflow 失败: %v", ticketErr))
	}
	runtimeByWorker := map[uint]contracts.TaskStatusView{}
	runtimeViewLoaded := len(running) == 0
	if len(running) > 0 {
		var rerr error
		runtimeByWorker, rerr = latestWorkerRuntimeStatus(ctx, taskRuntime)
		if rerr != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("zombie 检查加载 task_status_view 失败: %v", rerr))
		} else {
			runtimeViewLoaded = true
		}
	}

	// 加载 focus 管辖的 ticket IDs — focus controller 自己处理健康检查，
	// zombie 不应介入，避免在 worker closure grace window 内误判。
	focusManaged, _ := s.focusManagedTicketIDs(ctx, db)

	now := time.Now()
	for _, w := range running {
		out.Checked++

		// 跳过 focus 管辖的 tickets
		if _, managed := focusManaged[w.TicketID]; managed {
			continue
		}

		tv, hasTV := runtimeByWorker[w.ID]
		ticketWorkflow := ticketWorkflowByID[w.TicketID]
		deadReason := ""
		runtimeAlive := false
		deadInput := executionLossInput{}
		if !hasWorkerRuntimeHandle(w) {
			deadReason = "worker 缺少运行日志锚点"
			deadInput = executionLossInput{
				TicketID:        w.TicketID,
				WorkerID:        w.ID,
				Source:          "pm.zombie",
				ObservationKind: "host_loss",
				FailureCode:     "runtime_anchor_missing",
				Reason:          deadReason,
				Payload: map[string]any{
					"ticket_workflow":        string(ticketWorkflow),
					"runtime_anchor_present": false,
				},
				Now: now,
			}
		} else {
			switch {
			case !hasTV:
				if runtimeViewLoaded && ticketWorkflow == contracts.TicketActive {
					deadReason = "ticket active 且 worker running，但缺少可信 active task run"
					deadInput = executionLossInput{
						TicketID:        w.TicketID,
						WorkerID:        w.ID,
						Source:          "pm.zombie",
						ObservationKind: "host_loss",
						FailureCode:     "active_run_missing",
						Reason:          deadReason,
						Payload: map[string]any{
							"ticket_workflow":        string(ticketWorkflow),
							"runtime_anchor_present": true,
							"has_runtime_status":     false,
						},
						Now: now,
					}
				} else {
					// 允许 running worker 暂无活跃 run（例如刚 start 尚未 dispatch）。
					runtimeAlive = true
				}
			case isWorkerTaskRunActive(tv):
				runtimeAlive = true
			case isWithinTerminalClosureGrace(tv, now, defaultZombieClosureGrace):
				// Task run is terminal (succeeded/failed/canceled) with a semantic
				// done/wait_user, and the report is recent. Worker loop closure is
				// legitimately in progress — do NOT treat as execution lost.
				runtimeAlive = true
			default:
				state := strings.TrimSpace(strings.ToLower(tv.OrchestrationState))
				if state == "" {
					state = "unknown"
				}
				deadReason = fmt.Sprintf("worker 无活跃 task run：run=%d state=%s", tv.RunID, state)
				deadInput = executionLossInput{
					TicketID:        w.TicketID,
					WorkerID:        w.ID,
					TaskRunID:       tv.RunID,
					Source:          "pm.zombie",
					ObservationKind: "host_loss",
					FailureCode:     "active_run_missing",
					Reason:          deadReason,
					Payload: map[string]any{
						"ticket_workflow":              string(ticketWorkflow),
						"runtime_anchor_present":       true,
						"has_runtime_status":           true,
						"observed_run_id":              tv.RunID,
						"observed_orchestration_state": state,
					},
					Now: now,
				}
			}
		}
		if deadReason != "" {
			recovered, blocked, herr := s.handleDeadWorker(ctx, db, w, now, deadInput)
			if recovered {
				out.Recovered++
			}
			if blocked {
				out.Blocked++
			}
			if herr != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("dead 恢复失败：t%d w%d: %v", w.TicketID, w.ID, herr))
			}
			continue
		}
		if !runtimeAlive {
			// runtime 探测异常时不做 stalled 判定，避免误判触发恢复链路。
			continue
		}
		if !hasTV {
			continue
		}
		timedOut, lastActiveAt, hasLastActive, leaseExpired := zombieVisibilityTimedOut(tv, now)
		if !timedOut {
			continue
		}

		reason := zombieVisibilityTimeoutReason(lastActiveAt, hasLastActive, tv.LeaseExpiresAt, leaseExpired, now)
		payload := map[string]any{
			"ticket_workflow":              string(ticketWorkflow),
			"observed_run_id":              tv.RunID,
			"observed_orchestration_state": strings.TrimSpace(strings.ToLower(tv.OrchestrationState)),
			"visibility_timeout_seconds":   int(defaultZombieStallThreshold / time.Second),
		}
		if hasLastActive {
			payload["last_seen_at"] = lastActiveAt
		}
		if tv.LeaseExpiresAt != nil && !tv.LeaseExpiresAt.IsZero() {
			payload["lease_expires_at"] = *tv.LeaseExpiresAt
			payload["lease_expired"] = leaseExpired
		}
		recovered, blocked, herr := s.handleStalledWorker(ctx, db, taskRuntime, w, lastActiveAt, now, executionLossInput{
			TicketID:        w.TicketID,
			WorkerID:        w.ID,
			TaskRunID:       tv.RunID,
			Source:          "pm.zombie",
			ObservationKind: "visibility_timeout",
			FailureCode:     "runtime_stalled",
			Reason:          reason,
			Payload:         payload,
			Now:             now,
		})
		if recovered {
			out.Recovered++
		}
		if blocked {
			out.Blocked++
		}
		if herr != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("stalled 恢复失败：t%d w%d: %v", w.TicketID, w.ID, herr))
		}
	}

	stateDrift := s.reconcileZombieStateDrift(ctx, db, now)
	out.Illegal += stateDrift.Illegal
	out.Undefined += stateDrift.Undefined
	out.Blocked += stateDrift.Blocked
	out.Errors = append(out.Errors, stateDrift.Errors...)
	return out
}

type zombieStateDriftResult struct {
	Illegal   int
	Undefined int
	Blocked   int
	Errors    []string
}

func (s *Service) reconcileZombieStateDrift(ctx context.Context, db *gorm.DB, now time.Time) zombieStateDriftResult {
	out := zombieStateDriftResult{}
	if db == nil {
		out.Errors = append(out.Errors, "zombie 状态巡检失败：db 为空")
		return out
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}

	var tickets []contracts.Ticket
	if err := db.WithContext(ctx).
		Select("id", "workflow_status").
		Where("workflow_status != ?", contracts.TicketArchived).
		Order("id asc").
		Find(&tickets).Error; err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("zombie 状态巡检查询 tickets 失败: %v", err))
		return out
	}

	var workers []contracts.Worker
	if err := db.WithContext(ctx).
		Select("id", "ticket_id", "status").
		Order("id desc").
		Find(&workers).Error; err != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("zombie 状态巡检查询 workers 失败: %v", err))
		return out
	}
	latestWorkerByTicket := make(map[uint]contracts.Worker, len(workers))
	for _, w := range workers {
		if w.TicketID == 0 {
			continue
		}
		if _, exists := latestWorkerByTicket[w.TicketID]; exists {
			continue
		}
		latestWorkerByTicket[w.TicketID] = w
	}

	// 加载 focus 管辖的 ticket IDs — 同 checkZombieWorkers 的理由。
	focusManaged, _ := s.focusManagedTicketIDs(ctx, db)

	for _, t := range tickets {
		status := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
		if !fsm.TicketWorkflowTable.IsKnownState(status) {
			reason := fmt.Sprintf("ticket workflow_status 未定义：raw=%q canonical=%q", strings.TrimSpace(string(t.WorkflowStatus)), strings.TrimSpace(string(status)))
			demoted, err := s.demoteTicketBlockedOnStateAnomaly(ctx, db, t.ID, 0, "undefined_workflow_status", reason, now)
			if err != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("zombie 状态巡检处理未定义状态失败：t%d: %v", t.ID, err))
				continue
			}
			if demoted {
				out.Undefined++
				out.Blocked++
			}
			continue
		}

		if status != contracts.TicketActive {
			continue
		}

		// 跳过 focus 管辖的 tickets
		if _, managed := focusManaged[t.ID]; managed {
			continue
		}

		w, exists := latestWorkerByTicket[t.ID]
		if !exists {
			reason := "ticket active 但没有关联 worker"
			demoted, err := s.demoteTicketBlockedOnStateAnomaly(ctx, db, t.ID, 0, "active_without_worker", reason, now)
			if err != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("zombie 状态巡检处理非法状态失败：t%d: %v", t.ID, err))
				continue
			}
			if demoted {
				out.Illegal++
				out.Blocked++
			}
			continue
		}
		if w.Status != contracts.WorkerRunning {
			taskRunID, rerr := latestWorkerTaskRunIDTx(ctx, db, w.ID)
			if rerr != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("zombie 状态巡检读取最后 task run 失败：t%d w%d: %v", t.ID, w.ID, rerr))
				continue
			}
			cancelCause, cerr := latestTaskCancelCause(ctx, db, w.ID, taskRunID)
			if cerr != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("zombie 状态巡检读取 cancel cause 失败：t%d w%d: %v", t.ID, w.ID, cerr))
				continue
			}
			reason := fmt.Sprintf("ticket active 但 worker 不在 running（status=%s）", strings.TrimSpace(string(w.Status)))
			if err := s.convergeHandlerTermination(ctx, handlerTerminationInput{
				TicketID:        t.ID,
				WorkerID:        w.ID,
				TaskRunID:       taskRunID,
				Cause:           cancelCause,
				Source:          "pm.zombie",
				Reason:          reason,
				Now:             now,
				ObservationKind: "unexpected_exit",
				FailureCode:     "active_worker_not_running",
				Payload: map[string]any{
					"ticket_workflow": string(status),
					"worker_status":   string(w.Status),
				},
			}); err != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("zombie 状态巡检处理非法状态失败：t%d w%d: %v", t.ID, w.ID, err))
				continue
			}
			// user-initiated 的 blocked 是预期行为，不算 illegal；
			// 非 user-initiated（execution lost）才是异常状态漂移。
			if isUserInitiatedTaskCancelCause(cancelCause) {
				// 读取 ticket 判断是否已 blocked
				var afterTicket contracts.Ticket
				if err := db.WithContext(ctx).Select("id", "workflow_status").First(&afterTicket, t.ID).Error; err == nil {
					if contracts.CanonicalTicketWorkflowStatus(afterTicket.WorkflowStatus) == contracts.TicketBlocked {
						out.Blocked++
					}
				}
			} else {
				// 读取 ticket 判断最终状态
				var afterTicket contracts.Ticket
				if err := db.WithContext(ctx).Select("id", "workflow_status").First(&afterTicket, t.ID).Error; err != nil {
					out.Errors = append(out.Errors, fmt.Sprintf("zombie 状态巡检读取收敛后 ticket 失败：t%d: %v", t.ID, err))
					continue
				}
				afterStatus := contracts.CanonicalTicketWorkflowStatus(afterTicket.WorkflowStatus)
				if afterStatus != status {
					out.Illegal++
					if afterStatus == contracts.TicketBlocked {
						out.Blocked++
					}
				}
			}
		}
	}
	return out
}

func (s *Service) demoteTicketBlockedOnStateAnomaly(ctx context.Context, db *gorm.DB, ticketID, workerID uint, anomalyCode, reason string, now time.Time) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("db 为空")
	}
	if ticketID == 0 {
		return false, fmt.Errorf("ticket_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	anomalyCode = strings.TrimSpace(anomalyCode)
	if anomalyCode == "" {
		anomalyCode = "state_anomaly"
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "检测到非法/未定义状态，已自动降级 blocked"
	}
	if now.IsZero() {
		now = time.Now()
	}

	blocked := false
	var statusEvent *StatusChangeEvent
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t contracts.Ticket
		if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&t, ticketID).Error; err != nil {
			return err
		}
		from := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
		if !fsm.ShouldDemoteOnDispatchFailed(from) {
			return nil
		}
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleRepaired,
			Source:         "pm.zombie",
			ActorType:      contracts.TicketLifecycleActorSystem,
			WorkerID:       workerID,
			IdempotencyKey: ticketlifecycle.RepairedIdempotencyKey(ticketID, "pm.zombie.state_anomaly", now),
			Payload: lifecycleRepairPayload(contracts.TicketBlocked, contracts.IntegrationNone, map[string]any{
				"ticket_id":      ticketID,
				"worker_id":      workerID,
				"anomaly_code":   anomalyCode,
				"anomaly_reason": reason,
			}),
			CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !lifecycleResult.WorkflowChanged() {
			return nil
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.zombie", "检测到非法/未定义状态，自动降级 blocked", map[string]any{
			"ticket_id":      ticketID,
			"worker_id":      workerID,
			"anomaly_code":   anomalyCode,
			"anomaly_reason": reason,
		}, now); err != nil {
			return err
		}
		statusEvent = s.buildStatusChangeEvent(ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.zombie", now)
		if statusEvent != nil {
			statusEvent.WorkerID = workerID
			statusEvent.Detail = reason
		}

		key := inboxKeyTicketIncident(ticketID, anomalyCode)
		title := fmt.Sprintf("状态异常：t%d", ticketID)
		if workerID != 0 {
			key = inboxKeyWorkerIncident(workerID, anomalyCode)
			title = fmt.Sprintf("状态异常：t%d w%d", ticketID, workerID)
		}
		if _, err := s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
			Key:      key,
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxBlocker,
			Reason:   contracts.InboxIncident,
			Title:    title,
			Body:     reason,
			TicketID: ticketID,
			WorkerID: workerID,
		}); err != nil {
			return err
		}
		blocked = true
		return nil
	})
	if err != nil {
		return blocked, err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return blocked, nil
}

func latestWorkerRuntimeStatus(ctx context.Context, taskRuntime core.TaskRuntime) (map[uint]contracts.TaskStatusView, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if taskRuntime == nil {
		return nil, fmt.Errorf("task runtime 为空")
	}
	taskViews, err := taskRuntime.ListStatus(ctx, contracts.TaskListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		IncludeTerminal: true,
		Limit:           5000,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[uint]contracts.TaskStatusView, len(taskViews))
	for _, tv := range taskViews {
		if tv.WorkerID == 0 {
			continue
		}
		if _, exists := out[tv.WorkerID]; exists {
			continue
		}
		out[tv.WorkerID] = tv
	}
	return out, nil
}

func ticketWorkflowStatusesByWorker(ctx context.Context, db *gorm.DB, workers []contracts.Worker) (map[uint]contracts.TicketWorkflowStatus, error) {
	out := map[uint]contracts.TicketWorkflowStatus{}
	if db == nil || len(workers) == 0 {
		return out, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ids := make([]uint, 0, len(workers))
	seen := make(map[uint]struct{}, len(workers))
	for _, w := range workers {
		if w.TicketID == 0 {
			continue
		}
		if _, exists := seen[w.TicketID]; exists {
			continue
		}
		seen[w.TicketID] = struct{}{}
		ids = append(ids, w.TicketID)
	}
	if len(ids) == 0 {
		return out, nil
	}
	var tickets []contracts.Ticket
	if err := db.WithContext(ctx).
		Select("id", "workflow_status").
		Where("id IN ?", ids).
		Find(&tickets).Error; err != nil {
		return nil, err
	}
	for _, t := range tickets {
		out[t.ID] = contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
	}
	return out, nil
}

func latestZombieActivityAt(tv contracts.TaskStatusView) (time.Time, bool) {
	var latest time.Time
	for _, ts := range []*time.Time{tv.RuntimeObservedAt, tv.SemanticReportedAt, tv.LastEventAt} {
		if ts == nil || ts.IsZero() {
			continue
		}
		if latest.IsZero() || ts.After(latest) {
			latest = *ts
		}
	}
	if latest.IsZero() {
		return time.Time{}, false
	}
	return latest, true
}

func zombieVisibilityTimedOut(tv contracts.TaskStatusView, now time.Time) (bool, time.Time, bool, bool) {
	if now.IsZero() {
		now = time.Now()
	}
	lastSeenAt, hasLastSeen := latestZombieActivityAt(tv)
	leaseExpired := false
	if tv.LeaseExpiresAt != nil && !tv.LeaseExpiresAt.IsZero() && !tv.LeaseExpiresAt.After(now) {
		leaseExpired = true
	}
	if leaseExpired {
		return true, lastSeenAt, hasLastSeen, true
	}
	if !hasLastSeen {
		return false, time.Time{}, false, false
	}
	return now.Sub(lastSeenAt) >= defaultZombieStallThreshold, lastSeenAt, true, false
}

func zombieVisibilityTimeoutReason(lastSeenAt time.Time, hasLastSeen bool, leaseExpiresAt *time.Time, leaseExpired bool, now time.Time) string {
	parts := make([]string, 0, 2)
	if hasLastSeen {
		parts = append(parts, fmt.Sprintf("最近活动距今 %s（阈值 %s）", now.Sub(lastSeenAt).Round(time.Second), defaultZombieStallThreshold))
	}
	if leaseExpired && leaseExpiresAt != nil && !leaseExpiresAt.IsZero() {
		parts = append(parts, fmt.Sprintf("lease 已于 %s 过期", leaseExpiresAt.Format(time.RFC3339)))
	}
	if len(parts) == 0 {
		return fmt.Sprintf("worker 可见性超时（阈值 %s）", defaultZombieStallThreshold)
	}
	return strings.Join(parts, "；")
}

func isWorkerTaskRunActive(tv contracts.TaskStatusView) bool {
	state := strings.TrimSpace(strings.ToLower(tv.OrchestrationState))
	return state == string(contracts.TaskPending) || state == string(contracts.TaskRunning)
}

// isWorkerTaskRunTerminalButClosing returns true when the task run has reached
// a terminal orchestration state (succeeded/failed/canceled) AND the worker's
// semantic report indicates a terminal next_action (done/wait_user).
// This signals that the worker loop closure is in progress but has not yet
// committed the ticket workflow transition. The zombie detector must NOT treat
// this as "execution lost" during this legitimate grace window.
func isWorkerTaskRunTerminalButClosing(tv contracts.TaskStatusView) bool {
	state := strings.TrimSpace(strings.ToLower(tv.OrchestrationState))
	switch state {
	case string(contracts.TaskSucceeded), string(contracts.TaskFailed), string(contracts.TaskCanceled):
	default:
		return false
	}
	next := strings.TrimSpace(strings.ToLower(tv.SemanticNextAction))
	return next == string(contracts.NextDone) || next == string(contracts.NextWaitUser)
}

// isWithinTerminalClosureGrace returns true when the task run is terminal with
// a semantic done/wait_user and the semantic report is recent enough to be in
// the closure grace window. If the report is older than the grace threshold,
// the closure is assumed stuck and zombie recovery should proceed.
func isWithinTerminalClosureGrace(tv contracts.TaskStatusView, now time.Time, grace time.Duration) bool {
	if !isWorkerTaskRunTerminalButClosing(tv) {
		return false
	}
	if grace <= 0 {
		grace = defaultZombieClosureGrace
	}
	if tv.SemanticReportedAt != nil && !tv.SemanticReportedAt.IsZero() {
		return now.Sub(*tv.SemanticReportedAt) < grace
	}
	// Fallback: if no semantic report timestamp, use latest activity.
	lastSeen, ok := latestZombieActivityAt(tv)
	if !ok {
		return false
	}
	return now.Sub(lastSeen) < grace
}

func zombieRetryBackoff(retryCount int) time.Duration {
	if retryCount < 0 {
		retryCount = 0
	}
	wait := defaultZombieRetryBackoff
	for i := 0; i < retryCount; i++ {
		if wait >= defaultZombieRetryBackoffMax {
			return defaultZombieRetryBackoffMax
		}
		wait *= 2
		if wait >= defaultZombieRetryBackoffMax {
			return defaultZombieRetryBackoffMax
		}
	}
	return wait
}

func (s *Service) handleDeadWorker(ctx context.Context, db *gorm.DB, w contracts.Worker, now time.Time, input executionLossInput) (bool, bool, error) {
	if err := s.worker.MarkWorkerRuntimeNotAlive(ctx, w, now); err != nil {
		return false, false, err
	}
	if input.TicketID == 0 {
		input.TicketID = w.TicketID
	}
	if input.WorkerID == 0 {
		input.WorkerID = w.ID
	}
	if strings.TrimSpace(input.Source) == "" {
		input.Source = "pm.zombie"
	}
	if input.Now.IsZero() {
		input.Now = now
	}
	outcome, err := s.convergeExecutionLost(ctx, input)
	if err != nil {
		return false, false, err
	}
	if outcome.Requeued {
		return true, false, nil
	}
	if outcome.Escalated {
		return false, true, nil
	}
	return false, false, nil
}

func (s *Service) handleStalledWorker(ctx context.Context, db *gorm.DB, taskRuntime core.TaskRuntime, w contracts.Worker, lastActiveAt, now time.Time, input executionLossInput) (bool, bool, error) {
	interruptErr := error(nil)
	interruptRecovered := false
	if _, err := s.worker.InterruptWorker(ctx, w.ID); err != nil {
		interruptErr = err
	} else {
		if waitErr := sleepWithContext(ctx, 2*time.Second); waitErr != nil {
			interruptErr = waitErr
		} else {
			latest, ok, active, checkErr := latestWorkerActivityAfter(ctx, taskRuntime, w.ID)
			if checkErr != nil {
				interruptErr = joinZombieErrors(interruptErr, checkErr)
			} else if ok && active && latest.After(lastActiveAt) {
				interruptRecovered = true
			}
		}
	}
	if interruptRecovered {
		return true, false, nil
	}
	reason := strings.TrimSpace(input.Reason)
	failErr := s.worker.MarkWorkerFailed(ctx, w.ID, now, reason)
	if input.TicketID == 0 {
		input.TicketID = w.TicketID
	}
	if input.WorkerID == 0 {
		input.WorkerID = w.ID
	}
	if strings.TrimSpace(input.Source) == "" {
		input.Source = "pm.zombie"
	}
	if input.Now.IsZero() {
		input.Now = now
	}
	outcome, err := s.convergeExecutionLost(ctx, input)
	err = joinZombieErrors(interruptErr, failErr, err)
	if outcome.Requeued {
		return true, false, err
	}
	if outcome.Escalated {
		return false, true, err
	}
	return false, false, err
}

func latestWorkerActivityAfter(ctx context.Context, taskRuntime core.TaskRuntime, workerID uint) (time.Time, bool, bool, error) {
	if workerID == 0 {
		return time.Time{}, false, false, fmt.Errorf("worker_id 不能为空")
	}
	if taskRuntime == nil {
		return time.Time{}, false, false, fmt.Errorf("task runtime 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	list, err := taskRuntime.ListStatus(ctx, contracts.TaskListStatusOptions{
		OwnerType:       contracts.TaskOwnerWorker,
		WorkerID:        workerID,
		IncludeTerminal: true,
		Limit:           1,
	})
	if err != nil {
		return time.Time{}, false, false, err
	}
	if len(list) == 0 {
		return time.Time{}, false, false, nil
	}
	latest, ok := latestZombieActivityAt(list[0])
	return latest, ok, isWorkerTaskRunActive(list[0]), nil
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func joinZombieErrors(errs ...error) error {
	msgs := make([]string, 0, len(errs))
	for _, err := range errs {
		if err == nil {
			continue
		}
		msg := strings.TrimSpace(err.Error())
		if msg == "" {
			continue
		}
		msgs = append(msgs, msg)
	}
	if len(msgs) == 0 {
		return nil
	}
	return fmt.Errorf("%s", strings.Join(msgs, "；"))
}
