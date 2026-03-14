package pm

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
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
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "所有实现与验证已经完成", nil, true, strings.Repeat("b", 40), "clean")

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
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "所有实现与验证已经完成", nil, true, report.HeadSHA, "clean")

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

func TestRunTicketWorker_DoneClosureAllowsEmptyPhaseList(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "worker-run-done-empty-phases")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	writeWorkerLoopStateWithItemsForTest(t, w.WorktreePath, "done", "单阶段任务已完成", nil, "done", []map[string]any{}, strings.Repeat("c", 40), "clean")

	runID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  runID,
		HeadSHA:    strings.Repeat("c", 40),
		Summary:    "单阶段任务已完成",
		NextAction: string(contracts.NextDone),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test-runtime"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}
	writeWorkerLoopStateWithItemsForTest(t, w.WorktreePath, "done", "单阶段任务已完成", nil, "done", []map[string]any{}, report.HeadSHA, "clean")

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
		t.Fatalf("expected ticket done after empty phase closure, got=%s", ticket.WorkflowStatus)
	}
}

func TestRunTicketWorker_WaitUserClosureBlocksTicketOnLoopExit(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "worker-run-wait-user-closure")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "wait_user", "缺少外部凭据，需要人工补充", []string{"请提供生产环境 token"}, false, testWorkerDoneHeadSHA, "clean")

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
	writeWorkerLoopStateForTest(t, w.WorktreePath, "wait_user", "缺少外部凭据，需要人工补充", []string{"请提供生产环境 token"}, false, testWorkerDoneHeadSHA, "clean")

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

func TestRunTicketWorker_DirtyDoneClosureFallsBackToWaitUser(t *testing.T) {
	svc, p, git := newServiceForTest(t)
	git.WorktreeDirtyValue = true

	tk := createTicket(t, p.DB, "worker-run-dirty-done-closure")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "本地修改尚未收口", nil, true, testWorkerDoneHeadSHA, "dirty")

	runID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  runID,
		HeadSHA:    testWorkerDoneHeadSHA,
		Summary:    "实现看起来完成了",
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
	if result.LastNextAction != string(contracts.NextWaitUser) {
		t.Fatalf("expected fallback next_action wait_user, got=%q", result.LastNextAction)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked after dirty done fallback, got=%s", ticket.WorkflowStatus)
	}
	if got := contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus); got == contracts.IntegrationNeedsMerge {
		t.Fatalf("dirty done closure should not freeze integration, got=%s", got)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("ticket_id = ? AND reason = ? AND status = ?", tk.ID, contracts.InboxNeedsUser, contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected fallback inbox: %v", err)
	}
	if !strings.Contains(inbox.Body, "dirty") {
		t.Fatalf("expected fallback inbox to mention dirty closure, got=%q", inbox.Body)
	}
}

