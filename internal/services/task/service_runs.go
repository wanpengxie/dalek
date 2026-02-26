package task

import (
	"context"
	"dalek/internal/contracts"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

func (s *Service) CreateRun(ctx context.Context, in contracts.TaskRunCreateInput) (contracts.TaskRun, error) {
	db, err := s.requireDB()
	if err != nil {
		return contracts.TaskRun{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	in.RequestID = strings.TrimSpace(in.RequestID)
	if in.RequestID == "" {
		return contracts.TaskRun{}, fmt.Errorf("request_id 不能为空")
	}
	if !validOwnerType(in.OwnerType) {
		return contracts.TaskRun{}, fmt.Errorf("owner_type 非法: %s", in.OwnerType)
	}
	if strings.TrimSpace(in.TaskType) == "" {
		return contracts.TaskRun{}, fmt.Errorf("task_type 不能为空")
	}
	if !validOrchestrationState(in.OrchestrationState) {
		in.OrchestrationState = contracts.TaskPending
	}
	if in.Attempt < 0 {
		in.Attempt = 0
	}

	run := contracts.TaskRun{
		OwnerType:          in.OwnerType,
		TaskType:           strings.TrimSpace(in.TaskType),
		ProjectKey:         strings.TrimSpace(in.ProjectKey),
		TicketID:           in.TicketID,
		WorkerID:           in.WorkerID,
		SubjectType:        strings.TrimSpace(in.SubjectType),
		SubjectID:          strings.TrimSpace(in.SubjectID),
		RequestID:          in.RequestID,
		OrchestrationState: in.OrchestrationState,
		RunnerID:           strings.TrimSpace(in.RunnerID),
		LeaseExpiresAt:     in.LeaseExpiresAt,
		Attempt:            in.Attempt,
		RequestPayloadJSON: strings.TrimSpace(in.RequestPayloadJSON),
		ResultPayloadJSON:  strings.TrimSpace(in.ResultPayloadJSON),
		ErrorCode:          strings.TrimSpace(in.ErrorCode),
		ErrorMessage:       strings.TrimSpace(in.ErrorMessage),
		StartedAt:          in.StartedAt,
		FinishedAt:         in.FinishedAt,
	}
	if err := db.WithContext(ctx).Create(&run).Error; err != nil {
		if isRequestIDUniqueConflict(err) {
			if existing, ferr := s.FindRunByRequestID(ctx, in.RequestID); ferr == nil && existing != nil {
				return *existing, nil
			}
		}
		return contracts.TaskRun{}, err
	}
	return run, nil
}

func (s *Service) FindRunByID(ctx context.Context, runID uint) (*contracts.TaskRun, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return nil, fmt.Errorf("run_id 不能为空")
	}
	var run contracts.TaskRun
	if err := db.WithContext(ctx).First(&run, runID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *Service) FindRunByRequestID(ctx context.Context, requestID string) (*contracts.TaskRun, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return nil, fmt.Errorf("request_id 不能为空")
	}
	var run contracts.TaskRun
	if err := db.WithContext(ctx).Where("request_id = ?", requestID).Order("id desc").First(&run).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *Service) LatestActiveWorkerRun(ctx context.Context, workerID uint) (*contracts.TaskRun, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID == 0 {
		return nil, fmt.Errorf("worker_id 不能为空")
	}
	var run contracts.TaskRun
	if err := db.WithContext(ctx).
		Where("owner_type = ? AND worker_id = ? AND orchestration_state IN ?", contracts.TaskOwnerWorker, workerID, []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning}).
		Order("id desc").
		First(&run).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &run, nil
}

func (s *Service) CancelActiveWorkerRuns(ctx context.Context, workerID uint, reason string, now time.Time) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if workerID == 0 {
		return fmt.Errorf("worker_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "canceled"
	}
	return db.WithContext(ctx).Model(&contracts.TaskRun{}).
		Where("owner_type = ? AND worker_id = ? AND orchestration_state IN ?", contracts.TaskOwnerWorker, workerID, []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning}).
		Updates(map[string]any{
			"orchestration_state": contracts.TaskCanceled,
			"error_code":          "superseded",
			"error_message":       reason,
			"finished_at":         &now,
			"runner_id":           "",
			"lease_expires_at":    nil,
		}).Error
}

