package pm

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
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
	runtimeByWorker := map[uint]contracts.TaskStatusView{}
	if len(running) > 0 {
		var rerr error
		runtimeByWorker, rerr = latestWorkerRuntimeStatus(ctx, taskRuntime)
		if rerr != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("zombie 检查加载 task_status_view 失败: %v", rerr))
		}
	}

	now := time.Now()
	for _, w := range running {
		out.Checked++

		tv, hasTV := runtimeByWorker[w.ID]
		deadReason := ""
		runtimeAlive := false
		if !hasWorkerRuntimeHandle(w) {
			deadReason = "worker 缺少运行日志锚点"
		} else {
			switch {
			case !hasTV:
				// 允许 running worker 暂无活跃 run（例如刚 start 尚未 dispatch）。
				runtimeAlive = true
			case isWorkerTaskRunActive(tv):
				runtimeAlive = true
			default:
				state := strings.TrimSpace(strings.ToLower(tv.OrchestrationState))
				if state == "" {
					state = "unknown"
				}
				deadReason = fmt.Sprintf("worker 无活跃 task run：run=%d state=%s", tv.RunID, state)
			}
		}
		if deadReason != "" {
			recovered, blocked, herr := s.handleDeadWorker(ctx, db, w, now, deadReason)
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
		lastActiveAt, ok := latestZombieActivityAt(tv)
		if !ok {
			continue
		}
		idle := now.Sub(lastActiveAt)
		if idle < defaultZombieStallThreshold {
			continue
		}

		reason := fmt.Sprintf("最近活动距今 %s（阈值 %s）", idle.Round(time.Second), defaultZombieStallThreshold)
		recovered, blocked, herr := s.handleStalledWorker(ctx, db, taskRuntime, w, lastActiveAt, now, reason)
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
			reason := fmt.Sprintf("ticket active 但 worker 不在 running（status=%s）", strings.TrimSpace(string(w.Status)))
			demoted, err := s.demoteTicketBlockedOnStateAnomaly(ctx, db, t.ID, w.ID, "active_worker_not_running", reason, now)
			if err != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("zombie 状态巡检处理非法状态失败：t%d w%d: %v", t.ID, w.ID, err))
				continue
			}
			if demoted {
				out.Illegal++
				out.Blocked++
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
		if err := tx.WithContext(ctx).Model(&contracts.Ticket{}).
			Where("id = ?", ticketID).
			Updates(map[string]any{
				"workflow_status": contracts.TicketBlocked,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, from, contracts.TicketBlocked, "pm.zombie", "检测到非法/未定义状态，自动降级 blocked", map[string]any{
			"ticket_id":      ticketID,
			"worker_id":      workerID,
			"anomaly_code":   anomalyCode,
			"anomaly_reason": reason,
		}, now); err != nil {
			return err
		}
		if err := s.appendTicketLifecycleEventTx(ctx, tx, ticketlifecycle.AppendInput{
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
		}); err != nil {
			return err
		}
		statusEvent = s.buildStatusChangeEvent(ticketID, from, contracts.TicketBlocked, "pm.zombie", now)
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

func isWorkerTaskRunActive(tv contracts.TaskStatusView) bool {
	state := strings.TrimSpace(strings.ToLower(tv.OrchestrationState))
	return state == string(contracts.TaskPending) || state == string(contracts.TaskRunning)
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

func shouldAttemptZombieRecovery(w contracts.Worker, now time.Time) (attempt bool, block bool) {
	if w.RetryCount >= defaultZombieMaxRetries {
		return false, true
	}
	if w.LastRetryAt == nil || w.LastRetryAt.IsZero() {
		return true, false
	}
	wait := zombieRetryBackoff(w.RetryCount)
	if wait <= 0 {
		return true, false
	}
	return now.Sub(*w.LastRetryAt) >= wait, false
}

func (s *Service) handleDeadWorker(ctx context.Context, db *gorm.DB, w contracts.Worker, now time.Time, reason string) (bool, bool, error) {
	hash := zombieErrorHash("dead", reason)
	attempt, block := shouldAttemptZombieRecovery(w, now)
	if block {
		blocked, err := s.blockZombieWorker(ctx, db, w, now, reason, hash)
		return false, blocked, err
	}
	if !attempt {
		return false, false, nil
	}

	recoveryErr := s.recoverWorkerByRestartChain(ctx, w.TicketID)
	nextRetry := w.RetryCount + 1
	recordErr := s.recordZombieRetryAttempt(ctx, db, w.ID, nextRetry, now, hash)

	if recoveryErr != nil || recordErr != nil {
		err := joinZombieErrors(recoveryErr, recordErr)
		blocked := false
		if recoveryErr != nil && nextRetry >= defaultZombieMaxRetries {
			w.RetryCount = nextRetry
			b, berr := s.blockZombieWorker(ctx, db, w, now, reason, hash)
			blocked = b
			err = joinZombieErrors(err, berr)
		}
		return false, blocked, err
	}
	return true, false, nil
}

func (s *Service) handleStalledWorker(ctx context.Context, db *gorm.DB, taskRuntime core.TaskRuntime, w contracts.Worker, lastActiveAt, now time.Time, reason string) (bool, bool, error) {
	hash := zombieErrorHash("stalled", reason)
	attempt, block := shouldAttemptZombieRecovery(w, now)
	if block {
		blocked, err := s.blockZombieWorker(ctx, db, w, now, reason, hash)
		return false, blocked, err
	}
	if !attempt {
		return false, false, nil
	}

	interruptErr := error(nil)
	interruptRecovered := false
	if _, err := s.worker.InterruptWorker(ctx, w.ID); err != nil {
		interruptErr = err
	} else {
		if waitErr := sleepWithContext(ctx, 2*time.Second); waitErr != nil {
			interruptErr = waitErr
		} else {
			latest, ok, checkErr := latestWorkerActivityAfter(ctx, taskRuntime, w.ID)
			if checkErr != nil {
				interruptErr = joinZombieErrors(interruptErr, checkErr)
			} else if ok && latest.After(lastActiveAt) {
				interruptRecovered = true
			}
		}
	}

	recoveryErr := error(nil)
	if !interruptRecovered {
		recoveryErr = s.recoverWorkerByRestartChain(ctx, w.TicketID)
	}

	nextRetry := w.RetryCount + 1
	recordErr := s.recordZombieRetryAttempt(ctx, db, w.ID, nextRetry, now, hash)
	if recoveryErr == nil && recordErr == nil {
		return true, false, nil
	}

	err := joinZombieErrors(interruptErr, recoveryErr, recordErr)
	blocked := false
	if recoveryErr != nil && nextRetry >= defaultZombieMaxRetries {
		w.RetryCount = nextRetry
		b, berr := s.blockZombieWorker(ctx, db, w, now, reason, hash)
		blocked = b
		err = joinZombieErrors(err, berr)
	}
	return false, blocked, err
}

func latestWorkerActivityAfter(ctx context.Context, taskRuntime core.TaskRuntime, workerID uint) (time.Time, bool, error) {
	if workerID == 0 {
		return time.Time{}, false, fmt.Errorf("worker_id 不能为空")
	}
	if taskRuntime == nil {
		return time.Time{}, false, fmt.Errorf("task runtime 为空")
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
		return time.Time{}, false, err
	}
	if len(list) == 0 {
		return time.Time{}, false, nil
	}
	latest, ok := latestZombieActivityAt(list[0])
	return latest, ok, nil
}

func (s *Service) recoverWorkerByRestartChain(ctx context.Context, ticketID uint) error {
	stopErr := s.StopTicket(ctx, ticketID)
	if _, err := s.StartTicket(ctx, ticketID); err != nil {
		if stopErr != nil {
			return fmt.Errorf("stop 失败: %v；start 失败: %w", stopErr, err)
		}
		return fmt.Errorf("start 失败: %w", err)
	}
	if _, err := s.DirectDispatchWorker(ctx, ticketID, DirectDispatchOptions{}); err != nil {
		if stopErr != nil {
			return fmt.Errorf("stop 失败: %v；worker run 失败: %w", stopErr, err)
		}
		return fmt.Errorf("worker run 失败: %w", err)
	}
	return nil
}

func (s *Service) recordZombieRetryAttempt(ctx context.Context, db *gorm.DB, workerID uint, retryCount int, now time.Time, errHash string) error {
	if db == nil {
		return fmt.Errorf("db 为空")
	}
	if workerID == 0 {
		return fmt.Errorf("worker_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return db.WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", workerID).Updates(map[string]any{
		"retry_count":     retryCount,
		"last_retry_at":   &now,
		"last_error_hash": strings.TrimSpace(errHash),
		"updated_at":      now,
	}).Error
}

func (s *Service) blockZombieWorker(ctx context.Context, db *gorm.DB, w contracts.Worker, now time.Time, reason, errHash string) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("db 为空")
	}
	if w.TicketID == 0 {
		return false, fmt.Errorf("ticket_id 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "zombie 恢复重试耗尽"
	}
	errHash = strings.TrimSpace(errHash)

	blocked := false
	var statusEvent *StatusChangeEvent
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t contracts.Ticket
		if err := tx.WithContext(ctx).Select("id", "workflow_status").First(&t, w.TicketID).Error; err != nil {
			return err
		}
		from := contracts.CanonicalTicketWorkflowStatus(t.WorkflowStatus)
		if !fsm.ShouldDemoteOnDispatchFailed(from) {
			return tx.WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
				"last_error_hash": errHash,
				"updated_at":      now,
			}).Error
		}

		if err := tx.WithContext(ctx).Model(&contracts.Ticket{}).
			Where("id = ?", w.TicketID).
			Updates(map[string]any{
				"workflow_status": contracts.TicketBlocked,
				"updated_at":      now,
			}).Error; err != nil {
			return err
		}

		if err := s.appendTicketWorkflowEventTx(ctx, tx, w.TicketID, from, contracts.TicketBlocked, "pm.zombie", "zombie 恢复重试耗尽，自动降级 blocked", map[string]any{
			"ticket_id":       w.TicketID,
			"worker_id":       w.ID,
			"retry_count":     w.RetryCount,
			"max_retries":     defaultZombieMaxRetries,
			"last_error_hash": errHash,
			"reason":          reason,
		}, now); err != nil {
			return err
		}
		if err := s.appendTicketLifecycleEventTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       w.TicketID,
			EventType:      contracts.TicketLifecycleRepaired,
			Source:         "pm.zombie",
			ActorType:      contracts.TicketLifecycleActorSystem,
			WorkerID:       w.ID,
			IdempotencyKey: ticketlifecycle.RepairedIdempotencyKey(w.TicketID, "pm.zombie.retry_exhausted", now),
			Payload: lifecycleRepairPayload(contracts.TicketBlocked, contracts.IntegrationNone, map[string]any{
				"ticket_id":       w.TicketID,
				"worker_id":       w.ID,
				"retry_count":     w.RetryCount,
				"max_retries":     defaultZombieMaxRetries,
				"last_error_hash": errHash,
				"reason":          reason,
			}),
			CreatedAt: now,
		}); err != nil {
			return err
		}
		statusEvent = s.buildStatusChangeEvent(w.TicketID, from, contracts.TicketBlocked, "pm.zombie", now)
		if statusEvent != nil {
			statusEvent.WorkerID = w.ID
			statusEvent.Detail = reason
		}

		if _, err := s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
			Key:      inboxKeyWorkerIncident(w.ID, "zombie_blocked"),
			Status:   contracts.InboxOpen,
			Severity: contracts.InboxBlocker,
			Reason:   contracts.InboxIncident,
			Title:    fmt.Sprintf("僵尸恢复失败：t%d w%d", w.TicketID, w.ID),
			Body:     reason,
			TicketID: w.TicketID,
			WorkerID: w.ID,
		}); err != nil {
			return err
		}

		if err := tx.WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"last_error_hash": errHash,
			"updated_at":      now,
		}).Error; err != nil {
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

func zombieErrorHash(parts ...string) string {
	raw := strings.TrimSpace(strings.Join(parts, "|"))
	sum := sha1.Sum([]byte(raw))
	return hex.EncodeToString(sum[:])
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
