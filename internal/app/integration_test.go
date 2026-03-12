package app

import (
	"context"
	"crypto/sha1"
	"dalek/internal/contracts"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/repo"
	"dalek/internal/testutil"

	"gorm.io/gorm"
)

func newIntegrationHomeProject(t *testing.T) (*Home, *Project) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git 不可用，跳过集成测试: %v", err)
	}

	testutil.UseTmuxShim(t)
	repoRoot := testutil.InitGitRepo(t)
	homeDir := filepath.Join(t.TempDir(), "home")

	h, err := OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	p, err := h.InitProjectFromDir(repoRoot, "demo", repo.Config{
		BranchPrefix: "ts/demo/",
	})
	if err != nil {
		t.Fatalf("InitProjectFromDir failed: %v", err)
	}
	return h, p
}

func newIntegrationProject(t *testing.T) *Project {
	t.Helper()
	_, p := newIntegrationHomeProject(t)
	return p
}

func mustProjectDB(t *testing.T, p *Project) *gorm.DB {
	t.Helper()
	db, err := p.OpenDBForTest()
	if err != nil {
		t.Fatalf("OpenDBForTest failed: %v", err)
	}
	return db
}

func uintFromAny(v any) uint {
	switch n := v.(type) {
	case uint:
		return n
	case uint64:
		return uint(n)
	case uint32:
		return uint(n)
	case int:
		if n > 0 {
			return uint(n)
		}
	case int64:
		if n > 0 {
			return uint(n)
		}
	case float64:
		if n > 0 {
			return uint(n)
		}
	}
	return 0
}

func ensureNotebookShapingSkill(t *testing.T, p *Project) {
	t.Helper()
	skillPath := p.NotebookShapingSkillPath()
	if strings.TrimSpace(skillPath) == "" {
		t.Fatalf("skill path should not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll skill dir failed: %v", err)
	}
	content := `---
version: "1"
defaults:
  scope_estimate: "L"
  acceptance_template: |
    - [ ] CSV 导出支持批量与筛选
    - [ ] 覆盖导出成功与失败路径测试
title_rules:
  max_length: 60
  strip_markdown: true
---

# notebook shaping

优先突出业务目标与验收边界。`
	if err := os.WriteFile(skillPath, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile skill failed: %v", err)
	}
}

func normalizeNoteTextForTest(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(s), " ")
}

func hashNormalizedTextForTest(s string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(s)))
	return hex.EncodeToString(sum[:])
}

func TestIntegration_StartAndStopTicket(t *testing.T) {
	p := newIntegrationProject(t)

	tk, err := p.CreateTicketWithDescription(context.Background(), "integration start-stop", "integration test ticket")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}

	w, err := p.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if w.Status != contracts.WorkerStopped {
		t.Fatalf("expected stopped worker after start, got %s", w.Status)
	}
	if strings.TrimSpace(w.LogPath) == "" {
		t.Fatalf("expected runtime log path for started worker")
	}

	views, err := p.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews failed: %v", err)
	}
	if len(views) != 1 {
		t.Fatalf("expected one ticket view, got %d", len(views))
	}
	if views[0].SessionAlive {
		t.Fatalf("expected no active run immediately after start")
	}

	if err := p.StopTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StopTicket failed: %v", err)
	}
	w2, err := p.WorkerByID(context.Background(), w.ID)
	if err != nil {
		t.Fatalf("WorkerByID failed: %v", err)
	}
	if w2.Status != contracts.WorkerStopped {
		t.Fatalf("expected stopped worker, got %s", w2.Status)
	}
}

func TestIntegration_DaemonRecovery_ReconcileLostWorkerRuntime(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "integration recovery runtime", "daemon recovery should reconcile worker runtime")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	w, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set worker running failed: %v", err)
	}

	views, err := p.ListTicketViews(ctx)
	if err != nil {
		t.Fatalf("ListTicketViews before recovery failed: %v", err)
	}
	found := false
	for _, v := range views {
		if v.Ticket.ID != tk.ID {
			continue
		}
		found = true
		if v.LatestWorker == nil || v.LatestWorker.ID != w.ID {
			t.Fatalf("unexpected latest worker before recovery")
		}
		if v.SessionProbeFailed {
			t.Fatalf("session probe should not fail in this case")
		}
		if v.SessionAlive {
			t.Fatalf("session should be offline before recovery")
		}
	}
	if !found {
		t.Fatalf("ticket view not found before recovery")
	}

	manager := newDaemonManagerComponent(h, nil)
	manager.runRecovery(ctx)

	got, err := p.WorkerByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("WorkerByID failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected worker exists")
	}
	if got.Status != contracts.WorkerStopped {
		t.Fatalf("expected worker stopped after recovery, got=%s", got.Status)
	}
	if got.StoppedAt == nil {
		t.Fatalf("expected worker stopped_at populated after recovery")
	}

	var inbox []contracts.InboxItem
	if err := mustProjectDB(t, p).WithContext(ctx).
		Where("key = ?", fmt.Sprintf("worker_runtime_recover_%d", w.ID)).
		Order("id desc").
		Find(&inbox).Error; err != nil {
		t.Fatalf("query recovery inbox failed: %v", err)
	}
	if len(inbox) == 0 {
		t.Fatalf("expected recovery inbox item created")
	}
	if inbox[0].TicketID != tk.ID || inbox[0].WorkerID != w.ID {
		t.Fatalf("unexpected inbox relation: ticket=%d worker=%d", inbox[0].TicketID, inbox[0].WorkerID)
	}
	if inbox[0].Status != contracts.InboxOpen {
		t.Fatalf("expected inbox open, got=%s", inbox[0].Status)
	}
}