func (s *Service) MarkRunRunning(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time, now time.Time, bumpAttempt bool) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}
	updates := map[string]any{
		"orchestration_state": contracts.TaskRunning,
		"runner_id":           strings.TrimSpace(runnerID),
		"lease_expires_at":    leaseExpiresAt,
		"started_at":          &now,
		"finished_at":         nil,
		"error_code":          "",
		"error_message":       "",
	}
	if bumpAttempt {
		updates["attempt"] = gorm.Expr("attempt + 1")
	}
	res := db.WithContext(ctx).Model(&contracts.TaskRun{}).
		Where("id = ? AND orchestration_state IN ?", runID, []contracts.TaskOrchestrationState{contracts.TaskPending, contracts.TaskRunning}).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ensureRunCanMarkRunning(db.WithContext(ctx), runID)
	}
	return nil
}

func (s *Service) RenewLease(ctx context.Context, runID uint, runnerID string, leaseExpiresAt *time.Time) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	runnerID = strings.TrimSpace(runnerID)
	if runnerID == "" {
		return fmt.Errorf("runner_id 不能为空")
	}
	res := db.WithContext(ctx).Model(&contracts.TaskRun{}).
		Where("id = ? AND runner_id = ? AND orchestration_state = ?", runID, runnerID, contracts.TaskRunning).
		Updates(map[string]any{
			"lease_expires_at": leaseExpiresAt,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("task_run 不可续租: run_id=%d runner=%s", runID, runnerID)
	}
	return nil
}

func (s *Service) MarkRunSucceeded(ctx context.Context, runID uint, resultPayloadJSON string, now time.Time) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}
	res := db.WithContext(ctx).Model(&contracts.TaskRun{}).Where("id = ? AND orchestration_state != ?", runID, contracts.TaskCanceled).Updates(map[string]any{
		"orchestration_state": contracts.TaskSucceeded,
		"result_payload_json": strings.TrimSpace(resultPayloadJSON),
		"error_code":          "",
		"error_message":       "",
		"runner_id":           "",
		"lease_expires_at":    nil,
		"finished_at":         &now,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ensureRunExistsForTerminalUpdate(db.WithContext(ctx), runID)
	}
	return nil
}

func (s *Service) MarkRunFailed(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}
	res := db.WithContext(ctx).Model(&contracts.TaskRun{}).Where("id = ? AND orchestration_state != ?", runID, contracts.TaskCanceled).Updates(map[string]any{
		"orchestration_state": contracts.TaskFailed,
		"error_code":          strings.TrimSpace(errorCode),
		"error_message":       strings.TrimSpace(errorMessage),
		"runner_id":           "",
		"lease_expires_at":    nil,
		"finished_at":         &now,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ensureRunExistsForTerminalUpdate(db.WithContext(ctx), runID)
	}
	return nil
}

func (s *Service) MarkRunCanceled(ctx context.Context, runID uint, errorCode string, errorMessage string, now time.Time) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return fmt.Errorf("run_id 不能为空")
	}
	if now.IsZero() {
		now = time.Now()
	}
	return db.WithContext(ctx).Model(&contracts.TaskRun{}).Where("id = ?", runID).Updates(map[string]any{
		"orchestration_state": contracts.TaskCanceled,
		"error_code":          strings.TrimSpace(errorCode),
		"error_message":       strings.TrimSpace(errorMessage),
		"runner_id":           "",
		"lease_expires_at":    nil,
		"finished_at":         &now,
	}).Error
}

func isRequestIDUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "task_runs.request_id")
}

func ensureRunExistsForTerminalUpdate(db *gorm.DB, runID uint) error {
	if db == nil {
		return fmt.Errorf("task service db 为空")
	}
	var count int64
	if err := db.Model(&contracts.TaskRun{}).Where("id = ?", runID).Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("task_run 不存在: run_id=%d", runID)
	}
	return nil
}

func ensureRunCanMarkRunning(db *gorm.DB, runID uint) error {
	if db == nil {
		return fmt.Errorf("task service db 为空")
	}
	var run contracts.TaskRun
	if err := db.Select("id", "orchestration_state").First(&run, runID).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("task_run 不存在: run_id=%d", runID)
		}
		return err
	}
	return fmt.Errorf("task_run 不能标记为 running: run_id=%d state=%s", runID, run.OrchestrationState)
}
