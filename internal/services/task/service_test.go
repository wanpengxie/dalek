package task

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/store"
)

func newTaskServiceForTest(t *testing.T) *Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "task.db")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	return New(db)
}

func ptrTime(v time.Time) *time.Time {
	return &v
}

func TestService_TaskRunRoundTrip(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "dispatch_ticket",
		ProjectKey:         "demo",
		TicketID:           11,
		WorkerID:           22,
		SubjectType:        "ticket",
		SubjectID:          "11",
		RequestID:          "req-dispatch-1",
		OrchestrationState: contracts.TaskPending,
		RequestPayloadJSON: `{"ticket_id":11}`,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if run.ID == 0 {
		t.Fatalf("expected non-zero run id")
	}

	lease := now.Add(2 * time.Minute)
	if err := svc.MarkRunRunning(ctx, run.ID, "runner-1", &lease, now, true); err != nil {
		t.Fatalf("MarkRunRunning failed: %v", err)
	}
	if err := svc.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  run.ID,
		State:      contracts.TaskHealthBusy,
		NeedsUser:  false,
		Summary:    "dispatch executing",
		Source:     "pm_dispatch",
		ObservedAt: now,
		Metrics: map[string]any{
			"step": "entrypoint",
		},
	}); err != nil {
		t.Fatalf("AppendRuntimeSample failed: %v", err)
	}
	if err := svc.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  run.ID,
		Phase:      contracts.TaskPhasePlanning,
		Milestone:  "task_claimed",
		NextAction: "continue",
		Summary:    "claimed by runner",
		ReportedAt: now,
		Payload: map[string]any{
			"runner": "runner-1",
		},
	}); err != nil {
		t.Fatalf("AppendSemanticReport failed: %v", err)
	}
	if err := svc.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "task_claimed",
		FromState: map[string]any{"orchestration_state": "pending"},
		ToState:   map[string]any{"orchestration_state": "running"},
		Note:      "claim ok",
	}); err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	status, err := svc.GetStatusByRunID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil {
		t.Fatalf("expected status exists")
	}
	if status.OrchestrationState != string(contracts.TaskRunning) {
		t.Fatalf("unexpected orchestration state: %s", status.OrchestrationState)
	}
	if status.RuntimeHealthState != string(contracts.TaskHealthBusy) {
		t.Fatalf("unexpected runtime health state: %s", status.RuntimeHealthState)
	}
	if status.SemanticPhase != string(contracts.TaskPhasePlanning) {
		t.Fatalf("unexpected semantic phase: %s", status.SemanticPhase)
	}

	if err := svc.MarkRunSucceeded(ctx, run.ID, `{"ok":true}`, now.Add(10*time.Second)); err != nil {
		t.Fatalf("MarkRunSucceeded failed: %v", err)
	}
	status, err = svc.GetStatusByRunID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID after success failed: %v", err)
	}
	if status == nil || status.OrchestrationState != string(contracts.TaskSucceeded) {
		t.Fatalf("expected succeeded after MarkRunSucceeded, got=%v", status)
	}

	events, err := svc.ListEvents(ctx, run.ID, 20)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected non-empty events")
	}
}

func TestService_WorkerActiveRunLifecycle(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	_, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           7,
		WorkerID:           99,
		SubjectType:        "ticket",
		SubjectID:          "7",
		RequestID:          "wrk-a",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun wrk-a failed: %v", err)
	}
	latest, err := svc.LatestActiveWorkerRun(ctx, 99)
	if err != nil {
		t.Fatalf("LatestActiveWorkerRun failed: %v", err)
	}
	if latest == nil {
		t.Fatalf("expected active worker run")
	}

	if err := svc.CancelActiveWorkerRuns(ctx, 99, "redispatch", now.Add(time.Second)); err != nil {
		t.Fatalf("CancelActiveWorkerRuns failed: %v", err)
	}
	latest, err = svc.LatestActiveWorkerRun(ctx, 99)
	if err != nil {
		t.Fatalf("LatestActiveWorkerRun(2) failed: %v", err)
	}
	if latest != nil {
		t.Fatalf("expected no active run after cancel, got=%+v", *latest)
	}

	list, err := svc.ListStatus(ctx, contracts.TaskListStatusOptions{OwnerType: contracts.TaskOwnerWorker, IncludeTerminal: true, Limit: 10})
	if err != nil {
		t.Fatalf("ListStatus failed: %v", err)
	}
	if len(list) == 0 {
		t.Fatalf("expected canceled run visible in list")
	}
	if list[0].OrchestrationState != string(contracts.TaskCanceled) {
		t.Fatalf("unexpected orchestration_state=%s", list[0].OrchestrationState)
	}
}

