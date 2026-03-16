package run

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	tasksvc "dalek/internal/services/task"

	"gorm.io/gorm"
)

type RunStatusFetcher interface {
	FetchRun(ctx context.Context, runID uint) (RemoteRunStatus, error)
	FetchRunByRequestID(ctx context.Context, requestID string) (RemoteRunStatus, error)
}

type RemoteRunStatus struct {
	Found         bool
	RunID         uint
	RequestID     string
	Status        string
	Summary       string
	SnapshotID    string
	BaseCommit    string
	VerifyTarget  string
	LastEventType string
	LastEventNote string
	UpdatedAt     time.Time
}

type Reconciler struct {
	db      *gorm.DB
	task    *tasksvc.Service
	fetcher RunStatusFetcher
	now     func() time.Time
}

type ReconcileResult struct {
	Found      bool
	RunID      uint
	RequestID  string
	Status     contracts.RunStatus
	Reconciled bool
}

func NewReconciler(db *gorm.DB, task *tasksvc.Service, fetcher RunStatusFetcher) *Reconciler {
	return &Reconciler{
		db:      db,
		task:    task,
		fetcher: fetcher,
		now:     time.Now,
	}
}

func (r *Reconciler) ReconcileByRunID(ctx context.Context, runID uint) (ReconcileResult, error) {
	if r == nil || r.db == nil || r.fetcher == nil {
		return ReconcileResult{}, fmt.Errorf("run reconciler 未初始化")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return ReconcileResult{}, fmt.Errorf("run_id 不能为空")
	}
	var view contracts.RunView
	if err := r.db.WithContext(ctx).Where("run_id = ?", runID).First(&view).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return ReconcileResult{Found: false, RunID: runID}, nil
		}
		return ReconcileResult{}, err
	}
	remote, err := r.fetcher.FetchRun(ctx, runID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if !remote.Found {
		return ReconcileResult{Found: false, RunID: runID, RequestID: view.RequestID}, nil
	}
	if isStaleRemoteUpdate(view, remote) {
		return ReconcileResult{Found: true, RunID: runID, RequestID: view.RequestID, Status: view.RunStatus, Reconciled: false}, nil
	}
	status := normalizeRunStatus(remote.Status)
	updates := map[string]any{}
	if status != "" && view.RunStatus != status {
		updates["run_status"] = status
		view.RunStatus = status
	}
	if view.SnapshotID == "" && strings.TrimSpace(remote.SnapshotID) != "" {
		updates["snapshot_id"] = strings.TrimSpace(remote.SnapshotID)
		view.SnapshotID = strings.TrimSpace(remote.SnapshotID)
	}
	if view.BaseCommit == "" && strings.TrimSpace(remote.BaseCommit) != "" {
		updates["base_commit"] = strings.TrimSpace(remote.BaseCommit)
		view.BaseCommit = strings.TrimSpace(remote.BaseCommit)
	}
	if view.VerifyTarget == "" && strings.TrimSpace(remote.VerifyTarget) != "" {
		updates["verify_target"] = strings.TrimSpace(remote.VerifyTarget)
		view.VerifyTarget = strings.TrimSpace(remote.VerifyTarget)
	}
	if len(updates) > 0 {
		if err := r.db.WithContext(ctx).Model(&contracts.RunView{}).Where("run_id = ?", runID).Updates(updates).Error; err != nil {
			return ReconcileResult{}, err
		}
	}
	r.appendReconcileEvent(ctx, runID, status, remote)
	return ReconcileResult{
		Found:      true,
		RunID:      runID,
		RequestID:  view.RequestID,
		Status:     status,
		Reconciled: len(updates) > 0,
	}, nil
}

func (r *Reconciler) ReconcileByRequestID(ctx context.Context, requestID string) (ReconcileResult, error) {
	if r == nil || r.fetcher == nil {
		return ReconcileResult{}, fmt.Errorf("run reconciler 未初始化")
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return ReconcileResult{}, fmt.Errorf("request_id 不能为空")
	}
	remote, err := r.fetcher.FetchRunByRequestID(ctx, requestID)
	if err != nil {
		return ReconcileResult{}, err
	}
	if !remote.Found || remote.RunID == 0 {
		return ReconcileResult{Found: false, RequestID: requestID}, nil
	}
	return r.ReconcileByRunID(ctx, remote.RunID)
}

func (r *Reconciler) appendReconcileEvent(ctx context.Context, runID uint, status contracts.RunStatus, remote RemoteRunStatus) {
	if r == nil || r.task == nil {
		return
	}
	note := strings.TrimSpace(remote.LastEventNote)
	if note == "" {
		note = strings.TrimSpace(remote.Summary)
	}
	_ = r.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "run_reconciled",
		ToState: map[string]any{
			"run_status": status,
		},
		Note:      note,
		CreatedAt: r.now(),
		Payload: map[string]any{
			"remote_status":   strings.TrimSpace(remote.Status),
			"snapshot_id":     strings.TrimSpace(remote.SnapshotID),
			"base_commit":     strings.TrimSpace(remote.BaseCommit),
			"verify_target":   strings.TrimSpace(remote.VerifyTarget),
			"last_event_type": strings.TrimSpace(remote.LastEventType),
		},
	})
}

func normalizeRunStatus(raw string) contracts.RunStatus {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case string(contracts.RunRequested):
		return contracts.RunRequested
	case string(contracts.RunQueued):
		return contracts.RunQueued
	case string(contracts.RunSnapshotPreparing):
		return contracts.RunSnapshotPreparing
	case string(contracts.RunSnapshotReady):
		return contracts.RunSnapshotReady
	case string(contracts.RunDispatching):
		return contracts.RunDispatching
	case string(contracts.RunEnvPreparing):
		return contracts.RunEnvPreparing
	case string(contracts.RunReadyToRun):
		return contracts.RunReadyToRun
	case string(contracts.RunWaitingApproval):
		return contracts.RunWaitingApproval
	case string(contracts.RunRunning):
		return contracts.RunRunning
	case string(contracts.RunCanceling):
		return contracts.RunCanceling
	case string(contracts.RunNodeOffline):
		return contracts.RunNodeOffline
	case string(contracts.RunReconciling):
		return contracts.RunReconciling
	case string(contracts.RunTimedOut):
		return contracts.RunTimedOut
	case string(contracts.RunSucceeded):
		return contracts.RunSucceeded
	case string(contracts.RunFailed):
		return contracts.RunFailed
	case string(contracts.RunCanceled):
		return contracts.RunCanceled
	default:
		return ""
	}
}

func isTerminalRunStatus(status contracts.RunStatus) bool {
	switch status {
	case contracts.RunSucceeded, contracts.RunFailed, contracts.RunCanceled, contracts.RunTimedOut:
		return true
	default:
		return false
	}
}

func isStaleRemoteUpdate(view contracts.RunView, remote RemoteRunStatus) bool {
	if !remote.UpdatedAt.IsZero() && !view.UpdatedAt.IsZero() && !remote.UpdatedAt.After(view.UpdatedAt) {
		return true
	}
	remoteStatus := normalizeRunStatus(remote.Status)
	if isTerminalRunStatus(view.RunStatus) && !isTerminalRunStatus(remoteStatus) {
		return true
	}
	return false
}
