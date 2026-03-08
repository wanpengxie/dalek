package pm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestRecoverPlannerOpsForRun_MarksSucceededWhenReconciled(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	run := createPlannerTaskRunForTest(t, svc, p, fmt.Sprintf("planner-recovery-success-%d", time.Now().UnixNano()))
	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	startedAt := time.Now().Add(-2 * time.Minute).UTC()
	finishedAt := time.Now().Add(-time.Minute).UTC()
	if err := rt.MarkRunRunning(ctx, run.ID, "planner-recovery-runner", nil, startedAt, true); err != nil {
		t.Fatalf("MarkRunRunning failed: %v", err)
	}
	if err := rt.MarkRunFailed(ctx, run.ID, "planner_failed", "planner failed for recovery test", finishedAt); err != nil {
		t.Fatalf("MarkRunFailed failed: %v", err)
	}

	inbox := contracts.InboxItem{
		Key:      fmt.Sprintf("recovery-inbox-%d", time.Now().UnixNano()),
		Status:   contracts.InboxDone,
		Severity: contracts.InboxInfo,
		Reason:   contracts.InboxQuestion,
		Title:    "already closed",
		Body:     "for reconcile",
	}
	if err := p.DB.WithContext(ctx).Create(&inbox).Error; err != nil {
		t.Fatalf("create inbox failed: %v", err)
	}

	entry := contracts.PMOpJournalEntry{
		InstanceID:     plannerRunInstanceID(run.ID),
		PlannerRunID:   run.ID,
		OpID:           "recover-close-inbox",
		Kind:           contracts.PMOpCloseInbox,
		IdempotencyKey: "recover-close-inbox",
		ArgumentsJSON:  contracts.JSONMap{"inbox_id": inbox.ID},
		Status:         contracts.PMOpStatusRunning,
		StartedAt:      &startedAt,
	}
	if err := p.DB.WithContext(ctx).Create(&entry).Error; err != nil {
		t.Fatalf("create journal entry failed: %v", err)
	}

	recovered, err := svc.RecoverPlannerOpsForRun(ctx, run.ID, time.Now())
	if err != nil {
		t.Fatalf("RecoverPlannerOpsForRun failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("expected recovered=1, got=%d", recovered)
	}

	var after contracts.PMOpJournalEntry
	if err := p.DB.WithContext(ctx).First(&after, entry.ID).Error; err != nil {
		t.Fatalf("load journal entry failed: %v", err)
	}
	if after.Status != contracts.PMOpStatusSucceeded {
		t.Fatalf("expected status=succeeded, got=%s", after.Status)
	}
}

func TestRecoverPlannerOpsForRun_MarksFailedWhenNotReconciled(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	run := createPlannerTaskRunForTest(t, svc, p, fmt.Sprintf("planner-recovery-failed-%d", time.Now().UnixNano()))
	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	startedAt := time.Now().Add(-2 * time.Minute).UTC()
	finishedAt := time.Now().Add(-time.Minute).UTC()
	if err := rt.MarkRunRunning(ctx, run.ID, "planner-recovery-runner-2", nil, startedAt, true); err != nil {
		t.Fatalf("MarkRunRunning failed: %v", err)
	}
	if err := rt.MarkRunCanceled(ctx, run.ID, "planner_canceled", "planner canceled for recovery test", finishedAt); err != nil {
		t.Fatalf("MarkRunCanceled failed: %v", err)
	}

	entry := contracts.PMOpJournalEntry{
		InstanceID:     plannerRunInstanceID(run.ID),
		PlannerRunID:   run.ID,
		OpID:           "recover-run-acceptance",
		Kind:           contracts.PMOpRunAcceptance,
		IdempotencyKey: "recover-run-acceptance",
		ArgumentsJSON:  contracts.JSONMap{"feature_id": "feature-a"},
		Status:         contracts.PMOpStatusRunning,
		StartedAt:      &startedAt,
	}
	if err := p.DB.WithContext(ctx).Create(&entry).Error; err != nil {
		t.Fatalf("create journal entry failed: %v", err)
	}

	recovered, err := svc.RecoverPlannerOpsForRun(ctx, run.ID, time.Now())
	if err != nil {
		t.Fatalf("RecoverPlannerOpsForRun failed: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("expected recovered=1, got=%d", recovered)
	}

	var after contracts.PMOpJournalEntry
	if err := p.DB.WithContext(ctx).First(&after, entry.ID).Error; err != nil {
		t.Fatalf("load journal entry failed: %v", err)
	}
	if after.Status != contracts.PMOpStatusFailed {
		t.Fatalf("expected status=failed, got=%s", after.Status)
	}
}
