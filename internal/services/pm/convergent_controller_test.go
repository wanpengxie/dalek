package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/subagent"

	"gorm.io/gorm"
)

// Ensure gorm is used (for DB operations in test helpers).
var _ = (*gorm.DB)(nil)

// ---------------------------------------------------------------------------
// Test helpers for convergent mode
// ---------------------------------------------------------------------------

// createConvergentRun creates a convergent focus run with its first round.
func createConvergentRun(t *testing.T, db *gorm.DB, ticketIDs []uint, maxPMRuns int) (contracts.FocusRun, contracts.ConvergentRound) {
	t.Helper()
	if maxPMRuns <= 0 {
		maxPMRuns = 5
	}
	scopeJSON, _ := json.Marshal(ticketIDs)
	now := time.Now()
	run := contracts.FocusRun{
		ProjectKey:      "demo",
		Mode:            contracts.FocusModeConvergent,
		RequestID:       fmt.Sprintf("test-convergent-%d", now.UnixNano()),
		DesiredState:    contracts.FocusDesiredRunning,
		Status:          contracts.FocusQueued,
		ScopeTicketIDs:  string(scopeJSON),
		AgentBudget:     5,
		AgentBudgetMax:  5,
		MaxPMRuns:       maxPMRuns,
		PMRunCount:      0,
		ConvergentPhase: "",
		StartedAt:       &now,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("create convergent run failed: %v", err)
	}

	round := contracts.ConvergentRound{
		FocusRunID:     run.ID,
		RoundNumber:    1,
		BatchTicketIDs: string(scopeJSON),
		BatchStatus:    "pending",
		PMRunStatus:    "pending",
		StartedAt:      &now,
	}
	if err := db.Create(&round).Error; err != nil {
		t.Fatalf("create round 1 failed: %v", err)
	}

	// Create pending items for the tickets.
	for i, tid := range ticketIDs {
		item := contracts.FocusRunItem{
			FocusRunID: run.ID,
			Seq:        i + 1,
			TicketID:   tid,
			Status:     contracts.FocusItemPending,
		}
		if err := db.Create(&item).Error; err != nil {
			t.Fatalf("create item failed: %v", err)
		}
	}
	return run, round
}

// setConvergentPhase updates the convergent phase and run status.
func setConvergentPhase(t *testing.T, db *gorm.DB, runID uint, phase, status string) {
	t.Helper()
	if err := db.Model(&contracts.FocusRun{}).Where("id = ?", runID).Updates(map[string]any{
		"convergent_phase": phase,
		"status":           status,
	}).Error; err != nil {
		t.Fatalf("set convergent phase failed: %v", err)
	}
}

// completeAllItems marks all items for a run as completed.
func completeAllItems(t *testing.T, db *gorm.DB, runID uint) {
	t.Helper()
	now := time.Now()
	if err := db.Model(&contracts.FocusRunItem{}).Where("focus_run_id = ?", runID).Updates(map[string]any{
		"status":      contracts.FocusItemCompleted,
		"finished_at": &now,
	}).Error; err != nil {
		t.Fatalf("complete items failed: %v", err)
	}
}

// completeRoundItems marks items for specific ticket IDs as completed.
func completeRoundItems(t *testing.T, db *gorm.DB, runID uint, ticketIDs []uint) {
	t.Helper()
	now := time.Now()
	if err := db.Model(&contracts.FocusRunItem{}).
		Where("focus_run_id = ? AND ticket_id IN ?", runID, ticketIDs).
		Updates(map[string]any{
			"status":      contracts.FocusItemCompleted,
			"finished_at": &now,
		}).Error; err != nil {
		t.Fatalf("complete round items failed: %v", err)
	}
}

// failItem marks a specific item as failed.
func failItem(t *testing.T, db *gorm.DB, runID uint, ticketID uint) {
	t.Helper()
	now := time.Now()
	if err := db.Model(&contracts.FocusRunItem{}).
		Where("focus_run_id = ? AND ticket_id = ?", runID, ticketID).
		Updates(map[string]any{
			"status":      contracts.FocusItemFailed,
			"finished_at": &now,
		}).Error; err != nil {
		t.Fatalf("fail item failed: %v", err)
	}
}