func TestIntegration_DaemonRecovery_ReconcileLostWorkerRuntime_ArchivedTicket(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "integration recovery archived runtime", "daemon recovery should reconcile archived ticket worker runtime")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	w, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set worker running failed: %v", err)
	}

	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketArchived).Error; err != nil {
		t.Fatalf("archive ticket failed: %v", err)
	}

	manager := newDaemonManagerComponent(h, nil)
	manager.runRecovery(ctx)

	got, err := p.WorkerByID(ctx, w.ID)
	if err != nil {
		t.Fatalf("WorkerByID failed: %v", err)
	}
	if got == nil {
		t.Fatalf("expected worker exists")
	}
	if got.Status != contracts.WorkerStopped {
		t.Fatalf("expected worker stopped after recovery, got=%s", got.Status)
	}
	if got.StoppedAt == nil {
		t.Fatalf("expected worker stopped_at populated after recovery")
	}

	var inbox []contracts.InboxItem
	if err := mustProjectDB(t, p).WithContext(ctx).
		Where("key = ?", fmt.Sprintf("worker_runtime_recover_%d", w.ID)).
		Order("id desc").
		Find(&inbox).Error; err != nil {
		t.Fatalf("query recovery inbox failed: %v", err)
	}
	if len(inbox) == 0 {
		t.Fatalf("expected recovery inbox item created")
	}
	if inbox[0].TicketID != tk.ID || inbox[0].WorkerID != w.ID {
		t.Fatalf("unexpected inbox relation: ticket=%d worker=%d", inbox[0].TicketID, inbox[0].WorkerID)
	}
}

func createActiveDeliverRunForRecovery(t *testing.T, p *Project, ticketID uint, workflow contracts.TicketWorkflowStatus, workerStatus contracts.WorkerStatus) (contracts.Worker, contracts.TaskRun) {
	t.Helper()
	ctx := context.Background()
	worker, err := p.StartTicket(ctx, ticketID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Ticket{}).Where("id = ?", ticketID).Updates(map[string]any{
		"workflow_status": workflow,
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket workflow failed: %v", err)
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", worker.ID).Updates(map[string]any{
		"status":     workerStatus,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("set worker status failed: %v", err)
	}
	run, err := p.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
		ProjectKey:         p.Key(),
		TicketID:           ticketID,
		WorkerID:           worker.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", ticketID),
		RequestID:          fmt.Sprintf("recovery-active-%d-%d", ticketID, now.UnixNano()),
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("create active deliver run failed: %v", err)
	}
	return *worker, run
}

func TestIntegration_DaemonRecovery_RepairsQueuedProjectionFromActiveWorkerRun(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "integration recovery active run", "startup recovery should repair queued projection from active deliver run")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	w, run := createActiveDeliverRunForRecovery(t, p, tk.ID, contracts.TicketQueued, contracts.WorkerStopped)

	manager := newDaemonManagerComponent(h, nil)
	manager.runRecovery(ctx)

	var ticket contracts.Ticket
	if err := mustProjectDB(t, p).WithContext(ctx).First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketActive {
		t.Fatalf("expected queued projection repaired to active, got=%s", ticket.WorkflowStatus)
	}

	var worker contracts.Worker
	if err := mustProjectDB(t, p).WithContext(ctx).First(&worker, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if worker.Status != contracts.WorkerRunning {
		t.Fatalf("expected worker marked running from active run recovery, got=%s", worker.Status)
	}

	st, err := p.GetPMState(ctx)
	if err != nil {
		t.Fatalf("GetPMState failed: %v", err)
	}
	if st.LastRecoveryAt == nil || st.LastRecoveryTaskRuns == 0 {
		t.Fatalf("recovery summary not persisted: %+v", st)
	}
	if st.LastRecoveryPlannerOps != 0 {
		t.Fatalf("expected planner recovery count stays zero, got=%d", st.LastRecoveryPlannerOps)
	}

	var repaired contracts.TicketLifecycleEvent
	if err := mustProjectDB(t, p).WithContext(ctx).
		Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleRepaired).
		Order("id desc").
		First(&repaired).Error; err != nil {
		t.Fatalf("expected recovery repaired lifecycle event: %v", err)
	}
	if strings.TrimSpace(repaired.Source) != "pm.recovery.active_run" {
		t.Fatalf("unexpected recovery event source: %q", repaired.Source)
	}
	if got := strings.TrimSpace(fmt.Sprint(repaired.PayloadJSON["target_workflow"])); got != string(contracts.TicketActive) {
		t.Fatalf("expected repaired target_workflow=active, got=%q", got)
	}
	if got := uintFromAny(repaired.PayloadJSON["task_run_id"]); got != run.ID {
		t.Fatalf("expected repaired task_run_id=%d, got=%d", run.ID, got)
	}
}

