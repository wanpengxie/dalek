package app

import (
	"context"
	"dalek/internal/contracts"
	"fmt"
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

type stubManagerExecutionHost struct {
	mu    sync.Mutex
	calls []daemonsvc.TicketLoopSubmitRequest
}

func (s *stubManagerExecutionHost) SubmitTicketLoop(_ context.Context, req daemonsvc.TicketLoopSubmitRequest) (daemonsvc.TicketLoopSubmitReceipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	return daemonsvc.TicketLoopSubmitReceipt{
		Accepted:  true,
		Project:   req.Project,
		RequestID: req.RequestID,
		TicketID:  req.TicketID,
	}, nil
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

	calls := host.snapshot()
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