func TestService_ReconcileOrphanedExecutionHostRuns_FailsWorkerAndSubagentOnly(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	workerRun, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           31,
		WorkerID:           41,
		SubjectType:        "ticket",
		SubjectID:          "31",
		RequestID:          "reconcile-worker-running",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun(worker) failed: %v", err)
	}
	pendingWorkerRun, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           32,
		WorkerID:           42,
		SubjectType:        "ticket",
		SubjectID:          "32",
		RequestID:          "reconcile-worker-pending",
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun(pendingWorker) failed: %v", err)
	}
	activeWorkerRun, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           33,
		WorkerID:           43,
		SubjectType:        "ticket",
		SubjectID:          "33",
		RequestID:          "reconcile-worker-live",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun(activeWorker) failed: %v", err)
	}
	if err := svc.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: activeWorkerRun.ID,
		EventType: "task_started",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskRunning,
		},
		Note:      "worker run with prior signal",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendEvent(task_started) failed: %v", err)
	}
	subagentRun, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerSubagent,
		TaskType:           contracts.TaskTypeSubagentRun,
		ProjectKey:         "demo",
		SubjectType:        "project",
		SubjectID:          "demo",
		RequestID:          "reconcile-subagent",
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun(subagent) failed: %v", err)
	}
	if err := svc.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: subagentRun.ID,
		EventType: "task_enqueued",
		ToState: map[string]any{
			"orchestration_state": contracts.TaskPending,
		},
		Note:      "subagent queued",
		CreatedAt: now,
	}); err != nil {
		t.Fatalf("AppendEvent(subagent task_enqueued) failed: %v", err)
	}
	if err := svc.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  subagentRun.ID,
		State:      contracts.TaskHealthIdle,
		Summary:    "subagent queued",
		Source:     "test",
		ObservedAt: now,
	}); err != nil {
		t.Fatalf("AppendRuntimeSample(subagent) failed: %v", err)
	}
	if err := svc.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  subagentRun.ID,
		Phase:      contracts.TaskPhasePlanning,
		Milestone:  "queued",
		NextAction: "continue",
		Summary:    "subagent accepted",
		ReportedAt: now,
	}); err != nil {
		t.Fatalf("AppendSemanticReport(subagent) failed: %v", err)
	}
	channelRun, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerChannel,
		TaskType:           contracts.TaskTypeChannelTurn,
		ProjectKey:         "demo",
		SubjectType:        "channel_conversation",
		SubjectID:          "conv-1",
		RequestID:          "reconcile-channel",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun(channel) failed: %v", err)
	}

	reconciled, err := svc.ReconcileOrphanedExecutionHostRuns(ctx, now)
	if err != nil {
		t.Fatalf("ReconcileOrphanedExecutionHostRuns failed: %v", err)
	}
	if reconciled != 4 {
		t.Fatalf("expected 4 reconciled runs, got=%d", reconciled)
	}

	workerStatus, err := svc.GetStatusByRunID(ctx, workerRun.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID(worker) failed: %v", err)
	}
	if workerStatus == nil || workerStatus.OrchestrationState != string(contracts.TaskFailed) {
		t.Fatalf("expected worker run failed, got=%+v", workerStatus)
	}
	if workerStatus.ErrorCode != orphanedByCrashErrorCode {
		t.Fatalf("expected worker error_code=%q, got=%q", orphanedByCrashErrorCode, workerStatus.ErrorCode)
	}
	pendingWorkerStatus, err := svc.GetStatusByRunID(ctx, pendingWorkerRun.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID(pendingWorker) failed: %v", err)
	}
	if pendingWorkerStatus == nil || pendingWorkerStatus.OrchestrationState != string(contracts.TaskFailed) {
		t.Fatalf("expected pending worker run failed, got=%+v", pendingWorkerStatus)
	}
	activeWorkerStatus, err := svc.GetStatusByRunID(ctx, activeWorkerRun.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID(activeWorker) failed: %v", err)
	}
	if activeWorkerStatus == nil || activeWorkerStatus.OrchestrationState != string(contracts.TaskFailed) {
		t.Fatalf("expected active worker run failed, got=%+v", activeWorkerStatus)
	}
	subagentStatus, err := svc.GetStatusByRunID(ctx, subagentRun.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID(subagent) failed: %v", err)
	}
	if subagentStatus == nil || subagentStatus.OrchestrationState != string(contracts.TaskFailed) {
		t.Fatalf("expected subagent run failed, got=%+v", subagentStatus)
	}
	channelStatus, err := svc.GetStatusByRunID(ctx, channelRun.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID(channel) failed: %v", err)
	}
	if channelStatus == nil || channelStatus.OrchestrationState != string(contracts.TaskRunning) {
		t.Fatalf("expected channel run untouched, got=%+v", channelStatus)
	}

	events, err := svc.ListEvents(ctx, workerRun.ID, 10)
	if err != nil {
		t.Fatalf("ListEvents(worker) failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected worker task_failed event")
	}
	last := events[len(events)-1]
	if last.EventType != "task_failed" {
		t.Fatalf("expected latest worker event task_failed, got=%s", last.EventType)
	}
	if got := last.PayloadJSON["reason"]; got != orphanedByCrashErrorCode {
		t.Fatalf("expected payload reason=%q, got=%v", orphanedByCrashErrorCode, got)
	}

	second, err := svc.ReconcileOrphanedExecutionHostRuns(ctx, now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("second ReconcileOrphanedExecutionHostRuns failed: %v", err)
	}
	if second != 0 {
		t.Fatalf("expected second reconcile to be idempotent, got=%d", second)
	}
}

