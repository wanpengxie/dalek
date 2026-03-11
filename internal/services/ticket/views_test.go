package ticket

import (
	"testing"

	"dalek/internal/contracts"
)

func TestComputeTicketCapability_DefaultBacklogNoWorker(t *testing.T) {
	capability := ComputeTicketCapability("", nil, false, false, false, false, contracts.TaskHealthUnknown)
	if !capability.CanStart || !capability.CanQueueRun {
		t.Fatalf("expected backlog ticket to allow start and queue-run")
	}
	if !capability.CanArchive {
		t.Fatalf("expected backlog ticket without worker to allow archive")
	}
	if capability.Reason != "将自动准备 worker" {
		t.Fatalf("unexpected reason: %s", capability.Reason)
	}
}

func TestComputeTicketCapability_ActiveRunGateAndReason(t *testing.T) {
	worker := &contracts.Worker{
		Status:  contracts.WorkerRunning,
		LogPath: "/tmp/w1.log",
	}
	capability := ComputeTicketCapability(contracts.TicketActive, worker, true, false, true, false, contracts.TaskHealthBusy)
	if capability.CanQueueRun {
		t.Fatalf("expected active run to block queue-run compatibility")
	}
	if capability.CanArchive {
		t.Fatalf("expected running worker with active run to block archive")
	}
	if capability.Reason != "worker run 进行中" {
		t.Fatalf("unexpected reason: %s", capability.Reason)
	}
}

func TestComputeTicketCapability_ProbeFailureAllowsAttach(t *testing.T) {
	worker := &contracts.Worker{
		Status:  contracts.WorkerRunning,
		LogPath: "/tmp/w2.log",
	}
	capability := ComputeTicketCapability(contracts.TicketBacklog, worker, false, true, false, false, contracts.TaskHealthUnknown)
	if !capability.CanAttach {
		t.Fatalf("expected probe failure to keep attach allowed")
	}
	if capability.Reason != "运行态探测失败" {
		t.Fatalf("unexpected reason: %s", capability.Reason)
	}
}

func TestComputeDerivedRuntimeHealth(t *testing.T) {
	if got := computeDerivedRuntimeHealth(nil, false, false, 0, false, contracts.TaskHealthBusy); got != contracts.TaskHealthUnknown {
		t.Fatalf("expected no worker+run => unknown, got=%s", got)
	}

	stopped := &contracts.Worker{Status: contracts.WorkerStopped, LogPath: "/tmp/stopped.log"}
	if got := computeDerivedRuntimeHealth(stopped, false, false, 1, false, contracts.TaskHealthBusy); got != contracts.TaskHealthDead {
		t.Fatalf("expected stopped worker without session => dead, got=%s", got)
	}

	failed := &contracts.Worker{Status: contracts.WorkerFailed, LogPath: "/tmp/failed.log"}
	if got := computeDerivedRuntimeHealth(failed, false, false, 1, false, contracts.TaskHealthBusy); got != contracts.TaskHealthStalled {
		t.Fatalf("expected failed worker without session => stalled, got=%s", got)
	}

	running := &contracts.Worker{Status: contracts.WorkerRunning, LogPath: "/tmp/running.log"}
	if got := computeDerivedRuntimeHealth(running, false, true, 1, false, contracts.TaskHealthDead); got != contracts.TaskHealthUnknown {
		t.Fatalf("expected probe failure to downgrade dead => unknown, got=%s", got)
	}

	if got := computeDerivedRuntimeHealth(running, true, false, 1, false, contracts.TaskHealthBusy); got != contracts.TaskHealthBusy {
		t.Fatalf("expected alive session to keep runtime health, got=%s", got)
	}

	noHandle := &contracts.Worker{Status: contracts.WorkerRunning}
	if got := computeDerivedRuntimeHealth(noHandle, false, false, 1, false, contracts.TaskHealthBusy); got != contracts.TaskHealthBusy {
		t.Fatalf("expected worker without runtime handle keeps runtime health, got=%s", got)
	}

	if got := computeDerivedRuntimeHealth(running, false, false, 0, true, contracts.TaskHealthDead); got != contracts.TaskHealthBusy {
		t.Fatalf("expected active worker run to project runtime busy, got=%s", got)
	}
}
