package pm

import (
	"context"
	"strings"
	"time"

	"dalek/internal/contracts"

	"gorm.io/gorm"
)

const defaultHealthMetricsWindow = 7 * 24 * time.Hour

type HealthMetricsOptions struct {
	Since *time.Time
	Until *time.Time
}

type HealthMetrics struct {
	WindowStart time.Time `json:"window_start"`
	WindowEnd   time.Time `json:"window_end"`

	WorkerBootstrapFailureRate float64 `json:"worker_bootstrap_failure_rate"`

	TerminalStateConflictCount   int64 `json:"terminal_state_conflict_count"`
	DuplicateTerminalReportCount int64 `json:"duplicate_terminal_report_count"`
	MergeDiscardCount            int64 `json:"merge_discard_count"`
	IntegrationTicketCount       int64 `json:"integration_ticket_count"`
	ManualInterventionCount      int64 `json:"manual_intervention_count"`

	WorkerBootstrapFailureCount int64 `json:"worker_bootstrap_failure_count"`
	WorkerRunCount              int64 `json:"worker_run_count"`
}

type healthMetricsWindow struct {
	Start time.Time
	End   time.Time
}

func (s *Service) CalculateHealthMetrics(ctx context.Context, opt HealthMetricsOptions) (HealthMetrics, error) {
	_, db, err := s.require()
	if err != nil {
		return HealthMetrics{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	window := normalizeHealthMetricsWindow(opt, time.Now().UTC())
	out := HealthMetrics{
		WindowStart: window.Start,
		WindowEnd:   window.End,
	}

	out.TerminalStateConflictCount, err = s.countTaskEventsByType(ctx, db, "terminal_state_overridden", window)
	if err != nil {
		return out, err
	}
	out.DuplicateTerminalReportCount, err = s.countTaskEventsByType(ctx, db, "duplicate_terminal_report", window)
	if err != nil {
		return out, err
	}
	out.MergeDiscardCount, err = s.countMergeItemsByStatus(ctx, db, contracts.MergeDiscarded, window)
	if err != nil {
		return out, err
	}
	out.ManualInterventionCount, err = s.countInboxItemsByReason(ctx, db, contracts.InboxNeedsUser, window)
	if err != nil {
		return out, err
	}

	out.WorkerRunCount, err = s.countTaskRunsByTypeAndStatus(ctx, db, contracts.TaskOwnerWorker, contracts.TaskTypeDeliverTicket, "", window)
	if err != nil {
		return out, err
	}
	out.WorkerBootstrapFailureCount, err = s.countTaskRunsByTypeAndStatus(ctx, db, contracts.TaskOwnerWorker, contracts.TaskTypeDeliverTicket, contracts.TaskFailed, window)
	if err != nil {
		return out, err
	}
	out.WorkerBootstrapFailureRate = safeRate(out.WorkerBootstrapFailureCount, out.WorkerRunCount)

	index, found := s.tryLoadSurfaceConflictIndex()
	if found {
		out.IntegrationTicketCount = index.IntegrationNodeCount
	}

	return out, nil
}

func normalizeHealthMetricsWindow(opt HealthMetricsOptions, now time.Time) healthMetricsWindow {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	end := now.UTC()
	if opt.Until != nil && !opt.Until.IsZero() {
		end = opt.Until.UTC()
	}
	start := end.Add(-defaultHealthMetricsWindow)
	if opt.Since != nil && !opt.Since.IsZero() {
		start = opt.Since.UTC()
	}
	if start.After(end) {
		start, end = end, start
	}
	return healthMetricsWindow{
		Start: start,
		End:   end,
	}
}

func safeRate(numerator, denominator int64) float64 {
	if denominator <= 0 || numerator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func applyHealthMetricsWindow(q *gorm.DB, column string, window healthMetricsWindow) *gorm.DB {
	if q == nil {
		return nil
	}
	column = strings.TrimSpace(column)
	if column == "" {
		column = "created_at"
	}
	if !window.Start.IsZero() {
		q = q.Where(column+" >= ?", window.Start)
	}
	if !window.End.IsZero() {
		q = q.Where(column+" <= ?", window.End)
	}
	return q
}

func (s *Service) countTaskEventsByType(ctx context.Context, db *gorm.DB, eventType string, window healthMetricsWindow) (int64, error) {
	eventType = strings.TrimSpace(eventType)
	var count int64
	q := db.WithContext(ctx).Model(&contracts.TaskEvent{}).Where("event_type = ?", eventType)
	q = applyHealthMetricsWindow(q, "created_at", window)
	if err := q.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Service) countMergeItemsByStatus(ctx context.Context, db *gorm.DB, status contracts.MergeStatus, window healthMetricsWindow) (int64, error) {
	var count int64
	q := db.WithContext(ctx).Model(&contracts.MergeItem{}).Where("status = ?", status)
	q = applyHealthMetricsWindow(q, "updated_at", window)
	if err := q.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Service) countInboxItemsByReason(ctx context.Context, db *gorm.DB, reason contracts.InboxReason, window healthMetricsWindow) (int64, error) {
	var count int64
	q := db.WithContext(ctx).Model(&contracts.InboxItem{}).Where("reason = ?", reason)
	q = applyHealthMetricsWindow(q, "created_at", window)
	if err := q.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Service) countTaskRunsByTypeAndStatus(ctx context.Context, db *gorm.DB, owner contracts.TaskOwnerType, taskType string, status contracts.TaskOrchestrationState, window healthMetricsWindow, extraWhere ...any) (int64, error) {
	var count int64
	q := db.WithContext(ctx).Model(&contracts.TaskRun{})
	if strings.TrimSpace(string(owner)) != "" {
		q = q.Where("owner_type = ?", owner)
	}
	taskType = strings.TrimSpace(taskType)
	if taskType != "" {
		q = q.Where("task_type = ?", taskType)
	}
	if strings.TrimSpace(string(status)) != "" {
		q = q.Where("orchestration_state = ?", status)
	}
	q = applyHealthMetricsWindow(q, "created_at", window)
	if len(extraWhere) > 0 {
		query, ok := extraWhere[0].(string)
		if ok && strings.TrimSpace(query) != "" {
			q = q.Where(query, extraWhere[1:]...)
		}
	}
	if err := q.Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}
