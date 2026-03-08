package pm

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
	"dalek/internal/services/core"
)

type fakePlannerTaskRunner struct {
	runFn func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error)
}

func (f fakePlannerTaskRunner) Run(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
	if f.runFn != nil {
		return f.runFn(ctx, req, onEvent)
	}
	return sdkrunner.Result{}, nil
}

func TestRunPlannerJob_SuccessClearsActiveAndMarksRunSucceeded(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()
	svc.SetTaskRunner(fakePlannerTaskRunner{
		runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
			if strings.TrimSpace(req.Prompt) != "planner prompt for success" {
				t.Fatalf("unexpected planner prompt: %q", req.Prompt)
			}
			if strings.TrimSpace(req.WorkDir) != strings.TrimSpace(p.RepoRoot) {
				t.Fatalf("unexpected work dir: got=%q want=%q", req.WorkDir, p.RepoRoot)
			}
			if onEvent != nil {
				onEvent(sdkrunner.Event{Type: "assistant", Text: "planner is evaluating project"})
			}
			return sdkrunner.Result{
				Provider:   "claude",
				OutputMode: "json",
				Text:       "planner done",
				SessionID:  "sess_planner_ok",
			}, nil
		},
	})

	pmState, err := svc.getOrInitPMState(ctx)
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	run := createPlannerTaskRunForTest(t, svc, p, fmt.Sprintf("planner-success-%d", time.Now().UnixNano()))
	if err := p.DB.WithContext(ctx).Model(&contracts.PMState{}).Where("id = ?", pmState.ID).Updates(map[string]any{
		"planner_active_task_run_id": run.ID,
		"planner_dirty":              false,
		"planner_last_error":         "old planner error",
		"planner_last_run_at":        nil,
		"planner_cooldown_until":     nil,
		"updated_at":                 time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare pm state failed: %v", err)
	}

	if err := svc.RunPlannerJob(ctx, run.ID, PlannerRunOptions{
		RunnerID: "planner-runner-success",
		Prompt:   "planner prompt for success",
	}); err != nil {
		t.Fatalf("RunPlannerJob failed: %v", err)
	}

	var afterRun contracts.TaskRun
	if err := p.DB.WithContext(ctx).First(&afterRun, run.ID).Error; err != nil {
		t.Fatalf("load planner run failed: %v", err)
	}
	if afterRun.OrchestrationState != contracts.TaskSucceeded {
		t.Fatalf("expected run succeeded, got=%s", afterRun.OrchestrationState)
	}
	if afterRun.FinishedAt == nil {
		t.Fatalf("expected finished_at set")
	}
	if strings.TrimSpace(afterRun.RunnerID) != "" {
		t.Fatalf("expected runner_id cleared after terminal, got=%q", afterRun.RunnerID)
	}

	var startedCnt int64
	if err := p.DB.WithContext(ctx).Model(&contracts.TaskEvent{}).
		Where("task_run_id = ? AND event_type = ?", run.ID, "task_started").
		Count(&startedCnt).Error; err != nil {
		t.Fatalf("count task_started failed: %v", err)
	}
	if startedCnt == 0 {
		t.Fatalf("expected task_started event exists")
	}
	var streamCnt int64
	if err := p.DB.WithContext(ctx).Model(&contracts.TaskEvent{}).
		Where("task_run_id = ? AND event_type = ?", run.ID, "task_stream").
		Count(&streamCnt).Error; err != nil {
		t.Fatalf("count task_stream failed: %v", err)
	}
	if streamCnt == 0 {
		t.Fatalf("expected task_stream event exists")
	}
	var succeededCnt int64
	if err := p.DB.WithContext(ctx).Model(&contracts.TaskEvent{}).
		Where("task_run_id = ? AND event_type = ?", run.ID, "task_succeeded").
		Count(&succeededCnt).Error; err != nil {
		t.Fatalf("count task_succeeded failed: %v", err)
	}
	if succeededCnt == 0 {
		t.Fatalf("expected task_succeeded event exists")
	}

	afterState, err := svc.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if afterState.PlannerActiveTaskRunID != nil {
		t.Fatalf("expected planner active run cleared, got=%v", afterState.PlannerActiveTaskRunID)
	}
	if afterState.PlannerLastError != "" {
		t.Fatalf("expected planner_last_error cleared, got=%q", afterState.PlannerLastError)
	}
	if afterState.PlannerDirty {
		t.Fatalf("expected planner dirty unchanged=false after success")
	}
	assertPlannerCooldown(t, afterState.PlannerLastRunAt, afterState.PlannerCooldownUntil, plannerRunSuccessCooldown)
}