// blockItem marks a specific item as blocked.
func blockItem(t *testing.T, db *gorm.DB, runID uint, ticketID uint) {
	t.Helper()
	now := time.Now()
	if err := db.Model(&contracts.FocusRunItem{}).
		Where("focus_run_id = ? AND ticket_id = ?", runID, ticketID).
		Updates(map[string]any{
			"status":         contracts.FocusItemBlocked,
			"blocked_reason": "test_blocked",
			"finished_at":    &now,
		}).Error; err != nil {
		t.Fatalf("block item failed: %v", err)
	}
}

// setRoundBatchStatus updates the round's batch status.
func setRoundBatchStatus(t *testing.T, db *gorm.DB, roundID uint, status string) {
	t.Helper()
	if err := db.Model(&contracts.ConvergentRound{}).Where("id = ?", roundID).Updates(map[string]any{
		"batch_status": status,
	}).Error; err != nil {
		t.Fatalf("set batch status failed: %v", err)
	}
}

// setPMRunTaskRunID sets the round's PM run task run ID.
func setPMRunTaskRunID(t *testing.T, db *gorm.DB, roundID uint, taskRunID uint) {
	t.Helper()
	if err := db.Model(&contracts.ConvergentRound{}).Where("id = ?", roundID).Updates(map[string]any{
		"pm_run_task_run_id": taskRunID,
		"pm_run_status":      "running",
	}).Error; err != nil {
		t.Fatalf("set pm run task run id failed: %v", err)
	}
}

// loadFocusRun reloads a FocusRun from DB.
func loadFocusRun(t *testing.T, db *gorm.DB, id uint) contracts.FocusRun {
	t.Helper()
	var run contracts.FocusRun
	if err := db.First(&run, id).Error; err != nil {
		t.Fatalf("load focus run failed: %v", err)
	}
	return run
}

// loadLatestRound loads the latest round for a focus run.
func loadLatestRound(t *testing.T, db *gorm.DB, focusRunID uint) contracts.ConvergentRound {
	t.Helper()
	var round contracts.ConvergentRound
	if err := db.Where("focus_run_id = ?", focusRunID).Order("round_number desc").First(&round).Error; err != nil {
		t.Fatalf("load latest round failed: %v", err)
	}
	return round
}

// loadFocusEvents loads all events for a run.
func loadFocusEvents(t *testing.T, db *gorm.DB, focusRunID uint) []contracts.FocusEvent {
	t.Helper()
	var events []contracts.FocusEvent
	if err := db.Where("focus_run_id = ?", focusRunID).Order("id asc").Find(&events).Error; err != nil {
		t.Fatalf("load events failed: %v", err)
	}
	return events
}

// hasEvent checks if any event with the given kind exists.
func hasEvent(events []contracts.FocusEvent, kind string) bool {
	for _, e := range events {
		if strings.TrimSpace(e.Kind) == kind {
			return true
		}
	}
	return false
}

// createTaskRunForTest creates a task run with a given orchestration state.
func createTaskRunForTest(t *testing.T, db *gorm.DB, state contracts.TaskOrchestrationState) contracts.TaskRun {
	t.Helper()
	now := time.Now()
	run := contracts.TaskRun{
		OwnerType:          "pm",
		TaskType:           "pm_run",
		ProjectKey:         "demo",
		SubjectType:        "focus",
		SubjectID:          "1",
		RequestID:          fmt.Sprintf("test-pm-run-%d", now.UnixNano()),
		OrchestrationState: state,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("create task run failed: %v", err)
	}
	return run
}

