package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
	daemonsvc "dalek/internal/services/daemon"
)

func runGitForFocusRealIntegration(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func commitFileForFocusRealIntegration(t *testing.T, dir, relPath, content, message string) string {
	t.Helper()
	fullPath := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatalf("mkdir %s failed: %v", filepath.Dir(fullPath), err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s failed: %v", relPath, err)
	}
	runGitForFocusRealIntegration(t, dir, "add", relPath)
	runGitForFocusRealIntegration(t, dir, "commit", "-m", message)
	return runGitForFocusRealIntegration(t, dir, "rev-parse", "HEAD")
}

func latestWorkerForFocusRealIntegration(t *testing.T, p *Project, ticketID uint) contracts.Worker {
	t.Helper()
	var worker contracts.Worker
	if err := mustProjectDB(t, p).WithContext(context.Background()).
		Where("ticket_id = ?", ticketID).
		Order("id desc").
		First(&worker).Error; err != nil {
		t.Fatalf("load latest worker for t%d failed: %v", ticketID, err)
	}
	return worker
}

func TestIntegration_FocusHandoff_StrictSerialDaemonOwnedE2E(t *testing.T) {
	h, _ := newIntegrationHomeProject(t)
	registry := NewProjectRegistry(h)
	p, err := registry.Open("demo")
	if err != nil {
		t.Fatalf("registry Open failed: %v", err)
	}
	ctx := context.Background()

	targetBranch := runGitForFocusRealIntegration(t, p.RepoRoot(), "branch", "--show-current")
	tk1, err := p.CreateTicketWithDescription(ctx, "focus strict serial source", "source ticket should hand off after merge conflict")
	if err != nil {
		t.Fatalf("CreateTicket(t1) failed: %v", err)
	}
	tk2, err := p.CreateTicketWithDescription(ctx, "focus strict serial next", "next ticket must wait until handoff resolves")
	if err != nil {
		t.Fatalf("CreateTicket(t2) failed: %v", err)
	}

	var runMu sync.Mutex
	runCounts := map[uint]int{}
	var targetConflictCommitted bool

	p.pm.SetTaskRunner(integrationTaskRunnerFunc(func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
		ticketID := requiredEnvUint(t, req.Env, "DALEK_TICKET_ID")
		workerID := requiredEnvUint(t, req.Env, "DALEK_WORKER_ID")
		worktreePath := strings.TrimSpace(req.WorkDir)

		runMu.Lock()
		runCounts[ticketID]++
		runMu.Unlock()

		switch ticketID {
		case tk1.ID:
			headSHA := commitFileForFocusRealIntegration(t, worktreePath, "conflict.txt", "worker change\n", "source worker change")
			runMu.Lock()
			if !targetConflictCommitted {
				targetConflictCommitted = true
				runMu.Unlock()
				commitFileForFocusRealIntegration(t, p.RepoRoot(), "conflict.txt", "target change\n", "target conflict change")
			} else {
				runMu.Unlock()
			}
			writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "done", "source done and waiting merge", nil, true, headSHA, "clean")
			applyWorkerReportForIntegration(t, p, req, "done", "source done and waiting merge", nil, false, false, headSHA)
		case tk2.ID:
			headSHA := commitFileForFocusRealIntegration(t, worktreePath, "t2.txt", "second ticket merged after handoff\n", "second ticket change")
			writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "done", "second ticket done after handoff", nil, true, headSHA, "clean")
			applyWorkerReportForIntegration(t, p, req, "done", "second ticket done after handoff", nil, false, false, headSHA)
		default:
			status := runGitForFocusRealIntegration(t, worktreePath, "status", "--porcelain")
			if strings.TrimSpace(status) != "" {
				t.Fatalf("replacement worktree should start clean, got status=%q", status)
			}
			rawConflict, err := os.ReadFile(filepath.Join(worktreePath, "conflict.txt"))
			if err != nil {
				t.Fatalf("read replacement conflict file failed: %v", err)
			}
			if string(rawConflict) != "target change\n" {
				t.Fatalf("replacement worktree should be based on latest target head, got=%q", string(rawConflict))
			}
			headSHA := commitFileForFocusRealIntegration(t, worktreePath, "conflict.txt", "resolved change\n", "replacement resolved change")
			writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "done", "replacement integration done", nil, true, headSHA, "clean")
			applyWorkerReportForIntegration(t, p, req, "done", "replacement integration done", nil, false, false, headSHA)
		}

		if onEvent != nil {
			onEvent(sdkrunner.Event{Type: "agent_message", Text: "focus handoff e2e"})
		}
		return sdkrunner.Result{
			Provider:   "test",
			OutputMode: sdkrunner.OutputModeJSONL,
			Text:       "ok",
		}, nil
	}))

	manager := newDaemonManagerComponent(h, nil, registry)
	manager.interval = time.Hour
	resolver := newDaemonProjectResolver(h, registry)
	host, err := daemonsvc.NewExecutionHost(resolver, daemonsvc.ExecutionHostOptions{
		OnRunSettled: manager.NotifyProject,
	})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	manager.setExecutionHost(host)

	addr := reserveLoopbackAddr(t)
	api, err := daemonsvc.NewInternalAPI(host, daemonsvc.InternalAPIConfig{ListenAddr: addr}, daemonsvc.InternalAPIOptions{})
	if err != nil {
		t.Fatalf("NewInternalAPI failed: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = api.Stop(context.Background())
		_ = manager.Stop(context.Background())
		_ = host.Stop(context.Background())
	})
	if err := manager.Start(runCtx); err != nil {
		t.Fatalf("manager Start failed: %v", err)
	}
	if err := api.Start(runCtx); err != nil {
		t.Fatalf("InternalAPI Start failed: %v", err)
	}
	waitForManagerInitialTick(t, p, 3*time.Second)

	client, err := NewDaemonAPIClient(DaemonAPIClientConfig{BaseURL: "http://" + addr})
	if err != nil {
		t.Fatalf("NewDaemonAPIClient failed: %v", err)
	}

	focusRes, err := client.FocusStart(ctx, DaemonFocusStartRequest{
		Project: p.Name(),
		FocusStartInput: FocusStartInput{
			Mode:           contracts.FocusModeBatch,
			ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
		},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	blockedView := waitForFocusView(t, p, focusRes.FocusID, 10*time.Second, func(view FocusRunView) bool {
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
	sourceItem := focusViewItemByTicketID(blockedView.Items, tk1.ID)
	if sourceItem == nil || sourceItem.HandoffTicketID == nil || *sourceItem.HandoffTicketID == 0 {
		t.Fatalf("expected handoff replacement ticket, got=%+v", sourceItem)
	}
	replacementTicketID := *sourceItem.HandoffTicketID

	runMu.Lock()
	if runCounts[tk2.ID] != 0 {
		t.Fatalf("expected t%d not started before handoff resolve, got=%d", tk2.ID, runCounts[tk2.ID])
	}
	runMu.Unlock()

	if _, err := client.SubmitTicketLoop(ctx, DaemonTicketLoopSubmitRequest{
		Project:    p.Name(),
		TicketID:   replacementTicketID,
		RequestID:  "focus-handoff-replacement",
		Prompt:     "处理集成冲突并完成交付",
		BaseBranch: "refs/heads/" + targetBranch,
	}); err != nil {
		t.Fatalf("SubmitTicketLoop(replacement) failed: %v", err)
	}

	waitUntil(t, 10*time.Second, func() bool {
		view, viewErr := p.GetTicketViewByID(ctx, replacementTicketID)
		return viewErr == nil &&
			view != nil &&
			view.Ticket.WorkflowStatus == contracts.TicketDone &&
			contracts.CanonicalIntegrationStatus(view.Ticket.IntegrationStatus) == contracts.IntegrationNeedsMerge
	}, "replacement ticket reaches done+needs_merge")
	replacementView, err := p.GetTicketViewByID(ctx, replacementTicketID)
	if err != nil {
		t.Fatalf("GetTicketViewByID(replacement) failed: %v", err)
	}
	if strings.TrimSpace(replacementView.Ticket.MergeAnchorSHA) == "" {
		t.Fatalf("expected replacement merge anchor populated after done closure")
	}
	if strings.TrimSpace(replacementView.Ticket.TargetBranch) != "refs/heads/"+targetBranch {
		t.Fatalf("unexpected replacement target branch: %q", replacementView.Ticket.TargetBranch)
	}

	replacementWorker := latestWorkerForFocusRealIntegration(t, p, replacementTicketID)
	runGitForFocusRealIntegration(t, p.RepoRoot(), "checkout", targetBranch)
	runGitForFocusRealIntegration(t, p.RepoRoot(), "merge", strings.TrimSpace(replacementWorker.Branch), "--no-edit")
	rescanRes, err := p.RescanTicketMergeStatus(ctx, "refs/heads/"+targetBranch)
	if err != nil {
		t.Fatalf("RescanTicketMergeStatus failed: %v", err)
	}
	// 这条用例关注 strict-serial handoff，不覆盖 merge clean gate；
	// replacement 已手工 merge 后，显式把 repo root 收回 clean，再观察后续 focus 推进。
	runGitForFocusRealIntegration(t, p.RepoRoot(), "reset", "--hard", "HEAD")
	runGitForFocusRealIntegration(t, p.RepoRoot(), "clean", "-fd")
	foundReplacementMerged := false
	for _, item := range rescanRes.Results {
		for _, mergedID := range item.MergedTicketIDs {
			if mergedID == replacementTicketID {
				foundReplacementMerged = true
				break
			}
		}
		if foundReplacementMerged {
			break
		}
	}
	if !foundReplacementMerged {
		waitUntil(t, 5*time.Second, func() bool {
			view, viewErr := p.GetTicketViewByID(ctx, replacementTicketID)
			return viewErr == nil &&
				view != nil &&
				contracts.CanonicalIntegrationStatus(view.Ticket.IntegrationStatus) == contracts.IntegrationMerged
		}, "replacement ticket observed merged by daemon tick or rescan")
	}
	waitUntil(t, 10*time.Second, func() bool {
		runMu.Lock()
		defer runMu.Unlock()
		return runCounts[tk2.ID] > 0
	}, "second ticket starts after handoff resolve")
	// 这条用例关注 strict-serial handoff，不验证 repo root 在其他流程下的脏写细节；
	// 在第二张票开始后显式把 root 收回 clean，避免 clean gate 把无关噪声放大成失败。
	runGitForFocusRealIntegration(t, p.RepoRoot(), "reset", "--hard", "HEAD")
	runGitForFocusRealIntegration(t, p.RepoRoot(), "clean", "-fd")

	finalView := waitForFocusView(t, p, focusRes.FocusID, 15*time.Second, func(view FocusRunView) bool {
		item1 := focusViewItemByTicketID(view.Items, tk1.ID)
		item2 := focusViewItemByTicketID(view.Items, tk2.ID)
		return view.Run.Status == contracts.FocusCompleted &&
			item1 != nil &&
			item1.Status == contracts.FocusItemCompleted &&
			item2 != nil &&
			item2.Status == contracts.FocusItemCompleted
	})

	runMu.Lock()
	t2Runs := runCounts[tk2.ID]
	runMu.Unlock()
	if t2Runs == 0 {
		t.Fatalf("expected t%d to start after handoff resolved", tk2.ID)
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

	var resolvedEvent contracts.FocusEvent
	if err := mustProjectDB(t, p).WithContext(ctx).
		Where("focus_run_id = ? AND focus_item_id = ? AND kind = ?", focusRes.FocusID, sourceItem.ID, contracts.FocusEventHandoffResolved).
		First(&resolvedEvent).Error; err != nil {
		t.Fatalf("expected handoff resolved event, got err=%v", err)
	}

	item2 := focusViewItemByTicketID(finalView.Items, tk2.ID)
	if item2 == nil || item2.Status != contracts.FocusItemCompleted {
		t.Fatalf("expected second focus item completed, got=%+v", item2)
	}
}

// TestIntegration_ConvergentBatch_DaemonOwnedE2E verifies the convergent batch
// end-to-end in a real daemon-owned execution path:
//
//	t1 starts → worker completes → merge → t2 starts → worker completes → merge → batch done → enter pm_run
//
// It also verifies that item.selected is emitted exactly once per item (no event storm).
func TestIntegration_ConvergentBatch_DaemonOwnedE2E(t *testing.T) {
	h, _ := newIntegrationHomeProject(t)
	registry := NewProjectRegistry(h)
	p, err := registry.Open("demo")
	if err != nil {
		t.Fatalf("registry Open failed: %v", err)
	}
	ctx := context.Background()

	targetBranch := runGitForFocusRealIntegration(t, p.RepoRoot(), "branch", "--show-current")

	tk1, err := p.CreateTicketWithDescription(ctx, "convergent batch t1", "first batch ticket")
	if err != nil {
		t.Fatalf("CreateTicket(t1) failed: %v", err)
	}
	tk2, err := p.CreateTicketWithDescription(ctx, "convergent batch t2", "second batch ticket")
	if err != nil {
		t.Fatalf("CreateTicket(t2) failed: %v", err)
	}

	var runMu sync.Mutex
	runCounts := map[uint]int{}

	p.pm.SetTaskRunner(integrationTaskRunnerFunc(func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
		ticketID := requiredEnvUint(t, req.Env, "DALEK_TICKET_ID")
		workerID := requiredEnvUint(t, req.Env, "DALEK_WORKER_ID")
		worktreePath := strings.TrimSpace(req.WorkDir)

		runMu.Lock()
		runCounts[ticketID]++
		runMu.Unlock()

		// Each worker commits a unique file and reports done.
		fileName := fmt.Sprintf("t%d.txt", ticketID)
		headSHA := commitFileForFocusRealIntegration(t, worktreePath, fileName,
			fmt.Sprintf("change from ticket %d\n", ticketID),
			fmt.Sprintf("ticket %d worker change", ticketID))

		writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath,
			"done", fmt.Sprintf("ticket %d done", ticketID), nil, true, headSHA, "clean")
		applyWorkerReportForIntegration(t, p, req,
			"done", fmt.Sprintf("ticket %d done", ticketID), nil, false, false, headSHA)

		if onEvent != nil {
			onEvent(sdkrunner.Event{Type: "agent_message", Text: "convergent e2e"})
		}
		return sdkrunner.Result{
			Provider:   "test",
			OutputMode: sdkrunner.OutputModeJSONL,
			Text:       "ok",
		}, nil
	}))

	manager := newDaemonManagerComponent(h, nil, registry)
	manager.interval = time.Hour
	resolver := newDaemonProjectResolver(h, registry)
	host, err := daemonsvc.NewExecutionHost(resolver, daemonsvc.ExecutionHostOptions{
		OnRunSettled: manager.NotifyProject,
	})
	if err != nil {
		t.Fatalf("NewExecutionHost failed: %v", err)
	}
	manager.setExecutionHost(host)

	addr := reserveLoopbackAddr(t)
	api, err := daemonsvc.NewInternalAPI(host, daemonsvc.InternalAPIConfig{ListenAddr: addr}, daemonsvc.InternalAPIOptions{})
	if err != nil {
		t.Fatalf("NewInternalAPI failed: %v", err)
	}

	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = api.Stop(context.Background())
		_ = manager.Stop(context.Background())
		_ = host.Stop(context.Background())
	})
	if err := manager.Start(runCtx); err != nil {
		t.Fatalf("manager Start failed: %v", err)
	}
	if err := api.Start(runCtx); err != nil {
		t.Fatalf("InternalAPI Start failed: %v", err)
	}
	waitForManagerInitialTick(t, p, 3*time.Second)

	client, err := NewDaemonAPIClient(DaemonAPIClientConfig{BaseURL: "http://" + addr})
	if err != nil {
		t.Fatalf("NewDaemonAPIClient failed: %v", err)
	}

	// Start convergent focus run with 2 tickets.
	focusRes, err := client.FocusStart(ctx, DaemonFocusStartRequest{
		Project: p.Name(),
		FocusStartInput: FocusStartInput{
			Mode:           contracts.FocusModeConvergent,
			ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
			MaxPMRuns:      2,
		},
	})
	if err != nil {
		t.Fatalf("FocusStart convergent failed: %v", err)
	}

	// Wait for t1 to complete (worker runs + merge).
	waitUntil(t, 15*time.Second, func() bool {
		view, viewErr := p.FocusGet(ctx, focusRes.FocusID)
		if viewErr != nil {
			return false
		}
		item1 := focusViewItemByTicketID(view.Items, tk1.ID)
		return item1 != nil && item1.Status == contracts.FocusItemCompleted
	}, "t1 reaches completed")

	// Clean up repo root to avoid clean-gate noise.
	runGitForFocusRealIntegration(t, p.RepoRoot(), "checkout", targetBranch)
	runGitForFocusRealIntegration(t, p.RepoRoot(), "reset", "--hard", "HEAD")
	runGitForFocusRealIntegration(t, p.RepoRoot(), "clean", "-fd")

	// Wait for t2 to start.
	waitUntil(t, 10*time.Second, func() bool {
		runMu.Lock()
		defer runMu.Unlock()
		return runCounts[tk2.ID] > 0
	}, "t2 starts after t1 completes")

	// Clean up again before t2 merge.
	runGitForFocusRealIntegration(t, p.RepoRoot(), "checkout", targetBranch)
	runGitForFocusRealIntegration(t, p.RepoRoot(), "reset", "--hard", "HEAD")
	runGitForFocusRealIntegration(t, p.RepoRoot(), "clean", "-fd")

	// Wait for t2 to complete.
	waitUntil(t, 15*time.Second, func() bool {
		view, viewErr := p.FocusGet(ctx, focusRes.FocusID)
		if viewErr != nil {
			return false
		}
		item2 := focusViewItemByTicketID(view.Items, tk2.ID)
		return item2 != nil && item2.Status == contracts.FocusItemCompleted
	}, "t2 reaches completed")

	// Wait for convergent phase to transition to pm_run (batch done).
	finalView := waitForFocusView(t, p, focusRes.FocusID, 10*time.Second, func(view FocusRunView) bool {
		return strings.TrimSpace(view.Run.ConvergentPhase) == "pm_run" ||
			view.Run.IsTerminal()
	})

	if strings.TrimSpace(finalView.Run.ConvergentPhase) != "pm_run" && !finalView.Run.IsTerminal() {
		t.Fatalf("expected convergent phase=pm_run or terminal, got phase=%q status=%q",
			finalView.Run.ConvergentPhase, finalView.Run.Status)
	}

	// The run must NOT be stuck at blocked.
	if finalView.Run.Status == contracts.FocusBlocked {
		t.Fatalf("convergent run should not be blocked after batch completion: %+v", finalView.Run)
	}

	// Verify item.selected event count: exactly 2 (one per ticket item).
	db := mustProjectDB(t, p)
	var selectedCount int64
	db.Model(&contracts.FocusEvent{}).
		Where("focus_run_id = ? AND kind = ?", focusRes.FocusID, contracts.FocusEventItemSelected).
		Count(&selectedCount)
	if selectedCount != 2 {
		t.Errorf("expected exactly 2 item.selected events (one per item), got=%d", selectedCount)
	}

	// Verify both items completed.
	finalItem1 := focusViewItemByTicketID(finalView.Items, tk1.ID)
	finalItem2 := focusViewItemByTicketID(finalView.Items, tk2.ID)
	if finalItem1 == nil || finalItem1.Status != contracts.FocusItemCompleted {
		t.Errorf("expected t1 item completed, got=%+v", finalItem1)
	}
	if finalItem2 == nil || finalItem2.Status != contracts.FocusItemCompleted {
		t.Errorf("expected t2 item completed, got=%+v", finalItem2)
	}

	// Verify run counts: each ticket should have exactly 1 worker run.
	runMu.Lock()
	t1Runs := runCounts[tk1.ID]
	t2Runs := runCounts[tk2.ID]
	runMu.Unlock()
	if t1Runs != 1 {
		t.Errorf("expected 1 run for t1, got=%d", t1Runs)
	}
	if t2Runs != 1 {
		t.Errorf("expected 1 run for t2, got=%d", t2Runs)
	}
}
