package app

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/agent/sdkrunner"
	"dalek/internal/contracts"
	daemonsvc "dalek/internal/services/daemon"
	pmsvc "dalek/internal/services/pm"
	"dalek/internal/services/ticketlifecycle"
	workersvc "dalek/internal/services/worker"

	"gorm.io/gorm"
)

func boolPtr(v bool) *bool {
	return &v
}

func gitCurrentBranchForLifecycleTest(t *testing.T, repoRoot string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse --abbrev-ref HEAD failed: %v\n%s", err, string(out))
	}
	branch := strings.TrimSpace(string(out))
	if branch == "" {
		t.Fatalf("current branch should not be empty")
	}
	return branch
}

func gitHeadSHAForLifecycleTest(t *testing.T, repoRoot string) string {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git rev-parse HEAD failed: %v\n%s", err, string(out))
	}
	sha := strings.TrimSpace(string(out))
	if len(sha) != 40 {
		t.Fatalf("unexpected HEAD sha: %q", sha)
	}
	return sha
}

func createWorkerDeliverRunForLifecycleTest(t *testing.T, p *Project, ticketID, workerID uint, prefix string) contracts.TaskRun {
	t.Helper()
	if p == nil || p.task == nil {
		t.Fatalf("project task service is nil")
	}
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	run, err := p.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         p.Key(),
		TicketID:           ticketID,
		WorkerID:           workerID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", ticketID),
		RequestID:          fmt.Sprintf("%s-t%d-w%d-%d", strings.TrimSpace(prefix), ticketID, workerID, time.Now().UnixNano()),
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("create worker deliver run failed: %v", err)
	}
	return run
}

func markRunCanceledForLifecycleTest(t *testing.T, p *Project, runID uint, reason string) {
	t.Helper()
	if p == nil || p.task == nil {
		t.Fatalf("project task service is nil")
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := p.task.MarkRunCanceled(context.Background(), runID, "test_cleanup", strings.TrimSpace(reason), now); err != nil {
		t.Fatalf("MarkRunCanceled failed: %v", err)
	}
}

func markTicketWorkerActiveForLifecycleTest(t *testing.T, p *Project, ticketID, workerID uint) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Ticket{}).Where("id = ?", ticketID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", workerID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("set worker running failed: %v", err)
	}
}

func acceptWorkerRunForLifecycleTest(t *testing.T, p *Project, ticketID, workerID, taskRunID uint, source string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	if strings.TrimSpace(source) == "" {
		source = "test.lifecycle.accepted"
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, _, err := ticketlifecycle.AppendEventTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleActivated,
			Source:         strings.TrimSpace(source),
			ActorType:      contracts.TicketLifecycleActorSystem,
			WorkerID:       workerID,
			TaskRunID:      taskRunID,
			IdempotencyKey: ticketlifecycle.ActivatedRunIdempotencyKey(ticketID, taskRunID),
			Payload: map[string]any{
				"ticket_id":   ticketID,
				"worker_id":   workerID,
				"task_run_id": taskRunID,
				"source":      strings.TrimSpace(source),
			},
			CreatedAt: now,
		}); err != nil {
			return err
		}
		if err := tx.Model(&contracts.Ticket{}).Where("id = ?", ticketID).Updates(map[string]any{
			"workflow_status": contracts.TicketActive,
			"updated_at":      now,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&contracts.Worker{}).Where("id = ?", workerID).Updates(map[string]any{
			"status":     contracts.WorkerRunning,
			"updated_at": now,
		}).Error
	}); err != nil {
		t.Fatalf("accept worker run failed: %v", err)
	}
}

func loadTicketForLifecycleTest(t *testing.T, p *Project, ticketID uint) contracts.Ticket {
	t.Helper()
	var tk contracts.Ticket
	if err := mustProjectDB(t, p).WithContext(context.Background()).First(&tk, ticketID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	return tk
}

func loadWorkerForLifecycleTest(t *testing.T, p *Project, workerID uint) contracts.Worker {
	t.Helper()
	var w contracts.Worker
	if err := mustProjectDB(t, p).WithContext(context.Background()).First(&w, workerID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	return w
}

func countLifecycleEventsForLifecycleTest(t *testing.T, p *Project, ticketID uint, eventType contracts.TicketLifecycleEventType) int64 {
	t.Helper()
	var count int64
	if err := mustProjectDB(t, p).WithContext(context.Background()).Model(&contracts.TicketLifecycleEvent{}).
		Where("ticket_id = ? AND event_type = ?", ticketID, eventType).
		Count(&count).Error; err != nil {
		t.Fatalf("count lifecycle events failed: %v", err)
	}
	return count
}

func latestLifecycleEventForLifecycleTest(t *testing.T, p *Project, ticketID uint, eventType contracts.TicketLifecycleEventType) contracts.TicketLifecycleEvent {
	t.Helper()
	var ev contracts.TicketLifecycleEvent
	if err := mustProjectDB(t, p).WithContext(context.Background()).
		Where("ticket_id = ? AND event_type = ?", ticketID, eventType).
		Order("sequence desc").
		First(&ev).Error; err != nil {
		t.Fatalf("load lifecycle event failed: %v", err)
	}
	return ev
}

func loadTaskRunForLifecycleTest(t *testing.T, p *Project, runID uint) contracts.TaskRun {
	t.Helper()
	if p == nil || p.task == nil {
		t.Fatalf("project task service is nil")
	}
	run, err := p.task.FindRunByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("FindRunByID failed: %v", err)
	}
	if run == nil {
		t.Fatalf("expected task run exists: run_id=%d", runID)
	}
	return *run
}

func waitForTicketWorkflowForLifecycleTest(t *testing.T, p *Project, ticketID uint, wantWorkflow contracts.TicketWorkflowStatus, wantIntegration contracts.IntegrationStatus, timeout time.Duration) contracts.Ticket {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		tk := loadTicketForLifecycleTest(t, p, ticketID)
		if tk.WorkflowStatus == wantWorkflow && contracts.CanonicalIntegrationStatus(tk.IntegrationStatus) == contracts.CanonicalIntegrationStatus(wantIntegration) {
			return tk
		}
		time.Sleep(50 * time.Millisecond)
	}
	tk := loadTicketForLifecycleTest(t, p, ticketID)
	t.Fatalf("ticket state not reached within %s: ticket=%d workflow=%s integration=%s want_workflow=%s want_integration=%s", timeout, ticketID, tk.WorkflowStatus, tk.IntegrationStatus, wantWorkflow, wantIntegration)
	return contracts.Ticket{}
}

