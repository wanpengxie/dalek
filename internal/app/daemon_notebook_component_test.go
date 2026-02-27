package app

import (
	"bytes"
	"context"
	"os"
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

func registerProjectForNotebookComponentTest(t *testing.T, h *Home, name, repoRoot string) {
	t.Helper()
	r, err := h.LoadRegistry()
	if err != nil {
		t.Fatalf("LoadRegistry failed: %v", err)
	}
	now := time.Now()
	r.Projects = append(r.Projects, RegisteredProject{
		Name:      name,
		RepoRoot:  repoRoot,
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err := h.SaveRegistry(r); err != nil {
		t.Fatalf("SaveRegistry failed: %v", err)
	}
}

func containsProjectName(names []string, want string) bool {
	for _, name := range names {
		if strings.TrimSpace(name) == strings.TrimSpace(want) {
			return true
		}
	}
	return false
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

func TestDaemonNotebookComponent_NotInitializedProjectBackoffUntilNotify(t *testing.T) {
	h := newNotebookComponentTestHome(t)
	repoRoot := filepath.Join(t.TempDir(), "repo-uninitialized")
	if err := os.MkdirAll(repoRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll repoRoot failed: %v", err)
	}
	const projectName = "demo-uninitialized"
	registerProjectForNotebookComponentTest(t, h, projectName, repoRoot)

	registry := NewProjectRegistry(h)
	component := newDaemonNotebookComponent(h, nil, 1, registry)
	component.openBackoffGap = time.Hour

	if got := component.processProject(context.Background(), 1, projectName, false); got {
		t.Fatalf("expected no work for uninitialized project")
	}
	if !component.shouldSkipProjectOpen(projectName, time.Now()) {
		t.Fatalf("expected project open to enter backoff after ErrNotInitialized")
	}

	if _, err := h.AddOrUpdateProject(projectName, repoRoot, ProjectConfig{}); err != nil {
		t.Fatalf("AddOrUpdateProject failed: %v", err)
	}

	if got := component.processProject(context.Background(), 1, projectName, false); got {
		t.Fatalf("expected no work while open backoff is active")
	}
	if opened := registry.ListOpenProjectNames(); containsProjectName(opened, projectName) {
		t.Fatalf("project should still be skipped during backoff, opened=%v", opened)
	}

	component.NotifyProject(projectName)
	if component.shouldSkipProjectOpen(projectName, time.Now()) {
		t.Fatalf("expected NotifyProject to clear open backoff")
	}
	if got := component.processProject(context.Background(), 1, projectName, false); got {
		t.Fatalf("expected no pending note work after notify retry")
	}
	if opened := registry.ListOpenProjectNames(); !containsProjectName(opened, projectName) {
		t.Fatalf("expected project to be opened after notify clears backoff, opened=%v", opened)
	}
}

func TestDaemonNotebookComponent_TemporaryOpenErrorDoesNotEnterBackoff(t *testing.T) {
	h := newNotebookComponentTestHome(t)
	repoRoot := filepath.Join(t.TempDir(), "repo-broken-config")
	configPath := filepath.Join(repoRoot, ".dalek", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatalf("MkdirAll config dir failed: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("{invalid-json"), 0o644); err != nil {
		t.Fatalf("WriteFile config failed: %v", err)
	}
	const projectName = "demo-broken-config"
	registerProjectForNotebookComponentTest(t, h, projectName, repoRoot)

	component := newDaemonNotebookComponent(h, nil, 1)
	component.openBackoffGap = time.Hour

	if got := component.processProject(context.Background(), 1, projectName, false); got {
		t.Fatalf("expected no work for temporary open error")
	}
	if component.shouldSkipProjectOpen(projectName, time.Now()) {
		t.Fatalf("temporary open errors should not enter long backoff")
	}
}
