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

func (s *Service) AppendEvent(ctx context.Context, in contracts.TaskEventInput) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if in.TaskRunID == 0 {
		return fmt.Errorf("task_run_id 不能为空")
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now()
	}
	eventType := strings.TrimSpace(in.EventType)
	if eventType == "" {
		eventType = "task_event"
	}
	if eventType == "task_succeeded" || eventType == "task_failed" {
		var run contracts.TaskRun
		err := db.WithContext(ctx).
			Model(&contracts.TaskRun{}).
			Select("id", "orchestration_state").
			Where("id = ?", in.TaskRunID).
			Take(&run).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err == nil && run.OrchestrationState == contracts.TaskCanceled {
			return nil
		}
	}
	ev := contracts.TaskEvent{
		TaskRunID:     in.TaskRunID,
		EventType:     eventType,
		FromStateJSON: toJSON(in.FromState),
		ToStateJSON:   toJSON(in.ToState),
		Note:          strings.TrimSpace(in.Note),
		PayloadJSON:   toJSON(in.Payload),
		CreatedAt:     in.CreatedAt,
	}
	return db.WithContext(ctx).Create(&ev).Error
}

func (s *Service) AppendRuntimeSample(ctx context.Context, in contracts.TaskRuntimeSampleInput) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if in.TaskRunID == 0 {
		return fmt.Errorf("task_run_id 不能为空")
	}
	if !validHealthState(in.State) {
		in.State = contracts.TaskHealthUnknown
	}
	if in.ObservedAt.IsZero() {
		in.ObservedAt = time.Now()
	}
	sample := contracts.TaskRuntimeSample{
		TaskRunID:   in.TaskRunID,
		State:       in.State,
		NeedsUser:   in.NeedsUser,
		Summary:     strings.TrimSpace(in.Summary),
		Source:      strings.TrimSpace(in.Source),
		ObservedAt:  in.ObservedAt,
		MetricsJSON: toJSON(in.Metrics),
	}
	return db.WithContext(ctx).Create(&sample).Error
}

func (s *Service) AppendSemanticReport(ctx context.Context, in contracts.TaskSemanticReportInput) error {
	db, err := s.requireDB()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if in.TaskRunID == 0 {
		return fmt.Errorf("task_run_id 不能为空")
	}
	if !validSemanticPhase(in.Phase) {
		in.Phase = contracts.TaskPhaseImplementing
	}
	if in.ReportedAt.IsZero() {
		in.ReportedAt = time.Now()
	}
	report := contracts.TaskSemanticReport{
		TaskRunID:         in.TaskRunID,
		Phase:             in.Phase,
		Milestone:         strings.TrimSpace(in.Milestone),
		NextAction:        strings.TrimSpace(in.NextAction),
		Summary:           strings.TrimSpace(in.Summary),
		ReportPayloadJSON: toJSON(in.Payload),
		ReportedAt:        in.ReportedAt,
	}
	return db.WithContext(ctx).Create(&report).Error
}