// writeTestResultJSON writes a PM run result.json to a directory.
func writeTestResultJSON(t *testing.T, dir string, converged bool, fixIDs []uint) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir review dir failed: %v", err)
	}
	verdict := "converged"
	if !converged {
		verdict = "needs_fix"
	}
	data := pmRunResultFile{
		Verdict:              verdict,
		FixTicketIDs:         fixIDs,
		EffectiveIssuesCount: len(fixIDs),
		FilteredIssuesCount:  0,
		Summary:              "test verdict",
	}
	raw, _ := json.MarshalIndent(data, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "result.json"), raw, 0o644); err != nil {
		t.Fatalf("write result.json failed: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestAdvanceFocusController_DispatchesConvergentMode(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "convergent-dispatch")
	run, _ := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	// AdvanceFocusController should dispatch to ConvergentTick (which starts round 1).
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.ConvergentPhase != "batch" {
		t.Errorf("expected phase=batch after first tick, got=%q", reloaded.ConvergentPhase)
	}
	if reloaded.Status != contracts.FocusRunning {
		t.Errorf("expected status=running, got=%q", reloaded.Status)
	}

	events := loadFocusEvents(t, p.DB, run.ID)
	if !hasEvent(events, contracts.FocusEventConvergentRoundStarted) {
		t.Error("expected convergent.round_started event")
	}
}

func TestAdvanceFocusController_BatchModeUnaffected(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "batch-unaffected")
	_, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}

	// Should go through batch path without error.
	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController failed: %v", err)
	}
}

func TestConvergentStartNewRound_Round1(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "conv-r1-t1")
	tk2 := createTicket(t, p.DB, "conv-r1-t2")
	run, round := createConvergentRun(t, p.DB, []uint{tk1.ID, tk2.ID}, 5)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.ConvergentPhase != "batch" {
		t.Errorf("expected phase=batch, got=%q", reloaded.ConvergentPhase)
	}

	reloadedRound := loadLatestRound(t, p.DB, run.ID)
	if reloadedRound.ID != round.ID {
		t.Error("expected same round 1, not a new round")
	}
	if reloadedRound.BatchStatus != "running" {
		t.Errorf("expected batch_status=running, got=%q", reloadedRound.BatchStatus)
	}
}

func TestConvergentTickBatch_AllCompleted_TransitionToPMRun(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-batch-complete")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	// Set up: phase=batch, items completed.
	setConvergentPhase(t, p.DB, run.ID, "batch", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "running")
	completeAllItems(t, p.DB, run.ID)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick batch failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.ConvergentPhase != "pm_run" {
		t.Errorf("expected phase=pm_run after batch done, got=%q", reloaded.ConvergentPhase)
	}

	reloadedRound := loadLatestRound(t, p.DB, run.ID)
	if reloadedRound.BatchStatus != "completed" {
		t.Errorf("expected batch_status=completed, got=%q", reloadedRound.BatchStatus)
	}

	events := loadFocusEvents(t, p.DB, run.ID)
	if !hasEvent(events, contracts.FocusEventConvergentBatchDone) {
		t.Error("expected convergent.batch_done event")
	}
}

func TestConvergentTickBatch_ItemFailed_RunFailed(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-batch-fail")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "batch", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "running")
	failItem(t, p.DB, run.ID, tk.ID)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick batch-fail failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusFailed {
		t.Errorf("expected status=failed, got=%q", reloaded.Status)
	}
	if reloaded.FinishedAt == nil {
		t.Error("expected finished_at to be set")
	}
}

func TestConvergentTickBatch_ItemBlocked_RunBlocked(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-batch-blocked")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "batch", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "running")
	blockItem(t, p.DB, run.ID, tk.ID)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick batch-blocked failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusBlocked {
		t.Errorf("expected status=blocked, got=%q", reloaded.Status)
	}
}

