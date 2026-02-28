package preview

import (
	"context"
	"strings"
	"testing"

	"dalek/internal/contracts"
	"dalek/internal/repo"
	"dalek/internal/testutil"
)

type stubWorkerLookup struct {
	worker *contracts.Worker
	err    error
}

func (s stubWorkerLookup) LatestWorker(ctx context.Context, ticketID uint) (*contracts.Worker, error) {
	_ = ctx
	_ = ticketID
	return s.worker, s.err
}

func TestCaptureTicketTail_UsesWorkerRuntimeLog(t *testing.T) {
	p, _, _ := testutil.NewTestProject(t)
	fRuntime, ok := p.WorkerRuntime.(*testutil.FakeWorkerRuntime)
	if !ok {
		t.Fatalf("unexpected worker runtime type: %T", p.WorkerRuntime)
	}
	fRuntime.CaptureText = "line-1\nline-2\nline-3\n"

	w := &contracts.Worker{
		ID:         7,
		TicketID:   9,
		ProcessPID: 1234,
		LogPath:    "/tmp/w7-stream.log",
	}
	svc := New(p, stubWorkerLookup{worker: w})
	got, err := svc.CaptureTicketTail(context.Background(), 9, 2)
	if err != nil {
		t.Fatalf("CaptureTicketTail failed: %v", err)
	}
	if got.Source != "worker_log" {
		t.Fatalf("unexpected source: %q", got.Source)
	}
	if got.LogPath != w.LogPath {
		t.Fatalf("unexpected log path: got=%q want=%q", got.LogPath, w.LogPath)
	}
	if len(got.Lines) != 2 || got.Lines[0] != "line-2" || got.Lines[1] != "line-3" {
		t.Fatalf("unexpected lines: %#v", got.Lines)
	}
	if len(fRuntime.CaptureHandles) != 1 {
		t.Fatalf("expected one capture call, got=%d", len(fRuntime.CaptureHandles))
	}
	if fRuntime.CaptureHandles[0].PID != w.ProcessPID {
		t.Fatalf("unexpected pid: got=%d want=%d", fRuntime.CaptureHandles[0].PID, w.ProcessPID)
	}
	if strings.TrimSpace(fRuntime.CaptureHandles[0].LogPath) != w.LogPath {
		t.Fatalf("unexpected captured log path: got=%q want=%q", fRuntime.CaptureHandles[0].LogPath, w.LogPath)
	}
}

func TestCaptureTicketTail_FallbackToRepoWorkerLogPath(t *testing.T) {
	p, _, _ := testutil.NewTestProject(t)
	fRuntime, ok := p.WorkerRuntime.(*testutil.FakeWorkerRuntime)
	if !ok {
		t.Fatalf("unexpected worker runtime type: %T", p.WorkerRuntime)
	}
	fRuntime.CaptureText = "only-line\n"

	w := &contracts.Worker{
		ID:         22,
		TicketID:   3,
		ProcessPID: 2233,
	}
	svc := New(p, stubWorkerLookup{worker: w})
	got, err := svc.CaptureTicketTail(context.Background(), 3, 20)
	if err != nil {
		t.Fatalf("CaptureTicketTail failed: %v", err)
	}
	wantPath := repo.WorkerStreamLogPath(p.WorkersDir, w.ID)
	if got.LogPath != wantPath {
		t.Fatalf("unexpected log path: got=%q want=%q", got.LogPath, wantPath)
	}
	if len(fRuntime.CaptureHandles) != 1 || fRuntime.CaptureHandles[0].LogPath != wantPath {
		t.Fatalf("unexpected capture handle: %#v", fRuntime.CaptureHandles)
	}
}

func TestCaptureTicketTail_NoWorker(t *testing.T) {
	p, _, _ := testutil.NewTestProject(t)
	svc := New(p, stubWorkerLookup{})
	_, err := svc.CaptureTicketTail(context.Background(), 1, 20)
	if err == nil || !strings.Contains(err.Error(), "没有可抓取的 worker") {
		t.Fatalf("expected no-worker error, got=%v", err)
	}
}