func TestService_MarkRunSucceeded_DoesNotOverrideCanceled(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           17,
		WorkerID:           27,
		SubjectType:        "ticket",
		SubjectID:          "17",
		RequestID:          "req-cancel-guard-succeeded",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if err := svc.MarkRunCanceled(ctx, run.ID, "manual_cancel", "cancel first", now.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkRunCanceled failed: %v", err)
	}
	if err := svc.MarkRunSucceeded(ctx, run.ID, `{"ok":true}`, now.Add(3*time.Second)); err != nil {
		t.Fatalf("MarkRunSucceeded should be no-op after cancel, got=%v", err)
	}
	status, err := svc.GetStatusByRunID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.OrchestrationState != string(contracts.TaskCanceled) {
		t.Fatalf("expected canceled state unchanged, got=%+v", status)
	}
}

func TestService_MarkRunFailed_DoesNotOverrideCanceled(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           18,
		WorkerID:           28,
		SubjectType:        "ticket",
		SubjectID:          "18",
		RequestID:          "req-cancel-guard-failed",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if err := svc.MarkRunCanceled(ctx, run.ID, "manual_cancel", "cancel first", now.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkRunCanceled failed: %v", err)
	}
	if err := svc.MarkRunFailed(ctx, run.ID, "agent_exit_failed", "should not override", now.Add(3*time.Second)); err != nil {
		t.Fatalf("MarkRunFailed should be no-op after cancel, got=%v", err)
	}
	status, err := svc.GetStatusByRunID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.OrchestrationState != string(contracts.TaskCanceled) {
		t.Fatalf("expected canceled state unchanged, got=%+v", status)
	}
}