func waitForLatestWorkerRunForLifecycleTest(t *testing.T, p *Project, ticketID uint, timeout time.Duration) *TaskStatus {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		run, err := p.FindLatestWorkerRun(context.Background(), ticketID, 0)
		if err != nil {
			t.Fatalf("FindLatestWorkerRun(%d) failed while waiting: %v", ticketID, err)
		}
		if run != nil && run.RunID != 0 {
			return run
		}
		time.Sleep(20 * time.Millisecond)
	}
	run, err := p.FindLatestWorkerRun(context.Background(), ticketID, 0)
	if err != nil {
		t.Fatalf("FindLatestWorkerRun(%d) failed after timeout: %v", ticketID, err)
	}
	t.Fatalf("latest worker run not reached within %s: ticket=%d run=%+v", timeout, ticketID, run)
	return nil
}

type acceptingLifecycleManagerHost struct {
	t       *testing.T
	project *Project

	mu        sync.Mutex
	submitted []uint
}

func (h *acceptingLifecycleManagerHost) SubmitTicketLoop(ctx context.Context, req daemonsvc.TicketLoopSubmitRequest) (daemonsvc.TicketLoopSubmitReceipt, error) {
	if h == nil || h.project == nil {
		return daemonsvc.TicketLoopSubmitReceipt{}, fmt.Errorf("lifecycle manager host project 为空")
	}
	worker, err := h.project.LatestWorker(ctx, req.TicketID)
	if err != nil {
		return daemonsvc.TicketLoopSubmitReceipt{}, err
	}
	if worker == nil || worker.ID == 0 {
		return daemonsvc.TicketLoopSubmitReceipt{}, fmt.Errorf("ticket %d missing worker before submit", req.TicketID)
	}
	workerID := worker.ID
	h.mu.Lock()
	h.submitted = append(h.submitted, req.TicketID)
	h.mu.Unlock()
	go func(ticketID uint) {
		time.Sleep(20 * time.Millisecond)
		run := createWorkerDeliverRunForLifecycleTest(h.t, h.project, ticketID, workerID, "daemon-manager-accept")
		acceptWorkerRunForLifecycleTest(h.t, h.project, ticketID, workerID, run.ID, "test.daemon_manager.accepted")
	}(req.TicketID)
	return daemonsvc.TicketLoopSubmitReceipt{
		Accepted:  true,
		Project:   strings.TrimSpace(req.Project),
		RequestID: strings.TrimSpace(req.RequestID),
		TaskRunID: 1,
		TicketID:  req.TicketID,
		WorkerID:  workerID,
	}, nil
}

func (h *acceptingLifecycleManagerHost) CancelTaskRun(ctx context.Context, runID uint) (daemonsvc.CancelResult, error) {
	_ = ctx
	return daemonsvc.CancelResult{Found: runID != 0, Canceled: runID != 0}, nil
}

func (h *acceptingLifecycleManagerHost) CancelTaskRunWithCause(ctx context.Context, runID uint, cause contracts.TaskCancelCause) (daemonsvc.CancelResult, error) {
	_ = cause
	return h.CancelTaskRun(ctx, runID)
}

func (h *acceptingLifecycleManagerHost) CancelTicketLoop(ctx context.Context, project string, ticketID uint) (daemonsvc.CancelResult, error) {
	_ = ctx
	return daemonsvc.CancelResult{
		Found:    ticketID != 0,
		Canceled: ticketID != 0,
		Project:  strings.TrimSpace(project),
		TicketID: ticketID,
	}, nil
}

func (h *acceptingLifecycleManagerHost) CancelTicketLoopWithCause(ctx context.Context, project string, ticketID uint, cause contracts.TaskCancelCause) (daemonsvc.CancelResult, error) {
	_ = cause
	return h.CancelTicketLoop(ctx, project, ticketID)
}