func TestIntegration_StartStopArchiveFlow(t *testing.T) {
	p := newIntegrationProject(t)

	tk, err := p.CreateTicketWithDescription(context.Background(), "integration flow", "start -> stop -> archive")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}

	if _, err := p.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	views, err := p.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews after start failed: %v", err)
	}
	found := false
	for _, v := range views {
		if v.Ticket.ID != tk.ID {
			continue
		}
		found = true
		if v.DerivedStatus != contracts.TicketQueued {
			t.Fatalf("expected queued after start, got=%s", v.DerivedStatus)
		}
	}
	if !found {
		t.Fatalf("ticket view not found after start")
	}

	if err := p.StopTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StopTicket failed: %v", err)
	}
	views, err = p.ListTicketViews(context.Background())
	if err != nil {
		t.Fatalf("ListTicketViews after stop failed: %v", err)
	}
	found = false
	for _, v := range views {
		if v.Ticket.ID != tk.ID {
			continue
		}
		found = true
		if v.DerivedStatus != contracts.TicketQueued {
			t.Fatalf("expected queued after stop, got=%s", v.DerivedStatus)
		}
	}
	if !found {
		t.Fatalf("ticket view not found after stop")
	}

	if err := p.ArchiveTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("ArchiveTicket failed after stop: %v", err)
	}
	all, err := p.ListTickets(context.Background(), true)
	if err != nil {
		t.Fatalf("ListTickets(includeArchived) failed: %v", err)
	}
	archived := false
	for _, it := range all {
		if it.ID == tk.ID {
			archived = it.WorkflowStatus == contracts.TicketArchived
			break
		}
	}
	if !archived {
		t.Fatalf("expected ticket archived=true after archive")
	}

	worker, err := p.LatestWorker(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("LatestWorker after archive failed: %v", err)
	}
	if worker == nil {
		t.Fatalf("expected worker exists after archive")
	}
	if worker.WorktreeGCRequestedAt == nil {
		t.Fatalf("expected worktree_gc_requested_at set after archive")
	}
}

func createRunningTaskRunForFinishAgentTest(t *testing.T, p *Project, requestID string) uint {
	t.Helper()
	if p == nil || p.task == nil {
		t.Fatalf("project task service is nil")
	}
	ctx := context.Background()
	run, err := p.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerPM,
		TaskType:           "pm_dispatch_agent",
		ProjectKey:         p.Key(),
		RequestID:          strings.TrimSpace(requestID),
		OrchestrationState: contracts.TaskPending,
	})
	if err != nil {
		t.Fatalf("CreateRun failed: %v", err)
	}
	now := time.Now()
	if err := p.task.MarkRunRunning(ctx, run.ID, "test-runner", nil, now, true); err != nil {
		t.Fatalf("MarkRunRunning failed: %v", err)
	}
	return run.ID
}

func TestIntegration_FinishAgentRun_Succeeded(t *testing.T) {
	p := newIntegrationProject(t)

	runID := createRunningTaskRunForFinishAgentTest(t, p, fmt.Sprintf("finish-success-%d", time.Now().UnixNano()))
	if err := p.FinishAgentRun(context.Background(), runID, 0); err != nil {
		t.Fatalf("FinishAgentRun(success) failed: %v", err)
	}

	run, err := p.task.FindRunByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("FindRunByID failed: %v", err)
	}
	if run == nil {
		t.Fatalf("expected task run exists")
	}
	if run.OrchestrationState != contracts.TaskSucceeded {
		t.Fatalf("unexpected orchestration_state: got=%s want=%s", run.OrchestrationState, contracts.TaskSucceeded)
	}
	if run.FinishedAt == nil {
		t.Fatalf("expected finished_at set")
	}

	events, err := p.task.ListEvents(context.Background(), runID, 20)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected at least one event")
	}
	last := events[len(events)-1]
	if strings.TrimSpace(last.EventType) != "task_succeeded" {
		t.Fatalf("unexpected event_type: %s", last.EventType)
	}
	if !strings.Contains(last.Note, "exit_code=0") {
		t.Fatalf("unexpected event note: %s", last.Note)
	}
}