func TestService_MarkRunSucceeded_RejectsFromTerminalState(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	cases := []struct {
		name  string
		state contracts.TaskOrchestrationState
	}{
		{name: "from_succeeded", state: contracts.TaskSucceeded},
		{name: "from_failed", state: contracts.TaskFailed},
		{name: "from_canceled", state: contracts.TaskCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requestID := "req-terminal-guard-succeed-" + tc.name
			run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
				OwnerType:          contracts.TaskOwnerWorker,
				TaskType:           contracts.TaskTypeDeliverTicket,
				ProjectKey:         "demo",
				TicketID:           21,
				WorkerID:           31,
				SubjectType:        "ticket",
				SubjectID:          "21",
				RequestID:          requestID,
				OrchestrationState: tc.state,
				StartedAt:          &now,
				FinishedAt:         ptrTime(now.Add(time.Second)),
				ErrorCode:          "existing_error",
				ErrorMessage:       "existing message",
				ResultPayloadJSON:  `{"before":true}`,
			})
			if err != nil {
				t.Fatalf("CreateRun failed: %v", err)
			}

			if err := svc.MarkRunSucceeded(ctx, run.ID, `{"after":true}`, now.Add(2*time.Second)); err != nil {
				t.Fatalf("MarkRunSucceeded should reject terminal updates as no-op, got=%v", err)
			}

			var loaded contracts.TaskRun
			if err := svc.db.WithContext(ctx).First(&loaded, run.ID).Error; err != nil {
				t.Fatalf("load run failed: %v", err)
			}
			if loaded.OrchestrationState != tc.state {
				t.Fatalf("expected state unchanged=%s, got=%s", tc.state, loaded.OrchestrationState)
			}

			var ev contracts.TaskEvent
			if err := svc.db.WithContext(ctx).
				Where("task_run_id = ? AND event_type = ?", run.ID, "terminal_update_rejected").
				Order("id desc").
				First(&ev).Error; err != nil {
				t.Fatalf("expected terminal_update_rejected event: %v", err)
			}
			if got := ev.FromStateJSON["orchestration_state"]; got != string(tc.state) {
				t.Fatalf("unexpected from_state=%v, want=%s", got, tc.state)
			}
			if got := ev.ToStateJSON["orchestration_state"]; got != string(contracts.TaskSucceeded) {
				t.Fatalf("unexpected to_state=%v, want=%s", got, contracts.TaskSucceeded)
			}
		})
	}
}

func TestService_MarkRunFailed_RejectsFromTerminalState(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	cases := []struct {
		name  string
		state contracts.TaskOrchestrationState
	}{
		{name: "from_succeeded", state: contracts.TaskSucceeded},
		{name: "from_failed", state: contracts.TaskFailed},
		{name: "from_canceled", state: contracts.TaskCanceled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requestID := "req-terminal-guard-fail-" + tc.name
			run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
				OwnerType:          contracts.TaskOwnerWorker,
				TaskType:           contracts.TaskTypeDeliverTicket,
				ProjectKey:         "demo",
				TicketID:           22,
				WorkerID:           32,
				SubjectType:        "ticket",
				SubjectID:          "22",
				RequestID:          requestID,
				OrchestrationState: tc.state,
				StartedAt:          &now,
				FinishedAt:         ptrTime(now.Add(time.Second)),
				ErrorCode:          "existing_error",
				ErrorMessage:       "existing message",
				ResultPayloadJSON:  `{"before":true}`,
			})
			if err != nil {
				t.Fatalf("CreateRun failed: %v", err)
			}

			if err := svc.MarkRunFailed(ctx, run.ID, "new_error", "after", now.Add(2*time.Second)); err != nil {
				t.Fatalf("MarkRunFailed should reject terminal updates as no-op, got=%v", err)
			}

			var loaded contracts.TaskRun
			if err := svc.db.WithContext(ctx).First(&loaded, run.ID).Error; err != nil {
				t.Fatalf("load run failed: %v", err)
			}
			if loaded.OrchestrationState != tc.state {
				t.Fatalf("expected state unchanged=%s, got=%s", tc.state, loaded.OrchestrationState)
			}

			var ev contracts.TaskEvent
			if err := svc.db.WithContext(ctx).
				Where("task_run_id = ? AND event_type = ?", run.ID, "terminal_update_rejected").
				Order("id desc").
				First(&ev).Error; err != nil {
				t.Fatalf("expected terminal_update_rejected event: %v", err)
			}
			if got := ev.FromStateJSON["orchestration_state"]; got != string(tc.state) {
				t.Fatalf("unexpected from_state=%v, want=%s", got, tc.state)
			}
			if got := ev.ToStateJSON["orchestration_state"]; got != string(contracts.TaskFailed) {
				t.Fatalf("unexpected to_state=%v, want=%s", got, contracts.TaskFailed)
			}
		})
	}
}