func (h *acceptingLifecycleManagerHost) SubmittedTicketIDs() []uint {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.submitted) == 0 {
		return nil
	}
	out := make([]uint, len(h.submitted))
	copy(out, h.submitted)
	return out
}

func assertLifecycleConsistentForLifecycleTest(t *testing.T, p *Project, ticketID uint, wantWorkflow contracts.TicketWorkflowStatus, wantIntegration contracts.IntegrationStatus) {
	t.Helper()
	check, err := p.CheckTicketLifecycleConsistency(context.Background(), ticketID)
	if err != nil {
		t.Fatalf("CheckTicketLifecycleConsistency failed: %v", err)
	}
	if check.Mismatch {
		t.Fatalf("expected lifecycle consistency, mismatches=%v", check.Mismatches)
	}
	if check.Snapshot.WorkflowStatus != wantWorkflow || check.Rebuilt.WorkflowStatus != wantWorkflow {
		t.Fatalf("unexpected workflow snapshot/rebuilt: snapshot=%s rebuilt=%s want=%s", check.Snapshot.WorkflowStatus, check.Rebuilt.WorkflowStatus, wantWorkflow)
	}
	if contracts.CanonicalIntegrationStatus(check.Snapshot.IntegrationStatus) != contracts.CanonicalIntegrationStatus(wantIntegration) ||
		contracts.CanonicalIntegrationStatus(check.Rebuilt.IntegrationStatus) != contracts.CanonicalIntegrationStatus(wantIntegration) {
		t.Fatalf("unexpected integration snapshot/rebuilt: snapshot=%s rebuilt=%s want=%s", check.Snapshot.IntegrationStatus, check.Rebuilt.IntegrationStatus, wantIntegration)
	}
}

