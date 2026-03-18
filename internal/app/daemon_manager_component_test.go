package app

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	daemonsvc "dalek/internal/services/daemon"
	"dalek/internal/store"
)

func countMergeItemsForTicket(t *testing.T, p *Project, ticketID uint) int64 {
	t.Helper()
	var cnt int64
	if err := p.core.DB.WithContext(context.Background()).Model(&store.MergeItem{}).Where("ticket_id = ?", ticketID).Count(&cnt).Error; err != nil {
		t.Fatalf("count merge items failed: %v", err)
	}
	return cnt
}

func waitForManagerInitialTick(t *testing.T, p *Project, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var st contracts.PMState
		if err := p.core.DB.WithContext(context.Background()).First(&st).Error; err == nil && st.LastTickAt != nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("manager initial tick not observed within %s", timeout)
}

func waitForMergeItemCount(t *testing.T, p *Project, ticketID uint, want int64, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if got := countMergeItemsForTicket(t, p, ticketID); got == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("merge item count not reached: want=%d", want)
}

func waitForSubmitTicketLoopCalls(t *testing.T, host *stubManagerExecutionHost, want int, timeout time.Duration) []daemonsvc.TicketLoopSubmitRequest {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		calls := host.snapshot()
		if len(calls) >= want {
			return calls
		}
		time.Sleep(20 * time.Millisecond)
	}
	return host.snapshot()
}

func waitForTicketLoopSubmission(t *testing.T, host *stubManagerExecutionHost, ticketID uint, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		calls := host.snapshot()
		for _, call := range calls {
			if call.TicketID == ticketID {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("ticket loop submission for t%d not observed within %s: calls=%+v", ticketID, timeout, host.snapshot())
}

func waitForFocusView(t *testing.T, p *Project, focusID uint, timeout time.Duration, predicate func(FocusRunView) bool) FocusRunView {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		view, err := p.FocusGet(context.Background(), focusID)
		if err == nil && predicate(view) {
			return view
		}
		time.Sleep(50 * time.Millisecond)
	}
	view, err := p.FocusGet(context.Background(), focusID)
	if err != nil {
		t.Fatalf("FocusGet(%d) failed while waiting: %v", focusID, err)
	}
	t.Fatalf("focus %d did not reach expected state within %s: %+v", focusID, timeout, view)
	return FocusRunView{}
}

func focusViewItemByTicketID(items []contracts.FocusRunItem, ticketID uint) *contracts.FocusRunItem {
	for i := range items {
		if items[i].TicketID == ticketID {
			item := items[i]
			return &item
		}
	}
	return nil
}

func mustRunGitForDaemonManagerTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func prepareDaemonFocusMergeConflictTicket(t *testing.T, p *Project, tk contracts.Ticket) string {
	t.Helper()
	ctx := context.Background()

	targetBranch := strings.TrimSpace(mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "branch", "--show-current"))
	worker, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	conflictFile := "conflict.txt"
	conflictPath := filepath.Join(worker.WorktreePath, conflictFile)
	if err := os.WriteFile(conflictPath, []byte("worker change\n"), 0o644); err != nil {
		t.Fatalf("write worker conflict file failed: %v", err)
	}
	mustRunGitForDaemonManagerTest(t, worker.WorktreePath, "add", conflictFile)
	mustRunGitForDaemonManagerTest(t, worker.WorktreePath, "commit", "-m", "worker conflict change")
	workerAnchor := mustRunGitForDaemonManagerTest(t, worker.WorktreePath, "rev-parse", "HEAD")

	targetConflictPath := filepath.Join(p.RepoRoot(), conflictFile)
	mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "checkout", targetBranch)
	if err := os.WriteFile(targetConflictPath, []byte("target change\n"), 0o644); err != nil {
		t.Fatalf("write target conflict file failed: %v", err)
	}
	mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "add", conflictFile)
	mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "commit", "-m", "target conflict change")

	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"merge_anchor_sha":   workerAnchor,
		"target_branch":      "refs/heads/" + targetBranch,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare merge conflict ticket failed: %v", err)
	}
	return targetBranch
}