func TestService_MarkRunCanceled_FromTerminalStateAppendsDiagnostic(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           23,
		WorkerID:           33,
		SubjectType:        "ticket",
		SubjectID:          "23",
		RequestID:          "req-cancel-terminal-override",
		OrchestrationState: contracts.TaskSucceeded,
		StartedAt:          &now,
		FinishedAt:         ptrTime(now.Add(time.Second)),
		ResultPayloadJSON:  `{"ok":true}`,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	if err := svc.MarkRunCanceled(ctx, run.ID, "manual_cancel", "force cancel", now.Add(2*time.Second)); err != nil {
		t.Fatalf("MarkRunCanceled failed: %v", err)
	}

	var loaded contracts.TaskRun
	if err := svc.db.WithContext(ctx).First(&loaded, run.ID).Error; err != nil {
		t.Fatalf("load run failed: %v", err)
	}
	if loaded.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected canceled state, got=%s", loaded.OrchestrationState)
	}

	var ev contracts.TaskEvent
	if err := svc.db.WithContext(ctx).
		Where("task_run_id = ? AND event_type = ?", run.ID, "terminal_state_overridden").
		Order("id desc").
		First(&ev).Error; err != nil {
		t.Fatalf("expected terminal_state_overridden event: %v", err)
	}
	if got := ev.FromStateJSON["orchestration_state"]; got != string(contracts.TaskSucceeded) {
		t.Fatalf("unexpected from_state=%v", got)
	}
	if got := ev.ToStateJSON["orchestration_state"]; got != string(contracts.TaskCanceled) {
		t.Fatalf("unexpected to_state=%v", got)
	}
}

func TestService_MarkRunRunning_DoesNotOverrideCanceled(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           20,
		WorkerID:           30,
		SubjectType:        "ticket",
		SubjectID:          "20",
		RequestID:          "req-cancel-guard-running",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if err := svc.MarkRunCanceled(ctx, run.ID, "manual_cancel", "cancel first", now.Add(1*time.Second)); err != nil {
		t.Fatalf("MarkRunCanceled failed: %v", err)
	}
	lease := now.Add(5 * time.Minute)
	if err := svc.MarkRunRunning(ctx, run.ID, "runner-revive", &lease, now.Add(2*time.Second), true); err == nil {
		t.Fatalf("MarkRunRunning should fail after cancel")
	}
	status, err := svc.GetStatusByRunID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.OrchestrationState != string(contracts.TaskCanceled) {
		t.Fatalf("expected canceled state unchanged, got=%+v", status)
	}
}

func TestService_AppendEvent_DropsTerminalEventsAfterCanceled(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           19,
		WorkerID:           29,
		SubjectType:        "ticket",
		SubjectID:          "19",
		RequestID:          "req-cancel-event-guard",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	if err := svc.MarkRunCanceled(ctx, run.ID, "manual_cancel", "cancel first", now.Add(1*time.Second)); err != nil {
		t.Fatalf("MarkRunCanceled failed: %v", err)
	}
	if err := svc.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "task_succeeded",
		CreatedAt: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatalf("AppendEvent(task_succeeded) failed: %v", err)
	}
	if err := svc.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "task_failed",
		CreatedAt: now.Add(3 * time.Second),
	}); err != nil {
		t.Fatalf("AppendEvent(task_failed) failed: %v", err)
	}
	events, err := svc.ListEvents(ctx, run.ID, 20)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	for _, ev := range events {
		if ev.EventType == "task_succeeded" || ev.EventType == "task_failed" {
			t.Fatalf("terminal event should be dropped after canceled, got=%s", ev.EventType)
		}
	}
}