func TestIntegration_Lifecycle_MainlineBlockedResumeDoneMergeArchive(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()
	targetBranch := gitCurrentBranchForLifecycleTest(t, p.RepoRoot())
	targetRef := "refs/heads/" + targetBranch
	headSHA := gitHeadSHAForLifecycleTest(t, p.RepoRoot())
	runCount := 0
	p.pm.SetTaskRunner(integrationTaskRunnerFunc(func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
		runCount++
		worktreePath := strings.TrimSpace(req.WorkDir)
		workerID := requiredEnvUint(t, req.Env, "DALEK_WORKER_ID")
		ticketID := requiredEnvUint(t, req.Env, "DALEK_TICKET_ID")
		switch runCount {
		case 1:
			writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "wait_user", "需要用户补充配置", []string{"请提供生产环境 API token"}, false, headSHA, "clean")
			applyWorkerReportForIntegration(t, p, req, "wait_user", "需要用户补充配置", []string{"请提供生产环境 API token"}, true, false, headSHA)
			return sdkrunner.Result{Provider: "test", OutputMode: sdkrunner.OutputModeJSONL, Text: "wait_user"}, nil
		case 2:
			writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "done", "实现与验证已完成", nil, true, headSHA, "clean")
			applyWorkerReportForIntegration(t, p, req, "done", "实现与验证已完成", nil, false, false, headSHA)
			return sdkrunner.Result{Provider: "test", OutputMode: sdkrunner.OutputModeJSONL, Text: "done"}, nil
		default:
			t.Fatalf("unexpected runner call=%d", runCount)
			return sdkrunner.Result{}, nil
		}
	}))

	tk, err := p.CreateTicketWithDescription(ctx, "lifecycle-mainline", "mainline lifecycle validation")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}

	w, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if w.Status != contracts.WorkerStopped {
		t.Fatalf("expected worker stopped after start, got=%s", w.Status)
	}
	if run, err := p.FindLatestWorkerRun(ctx, tk.ID, 0); err != nil {
		t.Fatalf("FindLatestWorkerRun failed: %v", err)
	} else if run != nil {
		t.Fatalf("expected no accepted worker run immediately after start, got=%d", run.RunID)
	}
	started := loadTicketForLifecycleTest(t, p, tk.ID)
	if started.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected queued after start, got=%s", started.WorkflowStatus)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleStartRequested); got != 1 {
		t.Fatalf("expected exactly 1 start_requested event, got=%d", got)
	}

	waitUserResult, err := p.RunTicketWorker(ctx, tk.ID, pmsvc.WorkerRunOptions{
		EntryPrompt: "继续执行任务",
		AutoStart:   boolPtr(false),
	})
	if err != nil {
		t.Fatalf("RunTicketWorker(wait_user) failed: %v", err)
	}
	if waitUserResult.LastNextAction != string(contracts.NextWaitUser) {
		t.Fatalf("expected wait_user next_action, got=%q", waitUserResult.LastNextAction)
	}
	blocked := loadTicketForLifecycleTest(t, p, tk.ID)
	if blocked.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected blocked after wait_user report, got=%s", blocked.WorkflowStatus)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleWaitUserReported); got != 1 {
		t.Fatalf("expected exactly 1 wait_user_reported event, got=%d", got)
	}

	resumedWorker, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket(blocked) failed: %v", err)
	}
	resumed := loadTicketForLifecycleTest(t, p, tk.ID)
	if resumed.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected queued after blocked restart, got=%s", resumed.WorkflowStatus)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleStartRequested); got != 2 {
		t.Fatalf("expected exactly 2 start_requested events after restart, got=%d", got)
	}

	doneResult, err := p.RunTicketWorker(ctx, tk.ID, pmsvc.WorkerRunOptions{
		EntryPrompt: "继续执行任务",
		AutoStart:   boolPtr(false),
	})
	if err != nil {
		t.Fatalf("RunTicketWorker(done) failed: %v", err)
	}
	if doneResult.WorkerID != resumedWorker.ID {
		t.Fatalf("expected done run reuse resumed worker=%d, got=%d", resumedWorker.ID, doneResult.WorkerID)
	}
	if doneResult.LastNextAction != string(contracts.NextDone) {
		t.Fatalf("expected done next_action, got=%q", doneResult.LastNextAction)
	}

	doneTicket := loadTicketForLifecycleTest(t, p, tk.ID)
	if doneTicket.WorkflowStatus != contracts.TicketDone {
		t.Fatalf("expected done after done report, got=%s", doneTicket.WorkflowStatus)
	}
	if got := contracts.CanonicalIntegrationStatus(doneTicket.IntegrationStatus); got != contracts.IntegrationNeedsMerge {
		t.Fatalf("expected needs_merge after done report, got=%s", got)
	}
	if strings.TrimSpace(doneTicket.MergeAnchorSHA) != headSHA {
		t.Fatalf("expected merge anchor from done report head, got=%q want=%q", doneTicket.MergeAnchorSHA, headSHA)
	}
	if strings.TrimSpace(doneTicket.TargetBranch) != targetRef {
		t.Fatalf("expected frozen target ref=%q, got=%q", targetRef, doneTicket.TargetBranch)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleDoneReported); got != 1 {
		t.Fatalf("expected exactly 1 done_reported event, got=%d", got)
	}

	syncRes, err := p.SyncMergeRef(ctx, targetRef, "", headSHA)
	if err != nil {
		t.Fatalf("SyncMergeRef failed: %v", err)
	}
	if len(syncRes.MergedTicketIDs) != 1 || syncRes.MergedTicketIDs[0] != tk.ID {
		t.Fatalf("expected sync-ref to merge ticket t%d, got=%v", tk.ID, syncRes.MergedTicketIDs)
	}

	merged := loadTicketForLifecycleTest(t, p, tk.ID)
	if merged.WorkflowStatus != contracts.TicketDone {
		t.Fatalf("expected workflow remain done after merge observed, got=%s", merged.WorkflowStatus)
	}
	if got := contracts.CanonicalIntegrationStatus(merged.IntegrationStatus); got != contracts.IntegrationMerged {
		t.Fatalf("expected integration merged after sync-ref, got=%s", got)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleMergeObserved); got != 1 {
		t.Fatalf("expected exactly 1 merge_observed event, got=%d", got)
	}

	if err := p.ArchiveTicket(ctx, tk.ID); err != nil {
		t.Fatalf("ArchiveTicket failed: %v", err)
	}
	archived := loadTicketForLifecycleTest(t, p, tk.ID)
	if archived.WorkflowStatus != contracts.TicketArchived {
		t.Fatalf("expected archived after archive, got=%s", archived.WorkflowStatus)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleArchived); got != 1 {
		t.Fatalf("expected exactly 1 archived event, got=%d", got)
	}
	latestWorker, err := p.LatestWorker(ctx, tk.ID)
	if err != nil {
		t.Fatalf("LatestWorker failed: %v", err)
	}
	if latestWorker == nil || latestWorker.WorktreeGCRequestedAt == nil {
		t.Fatalf("expected archived worker worktree GC requested")
	}

	assertLifecycleConsistentForLifecycleTest(t, p, tk.ID, contracts.TicketArchived, contracts.IntegrationMerged)
}

func TestIntegration_Lifecycle_CapacityExhaustionKeepsQueuedWithoutIncident(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	if _, err := p.SetMaxRunningWorkers(ctx, 1); err != nil {
		t.Fatalf("SetMaxRunningWorkers failed: %v", err)
	}

	tk1, err := p.CreateTicketWithDescription(ctx, "capacity-running", "first ticket occupies only slot")
	if err != nil {
		t.Fatalf("CreateTicket(t1) failed: %v", err)
	}
	w1, err := p.StartTicket(ctx, tk1.ID)
	if err != nil {
		t.Fatalf("StartTicket(t1) failed: %v", err)
	}
	_ = createWorkerDeliverRunForLifecycleTest(t, p, tk1.ID, w1.ID, "capacity-running")
	markTicketWorkerActiveForLifecycleTest(t, p, tk1.ID, w1.ID)

	tk2, err := p.CreateTicketWithDescription(ctx, "capacity-queued", "second ticket should stay queued")
	if err != nil {
		t.Fatalf("CreateTicket(t2) failed: %v", err)
	}
	if _, err := p.StartTicket(ctx, tk2.ID); err != nil {
		t.Fatalf("StartTicket(t2) failed: %v", err)
	}

	res, err := p.ManagerTick(ctx, ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if res.Running != 1 {
		t.Fatalf("expected running=1, got=%d", res.Running)
	}
	if res.Capacity != 0 {
		t.Fatalf("expected capacity=0, got=%d", res.Capacity)
	}
	if len(res.ActivatedTickets) != 0 || len(res.StartedTickets) != 0 {
		t.Fatalf("expected no queued activation under exhausted capacity, started=%v activated=%v", res.StartedTickets, res.ActivatedTickets)
	}
	queued := loadTicketForLifecycleTest(t, p, tk2.ID)
	if queued.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected t2 remain queued, got=%s", queued.WorkflowStatus)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk2.ID, contracts.TicketLifecycleExecutionEscalated); got != 0 {
		t.Fatalf("expected no execution_escalated under capacity exhaustion, got=%d", got)
	}
	assertLifecycleConsistentForLifecycleTest(t, p, tk2.ID, contracts.TicketQueued, contracts.IntegrationNone)
}

