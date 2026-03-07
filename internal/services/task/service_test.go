package task

import (
	"context"
	"dalek/internal/contracts"
	"path/filepath"
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
		TaskType:           "deliver_ticket",
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

func TestService_ListStatus_FilterByPMPlannerRunType(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()

	_, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypePMPlannerRun,
		ProjectKey:         "demo",
		TicketID:           1,
		RequestID:          "pm-planner-run",
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun(pm_planner_run) failed: %v", err)
	}
	_, err = svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypeDispatchTicket,
		ProjectKey:         "demo",
		TicketID:           2,
		RequestID:          "pm-dispatch-run",
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun(dispatch_ticket) failed: %v", err)
	}

	list, err := svc.ListStatus(ctx, contracts.TaskListStatusOptions{
		OwnerType:       contracts.TaskOwnerPM,
		TaskType:        contracts.TaskTypePMPlannerRun,
		IncludeTerminal: true,
		Limit:           10,
	})
	if err != nil {
		t.Fatalf("ListStatus failed: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one pm_planner_run, got=%d", len(list))
	}
	if list[0].TaskType != contracts.TaskTypePMPlannerRun {
		t.Fatalf("unexpected task_type=%s", list[0].TaskType)
	}
}

func TestService_MarkRunSucceeded_DoesNotOverrideCanceled(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
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
		TaskType:           "deliver_ticket",
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

func TestService_MarkRunRunning_DoesNotOverrideCanceled(t *testing.T) {
	svc := newTaskServiceForTest(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	run, err := svc.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
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
		TaskType:           "deliver_ticket",
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
		TaskType:           "deliver_ticket",
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
		TaskType:           "deliver_ticket",
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
		TaskType:           "deliver_ticket",
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
		TaskType:           "deliver_ticket",
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
		TaskType:           "deliver_ticket",
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