func TestRunPlannerJob_CanceledMarksRunCanceledAndResetsPlannerState(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()
	svc.SetTaskRunner(fakePlannerTaskRunner{
		runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
			return sdkrunner.Result{}, nil
		},
	})

	pmState, err := svc.getOrInitPMState(ctx)
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	run := createPlannerTaskRunForTest(t, svc, p, fmt.Sprintf("planner-failed-%d", time.Now().UnixNano()))
	if err := p.DB.WithContext(ctx).Model(&contracts.PMState{}).Where("id = ?", pmState.ID).Updates(map[string]any{
		"planner_active_task_run_id": run.ID,
		"planner_dirty":              false,
		"planner_last_error":         "",
		"planner_last_run_at":        nil,
		"planner_cooldown_until":     nil,
		"updated_at":                 time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare pm state failed: %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	runErr := svc.RunPlannerJob(canceledCtx, run.ID, PlannerRunOptions{RunnerID: "planner-runner-failed"})
	if runErr == nil {
		t.Fatalf("expected RunPlannerJob returns error when context canceled")
	}

	var afterRun contracts.TaskRun
	if err := p.DB.WithContext(ctx).First(&afterRun, run.ID).Error; err != nil {
		t.Fatalf("load planner run failed: %v", err)
	}
	if afterRun.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected run canceled, got=%s", afterRun.OrchestrationState)
	}
	if strings.TrimSpace(afterRun.ErrorCode) != "planner_canceled" {
		t.Fatalf("expected error_code=planner_canceled, got=%q", afterRun.ErrorCode)
	}
	if !strings.Contains(strings.ToLower(afterRun.ErrorMessage), "context canceled") {
		t.Fatalf("expected error_message contains context canceled, got=%q", afterRun.ErrorMessage)
	}

	var canceledCnt int64
	if err := p.DB.WithContext(ctx).Model(&contracts.TaskEvent{}).
		Where("task_run_id = ? AND event_type = ?", run.ID, "task_canceled").
		Count(&canceledCnt).Error; err != nil {
		t.Fatalf("count task_canceled failed: %v", err)
	}
	if canceledCnt == 0 {
		t.Fatalf("expected task_canceled event exists")
	}

	afterState, err := svc.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if afterState.PlannerActiveTaskRunID != nil {
		t.Fatalf("expected planner active run cleared after failure, got=%v", afterState.PlannerActiveTaskRunID)
	}
	if !afterState.PlannerDirty {
		t.Fatalf("expected planner dirty true after failure")
	}
	if !strings.Contains(strings.ToLower(afterState.PlannerLastError), "context canceled") {
		t.Fatalf("expected planner_last_error contains context canceled, got=%q", afterState.PlannerLastError)
	}
	assertPlannerCooldown(t, afterState.PlannerLastRunAt, afterState.PlannerCooldownUntil, plannerRunFailureCooldown)
}

func TestRunPlannerJob_TimeoutUsesConfiguredPlannerBudget(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	p.Config.PMPlannerTimeoutMS = 20
	ctx := context.Background()
	svc.SetTaskRunner(fakePlannerTaskRunner{
		runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
			<-ctx.Done()
			return sdkrunner.Result{}, ctx.Err()
		},
	})

	pmState, err := svc.getOrInitPMState(ctx)
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	run := createPlannerTaskRunForTest(t, svc, p, fmt.Sprintf("planner-timeout-%d", time.Now().UnixNano()))
	if err := p.DB.WithContext(ctx).Model(&contracts.PMState{}).Where("id = ?", pmState.ID).Updates(map[string]any{
		"planner_active_task_run_id": run.ID,
		"planner_dirty":              false,
		"planner_last_error":         "",
		"planner_last_run_at":        nil,
		"planner_cooldown_until":     nil,
		"updated_at":                 time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare pm state failed: %v", err)
	}

	runErr := svc.RunPlannerJob(ctx, run.ID, PlannerRunOptions{RunnerID: "planner-runner-timeout"})
	if runErr == nil {
		t.Fatalf("expected RunPlannerJob returns timeout error")
	}
	if !strings.Contains(runErr.Error(), "20ms") {
		t.Fatalf("expected timeout error mentions configured budget, got=%q", runErr.Error())
	}

	var afterRun contracts.TaskRun
	if err := p.DB.WithContext(ctx).First(&afterRun, run.ID).Error; err != nil {
		t.Fatalf("load planner run failed: %v", err)
	}
	if afterRun.OrchestrationState != contracts.TaskFailed {
		t.Fatalf("expected run failed, got=%s", afterRun.OrchestrationState)
	}
	if strings.TrimSpace(afterRun.ErrorCode) != "planner_timeout" {
		t.Fatalf("expected error_code=planner_timeout, got=%q", afterRun.ErrorCode)
	}
	if !strings.Contains(afterRun.ErrorMessage, "20ms") {
		t.Fatalf("expected error_message mentions configured budget, got=%q", afterRun.ErrorMessage)
	}

	afterState, err := svc.GetState(ctx)
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if !afterState.PlannerDirty {
		t.Fatalf("expected planner dirty true after timeout")
	}
	if !strings.Contains(afterState.PlannerLastError, "20ms") {
		t.Fatalf("expected planner_last_error mentions configured budget, got=%q", afterState.PlannerLastError)
	}
	assertPlannerCooldown(t, afterState.PlannerLastRunAt, afterState.PlannerCooldownUntil, plannerRunFailureCooldown)
}

