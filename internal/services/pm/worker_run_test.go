package pm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/agentexec"
)

func TestRunTicketWorker_DoneClosurePromotesTicketOnLoopExit(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "worker-run-done-closure")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	runID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  runID,
		HeadSHA:    strings.Repeat("b", 40),
		Summary:    "所有实现与验证已经完成",
		NextAction: string(contracts.NextDone),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test-runtime"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{
			runID:  runID,
			result: agentexec.AgentRunResult{ExitCode: 0},
		}, nil
	}

	result, err := svc.RunTicketWorker(context.Background(), tk.ID, WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if err != nil {
		t.Fatalf("RunTicketWorker failed: %v", err)
	}
	if result.LastNextAction != string(contracts.NextDone) {
		t.Fatalf("unexpected last next_action: %q", result.LastNextAction)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketDone {
		t.Fatalf("expected ticket done after loop closure, got=%s", ticket.WorkflowStatus)
	}
	if got := contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus); got != contracts.IntegrationNeedsMerge {
		t.Fatalf("expected integration needs_merge, got=%s", got)
	}
}

func TestRunTicketWorker_WaitUserClosureBlocksTicketOnLoopExit(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "worker-run-wait-user-closure")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	runID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  runID,
		Summary:    "缺少外部凭据，需要人工补充",
		Blockers:   []string{"请提供生产环境 token"},
		NeedsUser:  true,
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test-runtime"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{
			runID:  runID,
			result: agentexec.AgentRunResult{ExitCode: 0},
		}, nil
	}

	result, err := svc.RunTicketWorker(context.Background(), tk.ID, WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if err != nil {
		t.Fatalf("RunTicketWorker failed: %v", err)
	}
	if result.LastNextAction != string(contracts.NextWaitUser) {
		t.Fatalf("unexpected last next_action: %q", result.LastNextAction)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked after loop closure, got=%s", ticket.WorkflowStatus)
	}
}

func TestRunTicketWorker_CanceledLoopSkipsExecutionLostClosure(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "worker-run-canceled-loop")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	runID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
	rt, err := svc.taskRuntime()
	if err != nil {
		t.Fatalf("taskRuntime failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	svc.sdkHandleLauncher = func(launchCtx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{
			runID: runID,
			waitFunc: func(waitCtx context.Context) (agentexec.AgentRunResult, error) {
				if err := rt.MarkRunCanceled(context.Background(), runID, "manual_cancel", "test canceled", time.Now()); err != nil {
					t.Fatalf("MarkRunCanceled failed: %v", err)
				}
				cancel()
				return agentexec.AgentRunResult{}, context.Canceled
			},
		}, nil
	}

	_, err = svc.RunTicketWorker(ctx, tk.ID, WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got=%v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketActive {
		t.Fatalf("expected ticket remain active after manual cancel, got=%s", ticket.WorkflowStatus)
	}

	var lifecycleCount int64
	if err := p.DB.Model(&contracts.TicketLifecycleEvent{}).
		Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleExecutionLost).
		Count(&lifecycleCount).Error; err != nil {
		t.Fatalf("count execution_lost lifecycle failed: %v", err)
	}
	if lifecycleCount != 0 {
		t.Fatalf("expected no execution_lost lifecycle after manual cancel, got=%d", lifecycleCount)
	}

	var afterRun contracts.TaskRun
	if err := p.DB.First(&afterRun, runID).Error; err != nil {
		t.Fatalf("query run failed: %v", err)
	}
	if afterRun.OrchestrationState != contracts.TaskCanceled {
		t.Fatalf("expected task run canceled, got=%s", afterRun.OrchestrationState)
	}
}