func TestIntegration_Lifecycle_RepeatedStartIsIdempotentInQueuedState(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "queued-start-idempotent", "repeated start should not duplicate queue projection")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	firstWorker, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("first StartTicket failed: %v", err)
	}
	secondWorker, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("second StartTicket failed: %v", err)
	}
	if firstWorker == nil || secondWorker == nil {
		t.Fatalf("expected workers returned from repeated start")
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleStartRequested); got != 1 {
		t.Fatalf("expected repeated queued start to keep exactly 1 start_requested event, got=%d", got)
	}
	queued := loadTicketForLifecycleTest(t, p, tk.ID)
	if queued.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket remain queued after repeated start, got=%s", queued.WorkflowStatus)
	}
	assertLifecycleConsistentForLifecycleTest(t, p, tk.ID, contracts.TicketQueued, contracts.IntegrationNone)
}

func TestIntegration_Lifecycle_ActiveWorkerHostLossRequeuesTicket(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "host-loss-requeue", "host loss should requeue active ticket")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	w, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	run := createWorkerDeliverRunForLifecycleTest(t, p, tk.ID, w.ID, "host-loss")
	markTicketWorkerActiveForLifecycleTest(t, p, tk.ID, w.ID)
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"log_path":   "",
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("clear worker log_path failed: %v", err)
	}

	res, err := p.ManagerTick(ctx, ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if res.ZombieRecovered != 1 {
		t.Fatalf("expected zombie_recovered=1, got=%d errors=%v", res.ZombieRecovered, res.Errors)
	}
	if res.ZombieBlocked != 0 {
		t.Fatalf("expected zombie_blocked=0, got=%d", res.ZombieBlocked)
	}

	requeued := loadTicketForLifecycleTest(t, p, tk.ID)
	if requeued.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected queued after host loss recovery, got=%s", requeued.WorkflowStatus)
	}
	worker := loadWorkerForLifecycleTest(t, p, w.ID)
	if worker.RetryCount != 1 {
		t.Fatalf("expected retry_count=1 after recovery, got=%d", worker.RetryCount)
	}
	lost := latestLifecycleEventForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleExecutionLost)
	if got := uintFromAny(lost.PayloadJSON["task_run_id"]); got != run.ID {
		t.Fatalf("expected execution_lost task_run_id=%d, got=%d", run.ID, got)
	}
	if strings.TrimSpace(fmt.Sprint(lost.PayloadJSON["observation_kind"])) != "host_loss" {
		t.Fatalf("expected observation_kind=host_loss, got=%v", lost.PayloadJSON["observation_kind"])
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleRequeued); got != 1 {
		t.Fatalf("expected exactly 1 requeued event, got=%d", got)
	}

	check, err := p.CheckTicketLifecycleConsistency(ctx, tk.ID)
	if err != nil {
		t.Fatalf("CheckTicketLifecycleConsistency failed: %v", err)
	}
	if check.Mismatch {
		t.Fatalf("expected consistent lifecycle after requeue, mismatches=%v", check.Mismatches)
	}
	if check.Rebuilt.Explanation == nil || check.Rebuilt.Explanation.EventType != contracts.TicketLifecycleRequeued {
		t.Fatalf("expected rebuilt explanation from requeued event, got=%+v", check.Rebuilt.Explanation)
	}
}