func TestRunPlannerJob_ProgressResetsTimeout(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	p.Config.PMPlannerTimeoutMS = 20
	ctx := context.Background()
	svc.SetTaskRunner(fakePlannerTaskRunner{
		runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
			for i := 0; i < 3; i++ {
				if onEvent != nil {
					onEvent(sdkrunner.Event{Type: "assistant", Text: "planner tick"})
				}
				time.Sleep(15 * time.Millisecond)
			}
			return sdkrunner.Result{
				Provider:   "claude",
				OutputMode: "json",
				Text:       "planner completed",
			}, nil
		},
	})

	pmState, err := svc.getOrInitPMState(ctx)
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	run := createPlannerTaskRunForTest(t, svc, p, fmt.Sprintf("planner-progress-%d", time.Now().UnixNano()))
	if err := p.DB.WithContext(ctx).Model(&contracts.PMState{}).Where("id = ?", pmState.ID).Updates(map[string]any{
		"planner_active_task_run_id": run.ID,
		"planner_dirty":              false,
		"planner_last_error":         "",
		"planner_last_run_at":        nil,
		"planner_cooldown_until":     nil,
		"updated_at":                 time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare pm state failed: %v", err)
	}

	if err := svc.RunPlannerJob(ctx, run.ID, PlannerRunOptions{RunnerID: "planner-runner-progress"}); err != nil {
		t.Fatalf("RunPlannerJob failed: %v", err)
	}

	var afterRun contracts.TaskRun
	if err := p.DB.WithContext(ctx).First(&afterRun, run.ID).Error; err != nil {
		t.Fatalf("load planner run failed: %v", err)
	}
	if afterRun.OrchestrationState != contracts.TaskSucceeded {
		t.Fatalf("expected run succeeded, got=%s", afterRun.OrchestrationState)
	}
}