func TestConvergentTickBatch_PMRunExhausted(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-exhausted")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 2)

	setConvergentPhase(t, p.DB, run.ID, "batch", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "running")
	completeAllItems(t, p.DB, run.ID)

	// Set pm_run_count to max.
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Update("pm_run_count", 2)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick exhausted failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusExhausted {
		t.Errorf("expected status=exhausted, got=%q", reloaded.Status)
	}

	events := loadFocusEvents(t, p.DB, run.ID)
	if !hasEvent(events, contracts.FocusEventConvergentExhausted) {
		t.Error("expected convergent.exhausted event")
	}
}

func TestConvergentTickPMRun_SubmitSuccess(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-pm-submit")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")

	// Inject mock submitter.
	mock := &mockPMRunSubmitter{
		submitFunc: func(ctx context.Context, in subagent.SubmitInput) (subagent.SubmitResult, error) {
			return subagent.SubmitResult{
				Accepted:  true,
				TaskRunID: 99,
				RequestID: in.RequestID,
			}, nil
		},
	}
	svc.SetPMRunSubmitter(mock)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick pm_run submit failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.PMRunCount != 1 {
		t.Errorf("expected pm_run_count=1, got=%d", reloaded.PMRunCount)
	}

	reloadedRound := loadLatestRound(t, p.DB, run.ID)
	if reloadedRound.PMRunTaskRunID == nil || *reloadedRound.PMRunTaskRunID != 99 {
		t.Errorf("expected pm_run_task_run_id=99, got=%v", reloadedRound.PMRunTaskRunID)
	}
	if reloadedRound.PMRunStatus != "running" {
		t.Errorf("expected pm_run_status=running, got=%q", reloadedRound.PMRunStatus)
	}

	events := loadFocusEvents(t, p.DB, run.ID)
	if !hasEvent(events, contracts.FocusEventConvergentPMRunStarted) {
		t.Error("expected convergent.pm_run_started event")
	}
}

func TestConvergentTickPMRun_SubmitFailed(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-pm-submit-fail")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")

	mock := &mockPMRunSubmitter{
		submitFunc: func(ctx context.Context, in subagent.SubmitInput) (subagent.SubmitResult, error) {
			return subagent.SubmitResult{}, fmt.Errorf("connection refused")
		},
	}
	svc.SetPMRunSubmitter(mock)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick pm_run submit-fail failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusFailed {
		t.Errorf("expected status=failed after submit error, got=%q", reloaded.Status)
	}
}

func TestConvergentTickPMRun_PollSucceeded_Converged(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-pm-converged")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Update("pm_run_count", 1)

	// Create a succeeded task run.
	taskRun := createTaskRunForTest(t, p.DB, contracts.TaskSucceeded)
	setPMRunTaskRunID(t, p.DB, round.ID, taskRun.ID)

	// Write converged result.json.
	reviewDir, _ := ensureReviewDir(p.RepoRoot, run.ID, round.RoundNumber)
	writeTestResultJSON(t, reviewDir, true, nil)

	// Set review_path on round so handler can find it.
	p.DB.Model(&contracts.ConvergentRound{}).Where("id = ?", round.ID).Update("review_path", reviewDir)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick pm_run converged failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusConverged {
		t.Errorf("expected status=converged, got=%q", reloaded.Status)
	}
	if reloaded.FinishedAt == nil {
		t.Error("expected finished_at to be set")
	}

	events := loadFocusEvents(t, p.DB, run.ID)
	if !hasEvent(events, contracts.FocusEventConvergentConverged) {
		t.Error("expected convergent.converged event")
	}
}