func TestIntegration_Lifecycle_ActiveWorkerHostLossEscalatesBlocked(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "host-loss-escalate", "host loss with exhausted retries should block")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	w, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	_ = createWorkerDeliverRunForLifecycleTest(t, p, tk.ID, w.ID, "host-loss-escalate")
	markTicketWorkerActiveForLifecycleTest(t, p, tk.ID, w.ID)
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"retry_count": 99,
		"log_path":    "",
		"updated_at":  time.Now(),
	}).Error; err != nil {
		t.Fatalf("set exhausted retry state failed: %v", err)
	}

	res, err := p.ManagerTick(ctx, ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if res.ZombieBlocked != 1 {
		t.Fatalf("expected zombie_blocked=1, got=%d errors=%v", res.ZombieBlocked, res.Errors)
	}
	if res.ZombieRecovered != 0 {
		t.Fatalf("expected zombie_recovered=0 when retries exhausted, got=%d", res.ZombieRecovered)
	}

	blocked := loadTicketForLifecycleTest(t, p, tk.ID)
	if blocked.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected blocked after exhausted retries, got=%s", blocked.WorkflowStatus)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleExecutionEscalated); got != 1 {
		t.Fatalf("expected exactly 1 execution_escalated event, got=%d", got)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleRequeued); got != 0 {
		t.Fatalf("expected no requeued event after escalation, got=%d", got)
	}

	check, err := p.CheckTicketLifecycleConsistency(ctx, tk.ID)
	if err != nil {
		t.Fatalf("CheckTicketLifecycleConsistency failed: %v", err)
	}
	if check.Mismatch {
		t.Fatalf("expected consistent lifecycle after escalation, mismatches=%v", check.Mismatches)
	}
	if check.Rebuilt.Explanation == nil {
		t.Fatalf("expected rebuilt explanation for escalated blocked ticket")
	}
	if check.Rebuilt.Explanation.EventType != contracts.TicketLifecycleExecutionEscalated {
		t.Fatalf("expected execution_escalated explanation, got=%s", check.Rebuilt.Explanation.EventType)
	}
	if check.Rebuilt.Explanation.BlockedReason != "system_incident" {
		t.Fatalf("expected blocked_reason=system_incident, got=%q", check.Rebuilt.Explanation.BlockedReason)
	}
	if check.Rebuilt.Explanation.FailureCode != "runtime_anchor_missing" {
		t.Fatalf("expected failure_code=runtime_anchor_missing, got=%q", check.Rebuilt.Explanation.FailureCode)
	}
}

func TestIntegration_Lifecycle_ArchiveRejectedWhileNeedsMerge(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()
	headSHA := gitHeadSHAForLifecycleTest(t, p.RepoRoot())
	p.pm.SetTaskRunner(integrationTaskRunnerFunc(func(ctx context.Context, req sdkrunner.Request, onEvent sdkrunner.EventHandler) (sdkrunner.Result, error) {
		worktreePath := strings.TrimSpace(req.WorkDir)
		workerID := requiredEnvUint(t, req.Env, "DALEK_WORKER_ID")
		ticketID := requiredEnvUint(t, req.Env, "DALEK_TICKET_ID")
		writeWorkerLoopStateForIntegration(t, ticketID, workerID, worktreePath, "done", "done but waiting merge", nil, true, headSHA, "clean")
		applyWorkerReportForIntegration(t, p, req, "done", "done but waiting merge", nil, false, false, headSHA)
		return sdkrunner.Result{Provider: "test", OutputMode: sdkrunner.OutputModeJSONL, Text: "done"}, nil
	}))

	tk, err := p.CreateTicketWithDescription(ctx, "archive-needs-merge", "done ticket should not archive before merge")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	w, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	runResult, err := p.RunTicketWorker(ctx, tk.ID, pmsvc.WorkerRunOptions{
		EntryPrompt: "继续执行任务",
		AutoStart:   boolPtr(false),
	})
	if err != nil {
		t.Fatalf("RunTicketWorker(done) failed: %v", err)
	}
	if runResult.WorkerID != w.ID {
		t.Fatalf("expected done run reuse started worker=%d, got=%d", w.ID, runResult.WorkerID)
	}
	if runResult.LastNextAction != string(contracts.NextDone) {
		t.Fatalf("expected done next_action, got=%q", runResult.LastNextAction)
	}

	err = p.ArchiveTicket(ctx, tk.ID)
	if err == nil {
		t.Fatal("ArchiveTicket should reject done+needs_merge")
	}
	if !strings.Contains(err.Error(), "当前状态不允许归档") {
		t.Fatalf("expected descriptive archive error, got=%v", err)
	}

	done := loadTicketForLifecycleTest(t, p, tk.ID)
	if done.WorkflowStatus != contracts.TicketDone {
		t.Fatalf("expected workflow stay done, got=%s", done.WorkflowStatus)
	}
	if got := contracts.CanonicalIntegrationStatus(done.IntegrationStatus); got != contracts.IntegrationNeedsMerge {
		t.Fatalf("expected integration stay needs_merge, got=%s", got)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleArchived); got != 0 {
		t.Fatalf("expected no archived event while needs_merge, got=%d", got)
	}
	assertLifecycleConsistentForLifecycleTest(t, p, tk.ID, contracts.TicketDone, contracts.IntegrationNeedsMerge)
}