func TestService_StatusViewContainsLatestObservationFields(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           31,
		WorkerID:           41,
		SubjectType:        "ticket",
		SubjectID:          "31",
		RequestID:          "req-updated-at-1",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &base,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	tRuntime := base.Add(1 * time.Minute)
	if err := svc.AppendRuntimeSample(ctx, contracts.TaskRuntimeSampleInput{
		TaskRunID:  run.ID,
		State:      contracts.TaskHealthBusy,
		NeedsUser:  false,
		Summary:    "runtime sample",
		Source:     "test",
		ObservedAt: tRuntime,
	}); err != nil {
		t.Fatalf("AppendRuntimeSample failed: %v", err)
	}

	tSemantic := base.Add(2 * time.Minute)
	if err := svc.AppendSemanticReport(ctx, contracts.TaskSemanticReportInput{
		TaskRunID:  run.ID,
		Phase:      contracts.TaskPhaseImplementing,
		Milestone:  "m1",
		NextAction: "continue",
		Summary:    "semantic report",
		ReportedAt: tSemantic,
	}); err != nil {
		t.Fatalf("AppendSemanticReport failed: %v", err)
	}

	tEvent := base.Add(3 * time.Minute)
	if err := svc.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "tick",
		CreatedAt: tEvent,
	}); err != nil {
		t.Fatalf("AppendEvent failed: %v", err)
	}

	status, err := svc.GetStatusByRunID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil {
		t.Fatalf("expected status exists")
	}
	if status.RuntimeObservedAt == nil || !status.RuntimeObservedAt.Equal(tRuntime) {
		t.Fatalf("expected runtime_observed_at=%s, got=%v", tRuntime.Format(time.RFC3339), status.RuntimeObservedAt)
	}
	if status.SemanticReportedAt == nil || !status.SemanticReportedAt.Equal(tSemantic) {
		t.Fatalf("expected semantic_reported_at=%s, got=%v", tSemantic.Format(time.RFC3339), status.SemanticReportedAt)
	}
	if status.LastEventAt == nil || !status.LastEventAt.Equal(tEvent) {
		t.Fatalf("expected last_event_at=%s, got=%v", tEvent.Format(time.RFC3339), status.LastEventAt)
	}
}

func TestService_CreateRun_DuplicateRequestIDReturnsExisting(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	in := contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "dispatch_ticket",
		ProjectKey:         "demo",
		TicketID:           11,
		WorkerID:           22,
		SubjectType:        "ticket",
		SubjectID:          "11",
		RequestID:          "dup-request-id-1",
		OrchestrationState: contracts.TaskPending,
	}
	first, err := svc.CreateRun(ctx, in)
	if err != nil {
		t.Fatalf("first CreateRun failed: %v", err)
	}
	second, err := svc.CreateRun(ctx, in)
	if err != nil {
		t.Fatalf("second CreateRun failed: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("expected duplicate request_id return same run, first=%d second=%d", first.ID, second.ID)
	}
}

func TestService_ListEvents_LatestNAscending(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           71,
		WorkerID:           81,
		SubjectType:        "ticket",
		SubjectID:          "71",
		RequestID:          "req-list-events-asc",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &base,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	cases := []struct {
		eventType string
		at        time.Time
	}{
		{eventType: "e1", at: base.Add(1 * time.Minute)},
		{eventType: "e2", at: base.Add(2 * time.Minute)},
		{eventType: "e3", at: base.Add(3 * time.Minute)},
	}
	for _, c := range cases {
		if err := svc.AppendEvent(ctx, contracts.TaskEventInput{
			TaskRunID: run.ID,
			EventType: c.eventType,
			CreatedAt: c.at,
		}); err != nil {
			t.Fatalf("AppendEvent(%s) failed: %v", c.eventType, err)
		}
	}
	got, err := svc.ListEvents(ctx, run.ID, 2)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got=%d", len(got))
	}
	if got[0].EventType != "e2" || got[1].EventType != "e3" {
		t.Fatalf("expected latest 2 events in ascending order [e2,e3], got=[%s,%s]", got[0].EventType, got[1].EventType)
	}
	if !got[0].CreatedAt.Before(got[1].CreatedAt) {
		t.Fatalf("expected ascending created_at")
	}
}