func prepareMergedReplacementTicketForDaemonFocus(t *testing.T, p *Project, replacementTicketID uint, targetBranch string) string {
	t.Helper()
	ctx := context.Background()

	branch := fmt.Sprintf("replacement-focus-%d", replacementTicketID)
	fileName := fmt.Sprintf("replacement-%d.txt", replacementTicketID)
	mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "checkout", targetBranch)
	mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "checkout", "-b", branch)
	if err := os.WriteFile(filepath.Join(p.RepoRoot(), fileName), []byte("replacement merged\n"), 0o644); err != nil {
		t.Fatalf("write replacement file failed: %v", err)
	}
	mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "add", fileName)
	mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "commit", "-m", "replacement merge change")
	anchorSHA := mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "rev-parse", "HEAD")
	mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "checkout", targetBranch)
	mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "merge", branch, "--no-edit")
	newHead := mustRunGitForDaemonManagerTest(t, p.RepoRoot(), "rev-parse", "HEAD")

	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Ticket{}).Where("id = ?", replacementTicketID).Updates(map[string]any{
		"workflow_status":    contracts.TicketDone,
		"integration_status": contracts.IntegrationNeedsMerge,
		"merge_anchor_sha":   anchorSHA,
		"target_branch":      "refs/heads/" + targetBranch,
		"updated_at":         time.Now(),
	}).Error; err != nil {
		t.Fatalf("prepare replacement ticket failed: %v", err)
	}
	return newHead
}

func TestDaemonManagerComponent_NotifyProject_TriggersTick(t *testing.T) {
	h, p := newIntegrationHomeProject(t)

	manager := newDaemonManagerComponent(h, nil)
	manager.interval = time.Hour

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = manager.Stop(context.Background())
	})
	if err := manager.Start(runCtx); err != nil {
		t.Fatalf("manager Start failed: %v", err)
	}

	waitForManagerInitialTick(t, p, 3*time.Second)
	var st contracts.PMState
	if err := p.core.DB.WithContext(context.Background()).First(&st).Error; err != nil {
		t.Fatalf("load pm state failed: %v", err)
	}
	if st.LastTickAt == nil {
		t.Fatalf("expected last_tick_at after initial tick")
	}
	beforeTick := st.LastTickAt.UTC()

	manager.NotifyProject(p.Name())
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var nowState contracts.PMState
		if err := p.core.DB.WithContext(context.Background()).First(&nowState).Error; err != nil {
			t.Fatalf("reload pm state failed: %v", err)
		}
		if nowState.LastTickAt != nil && nowState.LastTickAt.UTC().After(beforeTick) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("manager notify did not trigger tick within timeout")
}

type stubManagerExecutionHost struct {
	mu    sync.Mutex
	calls []daemonsvc.TicketLoopSubmitRequest
}

func (s *stubManagerExecutionHost) SubmitTicketLoop(_ context.Context, req daemonsvc.TicketLoopSubmitRequest) (daemonsvc.TicketLoopSubmitReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	taskRunID := uint(len(s.calls))
	return daemonsvc.TicketLoopSubmitReceipt{
		Accepted:  true,
		Project:   req.Project,
		RequestID: req.RequestID,
		TicketID:  req.TicketID,
		TaskRunID: taskRunID,
	}, nil
}

func (s *stubManagerExecutionHost) CancelTaskRun(_ context.Context, runID uint) (daemonsvc.CancelResult, error) {
	return daemonsvc.CancelResult{Found: runID != 0, Canceled: runID != 0}, nil
}

func (s *stubManagerExecutionHost) CancelTaskRunWithCause(ctx context.Context, runID uint, cause contracts.TaskCancelCause) (daemonsvc.CancelResult, error) {
	_ = cause
	return s.CancelTaskRun(ctx, runID)
}

func (s *stubManagerExecutionHost) CancelTicketLoop(_ context.Context, project string, ticketID uint) (daemonsvc.CancelResult, error) {
	return daemonsvc.CancelResult{Found: strings.TrimSpace(project) != "" && ticketID != 0, Canceled: ticketID != 0}, nil
}

func (s *stubManagerExecutionHost) CancelTicketLoopWithCause(ctx context.Context, project string, ticketID uint, cause contracts.TaskCancelCause) (daemonsvc.CancelResult, error) {
	_ = cause
	return s.CancelTicketLoop(ctx, project, ticketID)
}

func (s *stubManagerExecutionHost) snapshot() []daemonsvc.TicketLoopSubmitRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]daemonsvc.TicketLoopSubmitRequest, len(s.calls))
	copy(out, s.calls)
	return out
}

type stubWarmupExecutionHost struct {
	mu          sync.Mutex
	warmupCalls map[string][]uint
}