func TestIntegration_Lifecycle_RejectsWorkerReportBoundToWrongRun(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	tk1, err := p.CreateTicketWithDescription(ctx, "wrong-run-1", "worker report should reject foreign run binding")
	if err != nil {
		t.Fatalf("CreateTicket(t1) failed: %v", err)
	}
	w1, err := p.StartTicket(ctx, tk1.ID)
	if err != nil {
		t.Fatalf("StartTicket(t1) failed: %v", err)
	}
	_ = createWorkerDeliverRunForLifecycleTest(t, p, tk1.ID, w1.ID, "wrong-run-1")

	tk2, err := p.CreateTicketWithDescription(ctx, "wrong-run-2", "second ticket provides foreign run id")
	if err != nil {
		t.Fatalf("CreateTicket(t2) failed: %v", err)
	}
	w2, err := p.StartTicket(ctx, tk2.ID)
	if err != nil {
		t.Fatalf("StartTicket(t2) failed: %v", err)
	}
	foreignRun := createWorkerDeliverRunForLifecycleTest(t, p, tk2.ID, w2.ID, "wrong-run-2")

	err = p.ApplyWorkerReport(ctx, contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: p.Key(),
		TicketID:   tk1.ID,
		WorkerID:   w1.ID,
		TaskRunID:  foreignRun.ID,
		Summary:    "should fail",
		NeedsUser:  true,
		Blockers:   []string{"foreign run"},
		NextAction: string(contracts.NextWaitUser),
	}, "integration.invalid_binding")
	if !errors.Is(err, workersvc.ErrInvalidWorkerReportTaskRun) {
		t.Fatalf("expected ErrInvalidWorkerReportTaskRun, got=%v", err)
	}

	ticket := loadTicketForLifecycleTest(t, p, tk1.ID)
	if ticket.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket remain queued after invalid report, got=%s", ticket.WorkflowStatus)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk1.ID, contracts.TicketLifecycleWaitUserReported); got != 0 {
		t.Fatalf("expected no wait_user_reported event after invalid report, got=%d", got)
	}
	assertLifecycleConsistentForLifecycleTest(t, p, tk1.ID, contracts.TicketQueued, contracts.IntegrationNone)
}

func TestIntegration_Lifecycle_ProjectApplyWorkerReportDoesNotAdvanceWorkflow(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "report-runtime-only", "project ApplyWorkerReport should only write runtime state")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	w, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	run := createWorkerDeliverRunForLifecycleTest(t, p, tk.ID, w.ID, "report-runtime-only")

	err = p.ApplyWorkerReport(ctx, contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: p.Key(),
		TicketID:   tk.ID,
		WorkerID:   w.ID,
		TaskRunID:  run.ID,
		Summary:    "need input before closure",
		NeedsUser:  true,
		Blockers:   []string{"waiting for PM"},
		NextAction: string(contracts.NextWaitUser),
	}, "integration.runtime_only")
	if err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	ticket := loadTicketForLifecycleTest(t, p, tk.ID)
	if ticket.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket remain queued after direct report ingestion, got=%s", ticket.WorkflowStatus)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, tk.ID, contracts.TicketLifecycleWaitUserReported); got != 0 {
		t.Fatalf("expected no wait_user_reported event from direct report ingestion, got=%d", got)
	}
	assertLifecycleConsistentForLifecycleTest(t, p, tk.ID, contracts.TicketQueued, contracts.IntegrationNone)
}

func TestIntegration_Lifecycle_DaemonStartRecoveryAndTickConvergesQueuedAndOrphanedTickets(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	// autopilot removed (planner loop cleaned up)

	queuedTicket, err := p.CreateTicketWithDescription(ctx, "daemon-queued", "queued ticket should activate on first tick")
	if err != nil {
		t.Fatalf("CreateTicket(queued) failed: %v", err)
	}
	if _, err := p.StartTicket(ctx, queuedTicket.ID); err != nil {
		t.Fatalf("StartTicket(queued) failed: %v", err)
	}

	driftTicket, err := p.CreateTicketWithDescription(ctx, "daemon-orphaned-drift", "orphaned active run should be failed before first tick")
	if err != nil {
		t.Fatalf("CreateTicket(drift) failed: %v", err)
	}
	_, driftRun := createActiveDeliverRunForRecovery(t, p, driftTicket.ID, contracts.TicketQueued, contracts.WorkerStopped)

	host := &acceptingLifecycleManagerHost{t: t, project: p}
	manager := newDaemonManagerComponent(h, nil)
	manager.interval = time.Hour
	manager.setExecutionHost(host)

	runCtx, cancel := context.WithCancel(ctx)
	t.Cleanup(func() {
		cancel()
		_ = manager.Stop(context.Background())
	})
	if err := manager.Start(runCtx); err != nil {
		t.Fatalf("manager Start failed: %v", err)
	}

	waitForManagerInitialTick(t, p, 3*time.Second)

	waitForTicketWorkflowForLifecycleTest(t, p, queuedTicket.ID, contracts.TicketActive, contracts.IntegrationNone, 3*time.Second)
	waitForTicketWorkflowForLifecycleTest(t, p, driftTicket.ID, contracts.TicketActive, contracts.IntegrationNone, 3*time.Second)

	submitted := host.SubmittedTicketIDs()
	hasQueued := false
	hasDrift := false
	for _, id := range submitted {
		if id == queuedTicket.ID {
			hasQueued = true
		}
		if id == driftTicket.ID {
			hasDrift = true
		}
	}
	if !hasQueued || !hasDrift {
		t.Fatalf("expected host submissions include queued=%d and drift=%d, got=%v", queuedTicket.ID, driftTicket.ID, submitted)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, queuedTicket.ID, contracts.TicketLifecycleActivated); got != 1 {
		t.Fatalf("expected queued ticket activated exactly once, got=%d", got)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, driftTicket.ID, contracts.TicketLifecycleActivated); got != 1 {
		t.Fatalf("expected drift ticket reactivated exactly once, got=%d", got)
	}
	if got := countLifecycleEventsForLifecycleTest(t, p, driftTicket.ID, contracts.TicketLifecycleRepaired); got != 0 {
		t.Fatalf("expected no repaired lifecycle for orphaned drift run, got=%d", got)
	}

	driftRunAfter := loadTaskRunForLifecycleTest(t, p, driftRun.ID)
	if driftRunAfter.OrchestrationState != contracts.TaskFailed {
		t.Fatalf("expected original drift run failed during startup recovery, got=%s", driftRunAfter.OrchestrationState)
	}
	if strings.TrimSpace(driftRunAfter.ErrorCode) != "orphaned_by_crash" {
		t.Fatalf("expected original drift run error_code=orphaned_by_crash, got=%q", driftRunAfter.ErrorCode)
	}

	runStatus := waitForLatestWorkerRunForLifecycleTest(t, p, queuedTicket.ID, 3*time.Second)
	if got := loadWorkerForLifecycleTest(t, p, runStatus.WorkerID); got.Status != contracts.WorkerRunning {
		t.Fatalf("expected queued ticket worker running after daemon tick, got=%s", got.Status)
	}

	driftStatus := waitForLatestWorkerRunForLifecycleTest(t, p, driftTicket.ID, 3*time.Second)
	if driftStatus.RunID == driftRun.ID {
		t.Fatalf("expected drift ticket to use a new worker run instead of stale run=%d", driftRun.ID)
	}

	assertLifecycleConsistentForLifecycleTest(t, p, queuedTicket.ID, contracts.TicketActive, contracts.IntegrationNone)
	assertLifecycleConsistentForLifecycleTest(t, p, driftTicket.ID, contracts.TicketActive, contracts.IntegrationNone)
}

