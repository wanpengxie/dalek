package pm

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestStopTicket_ForceFailsActiveDispatchJobs(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "pm-stop-ticket-force-fail")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	sub, err := svc.SubmitDispatchTicket(context.Background(), tk.ID, DispatchSubmitOptions{
		RequestID: "pm-stop-ticket-dispatch",
	})
	if err != nil {
		t.Fatalf("SubmitDispatchTicket failed: %v", err)
	}

	if err := svc.StopTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StopTicket failed: %v", err)
	}

	var job contracts.PMDispatchJob
	if err := p.DB.First(&job, sub.JobID).Error; err != nil {
		t.Fatalf("load dispatch job failed: %v", err)
	}
	if job.Status != contracts.PMDispatchFailed {
		t.Fatalf("expected dispatch failed after stop, got=%s", job.Status)
	}
	if !strings.Contains(job.Error, "ticket stop") {
		t.Fatalf("expected force-fail reason in dispatch error, got=%q", job.Error)
	}
	if job.TaskRunID != 0 {
		var run contracts.TaskRun
		if err := p.DB.First(&run, job.TaskRunID).Error; err != nil {
			t.Fatalf("load task run failed: %v", err)
		}
		if run.OrchestrationState != contracts.TaskFailed {
			t.Fatalf("expected task run failed after stop, got=%s", run.OrchestrationState)
		}
	}
}

func TestStopTicket_DoesNotOverrideSucceededDispatchTaskRun(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()
	tk := createTicket(t, p.DB, "pm-stop-ticket-keep-succeeded-run")
	if _, err := svc.StartTicket(ctx, tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	sub, err := svc.SubmitDispatchTicket(ctx, tk.ID, DispatchSubmitOptions{
		RequestID: "req-stop-after-success",
	})
	if err != nil {
		t.Fatalf("SubmitDispatchTicket failed: %v", err)
	}
	var job contracts.PMDispatchJob
	if err := p.DB.First(&job, sub.JobID).Error; err != nil {
		t.Fatalf("load dispatch job failed: %v", err)
	}
	runnerID := "runner-stop-after-success"
	if _, claimed, err := svc.claimPMDispatchJob(ctx, job.ID, runnerID, 2*time.Minute); err != nil {
		t.Fatalf("claimPMDispatchJob failed: %v", err)
	} else if !claimed {
		t.Fatalf("expected claimed=true")
	}
	if err := svc.completePMDispatchJobSuccess(ctx, job.ID, runnerID, `{"ok":true}`); err != nil {
		t.Fatalf("completePMDispatchJobSuccess failed: %v", err)
	}

	if err := svc.StopTicket(ctx, tk.ID); err != nil {
		t.Fatalf("StopTicket failed: %v", err)
	}

	var afterJob contracts.PMDispatchJob
	if err := p.DB.First(&afterJob, job.ID).Error; err != nil {
		t.Fatalf("load dispatch job failed: %v", err)
	}
	if afterJob.Status != contracts.PMDispatchSucceeded {
		t.Fatalf("expected dispatch keep succeeded after stop, got=%s", afterJob.Status)
	}

	var run contracts.TaskRun
	if err := p.DB.First(&run, job.TaskRunID).Error; err != nil {
		t.Fatalf("load task run failed: %v", err)
	}
	if run.OrchestrationState != contracts.TaskSucceeded {
		t.Fatalf("expected task run keep succeeded after stop, got=%s", run.OrchestrationState)
	}

	var forceFailCnt int64
	if err := p.DB.Model(&contracts.TaskEvent{}).
		Where("task_run_id = ? AND event_type = ?", job.TaskRunID, "dispatch_force_failed_on_stop").
		Count(&forceFailCnt).Error; err != nil {
		t.Fatalf("count dispatch_force_failed_on_stop failed: %v", err)
	}
	if forceFailCnt != 0 {
		t.Fatalf("expected no dispatch_force_failed_on_stop event, got=%d", forceFailCnt)
	}
}