func TestRunPlannerJob_ExecutesPMOpsAndWritesCheckpoint(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()
	svc.SetTaskRunner(fakePlannerTaskRunner{
		runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
			return sdkrunner.Result{
				Provider: "codex",
				Text: `<pmops>
{
  "ops": [
    {
      "op_id": "op-create-1",
      "kind": "create_ticket",
      "idempotency_key": "planner-run-create-ticket",
      "critical": true,
      "arguments": {
        "title": "pmops-created-ticket",
        "description": "created from planner run"
      }
    }
  ]
}
</pmops>`,
			}, nil
		},
	})

	pmState, err := svc.getOrInitPMState(ctx)
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	run := createPlannerTaskRunForTest(t, svc, p, fmt.Sprintf("planner-pmops-success-%d", time.Now().UnixNano()))
	if err := p.DB.WithContext(ctx).Model(&contracts.PMState{}).Where("id = ?", pmState.ID).Updates(map[string]any{
		"planner_active_task_run_id": run.ID,
		"planner_dirty":              false,
		"planner_last_error":         "",
		"planner_last_run_at":        nil,
		"planner_cooldown_until":     nil,
		"updated_at":                 time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare pm state failed: %v", err)
	}

	if err := svc.RunPlannerJob(ctx, run.ID, PlannerRunOptions{RunnerID: "planner-runner-pmops"}); err != nil {
		t.Fatalf("RunPlannerJob failed: %v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.WithContext(ctx).Where("title = ?", "pmops-created-ticket").Order("id desc").First(&ticket).Error; err != nil {
		t.Fatalf("expected pmops created ticket: %v", err)
	}

	var journal contracts.PMOpJournalEntry
	if err := p.DB.WithContext(ctx).
		Where("planner_run_id = ? AND op_id = ?", run.ID, "op-create-1").
		First(&journal).Error; err != nil {
		t.Fatalf("load loop_op_journal failed: %v", err)
	}
	if journal.Status != contracts.PMOpStatusSucceeded {
		t.Fatalf("expected journal status=succeeded, got=%s", journal.Status)
	}

	var checkpoint contracts.PMCheckpoint
	if err := p.DB.WithContext(ctx).
		Where("planner_run_id = ?", run.ID).
		Order("id desc").
		First(&checkpoint).Error; err != nil {
		t.Fatalf("load checkpoint failed: %v", err)
	}
	if checkpoint.Revision <= 0 {
		t.Fatalf("expected checkpoint revision > 0")
	}
}

func TestRunPlannerJob_CriticalPMOpFailureMarksRunFailed(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()
	svc.SetTaskRunner(fakePlannerTaskRunner{
		runFn: func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
			return sdkrunner.Result{
				Provider: "codex",
				Text: `<pmops>
{
  "ops": [
    {
      "op_id": "op-unknown-1",
      "kind": "unknown_op_kind",
      "idempotency_key": "planner-run-unknown-op",
      "critical": true,
      "arguments": {}
    }
  ]
}
</pmops>`,
			}, nil
		},
	})

	pmState, err := svc.getOrInitPMState(ctx)
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	run := createPlannerTaskRunForTest(t, svc, p, fmt.Sprintf("planner-pmops-failed-%d", time.Now().UnixNano()))
	if err := p.DB.WithContext(ctx).Model(&contracts.PMState{}).Where("id = ?", pmState.ID).Updates(map[string]any{
		"planner_active_task_run_id": run.ID,
		"planner_dirty":              false,
		"planner_last_error":         "",
		"planner_last_run_at":        nil,
		"planner_cooldown_until":     nil,
		"updated_at":                 time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare pm state failed: %v", err)
	}

	runErr := svc.RunPlannerJob(ctx, run.ID, PlannerRunOptions{RunnerID: "planner-runner-pmops-failed"})
	if runErr == nil {
		t.Fatalf("expected RunPlannerJob returns error")
	}
	if !strings.Contains(strings.ToLower(runErr.Error()), "unsupported") {
		t.Fatalf("expected run error mentions unsupported op, got=%q", runErr.Error())
	}

	var afterRun contracts.TaskRun
	if err := p.DB.WithContext(ctx).First(&afterRun, run.ID).Error; err != nil {
		t.Fatalf("load planner run failed: %v", err)
	}
	if afterRun.OrchestrationState != contracts.TaskFailed {
		t.Fatalf("expected run failed, got=%s", afterRun.OrchestrationState)
	}

	var journal contracts.PMOpJournalEntry
	if err := p.DB.WithContext(ctx).
		Where("planner_run_id = ? AND op_id = ?", run.ID, "op-unknown-1").
		First(&journal).Error; err != nil {
		t.Fatalf("load loop_op_journal failed: %v", err)
	}
	if journal.Status != contracts.PMOpStatusFailed {
		t.Fatalf("expected journal status=failed, got=%s", journal.Status)
	}
	if strings.TrimSpace(journal.ErrorText) == "" {
		t.Fatalf("expected journal error text populated")
	}
}

func createPlannerTaskRunForTest(t *testing.T, svc *Service, p *core.Project, requestID string) contracts.TaskRun {
	t.Helper()
	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("taskRuntimeForDB failed: %v", err)
	}
	run, err := rt.CreateRun(context.Background(), contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           contracts.TaskTypePMPlannerRun,
		ProjectKey:         p.Key,
		SubjectType:        "pm",
		SubjectID:          "planner",
		RequestID:          strings.TrimSpace(requestID),
		OrchestrationState: contracts.TaskPending,
		RequestPayloadJSON: marshalJSON(map[string]any{
			"wake_version": 1,
		}),
	})
	if err != nil {
		t.Fatalf("CreateRun(planner) failed: %v", err)
	}
	return run
}

func assertPlannerCooldown(t *testing.T, lastRunAt, cooldownAt *time.Time, want time.Duration) {
	t.Helper()
	if lastRunAt == nil {
		t.Fatalf("expected planner_last_run_at set")
	}
	if cooldownAt == nil {
		t.Fatalf("expected planner_cooldown_until set")
	}
	got := cooldownAt.Sub(*lastRunAt)
	if got < want-2*time.Second || got > want+2*time.Second {
		t.Fatalf("unexpected cooldown duration: got=%s want≈%s", got, want)
	}
}