func TestIntegration_Lifecycle_DaemonRecoveryCancelsLegacyDispatchRuns(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "legacy-dispatch-cleanup", "legacy dispatch runs should be canceled during recovery")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	worker, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	legacyDispatchRun, err := p.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "dispatch_ticket",
		ProjectKey:         p.Key(),
		TicketID:           tk.ID,
		WorkerID:           worker.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          fmt.Sprintf("legacy-dispatch-%d", now.UnixNano()),
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("create legacy dispatch_ticket run failed: %v", err)
	}
	legacyAgentRun, err := p.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "pm_dispatch_agent",
		ProjectKey:         p.Key(),
		TicketID:           tk.ID,
		WorkerID:           worker.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          fmt.Sprintf("legacy-agent-%d", now.UnixNano()),
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("create legacy pm_dispatch_agent run failed: %v", err)
	}

	manager := newDaemonManagerComponent(h, nil)
	manager.runRecovery(ctx)

	dispatchRunAfter := loadTaskRunForLifecycleTest(t, p, legacyDispatchRun.ID)
	if dispatchRunAfter.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected legacy dispatch_ticket run canceled, got=%s", dispatchRunAfter.OrchestrationState)
	}
	if strings.TrimSpace(dispatchRunAfter.ErrorCode) != "legacy_dispatch_removed" {
		t.Fatalf("expected legacy dispatch run error_code=legacy_dispatch_removed, got=%q", dispatchRunAfter.ErrorCode)
	}
	agentRunAfter := loadTaskRunForLifecycleTest(t, p, legacyAgentRun.ID)
	if agentRunAfter.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected legacy pm_dispatch_agent run canceled, got=%s", agentRunAfter.OrchestrationState)
	}
	if strings.TrimSpace(agentRunAfter.ErrorCode) != "legacy_dispatch_removed" {
		t.Fatalf("expected legacy agent run error_code=legacy_dispatch_removed, got=%q", agentRunAfter.ErrorCode)
	}

	dispatchEvents, err := p.ListTaskEvents(ctx, legacyDispatchRun.ID, 20)
	if err != nil {
		t.Fatalf("ListTaskEvents(dispatch) failed: %v", err)
	}
	foundDispatchCanceled := false
	for _, ev := range dispatchEvents {
		if strings.TrimSpace(ev.EventType) == "task_canceled" && strings.Contains(ev.Note, "legacy dispatch runs canceled") {
			foundDispatchCanceled = true
			break
		}
	}
	if !foundDispatchCanceled {
		t.Fatalf("expected task_canceled event for legacy dispatch run, got=%v", dispatchEvents)
	}

	agentEvents, err := p.ListTaskEvents(ctx, legacyAgentRun.ID, 20)
	if err != nil {
		t.Fatalf("ListTaskEvents(agent) failed: %v", err)
	}
	foundAgentCanceled := false
	for _, ev := range agentEvents {
		if strings.TrimSpace(ev.EventType) == "task_canceled" && strings.Contains(ev.Note, "legacy dispatch runs canceled") {
			foundAgentCanceled = true
			break
		}
	}
	if !foundAgentCanceled {
		t.Fatalf("expected task_canceled event for legacy agent run, got=%v", agentEvents)
	}
}