func (s *stubWarmupExecutionHost) SubmitTicketLoop(_ context.Context, req daemonsvc.TicketLoopSubmitRequest) (daemonsvc.TicketLoopSubmitReceipt, error) {
	return daemonsvc.TicketLoopSubmitReceipt{
		Accepted:  true,
		Project:   req.Project,
		RequestID: req.RequestID,
		TicketID:  req.TicketID,
	}, nil
}

func (s *stubWarmupExecutionHost) CancelTaskRun(_ context.Context, runID uint) (daemonsvc.CancelResult, error) {
	return daemonsvc.CancelResult{Found: runID != 0, Canceled: runID != 0}, nil
}

func (s *stubWarmupExecutionHost) CancelTaskRunWithCause(ctx context.Context, runID uint, cause contracts.TaskCancelCause) (daemonsvc.CancelResult, error) {
	_ = cause
	return s.CancelTaskRun(ctx, runID)
}

func (s *stubWarmupExecutionHost) CancelTicketLoop(_ context.Context, project string, ticketID uint) (daemonsvc.CancelResult, error) {
	return daemonsvc.CancelResult{Found: strings.TrimSpace(project) != "" && ticketID != 0, Canceled: ticketID != 0}, nil
}

func (s *stubWarmupExecutionHost) CancelTicketLoopWithCause(ctx context.Context, project string, ticketID uint, cause contracts.TaskCancelCause) (daemonsvc.CancelResult, error) {
	_ = cause
	return s.CancelTicketLoop(ctx, project, ticketID)
}

func (s *stubWarmupExecutionHost) WarmupRunProjectIndex(project string, runIDs []uint) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.warmupCalls == nil {
		s.warmupCalls = map[string][]uint{}
	}
	project = strings.TrimSpace(project)
	copied := make([]uint, len(runIDs))
	copy(copied, runIDs)
	s.warmupCalls[project] = append(s.warmupCalls[project], copied...)
	return len(runIDs)
}

func (s *stubWarmupExecutionHost) snapshotWarmup(project string) []uint {
	s.mu.Lock()
	defer s.mu.Unlock()
	project = strings.TrimSpace(project)
	got := s.warmupCalls[project]
	out := make([]uint, len(got))
	copy(out, got)
	return out
}

func TestDaemonManagerComponent_RunTickProject_UsesWorkerRunHostSubmitter(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()
	tk, err := p.CreateTicketWithDescription(ctx, "manager submitter wiring", "worker run activation should go through execution host submitter")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	if err := p.core.DB.WithContext(ctx).Model(&store.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}

	host := &stubManagerExecutionHost{}
	manager := newDaemonManagerComponent(h, nil)
	manager.setExecutionHost(host)
	manager.runTickProject(ctx, p.Name(), "test")

	calls := waitForSubmitTicketLoopCalls(t, host, 1, 3*time.Second)
	if len(calls) != 1 {
		t.Fatalf("expected one SubmitTicketLoop call, got=%d", len(calls))
	}
	if calls[0].TicketID != tk.ID {
		t.Fatalf("unexpected ticket id: got=%d want=%d", calls[0].TicketID, tk.ID)
	}
	if calls[0].Project != p.Name() {
		t.Fatalf("unexpected project: got=%q want=%q", calls[0].Project, p.Name())
	}
	wantPrefix := fmt.Sprintf("mgr_t%d_", tk.ID)
	if !strings.HasPrefix(calls[0].RequestID, wantPrefix) {
		t.Fatalf("unexpected request id prefix: got=%q want_prefix=%q", calls[0].RequestID, wantPrefix)
	}
}

