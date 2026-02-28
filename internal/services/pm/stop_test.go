package pm

import (
	"context"
	"strings"
	"testing"

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