func TestService_FinishAgentRun_Succeeded(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           91,
		WorkerID:           101,
		SubjectType:        "ticket",
		SubjectID:          "91",
		RequestID:          "req-finish-agent-ok",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	finishAt := now.Add(2 * time.Minute)
	if err := svc.FinishAgentRun(ctx, run.ID, 0, finishAt); err != nil {
		t.Fatalf("FinishAgentRun failed: %v", err)
	}

	status, err := svc.GetStatusByRunID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.OrchestrationState != string(contracts.TaskSucceeded) {
		t.Fatalf("expected succeeded after FinishAgentRun, got=%+v", status)
	}

	evs, err := svc.ListEvents(ctx, run.ID, 1)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(evs) != 1 || evs[0].EventType != "task_succeeded" {
		t.Fatalf("expected last event task_succeeded, got=%+v", evs)
	}
}

func TestService_FinishAgentRun_Failed(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           92,
		WorkerID:           102,
		SubjectType:        "ticket",
		SubjectID:          "92",
		RequestID:          "req-finish-agent-failed",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	finishAt := now.Add(2 * time.Minute)
	if err := svc.FinishAgentRun(ctx, run.ID, 17, finishAt); err != nil {
		t.Fatalf("FinishAgentRun failed: %v", err)
	}

	var loaded contracts.TaskRun
	if err := svc.db.WithContext(ctx).First(&loaded, run.ID).Error; err != nil {
		t.Fatalf("load run failed: %v", err)
	}
	if loaded.OrchestrationState != contracts.TaskFailed {
		t.Fatalf("expected failed state, got=%s", loaded.OrchestrationState)
	}
	if loaded.ErrorCode != "agent_exit" {
		t.Fatalf("expected error_code=agent_exit, got=%s", loaded.ErrorCode)
	}

	evs, err := svc.ListEvents(ctx, run.ID, 1)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(evs) != 1 || evs[0].EventType != "task_failed" {
		t.Fatalf("expected last event task_failed, got=%+v", evs)
	}
}

func TestService_CancelRun(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           93,
		WorkerID:           103,
		SubjectType:        "ticket",
		SubjectID:          "93",
		RequestID:          "req-cancel-run",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	cancelAt := now.Add(1 * time.Minute)
	got, err := svc.CancelRun(ctx, run.ID, cancelAt)
	if err != nil {
		t.Fatalf("CancelRun failed: %v", err)
	}
	if !got.Found || !got.Canceled || got.ToState != string(contracts.TaskCanceled) {
		t.Fatalf("unexpected cancel result: %+v", got)
	}

	status, err := svc.GetStatusByRunID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.OrchestrationState != string(contracts.TaskCanceled) {
		t.Fatalf("expected canceled state, got=%+v", status)
	}

	evs, err := svc.ListEvents(ctx, run.ID, 1)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(evs) != 1 || evs[0].EventType != "task_canceled" {
		t.Fatalf("expected last event task_canceled, got=%+v", evs)
	}
}

func TestService_CancelRunWithCause(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         "demo",
		TicketID:           94,
		WorkerID:           104,
		SubjectType:        "ticket",
		SubjectID:          "94",
		RequestID:          "req-cancel-run-cause",
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}

	cancelAt := now.Add(1 * time.Minute)
	got, err := svc.CancelRunWithCause(ctx, run.ID, contracts.TaskCancelCauseUserCancel, cancelAt)
	if err != nil {
		t.Fatalf("CancelRunWithCause failed: %v", err)
	}
	if !got.Found || !got.Canceled || got.ToState != string(contracts.TaskCanceled) {
		t.Fatalf("unexpected cancel result: %+v", got)
	}

	status, err := svc.GetStatusByRunID(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetStatusByRunID failed: %v", err)
	}
	if status == nil || status.OrchestrationState != string(contracts.TaskCanceled) {
		t.Fatalf("expected canceled state, got=%+v", status)
	}
	if got := strings.TrimSpace(status.ErrorCode); got != string(contracts.TaskCancelCauseUserCancel) {
		t.Fatalf("expected error_code=user_cancel, got=%q", got)
	}

	evs, err := svc.ListEvents(ctx, run.ID, 1)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(evs) != 1 || evs[0].EventType != "task_canceled" {
		t.Fatalf("expected last event task_canceled, got=%+v", evs)
	}
	if got := strings.TrimSpace(fmt.Sprint(evs[0].PayloadJSON["cancel_cause"])); got != string(contracts.TaskCancelCauseUserCancel) {
		t.Fatalf("expected payload cancel_cause=user_cancel, got=%q", got)
	}
}