func TestIntegration_FinishAgentRun_Failed(t *testing.T) {
	p := newIntegrationProject(t)

	runID := createRunningTaskRunForFinishAgentTest(t, p, fmt.Sprintf("finish-failed-%d", time.Now().UnixNano()))
	if err := p.FinishAgentRun(context.Background(), runID, 17); err != nil {
		t.Fatalf("FinishAgentRun(failed) returned error: %v", err)
	}

	run, err := p.task.FindRunByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("FindRunByID failed: %v", err)
	}
	if run == nil {
		t.Fatalf("expected task run exists")
	}
	if run.OrchestrationState != contracts.TaskFailed {
		t.Fatalf("unexpected orchestration_state: got=%s want=%s", run.OrchestrationState, contracts.TaskFailed)
	}
	if strings.TrimSpace(run.ErrorCode) != "agent_exit" {
		t.Fatalf("unexpected error_code: %s", run.ErrorCode)
	}
	if !strings.Contains(run.ErrorMessage, "17") {
		t.Fatalf("unexpected error_message: %s", run.ErrorMessage)
	}

	events, err := p.task.ListEvents(context.Background(), runID, 20)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected at least one event")
	}
	last := events[len(events)-1]
	if strings.TrimSpace(last.EventType) != "task_failed" {
		t.Fatalf("unexpected event_type: %s", last.EventType)
	}
	if !strings.Contains(last.Note, "agent_exit code=17") {
		t.Fatalf("unexpected event note: %s", last.Note)
	}
}

func TestIntegration_CancelTaskRun_CancelsRunningAndAppendsEvent(t *testing.T) {
	p := newIntegrationProject(t)

	runID := createRunningTaskRunForFinishAgentTest(t, p, fmt.Sprintf("cancel-running-%d", time.Now().UnixNano()))
	res, err := p.CancelTaskRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("CancelTaskRun failed: %v", err)
	}
	if !res.Found || !res.Canceled {
		t.Fatalf("unexpected cancel result: %+v", res)
	}
	if strings.TrimSpace(res.FromState) != string(contracts.TaskRunning) {
		t.Fatalf("unexpected from_state: %s", res.FromState)
	}
	if strings.TrimSpace(res.ToState) != string(contracts.TaskCanceled) {
		t.Fatalf("unexpected to_state: %s", res.ToState)
	}

	run, err := p.task.FindRunByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("FindRunByID failed: %v", err)
	}
	if run == nil {
		t.Fatalf("expected task run exists")
	}
	if run.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("unexpected orchestration_state: got=%s want=%s", run.OrchestrationState, contracts.TaskCanceled)
	}

	events, err := p.task.ListEvents(context.Background(), runID, 20)
	if err != nil {
		t.Fatalf("ListEvents failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatalf("expected at least one event")
	}
	last := events[len(events)-1]
	if strings.TrimSpace(last.EventType) != "task_canceled" {
		t.Fatalf("unexpected event_type: %s", last.EventType)
	}
}

func TestIntegration_CancelTaskRun_NotFound(t *testing.T) {
	p := newIntegrationProject(t)

	res, err := p.CancelTaskRun(context.Background(), 999999)
	if err != nil {
		t.Fatalf("CancelTaskRun returned error: %v", err)
	}
	if res.Found {
		t.Fatalf("expected not found result, got=%+v", res)
	}
	if res.Canceled {
		t.Fatalf("expected canceled=false for not found")
	}
}

func TestIntegration_CancelTaskRun_TerminalRunNotCanceled(t *testing.T) {
	p := newIntegrationProject(t)

	runID := createRunningTaskRunForFinishAgentTest(t, p, fmt.Sprintf("cancel-terminal-%d", time.Now().UnixNano()))
	if err := p.FinishAgentRun(context.Background(), runID, 0); err != nil {
		t.Fatalf("FinishAgentRun failed: %v", err)
	}

	res, err := p.CancelTaskRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("CancelTaskRun returned error: %v", err)
	}
	if !res.Found {
		t.Fatalf("expected found=true")
	}
	if res.Canceled {
		t.Fatalf("expected canceled=false for terminal run")
	}
	if !strings.Contains(res.Reason, "已结束") {
		t.Fatalf("unexpected reason: %q", res.Reason)
	}

	run, err := p.task.FindRunByID(context.Background(), runID)
	if err != nil {
		t.Fatalf("FindRunByID failed: %v", err)
	}
	if run == nil {
		t.Fatalf("expected task run exists")
	}
	if run.OrchestrationState != contracts.TaskSucceeded {
		t.Fatalf("unexpected orchestration_state: got=%s want=%s", run.OrchestrationState, contracts.TaskSucceeded)
	}
}