func TestConvergentTickPMRun_PollSucceeded_NeedsFix_NewRound(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-pm-needsfix")
	fixTk := createTicket(t, p.DB, "conv-fix-ticket")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Update("pm_run_count", 1)

	taskRun := createTaskRunForTest(t, p.DB, contracts.TaskSucceeded)
	setPMRunTaskRunID(t, p.DB, round.ID, taskRun.ID)

	reviewDir, _ := ensureReviewDir(p.RepoRoot, run.ID, round.RoundNumber)
	writeTestResultJSON(t, reviewDir, false, []uint{fixTk.ID})
	p.DB.Model(&contracts.ConvergentRound{}).Where("id = ?", round.ID).Update("review_path", reviewDir)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick pm_run needs_fix failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.ConvergentPhase != "" {
		t.Errorf("expected phase='' (new round trigger), got=%q", reloaded.ConvergentPhase)
	}
	if reloaded.Status == contracts.FocusConverged {
		t.Error("should not be converged")
	}

	reloadedRound := loadLatestRound(t, p.DB, run.ID)
	if reloadedRound.Verdict != "needs_fix" {
		t.Errorf("expected verdict=needs_fix, got=%q", reloadedRound.Verdict)
	}

	events := loadFocusEvents(t, p.DB, run.ID)
	if !hasEvent(events, contracts.FocusEventConvergentPMRunDone) {
		t.Error("expected convergent.pm_run_done event")
	}
}

func TestConvergentTickPMRun_PollSucceeded_NeedsFix_NoTickets_Blocked(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-pm-nofix")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Update("pm_run_count", 1)

	taskRun := createTaskRunForTest(t, p.DB, contracts.TaskSucceeded)
	setPMRunTaskRunID(t, p.DB, round.ID, taskRun.ID)

	reviewDir, _ := ensureReviewDir(p.RepoRoot, run.ID, round.RoundNumber)
	writeTestResultJSON(t, reviewDir, false, []uint{}) // needs_fix but no fix tickets
	p.DB.Model(&contracts.ConvergentRound{}).Where("id = ?", round.ID).Update("review_path", reviewDir)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick pm_run nofix-blocked failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusBlocked {
		t.Errorf("expected status=blocked, got=%q", reloaded.Status)
	}
}

func TestConvergentTickPMRun_PollFailed_RunFailed(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-pm-taskfailed")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Update("pm_run_count", 1)

	taskRun := createTaskRunForTest(t, p.DB, contracts.TaskFailed)
	setPMRunTaskRunID(t, p.DB, round.ID, taskRun.ID)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick pm_run task-failed failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusFailed {
		t.Errorf("expected status=failed, got=%q", reloaded.Status)
	}
}

func TestConvergentTickPMRun_PollRunning_NoOp(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-pm-running")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Update("pm_run_count", 1)

	taskRun := createTaskRunForTest(t, p.DB, contracts.TaskRunning)
	setPMRunTaskRunID(t, p.DB, round.ID, taskRun.ID)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick pm_run running failed: %v", err)
	}

	// Should still be in pm_run phase, running.
	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.ConvergentPhase != "pm_run" {
		t.Errorf("expected phase=pm_run, got=%q", reloaded.ConvergentPhase)
	}
	if reloaded.IsTerminal() {
		t.Error("should not be terminal while PM run is running")
	}
}

func TestConvergentHandleStop_AtPhaseBoundary(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-stop")
	run, _ := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	// ConvergentPhase="" means phase boundary.
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"desired_state":    contracts.FocusDesiredStopping,
		"convergent_phase": "",
		"status":           contracts.FocusRunning,
	})

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick stop failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusStopped {
		t.Errorf("expected status=stopped, got=%q", reloaded.Status)
	}
}

func TestConvergentHandleStop_PMRunPhase_NotStarted(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-stop-pmrun-notstarted")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Update("desired_state", contracts.FocusDesiredStopping)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick stop-pmrun failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusStopped {
		t.Errorf("expected status=stopped, got=%q", reloaded.Status)
	}
}

func TestConvergentHandleStop_PMRunPhase_StillRunning(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-stop-pmrun-running")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")

	taskRun := createTaskRunForTest(t, p.DB, contracts.TaskRunning)
	setPMRunTaskRunID(t, p.DB, round.ID, taskRun.ID)
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"desired_state": contracts.FocusDesiredStopping,
		"pm_run_count":  1,
	})

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick stop-pmrun-running failed: %v", err)
	}

	// Should NOT stop yet — PM run still running.
	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status == contracts.FocusStopped {
		t.Error("should not stop while PM run is still running")
	}
}

