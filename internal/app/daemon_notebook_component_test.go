package app

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"testing"
	"time"

	"dalek/internal/store"
)

func newNotebookComponentTestHome(t *testing.T) *Home {
	t.Helper()
	h, err := OpenHome(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	return h
}

func countNotebookWorkerLoops() int {
	var buf bytes.Buffer
	if p := pprof.Lookup("goroutine"); p != nil {
		_ = p.WriteTo(&buf, 2)
	}
	return strings.Count(buf.String(), "(*daemonNotebookComponent).workerLoop")
}

func waitForNotebookWorkerLoops(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	last := -1
	for time.Now().Before(deadline) {
		last = countNotebookWorkerLoops()
		if last == want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("worker loop goroutine count mismatch: got=%d want=%d", last, want)
}

func stopNotebookComponent(t *testing.T, c *daemonNotebookComponent, cancel context.CancelFunc) {
	t.Helper()
	cancel()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer stopCancel()
	if err := c.Stop(stopCtx); err != nil {
		t.Fatalf("Stop failed: %v", err)
	}
}

func waitForNoteStatus(t *testing.T, p *Project, noteID uint, want store.NoteStatus, timeout time.Duration) *NoteView {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		note, err := p.GetNote(context.Background(), noteID)
		if err != nil {
			t.Fatalf("GetNote failed: %v", err)
		}
		if note != nil && strings.TrimSpace(note.Status) == string(want) {
			return note
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("note status not reached: note=%d want=%s timeout=%s", noteID, want, timeout)
	return nil
}

func TestNewDaemonNotebookComponent_UsesConfiguredWorkerCount(t *testing.T) {
	h := newNotebookComponentTestHome(t)
	component := newDaemonNotebookComponent(h, nil, 4)
	if component.workerCount != 4 {
		t.Fatalf("unexpected workerCount: got=%d want=4", component.workerCount)
	}
}

func TestDaemonNotebookComponent_StartUsesConfiguredWorkerCount(t *testing.T) {
	h := newNotebookComponentTestHome(t)
	base := countNotebookWorkerLoops()

	ctx, cancel := context.WithCancel(context.Background())
	component := newDaemonNotebookComponent(h, nil, 3)
	component.pollGap = 100 * time.Millisecond
	if err := component.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		stopNotebookComponent(t, component, cancel)
		waitForNotebookWorkerLoops(t, base, 3*time.Second)
	})

	waitForNotebookWorkerLoops(t, base+3, 3*time.Second)
}

func TestDaemonNotebookComponent_StartFallbackToDefaultWorkerCount(t *testing.T) {
	h := newNotebookComponentTestHome(t)
	base := countNotebookWorkerLoops()

	ctx, cancel := context.WithCancel(context.Background())
	component := newDaemonNotebookComponent(h, nil, 0)
	component.pollGap = 100 * time.Millisecond
	if err := component.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		stopNotebookComponent(t, component, cancel)
		waitForNotebookWorkerLoops(t, base, 3*time.Second)
	})

	waitForNotebookWorkerLoops(t, base+defaultNotebookWorkerCount, 3*time.Second)
}

func TestDaemonNotebookComponent_NotifyProjectBypassesShapeGap(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ensureNotebookShapingSkill(t, p)

	component := newDaemonNotebookComponent(h, nil, 1)
	component.pollGap = 5 * time.Second
	component.lastShapeAt[p.Name()] = time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	if err := component.Start(ctx); err != nil {
		cancel()
		t.Fatalf("Start failed: %v", err)
	}
	t.Cleanup(func() {
		stopNotebookComponent(t, component, cancel)
	})

	added, err := p.AddNote(ctx, "通知触发路径应绕过60秒门控")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	if added.NoteID == 0 {
		t.Fatalf("expected note id")
	}

	component.NotifyProject(p.Name())

	note := waitForNoteStatus(t, p, added.NoteID, store.NoteShaped, 3*time.Second)
	if note.ShapedItemID == 0 {
		t.Fatalf("expected shaped_item_id after notify trigger")
	}
}