func TestIntegration_NoteShapingSkillMissingRollsBackToOpen(t *testing.T) {
	p := newIntegrationProject(t)
	skillPath := p.NotebookShapingSkillPath()
	if strings.TrimSpace(skillPath) == "" {
		t.Fatalf("skill path should not be empty")
	}
	if err := os.Remove(skillPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("remove seeded skill failed: %v", err)
	}
	added, err := p.AddNote(context.Background(), "需要支持导出 CSV")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	if added.NoteID == 0 {
		t.Fatalf("expected note id")
	}
	processed, err := p.ProcessOnePendingNote(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnePendingNote failed: %v", err)
	}
	if !processed {
		t.Fatalf("expected one note processed")
	}
	note, err := p.GetNote(context.Background(), added.NoteID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if note == nil {
		t.Fatalf("expected note exists")
	}
	if note.Status != string(contracts.NoteOpen) {
		t.Fatalf("expected note reopen to open when skill missing, got=%s", note.Status)
	}
	if strings.TrimSpace(note.LastError) == "" {
		t.Fatalf("expected note last_error populated")
	}
	inbox, err := p.ListInbox(context.Background(), ListInboxOptions{Status: contracts.InboxOpen, Limit: 50})
	if err != nil {
		t.Fatalf("ListInbox failed: %v", err)
	}
	found := false
	for _, it := range inbox {
		if strings.Contains(strings.TrimSpace(it.Title), "Notebook shaping skill 缺失") {
			if !strings.Contains(it.Body, "执行 dalek init 重新播种 control") {
				t.Fatalf("inbox body should contain remediation hint, body=%q", it.Body)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected inbox incident for missing shaping skill")
	}
}

func TestIntegration_NoteShapingSuccessWithSkill(t *testing.T) {
	p := newIntegrationProject(t)
	ensureNotebookShapingSkill(t, p)
	added, err := p.AddNote(context.Background(), "支持批量导出 CSV")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	processed, err := p.ProcessOnePendingNote(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnePendingNote failed: %v", err)
	}
	if !processed {
		t.Fatalf("expected one note processed")
	}
	note, err := p.GetNote(context.Background(), added.NoteID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if note == nil {
		t.Fatalf("expected note exists")
	}
	if note.Status != string(contracts.NoteShaped) {
		t.Fatalf("expected note shaped after shaping, got=%s", note.Status)
	}
	if note.ShapedItemID == 0 {
		t.Fatalf("expected shaped item id")
	}
	if note.Shaped == nil {
		t.Fatalf("expected shaped view")
	}
	if strings.TrimSpace(note.Shaped.DedupKey) == "" {
		t.Fatalf("expected dedup key")
	}
	if note.Shaped.Status != string(contracts.ShapedPendingReview) {
		t.Fatalf("expected shaped item pending_review, got=%s", note.Shaped.Status)
	}
	if strings.TrimSpace(note.Shaped.ScopeEstimate) != "L" {
		t.Fatalf("expected scope from skill front matter, got=%q", note.Shaped.ScopeEstimate)
	}
	var acceptance []string
	if err := json.Unmarshal([]byte(note.Shaped.AcceptanceJSON), &acceptance); err != nil {
		t.Fatalf("unmarshal acceptance_json failed: %v", err)
	}
	if len(acceptance) != 2 {
		t.Fatalf("expected 2 acceptance items from skill, got=%d raw=%s", len(acceptance), note.Shaped.AcceptanceJSON)
	}
	if !strings.Contains(note.Shaped.PMNotes, "优先突出业务目标与验收边界") {
		t.Fatalf("expected PMNotes sourced from skill body, got=%q", note.Shaped.PMNotes)
	}
	if !strings.Contains(note.Shaped.SourceNoteIDs, fmt.Sprintf("%d", note.ID)) {
		t.Fatalf("source_note_ids should include note id, got=%s", note.Shaped.SourceNoteIDs)
	}
}

func TestIntegration_NoteShapingInvalidFrontMatterFallsBackToDefault(t *testing.T) {
	p := newIntegrationProject(t)
	skillPath := p.NotebookShapingSkillPath()
	if strings.TrimSpace(skillPath) == "" {
		t.Fatalf("skill path should not be empty")
	}
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll skill dir failed: %v", err)
	}
	invalidSkill := `---
version: "1"
defaults:
  scope_estimate: "S"
  acceptance_template: |
    - [ ] 应该不会被解析
# 缺少 front matter 结束分隔符
`
	if err := os.WriteFile(skillPath, []byte(invalidSkill), 0o644); err != nil {
		t.Fatalf("WriteFile invalid skill failed: %v", err)
	}
	added, err := p.AddNote(context.Background(), "### 支持失败回退")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	processed, err := p.ProcessOnePendingNote(context.Background())
	if err != nil {
		t.Fatalf("ProcessOnePendingNote failed: %v", err)
	}
	if !processed {
		t.Fatalf("expected one note processed")
	}
	note, err := p.GetNote(context.Background(), added.NoteID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if note == nil || note.Shaped == nil {
		t.Fatalf("expected shaped note")
	}
	if note.Status != string(contracts.NoteShaped) {
		t.Fatalf("expected note shaped after fallback, got=%s", note.Status)
	}
	if strings.TrimSpace(note.Shaped.ScopeEstimate) != "M" {
		t.Fatalf("expected default scope estimate fallback, got=%q", note.Shaped.ScopeEstimate)
	}
	var acceptance []string
	if err := json.Unmarshal([]byte(note.Shaped.AcceptanceJSON), &acceptance); err != nil {
		t.Fatalf("unmarshal acceptance_json failed: %v", err)
	}
	if len(acceptance) != 3 {
		t.Fatalf("expected default acceptance items, got=%d raw=%s", len(acceptance), note.Shaped.AcceptanceJSON)
	}
	if !strings.Contains(note.Shaped.PMNotes, "front matter 解析失败") {
		t.Fatalf("expected parse warning in PMNotes, got=%q", note.Shaped.PMNotes)
	}
}

func TestIntegration_NoteAdd_DedupByProjectAndHash(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()

	first, err := p.AddNote(ctx, "支持导出 CSV")
	if err != nil {
		t.Fatalf("AddNote(first) failed: %v", err)
	}
	if first.Deduped {
		t.Fatalf("first add should not dedup")
	}

	second, err := p.AddNote(ctx, "  支持导出   CSV  ")
	if err != nil {
		t.Fatalf("AddNote(second) failed: %v", err)
	}
	if !second.Deduped {
		t.Fatalf("second add should dedup")
	}
	if second.NoteID != first.NoteID {
		t.Fatalf("dedup note id mismatch: got=%d want=%d", second.NoteID, first.NoteID)
	}

	normalized := normalizeNoteTextForTest("支持导出 CSV")
	foreign := contracts.NoteItem{
		ProjectKey:     "other_project",
		Status:         contracts.NoteOpen,
		Source:         "cli",
		Text:           "支持导出 CSV",
		ContextJSON:    contracts.JSONMap{},
		NormalizedHash: hashNormalizedTextForTest(normalized),
		ShapedItemID:   0,
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Create(&foreign).Error; err != nil {
		t.Fatalf("insert foreign project note failed: %v", err)
	}
	if err := mustProjectDB(t, p).WithContext(ctx).
		Model(&contracts.NoteItem{}).
		Where("id = ?", first.NoteID).
		Update("status", contracts.NoteDiscarded).Error; err != nil {
		t.Fatalf("mark first note discarded failed: %v", err)
	}

	third, err := p.AddNote(ctx, "支持导出 CSV")
	if err != nil {
		t.Fatalf("AddNote(third) failed: %v", err)
	}
	if third.Deduped {
		t.Fatalf("should not dedup across project_key")
	}
	if third.NoteID == foreign.ID {
		t.Fatalf("new note should not reuse foreign project note id")
	}
}

func TestIntegration_Shaping_DedupByProjectAndDedupKey(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()
	ensureNotebookShapingSkill(t, p)

	first, err := p.AddNote(ctx, "支持批量导出 CSV")
	if err != nil {
		t.Fatalf("AddNote(first) failed: %v", err)
	}
	if first.Deduped {
		t.Fatalf("first add should not dedup")
	}
	processed, err := p.ProcessOnePendingNote(ctx)
	if err != nil {
		t.Fatalf("ProcessOnePendingNote(first) failed: %v", err)
	}
	if !processed {
		t.Fatalf("expected first note processed")
	}

	second, err := p.AddNote(ctx, "支持批量导出 CSV")
	if err != nil {
		t.Fatalf("AddNote(second) failed: %v", err)
	}
	if second.Deduped {
		t.Fatalf("second add should create new note when previous note is shaped")
	}
	processed, err = p.ProcessOnePendingNote(ctx)
	if err != nil {
		t.Fatalf("ProcessOnePendingNote(second) failed: %v", err)
	}
	if !processed {
		t.Fatalf("expected second note processed")
	}

	n1, err := p.GetNote(ctx, first.NoteID)
	if err != nil {
		t.Fatalf("GetNote(first) failed: %v", err)
	}
	n2, err := p.GetNote(ctx, second.NoteID)
	if err != nil {
		t.Fatalf("GetNote(second) failed: %v", err)
	}
	if n1 == nil || n2 == nil {
		t.Fatalf("expected both notes exist")
	}
	if n1.ShapedItemID == 0 || n2.ShapedItemID == 0 {
		t.Fatalf("expected both notes shaped")
	}
	if n1.ShapedItemID != n2.ShapedItemID {
		t.Fatalf("expected shaping dedup by project+dedup_key, got %d and %d", n1.ShapedItemID, n2.ShapedItemID)
	}
	if n2.Shaped == nil {
		t.Fatalf("expected shaped view on second note")
	}
	if !strings.Contains(n2.Shaped.SourceNoteIDs, fmt.Sprintf("%d", n1.ID)) || !strings.Contains(n2.Shaped.SourceNoteIDs, fmt.Sprintf("%d", n2.ID)) {
		t.Fatalf("source_note_ids should contain both note ids, got=%s", n2.Shaped.SourceNoteIDs)
	}
}

func TestIntegration_ApproveNote_OperatesOnShapedItem(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()
	ensureNotebookShapingSkill(t, p)

	added, err := p.AddNote(ctx, "审批测试：支持导出 CSV")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	if _, err := p.ProcessOnePendingNote(ctx); err != nil {
		t.Fatalf("ProcessOnePendingNote failed: %v", err)
	}

	ticket, err := p.ApproveNote(ctx, added.NoteID, "pm")
	if err != nil {
		t.Fatalf("ApproveNote failed: %v", err)
	}
	if ticket == nil || ticket.ID == 0 {
		t.Fatalf("expected created ticket")
	}

	note, err := p.GetNote(ctx, added.NoteID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if note == nil || note.Shaped == nil {
		t.Fatalf("expected shaped note exists")
	}
	if note.Status != string(contracts.NoteShaped) {
		t.Fatalf("note status should stay shaped after approve, got=%s", note.Status)
	}
	if note.Shaped.Status != string(contracts.ShapedApproved) {
		t.Fatalf("shaped status should be approved, got=%s", note.Shaped.Status)
	}
	if note.Shaped.TicketID != ticket.ID {
		t.Fatalf("shaped ticket_id mismatch: got=%d want=%d", note.Shaped.TicketID, ticket.ID)
	}
}

func TestIntegration_RejectNote_OperatesOnShapedItem(t *testing.T) {
	p := newIntegrationProject(t)
	ctx := context.Background()
	ensureNotebookShapingSkill(t, p)

	added, err := p.AddNote(ctx, "驳回测试：信息不足")
	if err != nil {
		t.Fatalf("AddNote failed: %v", err)
	}
	if _, err := p.ProcessOnePendingNote(ctx); err != nil {
		t.Fatalf("ProcessOnePendingNote failed: %v", err)
	}

	if err := p.RejectNote(ctx, added.NoteID, "信息不完整"); err != nil {
		t.Fatalf("RejectNote failed: %v", err)
	}

	note, err := p.GetNote(ctx, added.NoteID)
	if err != nil {
		t.Fatalf("GetNote failed: %v", err)
	}
	if note == nil || note.Shaped == nil {
		t.Fatalf("expected shaped note exists")
	}
	if note.Status != string(contracts.NoteShaped) {
		t.Fatalf("note status should stay shaped after reject, got=%s", note.Status)
	}
	if note.Shaped.Status != string(contracts.ShapedRejected) {
		t.Fatalf("shaped status should be rejected, got=%s", note.Shaped.Status)
	}
	if strings.TrimSpace(note.Shaped.ReviewComment) != "信息不完整" {
		t.Fatalf("review_comment mismatch: got=%q", note.Shaped.ReviewComment)
	}
}

func TestIntegration_InitCreatesControlPlaneSeed(t *testing.T) {
	p := newIntegrationProject(t)

	wantDB := filepath.Join(p.ProjectDir(), "runtime", "dalek.sqlite3")
	if strings.TrimSpace(p.DBPath()) != wantDB {
		t.Fatalf("unexpected DBPath: got=%s want=%s", p.DBPath(), wantDB)
	}

	mustExist := []string{
		filepath.Join(p.ProjectDir(), "agent-kernel.md"),
		filepath.Join(p.ProjectDir(), "agent-user.md"),
		filepath.Join(p.ProjectDir(), "bootstrap.sh"),
		filepath.Join(p.ProjectDir(), "control", "worker"),
		filepath.Join(p.ProjectDir(), "control", "worker", "worker-kernel.md"),
		filepath.Join(p.ProjectDir(), "control", "worker", "state.json"),
		filepath.Join(p.ProjectDir(), "control", "skills", "notebook-shaping"),
		filepath.Join(p.ProjectDir(), "control", "skills", "notebook-shaping", "SKILL.md"),
		filepath.Join(p.ProjectDir(), "runtime"),
		filepath.Join(p.ProjectDir(), ".gitignore"),
	}
	for _, path := range mustExist {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected path exists: %s err=%v", path, err)
		}
	}
	if err := exec.Command("git", "-C", p.RepoRoot(), "ls-files", "--error-unmatch", "AGENTS.md").Run(); err != nil {
		t.Fatalf("AGENTS.md should be tracked after init: %v", err)
	}
	if err := exec.Command("git", "-C", p.RepoRoot(), "ls-files", "--error-unmatch", "CLAUDE.md").Run(); err != nil {
		t.Fatalf("CLAUDE.md should be tracked after init: %v", err)
	}

	if p.SchemaVersion() != repo.CurrentProjectSchemaVersion {
		t.Fatalf("unexpected schema version: got=%d want=%d", p.SchemaVersion(), repo.CurrentProjectSchemaVersion)
	}
	cfgRaw, err := os.ReadFile(filepath.Join(p.ProjectDir(), "config.json"))
	if err != nil {
		t.Fatalf("read config.json failed: %v", err)
	}
	if !strings.Contains(string(cfgRaw), "\"schema_version\"") {
		t.Fatalf("config.json should contain schema_version")
	}
}

func TestIntegration_OpenProject_BackfillsSchemaVersion(t *testing.T) {
	testutil.UseTmuxShim(t)
	repoRoot := testutil.InitGitRepo(t)
	homeDir := filepath.Join(t.TempDir(), "home")

	h, err := OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	p, err := h.InitProjectFromDir(repoRoot, "demo", repo.Config{
		BranchPrefix: "ts/demo/",
	})
	if err != nil {
		t.Fatalf("InitProjectFromDir failed: %v", err)
	}

	cfgPath := filepath.Join(p.ProjectDir(), "config.json")
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config failed: %v", err)
	}
	var cfgMap map[string]any
	if err := json.Unmarshal(raw, &cfgMap); err != nil {
		t.Fatalf("unmarshal config failed: %v", err)
	}
	delete(cfgMap, "schema_version")
	rewritten, err := json.MarshalIndent(cfgMap, "", "  ")
	if err != nil {
		t.Fatalf("marshal config failed: %v", err)
	}
	rewritten = append(rewritten, '\n')
	if err := os.WriteFile(cfgPath, rewritten, 0o644); err != nil {
		t.Fatalf("write config without schema failed: %v", err)
	}

	reopened, err := h.OpenProjectByName("demo")
	if err != nil {
		t.Fatalf("OpenProjectByName failed: %v", err)
	}
	if reopened.SchemaVersion() != repo.CurrentProjectSchemaVersion {
		t.Fatalf("unexpected schema version after reopen: got=%d want=%d", reopened.SchemaVersion(), repo.CurrentProjectSchemaVersion)
	}
	backfilled, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read backfilled config failed: %v", err)
	}
	if !strings.Contains(string(backfilled), "\"schema_version\"") {
		t.Fatalf("schema_version should be backfilled")
	}
}

func TestIntegration_AddOrUpdateProject_ExistingDoesNotAutoCommitEntryPoints(t *testing.T) {
	testutil.UseTmuxShim(t)
	repoRoot := testutil.InitGitRepo(t)
	homeDir := filepath.Join(t.TempDir(), "home")

	runGit := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repoRoot}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(out))
		}
		return strings.TrimSpace(string(out))
	}

	h, err := OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	p, err := h.InitProjectFromDir(repoRoot, "demo", repo.Config{
		BranchPrefix: "ts/demo/",
	})
	if err != nil {
		t.Fatalf("InitProjectFromDir failed: %v", err)
	}
	repoRoot = p.RepoRoot()

	headBefore := runGit("rev-parse", "HEAD")
	agentsPath := filepath.Join(repoRoot, "AGENTS.md")
	raw, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatalf("read AGENTS.md failed: %v", err)
	}
	if err := os.WriteFile(agentsPath, append(raw, []byte("\n# local edit\n")...), 0o644); err != nil {
		t.Fatalf("write AGENTS.md failed: %v", err)
	}

	if _, err := h.AddOrUpdateProject("demo", repoRoot, repo.Config{RefreshIntervalMS: 120_000}); err != nil {
		t.Fatalf("AddOrUpdateProject failed: %v", err)
	}

	headAfter := runGit("rev-parse", "HEAD")
	if headAfter != headBefore {
		t.Fatalf("AddOrUpdateProject should not auto-commit entry files on existing project: before=%s after=%s", headBefore, headAfter)
	}
	status := runGit("status", "--porcelain", "--", "AGENTS.md", "CLAUDE.md")
	if !strings.Contains(status, "AGENTS.md") {
		t.Fatalf("expected AGENTS.md local edit remains uncommitted, got status=%q", status)
	}
}

