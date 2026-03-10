package app

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
	"os"
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

type stubManagerDispatchHost struct {
	mu           sync.Mutex
	calls        []daemonsvc.WorkerRunSubmitRequest
	plannerCalls []daemonsvc.PlannerSubmitRequest
}

func (s *stubManagerDispatchHost) SubmitWorkerRun(_ context.Context, req daemonsvc.WorkerRunSubmitRequest) (daemonsvc.WorkerRunSubmitReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	return daemonsvc.WorkerRunSubmitReceipt{
		Accepted:  true,
		Project:   req.Project,
		RequestID: req.RequestID,
		TicketID:  req.TicketID,
	}, nil
}

func (s *stubManagerDispatchHost) SubmitPlannerRun(_ context.Context, req daemonsvc.PlannerSubmitRequest) (daemonsvc.PlannerSubmitReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plannerCalls = append(s.plannerCalls, req)
	return daemonsvc.PlannerSubmitReceipt{
		Accepted:  true,
		Project:   req.Project,
		RequestID: req.RequestID,
		TaskRunID: req.TaskRunID,
	}, nil
}

func (s *stubManagerDispatchHost) snapshot() []daemonsvc.WorkerRunSubmitRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]daemonsvc.WorkerRunSubmitRequest, len(s.calls))
	copy(out, s.calls)
	return out
}

func (s *stubManagerDispatchHost) snapshotPlanner() []daemonsvc.PlannerSubmitRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]daemonsvc.PlannerSubmitRequest, len(s.plannerCalls))
	copy(out, s.plannerCalls)
	return out
}

type stubWarmupDispatchHost struct {
	mu          sync.Mutex
	warmupCalls map[string][]uint
}

func (s *stubWarmupDispatchHost) SubmitWorkerRun(_ context.Context, req daemonsvc.WorkerRunSubmitRequest) (daemonsvc.WorkerRunSubmitReceipt, error) {
	return daemonsvc.WorkerRunSubmitReceipt{
		Accepted:  true,
		Project:   req.Project,
		RequestID: req.RequestID,
		TicketID:  req.TicketID,
	}, nil
}

func (s *stubWarmupDispatchHost) SubmitPlannerRun(_ context.Context, req daemonsvc.PlannerSubmitRequest) (daemonsvc.PlannerSubmitReceipt, error) {
	return daemonsvc.PlannerSubmitReceipt{
		Accepted:  true,
		Project:   req.Project,
		RequestID: req.RequestID,
		TaskRunID: req.TaskRunID,
	}, nil
}