func TestConvergentHandleStop_PMRunPhase_Completed(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-stop-pmrun-done")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")

	taskRun := createTaskRunForTest(t, p.DB, contracts.TaskSucceeded)
	setPMRunTaskRunID(t, p.DB, round.ID, taskRun.ID)
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
		"desired_state": contracts.FocusDesiredStopping,
		"pm_run_count":  1,
	})

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick stop-pmrun-done failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusStopped {
		t.Errorf("expected status=stopped, got=%q", reloaded.Status)
	}
}

func TestConvergentHandleCancel(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-cancel")
	run, _ := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	setConvergentPhase(t, p.DB, run.ID, "batch", contracts.FocusRunning)
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Update("desired_state", contracts.FocusDesiredCanceling)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("ConvergentTick cancel failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusCanceled {
		t.Errorf("expected status=canceled, got=%q", reloaded.Status)
	}
}

func TestConvergentMultiRound_NeedsFix_ThenConverged(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "conv-multi-orig")
	fixTk := createTicket(t, p.DB, "conv-multi-fix")
	run, round := createConvergentRun(t, p.DB, []uint{tk.ID}, 5)

	// --- Round 1: batch done → pm_run → needs_fix ---
	setConvergentPhase(t, p.DB, run.ID, "pm_run", contracts.FocusRunning)
	setRoundBatchStatus(t, p.DB, round.ID, "completed")
	completeAllItems(t, p.DB, run.ID)
	p.DB.Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Update("pm_run_count", 1)

	taskRun := createTaskRunForTest(t, p.DB, contracts.TaskSucceeded)
	setPMRunTaskRunID(t, p.DB, round.ID, taskRun.ID)

	reviewDir, _ := ensureReviewDir(p.RepoRoot, run.ID, round.RoundNumber)
	writeTestResultJSON(t, reviewDir, false, []uint{fixTk.ID})
	p.DB.Model(&contracts.ConvergentRound{}).Where("id = ?", round.ID).Update("review_path", reviewDir)

	view, _ := svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("Round 1 PM run tick failed: %v", err)
	}

	reloaded := loadFocusRun(t, p.DB, run.ID)
	if reloaded.ConvergentPhase != "" {
		t.Fatalf("expected phase='' for new round, got=%q", reloaded.ConvergentPhase)
	}

	// --- Trigger round 2 start ---
	view, _ = svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("Round 2 start tick failed: %v", err)
	}

	reloaded = loadFocusRun(t, p.DB, run.ID)
	if reloaded.ConvergentPhase != "batch" {
		t.Fatalf("expected phase=batch for round 2, got=%q", reloaded.ConvergentPhase)
	}

	round2 := loadLatestRound(t, p.DB, run.ID)
	if round2.RoundNumber != 2 {
		t.Fatalf("expected round 2, got=%d", round2.RoundNumber)
	}

	// Verify round 2 has the fix ticket.
	var round2TicketIDs []uint
	json.Unmarshal([]byte(round2.BatchTicketIDs), &round2TicketIDs)
	if len(round2TicketIDs) != 1 || round2TicketIDs[0] != fixTk.ID {
		t.Errorf("expected round 2 ticket_ids=[%d], got=%v", fixTk.ID, round2TicketIDs)
	}

	// Verify new items were created for round 2.
	var round2Items []contracts.FocusRunItem
	p.DB.Where("focus_run_id = ? AND ticket_id = ?", run.ID, fixTk.ID).Find(&round2Items)
	if len(round2Items) == 0 {
		t.Error("expected items created for round 2")
	}

	// --- Round 2: batch done → pm_run → converged ---
	completeRoundItems(t, p.DB, run.ID, []uint{fixTk.ID})

	view, _ = svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("Round 2 batch done tick failed: %v", err)
	}

	reloaded = loadFocusRun(t, p.DB, run.ID)
	if reloaded.ConvergentPhase != "pm_run" {
		t.Fatalf("expected phase=pm_run for round 2, got=%q", reloaded.ConvergentPhase)
	}

	// Submit PM run for round 2.
	mock := &mockPMRunSubmitter{
		submitFunc: func(ctx context.Context, in subagent.SubmitInput) (subagent.SubmitResult, error) {
			return subagent.SubmitResult{Accepted: true, TaskRunID: 200}, nil
		},
	}
	svc.SetPMRunSubmitter(mock)

	view, _ = svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("Round 2 PM run submit failed: %v", err)
	}

	// Mark task run as succeeded.
	p.DB.Model(&contracts.TaskRun{}).Where("id = ?", 200).Updates(map[string]any{
		"orchestration_state": contracts.TaskSucceeded,
	})
	// Actually need to create it first since it's a mock.
	// The task run with ID 200 was created by mock submitter;
	// we need to actually create the record so poll can find it.
	p.DB.Exec("INSERT OR IGNORE INTO task_runs (id, owner_type, task_type, project_key, subject_type, subject_id, request_id, orchestration_state, created_at, updated_at) VALUES (?, 'pm', 'pm_run', 'demo', 'focus', '1', 'test-pm-run-r2', ?, ?, ?)",
		200, string(contracts.TaskSucceeded), time.Now(), time.Now())

	round2 = loadLatestRound(t, p.DB, run.ID)
	reviewDir2, _ := ensureReviewDir(p.RepoRoot, run.ID, round2.RoundNumber)
	writeTestResultJSON(t, reviewDir2, true, nil)
	p.DB.Model(&contracts.ConvergentRound{}).Where("id = ?", round2.ID).Update("review_path", reviewDir2)

	view, _ = svc.focusViewForDB(ctx, p.DB, run.ID)
	if err := svc.ConvergentTick(ctx, view); err != nil {
		t.Fatalf("Round 2 PM run converged tick failed: %v", err)
	}

	reloaded = loadFocusRun(t, p.DB, run.ID)
	if reloaded.Status != contracts.FocusConverged {
		t.Errorf("expected final status=converged, got=%q", reloaded.Status)
	}
	if reloaded.PMRunCount != 2 {
		t.Errorf("expected pm_run_count=2, got=%d", reloaded.PMRunCount)
	}

	events := loadFocusEvents(t, p.DB, run.ID)
	if !hasEvent(events, contracts.FocusEventConvergentConverged) {
		t.Error("expected convergent.converged event")
	}
}

