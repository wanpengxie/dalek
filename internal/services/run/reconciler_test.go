package run

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"dalek/internal/contracts"
	tasksvc "dalek/internal/services/task"
	"dalek/internal/store"
)

type fakeRunFetcher struct {
	byRunID     map[uint]RemoteRunStatus
	byRequestID map[string]RemoteRunStatus
}

func (f *fakeRunFetcher) FetchRun(ctx context.Context, runID uint) (RemoteRunStatus, error) {
	_ = ctx
	if f == nil {
		return RemoteRunStatus{}, nil
	}
	if res, ok := f.byRunID[runID]; ok {
		return res, nil
	}
	return RemoteRunStatus{Found: false, RunID: runID}, nil
}

func (f *fakeRunFetcher) FetchRunByRequestID(ctx context.Context, requestID string) (RemoteRunStatus, error) {
	_ = ctx
	if f == nil {
		return RemoteRunStatus{}, nil
	}
	if res, ok := f.byRequestID[requestID]; ok {
		return res, nil
	}
	return RemoteRunStatus{Found: false, RequestID: requestID}, nil
}

func TestReconciler_ReconcileByRunID_UpdatesRunView(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	taskSvc := tasksvc.New(db)
	run, err := taskSvc.CreateRun(context.Background(), contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeRunVerify,
		ProjectKey:         "demo",
		RequestID:          "req-1",
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	seedUpdatedAt := time.Date(2026, 3, 15, 8, 0, 0, 0, time.UTC)
	if err := db.WithContext(context.Background()).Create(&contracts.RunView{
		RunID:        run.ID,
		TaskRunID:    run.ID,
		ProjectKey:   "demo",
		RequestID:    "req-1",
		RunStatus:    contracts.RunRequested,
		VerifyTarget: "test",
		UpdatedAt:    seedUpdatedAt,
	}).Error; err != nil {
		t.Fatalf("seed run view failed: %v", err)
	}
	fetcher := &fakeRunFetcher{
		byRunID: map[uint]RemoteRunStatus{
			run.ID: {
				Found:        true,
				RunID:        run.ID,
				RequestID:    "req-1",
				Status:       "running",
				Summary:      "remote running",
				SnapshotID:   "snap-1",
				BaseCommit:   "abc123",
				VerifyTarget: "test",
				UpdatedAt:    time.Date(2026, 3, 15, 9, 0, 0, 0, time.UTC),
			},
		},
	}
	reconciler := NewReconciler(db, taskSvc, fetcher)

	res, err := reconciler.ReconcileByRunID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ReconcileByRunID failed: %v", err)
	}
	if !res.Found || !res.Reconciled || res.Status != contracts.RunRunning {
		t.Fatalf("unexpected reconcile result: %+v", res)
	}
	var view contracts.RunView
	if err := db.WithContext(context.Background()).Where("run_id = ?", run.ID).First(&view).Error; err != nil {
		t.Fatalf("load run view failed: %v", err)
	}
	if view.RunStatus != contracts.RunRunning || view.SnapshotID != "snap-1" || view.BaseCommit != "abc123" {
		t.Fatalf("unexpected run view after reconcile: %+v", view)
	}
	events, err := taskSvc.ListEvents(context.Background(), run.ID, 10)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	found := false
	for _, ev := range events {
		if ev.EventType == "run_reconciled" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected run_reconciled event, got=%+v", events)
	}
}

func TestReconciler_ReconcileByRequestID_FollowsRemoteLookup(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	taskSvc := tasksvc.New(db)
	run, err := taskSvc.CreateRun(context.Background(), contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeRunVerify,
		ProjectKey:         "demo",
		RequestID:          "req-2",
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if err := db.WithContext(context.Background()).Create(&contracts.RunView{
		RunID:      run.ID,
		TaskRunID:  run.ID,
		ProjectKey: "demo",
		RequestID:  "req-2",
		RunStatus:  contracts.RunRequested,
	}).Error; err != nil {
		t.Fatalf("seed run view failed: %v", err)
	}
	fetcher := &fakeRunFetcher{
		byRequestID: map[string]RemoteRunStatus{
			"req-2": {
				Found:     true,
				RunID:     run.ID,
				RequestID: "req-2",
				Status:    "queued",
			},
		},
		byRunID: map[uint]RemoteRunStatus{
			run.ID: {
				Found:     true,
				RunID:     run.ID,
				RequestID: "req-2",
				Status:    "queued",
			},
		},
	}
	reconciler := NewReconciler(db, taskSvc, fetcher)

	res, err := reconciler.ReconcileByRequestID(context.Background(), "req-2")
	if err != nil {
		t.Fatalf("ReconcileByRequestID failed: %v", err)
	}
	if !res.Found || res.RunID != run.ID || res.Status != contracts.RunQueued {
		t.Fatalf("unexpected reconcile result: %+v", res)
	}
}

func TestReconciler_ReconcileByRunID_SkipsStaleUpdate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	taskSvc := tasksvc.New(db)
	run, err := taskSvc.CreateRun(context.Background(), contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeRunVerify,
		ProjectKey:         "demo",
		RequestID:          "req-stale",
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	now := time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC)
	if err := db.WithContext(context.Background()).Create(&contracts.RunView{
		RunID:        run.ID,
		TaskRunID:    run.ID,
		ProjectKey:   "demo",
		RequestID:    "req-stale",
		RunStatus:    contracts.RunSucceeded,
		VerifyTarget: "test",
		UpdatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed run view failed: %v", err)
	}
	fetcher := &fakeRunFetcher{
		byRunID: map[uint]RemoteRunStatus{
			run.ID: {
				Found:     true,
				RunID:     run.ID,
				RequestID: "req-stale",
				Status:    "running",
				UpdatedAt: now.Add(-time.Minute),
			},
		},
	}
	reconciler := NewReconciler(db, taskSvc, fetcher)

	res, err := reconciler.ReconcileByRunID(context.Background(), run.ID)
	if err != nil {
		t.Fatalf("ReconcileByRunID failed: %v", err)
	}
	if !res.Found || res.Reconciled {
		t.Fatalf("expected stale update to be skipped: %+v", res)
	}
	var view contracts.RunView
	if err := db.WithContext(context.Background()).Where("run_id = ?", run.ID).First(&view).Error; err != nil {
		t.Fatalf("load run view failed: %v", err)
	}
	if view.RunStatus != contracts.RunSucceeded {
		t.Fatalf("unexpected run view after reconcile: %+v", view)
	}
}