func TestIntegration_InitFailOnControlSeed_NoConfigAnchor(t *testing.T) {
	testutil.UseTmuxShim(t)
	repoRoot := testutil.InitGitRepo(t)
	homeDir := filepath.Join(t.TempDir(), "home")

	// 制造 control seed 冲突：.dalek/control/knowledge 是文件，MkdirAll(.../knowledge) 必然失败。
	projectDir := filepath.Join(repoRoot, ".dalek")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatalf("mkdir project dir failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(projectDir, "control"), 0o755); err != nil {
		t.Fatalf("mkdir control failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "control", "knowledge"), []byte("not-a-dir\n"), 0o644); err != nil {
		t.Fatalf("write conflicting knowledge file failed: %v", err)
	}

	h, err := OpenHome(homeDir)
	if err != nil {
		t.Fatalf("OpenHome failed: %v", err)
	}
	if _, err := h.InitProjectFromDir(repoRoot, "demo", repo.Config{
		BranchPrefix: "ts/demo/",
	}); err == nil {
		t.Fatalf("expected init failed when control seed cannot create directories")
	}

	cfgPath := filepath.Join(projectDir, "config.json")
	if _, err := os.Stat(cfgPath); err == nil {
		t.Fatalf("config.json should not be written when init fails before completion")
	}
}
