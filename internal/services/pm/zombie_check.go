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

	"gorm.io/gorm"
)

type zombieCheckResult struct {
	Checked   int
	Recovered int
	Blocked   int
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

	p, _, err := s.require()
	if err != nil {
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
	if len(running) == 0 {
		return out
	}

	runtimeByWorker, rerr := latestWorkerRuntimeStatus(ctx, taskRuntime)
	if rerr != nil {
		out.Errors = append(out.Errors, fmt.Sprintf("zombie 检查加载 task_status_view 失败: %v", rerr))
	}

	defaultSocket := strings.TrimSpace(p.Config.WithDefaults().TmuxSocket)
	sessionCache := map[string]map[string]bool{}
	sessionErrs := map[string]error{}
	loadSessions := func(socket string) (map[string]bool, error) {
		socket = strings.TrimSpace(socket)
		if socket == "" {
			socket = defaultSocket
		}
		if sess, ok := sessionCache[socket]; ok {
			return sess, nil
		}
		if err, ok := sessionErrs[socket]; ok {
			return nil, err
		}
		listCtx, cancel := context.WithTimeout(ctx, tmuxListSessionsTimeout)
		defer cancel()
		sess, err := p.Tmux.ListSessions(listCtx, socket)
		if err != nil {
			sessionErrs[socket] = err
			return nil, err
		}
		sessionCache[socket] = sess
		return sess, nil
	}

	now := time.Now()
	for _, w := range running {
		out.Checked++
		sessionName := strings.TrimSpace(w.TmuxSession)
		socket := strings.TrimSpace(w.TmuxSocket)
		if socket == "" {
			socket = defaultSocket
		}

		deadReason := ""
		switch {
		case sessionName == "":
			deadReason = "tmux_session 为空"
		default:
			sessions, serr := loadSessions(socket)
			if serr != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("zombie dead 检查失败：t%d w%d: %v", w.TicketID, w.ID, serr))
			} else if !sessions[sessionName] {
				deadReason = fmt.Sprintf("tmux session 不存在：%s", sessionName)
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

		tv, ok := runtimeByWorker[w.ID]
		if !ok {
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
	return out
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
	if _, err := s.DispatchTicket(ctx, ticketID); err != nil {
		if stopErr != nil {
			return fmt.Errorf("stop 失败: %v；dispatch 失败: %w", stopErr, err)
		}
		return fmt.Errorf("dispatch 失败: %w", err)
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
	return blocked, err
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