func TestConvergentParseTicketIDs(t *testing.T) {
	tests := []struct {
		input   string
		wantLen int
		wantErr bool
	}{
		{"", 0, false},
		{"[]", 0, false},
		{"[1,2,3]", 3, false},
		{"invalid", 0, true},
	}
	for _, tt := range tests {
		ids, err := convergentParseTicketIDs(tt.input)
		if (err != nil) != tt.wantErr {
			t.Errorf("convergentParseTicketIDs(%q): err=%v, wantErr=%v", tt.input, err, tt.wantErr)
		}
		if len(ids) != tt.wantLen {
			t.Errorf("convergentParseTicketIDs(%q): len=%d, want=%d", tt.input, len(ids), tt.wantLen)
		}
	}
}

func TestConvergentItemStatusForRun(t *testing.T) {
	tests := []struct {
		runStatus string
		want      string
	}{
		{contracts.FocusStopped, contracts.FocusItemStopped},
		{contracts.FocusCanceled, contracts.FocusItemCanceled},
		{contracts.FocusFailed, contracts.FocusItemFailed},
		{contracts.FocusConverged, ""},
		{contracts.FocusExhausted, ""},
	}
	for _, tt := range tests {
		got := convergentItemStatusForRun(tt.runStatus)
		if got != tt.want {
			t.Errorf("convergentItemStatusForRun(%q) = %q, want %q", tt.runStatus, got, tt.want)
		}
	}
}