func TestDaemonManagerComponent_ResolvesFocusHandoffAndContinuesBatch(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk1, err := p.CreateTicketWithDescription(ctx, "focus handoff first", "first ticket should conflict and hand off")
	if err != nil {
		t.Fatalf("CreateTicket(t1) failed: %v", err)
	}
	tk2, err := p.CreateTicketWithDescription(ctx, "focus handoff second", "second ticket should wait until handoff resolved")
	if err != nil {
		t.Fatalf("CreateTicket(t2) failed: %v", err)
	}
	targetBranch := prepareDaemonFocusMergeConflictTicket(t, p, *tk1)

	host := &stubManagerExecutionHost{}
	manager := newDaemonManagerComponent(h, nil)
	manager.interval = time.Hour
	manager.setExecutionHost(host)

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = manager.Stop(context.Background())
	})
	if err := manager.Start(runCtx); err != nil {
		t.Fatalf("manager Start failed: %v", err)
	}
	waitForManagerInitialTick(t, p, 3*time.Second)
	focusProject, err := manager.registry.Open(p.Name())
	if err != nil {
		t.Fatalf("registry.Open project failed: %v", err)
	}

	res, err := focusProject.FocusStart(ctx, FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	manager.runTickProject(ctx, p.Name(), "test.focus_start")

	blockedView := waitForFocusView(t, focusProject, res.FocusID, 5*time.Second, func(view FocusRunView) bool {
		item1 := focusViewItemByTicketID(view.Items, tk1.ID)
		item2 := focusViewItemByTicketID(view.Items, tk2.ID)
		return view.Run.Status == contracts.FocusBlocked &&
			item1 != nil &&
			item1.Status == contracts.FocusItemBlocked &&
			item1.BlockedReason == "handoff_waiting_merge" &&
			item1.HandoffTicketID != nil &&
			item2 != nil &&
			item2.Status == contracts.FocusItemPending
	})
	item1 := focusViewItemByTicketID(blockedView.Items, tk1.ID)
	if item1 == nil || item1.HandoffTicketID == nil || *item1.HandoffTicketID == 0 {
		t.Fatalf("expected replacement ticket created, got=%+v", item1)
	}
	replacementTicketID := *item1.HandoffTicketID

	newHead := prepareMergedReplacementTicketForDaemonFocus(t, p, replacementTicketID, targetBranch)
	if _, err := focusProject.pm.SyncRef(ctx, "refs/heads/"+targetBranch, "", newHead); err != nil {
		t.Fatalf("SyncRef failed: %v", err)
	}
	manager.runTickProject(ctx, p.Name(), "test.sync_ref")

	runningView := waitForFocusView(t, focusProject, res.FocusID, 5*time.Second, func(view FocusRunView) bool {
		item1 := focusViewItemByTicketID(view.Items, tk1.ID)
		item2 := focusViewItemByTicketID(view.Items, tk2.ID)
		return item1 != nil &&
			item1.Status == contracts.FocusItemCompleted &&
			item2 != nil &&
			item2.Status != contracts.FocusItemPending
	})
	sourceItem := focusViewItemByTicketID(runningView.Items, tk1.ID)
	if sourceItem == nil {
		t.Fatalf("expected source item in focus view")
	}
	var resolvedEvent contracts.FocusEvent
	if err := mustProjectDB(t, p).WithContext(ctx).
		Where("focus_run_id = ? AND focus_item_id = ? AND kind = ?", res.FocusID, sourceItem.ID, contracts.FocusEventHandoffResolved).
		First(&resolvedEvent).Error; err != nil {
		t.Fatalf("expected handoff resolved event, got err=%v", err)
	}

	var sourceAfter contracts.Ticket
	if err := mustProjectDB(t, p).WithContext(ctx).First(&sourceAfter, tk1.ID).Error; err != nil {
		t.Fatalf("reload source ticket failed: %v", err)
	}
	if sourceAfter.SupersededByTicketID == nil || *sourceAfter.SupersededByTicketID != replacementTicketID {
		t.Fatalf("expected source superseded_by_ticket_id=%d, got=%v", replacementTicketID, sourceAfter.SupersededByTicketID)
	}
	if contracts.CanonicalIntegrationStatus(sourceAfter.IntegrationStatus) != contracts.IntegrationAbandoned {
		t.Fatalf("expected source integration abandoned, got=%s", sourceAfter.IntegrationStatus)
	}

	waitForTicketLoopSubmission(t, host, tk2.ID, 5*time.Second)
}

func TestDaemonManagerComponent_WarmupRunProjectIndex_LoadsActiveRuns(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "manager warmup index", "warmup should index active runs")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	w, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	activeRun, err := p.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         p.Key(),
		TicketID:           tk.ID,
		WorkerID:           w.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          fmt.Sprintf("warmup-active-%d", now.UnixNano()),
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("create active worker run failed: %v", err)
	}

	host := &stubWarmupExecutionHost{}
	manager := newDaemonManagerComponent(h, nil)
	manager.setExecutionHost(host)
	manager.warmupRunProjectIndex(ctx)

	warmed := host.snapshotWarmup(p.Name())
	foundActive := false
	for _, runID := range warmed {
		if runID == activeRun.ID {
			foundActive = true
		}
	}
	if !foundActive {
		t.Fatalf("expected warmup includes active worker run %d, got=%v", activeRun.ID, warmed)
	}
}