func TestRunTicketWorker_ClosureFallbackFailureSkipsExecutionLost(t *testing.T) {
	svc, p, git := newServiceForTest(t)
	git.WorktreeDirtyValue = true
	svc.workerLoopClosureFallbackApplier = func(ctx context.Context, ticketID uint, w contracts.Worker, loopResult WorkerLoopResult, decision workerLoopStageClosureDecision, source string) error {
		return errors.New("forced fallback failure")
	}

	tk := createTicket(t, p.DB, "worker-run-closure-fallback-failed")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "本地修改尚未收口", nil, true, testWorkerDoneHeadSHA, "dirty")

	runID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  runID,
		HeadSHA:    testWorkerDoneHeadSHA,
		Summary:    "实现看起来完成了",
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

	_, err = svc.RunTicketWorker(context.Background(), tk.ID, WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if err == nil {
		t.Fatalf("expected closure fallback failure")
	}
	if !strings.Contains(err.Error(), "worker loop closure fallback 失败") {
		t.Fatalf("expected fallback failure in error, got=%v", err)
	}
	if !strings.Contains(err.Error(), "forced fallback failure") {
		t.Fatalf("expected wrapped fallback cause, got=%v", err)
	}

	var lifecycleCount int64
	if err := p.DB.Model(&contracts.TicketLifecycleEvent{}).
		Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleExecutionLost).
		Count(&lifecycleCount).Error; err != nil {
		t.Fatalf("count execution_lost lifecycle failed: %v", err)
	}
	if lifecycleCount != 0 {
		t.Fatalf("expected no execution_lost lifecycle after closure fallback failure, got=%d", lifecycleCount)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketActive {
		t.Fatalf("expected ticket remain active after closure fallback failure, got=%s", ticket.WorkflowStatus)
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

func TestRunTicketWorker_FirstBootstrapForceOverwritesPreexistingFiles(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	writeControlWorkerTemplatesForTest(t, p.Layout,
		`
<identity>worker-template</identity>
<ticket>{{DALEK_TICKET_ID}}</ticket>
<worker>{{DALEK_WORKER_ID}}</worker>
`,
		`
{
  "ticket": {
    "id": "{{DALEK_TICKET_ID}}",
    "worker_id": "{{DALEK_WORKER_ID}}"
  },
  "code": {
    "head_sha": "{{HEAD_SHA}}"
  },
  "updated_at": "{{NOW_RFC3339}}"
}
`)

	tk := createTicket(t, p.DB, "worker-run-bootstrap-force")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	primeWorkerBootstrapFilesForTest(t, w.WorktreePath, "PM KERNEL SHOULD BE OVERWRITTEN\n", "{\n  \"owner\": \"pm\"\n}\n")

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return nil, errors.New("stop after bootstrap")
	}

	_, err = svc.RunTicketWorker(context.Background(), tk.ID, WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if err == nil || !strings.Contains(err.Error(), "stop after bootstrap") {
		t.Fatalf("expected stop-after-bootstrap error, got=%v", err)
	}

	kernel, state := readWorkerBootstrapFilesForTest(t, w.WorktreePath)
	if strings.Contains(kernel, "PM KERNEL SHOULD BE OVERWRITTEN") {
		t.Fatalf("expected first bootstrap to overwrite preexisting kernel, got=%q", kernel)
	}
	if !strings.Contains(kernel, "<identity>worker-template</identity>") {
		t.Fatalf("expected worker template kernel, got=%q", kernel)
	}
	if strings.Contains(state, "\"owner\": \"pm\"") {
		t.Fatalf("expected first bootstrap to overwrite preexisting state, got=%q", state)
	}
	if !strings.Contains(state, "\"worker_id\": \""+strconv.FormatUint(uint64(w.ID), 10)+"\"") {
		t.Fatalf("expected state to contain worker id, got=%q", state)
	}
}

func TestRunTicketWorker_RecoveryKeepsExistingValidBootstrapFiles(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	writeControlWorkerTemplatesForTest(t, p.Layout,
		`
<identity>worker-template</identity>
<ticket>{{DALEK_TICKET_ID}}</ticket>
`,
		`
{
  "ticket": {
    "id": "{{DALEK_TICKET_ID}}"
  },
  "updated_at": "{{NOW_RFC3339}}"
}
`)

	tk := createTicket(t, p.DB, "worker-run-bootstrap-recovery-keep")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "bootstrap_recovery_keep")
	primeWorkerBootstrapFilesForTest(t, w.WorktreePath, "KEEP EXISTING WORKER KERNEL\n", "{\n  \"mode\": \"keep\"\n}\n")

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return nil, errors.New("stop after bootstrap")
	}

	_, err = svc.RunTicketWorker(context.Background(), tk.ID, WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if err == nil || !strings.Contains(err.Error(), "stop after bootstrap") {
		t.Fatalf("expected stop-after-bootstrap error, got=%v", err)
	}

	kernel, state := readWorkerBootstrapFilesForTest(t, w.WorktreePath)
	if kernel != "KEEP EXISTING WORKER KERNEL\n" {
		t.Fatalf("expected recovery to preserve valid kernel, got=%q", kernel)
	}
	if state != "{\n  \"mode\": \"keep\"\n}\n" {
		t.Fatalf("expected recovery to preserve valid state, got=%q", state)
	}
}

func TestRunTicketWorker_RecoveryRepairsDamagedBootstrapFiles(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	writeControlWorkerTemplatesForTest(t, p.Layout,
		`
<identity>worker-template</identity>
<ticket>{{DALEK_TICKET_ID}}</ticket>
`,
		`
{
  "ticket": {
    "id": "{{DALEK_TICKET_ID}}",
    "worker_id": "{{DALEK_WORKER_ID}}"
  },
  "updated_at": "{{NOW_RFC3339}}"
}
`)

	tk := createTicket(t, p.DB, "worker-run-bootstrap-recovery-repair")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "bootstrap_recovery_repair")
	primeWorkerBootstrapFilesForTest(t, w.WorktreePath, "", "{invalid json")

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return nil, errors.New("stop after bootstrap")
	}

	_, err = svc.RunTicketWorker(context.Background(), tk.ID, WorkerRunOptions{EntryPrompt: "继续执行任务"})
	if err == nil || !strings.Contains(err.Error(), "stop after bootstrap") {
		t.Fatalf("expected stop-after-bootstrap error, got=%v", err)
	}

	kernel, state := readWorkerBootstrapFilesForTest(t, w.WorktreePath)
	if !strings.Contains(kernel, "<identity>worker-template</identity>") {
		t.Fatalf("expected recovery to repair kernel from template, got=%q", kernel)
	}
	if !json.Valid([]byte(state)) {
		t.Fatalf("expected recovery to repair state into valid JSON, got=%q", state)
	}
	if !strings.Contains(state, "\"worker_id\": \""+strconv.FormatUint(uint64(w.ID), 10)+"\"") {
		t.Fatalf("expected repaired state to contain worker id, got=%q", state)
	}
}
