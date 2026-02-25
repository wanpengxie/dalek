package task

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"dalek/internal/store"

	"gorm.io/gorm"
)

type CreateSubagentRunInput struct {
	ProjectKey string
	TaskRunID  uint
	RequestID  string

	Provider   string
	Model      string
	Prompt     string
	CWD        string
	RuntimeDir string
}

func (s *Service) CreateSubagentRun(ctx context.Context, in CreateSubagentRunInput) (store.SubagentRun, error) {
	db, err := s.requireDB()
	if err != nil {
		return store.SubagentRun{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectKey := strings.TrimSpace(in.ProjectKey)
	if projectKey == "" {
		return store.SubagentRun{}, fmt.Errorf("project_key 不能为空")
	}
	if in.TaskRunID == 0 {
		return store.SubagentRun{}, fmt.Errorf("task_run_id 不能为空")
	}
	requestID := strings.TrimSpace(in.RequestID)
	if requestID == "" {
		return store.SubagentRun{}, fmt.Errorf("request_id 不能为空")
	}

	rec := store.SubagentRun{
		ProjectKey: projectKey,
		TaskRunID:  in.TaskRunID,
		RequestID:  requestID,
		Provider:   strings.TrimSpace(strings.ToLower(in.Provider)),
		Model:      strings.TrimSpace(in.Model),
		Prompt:     strings.TrimSpace(in.Prompt),
		CWD:        strings.TrimSpace(in.CWD),
		RuntimeDir: strings.TrimSpace(in.RuntimeDir),
	}
	if err := db.WithContext(ctx).Create(&rec).Error; err != nil {
		if isSubagentRunUniqueConflict(err) {
			if existing, ferr := s.FindSubagentRunByTaskRunID(ctx, in.TaskRunID); ferr == nil && existing != nil {
				return *existing, nil
			}
			if existing, ferr := s.FindSubagentRunByRequestID(ctx, projectKey, requestID); ferr == nil && existing != nil {
				return *existing, nil
			}
		}
		return store.SubagentRun{}, err
	}
	return rec, nil
}

func (s *Service) FindSubagentRunByTaskRunID(ctx context.Context, taskRunID uint) (*store.SubagentRun, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if taskRunID == 0 {
		return nil, fmt.Errorf("task_run_id 不能为空")
	}
	var rec store.SubagentRun
	if err := db.WithContext(ctx).Where("task_run_id = ?", taskRunID).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (s *Service) FindSubagentRunByRequestID(ctx context.Context, projectKey, requestID string) (*store.SubagentRun, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectKey = strings.TrimSpace(projectKey)
	requestID = strings.TrimSpace(requestID)
	if projectKey == "" {
		return nil, fmt.Errorf("project_key 不能为空")
	}
	if requestID == "" {
		return nil, fmt.Errorf("request_id 不能为空")
	}
	var rec store.SubagentRun
	if err := db.WithContext(ctx).
		Where("project_key = ? AND request_id = ?", projectKey, requestID).
		Order("id desc").
		First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

func (s *Service) ListSubagentRuns(ctx context.Context, projectKey string, limit int) ([]store.SubagentRun, error) {
	db, err := s.requireDB()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	projectKey = strings.TrimSpace(projectKey)
	if projectKey == "" {
		return nil, fmt.Errorf("project_key 不能为空")
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	var out []store.SubagentRun
	if err := db.WithContext(ctx).
		Model(&store.SubagentRun{}).
		Where("project_key = ?", projectKey).
		Order("task_run_id desc").
		Limit(limit).
		Find(&out).Error; err != nil {
		return nil, err
	}
	if out == nil {
		return []store.SubagentRun{}, nil
	}
	return out, nil
}

func isSubagentRunUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "subagent_runs.task_run_id") {
		return true
	}
	if strings.Contains(msg, "unique constraint failed") && strings.Contains(msg, "subagent_runs.project_key") && strings.Contains(msg, "subagent_runs.request_id") {
		return true
	}
	return false
}