func (s *stubWarmupDispatchHost) WarmupRunProjectIndex(project string, runIDs []uint) int {
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

func (s *stubWarmupDispatchHost) snapshotWarmup(project string) []uint {
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
	if _, err := p.SetAutopilotEnabled(ctx, true); err != nil {
		t.Fatalf("SetAutopilotEnabled(true) failed: %v", err)
	}

	tk, err := p.CreateTicketWithDescription(ctx, "manager submitter wiring", "dispatch should go through execution host submitter")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	if err := p.core.DB.WithContext(ctx).Model(&store.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}

	host := &stubManagerDispatchHost{}
	manager := newDaemonManagerComponent(h, nil)
	manager.setDispatchHost(host)
	manager.runTickProject(ctx, p.Name(), "test")

	calls := host.snapshot()
	if len(calls) != 1 {
		t.Fatalf("expected one SubmitWorkerRun call, got=%d", len(calls))
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

func TestDaemonManagerComponent_RunTickProject_SubmitsPlannerRunWhenScheduled(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()
	if _, err := p.SetAutopilotEnabled(ctx, true); err != nil {
		t.Fatalf("SetAutopilotEnabled(true) failed: %v", err)
	}
	planPath := filepath.Join(p.RepoRoot(), ".dalek", "pm", "plan.md")
	if err := os.MkdirAll(filepath.Dir(planPath), 0o755); err != nil {
		t.Fatalf("mkdir planner plan dir failed: %v", err)
	}
	if err := os.WriteFile(planPath, []byte("# Planner Plan\n- prioritize blockers\n"), 0o644); err != nil {
		t.Fatalf("write planner plan failed: %v", err)
	}

	tk, err := p.CreateTicketWithDescription(ctx, "manager planner submit wiring", "planner run should be submitted to execution host")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	w, err := p.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	workerRun, err := p.task.CreateRun(ctx, contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
		ProjectKey:         p.Key(),
		TicketID:           tk.ID,
		WorkerID:           w.ID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", tk.ID),
		RequestID:          fmt.Sprintf("planner-trigger-%d", now.UnixNano()),
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("create worker task run failed: %v", err)
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketActive,
		"updated_at":      now,
	}).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	if err := mustProjectDB(t, p).WithContext(ctx).Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerRunning,
		"updated_at": now,
	}).Error; err != nil {
		t.Fatalf("set worker running failed: %v", err)
	}
	if err := p.task.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: workerRun.ID,
		EventType: "watch_error",
		Note:      "trigger planner dirty",
	}); err != nil {
		t.Fatalf("append watch_error event failed: %v", err)
	}

	host := &stubManagerDispatchHost{}
	manager := newDaemonManagerComponent(h, nil)
	manager.setDispatchHost(host)
	manager.runTickProject(ctx, p.Name(), "test")

	if got := len(host.snapshot()); got != 0 {
		t.Fatalf("expected no dispatch submits in this scenario, got=%d", got)
	}
	plannerCalls := host.snapshotPlanner()
	if len(plannerCalls) != 1 {
		t.Fatalf("expected one planner submit call, got=%d", len(plannerCalls))
	}
	call := plannerCalls[0]
	if call.Project != p.Name() {
		t.Fatalf("unexpected planner submit project: got=%q want=%q", call.Project, p.Name())
	}
	if call.TaskRunID == 0 {
		t.Fatalf("expected planner submit task_run_id > 0")
	}
	if strings.TrimSpace(call.RequestID) == "" {
		t.Fatalf("expected planner submit request_id not empty")
	}
	if strings.TrimSpace(call.Prompt) == "" {
		t.Fatalf("expected planner submit prompt not empty")
	}
	if !strings.Contains(call.Prompt, "Planner Plan") {
		t.Fatalf("expected prompt contains planner plan markdown")
	}
	if !strings.Contains(call.Prompt, "\"command\": \"dalek pm state sync\"") {
		t.Fatalf("expected prompt contains pm state sync snapshot command")
	}
	if !strings.Contains(call.Prompt, "\"schema\": \"dalek.pm.state.v1\"") {
		t.Fatalf("expected prompt contains pm workspace state snapshot")
	}
	if !strings.Contains(call.Prompt, "\"command\": \"dalek ticket ls\"") {
		t.Fatalf("expected prompt contains ticket list snapshot command")
	}
	if !strings.Contains(call.Prompt, "\"command\": \"dalek merge ls\"") {
		t.Fatalf("expected prompt contains merge list snapshot command")
	}
	if !strings.Contains(call.Prompt, "不要直接修改产品源码、测试或功能实现文件") {
		t.Fatalf("expected prompt enforces PM no-direct-code rule")
	}
	if !strings.Contains(call.Prompt, "git merge --abort") {
		t.Fatalf("expected prompt instructs planner to abort product-file merge conflicts")
	}
	if !strings.Contains(call.Prompt, "\"command\": \"dalek inbox ls --status open\"") {
		t.Fatalf("expected prompt contains inbox list snapshot command")
	}
	if !strings.Contains(call.Prompt, "\"surface_conflicts\"") {
		t.Fatalf("expected prompt contains surface_conflicts snapshot")
	}

	pmState, err := p.GetPMState(ctx)
	if err != nil {
		t.Fatalf("GetPMState failed: %v", err)
	}
	if pmState.PlannerActiveTaskRunID == nil {
		t.Fatalf("expected planner active task run id set")
	}
	if *pmState.PlannerActiveTaskRunID != call.TaskRunID {
		t.Fatalf("planner active run mismatch: state=%d submit=%d", *pmState.PlannerActiveTaskRunID, call.TaskRunID)
	}

	var plannerRun contracts.TaskRun
	if err := p.core.DB.WithContext(ctx).First(&plannerRun, call.TaskRunID).Error; err != nil {
		t.Fatalf("load planner task run failed: %v", err)
	}
	if plannerRun.TaskType != contracts.TaskTypePMPlannerRun {
		t.Fatalf("expected planner task type=%s, got=%s", contracts.TaskTypePMPlannerRun, plannerRun.TaskType)
	}
	if strings.TrimSpace(plannerRun.RequestID) != call.RequestID {
		t.Fatalf("planner request id mismatch: task=%q submit=%q", plannerRun.RequestID, call.RequestID)
	}
}

func TestDaemonManagerComponent_WarmupRunProjectIndex_LoadsActiveRuns(t *testing.T) {
	h, p := newIntegrationHomeProject(t)
	ctx := context.Background()

	tk, err := p.CreateTicketWithDescription(ctx, "manager warmup index", "warmup should index active runs")
	if err != nil {
		t.Fatalf("CreateTicket failed: %v", err)
	}
	job := createStuckDispatchJobForRecovery(t, p, tk.ID, contracts.PMDispatchRunning)
	if job.TaskRunID == 0 {
		t.Fatalf("expected dispatch submission has task_run_id")
	}

	host := &stubWarmupDispatchHost{}
	manager := newDaemonManagerComponent(h, nil)
	manager.setDispatchHost(host)
	manager.warmupRunProjectIndex(ctx)

	warmed := host.snapshotWarmup(p.Name())
	found := false
	for _, runID := range warmed {
		if runID == job.TaskRunID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected warmup includes active run %d, got=%v", job.TaskRunID, warmed)
	}
}
