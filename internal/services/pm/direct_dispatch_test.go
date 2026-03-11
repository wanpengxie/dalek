package pm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
	"dalek/internal/services/agentexec"
)

func TestDirectDispatchWorker_AllowsStoppedWorkerWithAliveRuntime(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	fakeWorkerCodex := filepath.Join(t.TempDir(), "worker-codex")
	workerScript := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-direct-dispatch-1"}'
echo '{"type":"item.completed","item":{"id":"msg-direct-1","type":"agent_message","text":"direct dispatch ok"}}'
`
	if err := os.WriteFile(fakeWorkerCodex, []byte(workerScript), 0o755); err != nil {
		t.Fatalf("write fake worker codex failed: %v", err)
	}
	if err := os.Chmod(fakeWorkerCodex, 0o755); err != nil {
		t.Fatalf("chmod fake worker codex failed: %v", err)
	}
	p.Config.WorkerAgent = repo.AgentExecConfig{
		Provider: "codex",
		Mode:     "sdk",
		Command:  fakeWorkerCodex,
		Model:    "gpt-5.3-codex",
	}

	tk := createTicket(t, p.DB, "direct-dispatch-stopped-worker")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	// 模拟上一轮 loop 已退出：worker 状态变为 stopped，但 runtime 仍在线。
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status": contracts.WorkerStopped,
	}).Error; err != nil {
		t.Fatalf("mark worker stopped failed: %v", err)
	}

	out, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "继续处理这个 ticket",
	})
	if err != nil {
		t.Fatalf("DirectDispatchWorker failed: %v", err)
	}
	if out.WorkerID != w.ID {
		t.Fatalf("unexpected worker id: got=%d want=%d", out.WorkerID, w.ID)
	}
	if out.Stages <= 0 {
		t.Fatalf("expected loop stages > 0, got=%d", out.Stages)
	}
}

func TestDirectDispatchWorker_AutoStartWhenStoppedSessionOffline(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	fakeWorkerCodex := filepath.Join(t.TempDir(), "worker-codex-stopped-offline")
	workerScript := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-direct-dispatch-stopped-offline"}'
echo '{"type":"item.completed","item":{"id":"msg-direct-stopped-offline","type":"agent_message","text":"direct dispatch stopped offline ok"}}'
`
	if err := os.WriteFile(fakeWorkerCodex, []byte(workerScript), 0o755); err != nil {
		t.Fatalf("write fake worker codex failed: %v", err)
	}
	if err := os.Chmod(fakeWorkerCodex, 0o755); err != nil {
		t.Fatalf("chmod fake worker codex failed: %v", err)
	}
	p.Config.WorkerAgent = repo.AgentExecConfig{
		Provider: "codex",
		Mode:     "sdk",
		Command:  fakeWorkerCodex,
		Model:    "gpt-5.3-codex",
	}

	tk := createTicket(t, p.DB, "direct-dispatch-stopped-offline")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":   contracts.WorkerStopped,
		"log_path": "",
	}).Error; err != nil {
		t.Fatalf("mark worker stopped failed: %v", err)
	}

	out, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "继续处理这个 ticket",
	})
	if err != nil {
		t.Fatalf("DirectDispatchWorker should auto-start stopped offline session: %v", err)
	}
	if out.WorkerID == 0 {
		t.Fatalf("expected worker_id in direct dispatch result")
	}
	var after contracts.Worker
	if err := p.DB.First(&after, out.WorkerID).Error; err != nil {
		t.Fatalf("load worker after direct dispatch failed: %v", err)
	}
	if strings.TrimSpace(after.LogPath) == "" {
		t.Fatalf("expected auto-start to restore runtime log path")
	}
}

func TestDirectDispatchWorker_RollbackWorkflowOnLoopFailure(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-rollback-workflow")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketBlocked,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket blocked failed: %v", err)
	}

	// 让 worker loop 在 launch 阶段失败，验证 workflow 不会残留 active。
	p.Config.WorkerAgent = repo.AgentExecConfig{
		Provider: "invalid-provider",
		Mode:     "sdk",
	}
	if _, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{}); err == nil {
		t.Fatalf("expected direct dispatch failure")
	}

	var after contracts.Ticket
	if err := p.DB.First(&after, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if after.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("workflow should rollback to blocked, got=%s", after.WorkflowStatus)
	}
}

func TestDirectDispatchWorker_RollbackWorkflowFallsBackToBlockedWhenPrevInvalid(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-rollback-invalid-prev")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketWorkflowStatus("old_unknown_state"),
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket old workflow failed: %v", err)
	}

	p.Config.WorkerAgent = repo.AgentExecConfig{
		Provider: "invalid-provider",
		Mode:     "sdk",
	}
	if _, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{}); err == nil {
		t.Fatalf("expected direct dispatch failure")
	}

	var after contracts.Ticket
	if err := p.DB.First(&after, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if after.WorkflowStatus != contracts.TicketWorkflowStatus("old_unknown_state") {
		t.Fatalf("workflow should remain unchanged before activation, got=%s", after.WorkflowStatus)
	}
}

func TestDirectDispatchWorker_RequeuesAfterActiveRunFailure(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-requeue-on-run-failure")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	run := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "direct_dispatch_run_failure")
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{
			runID: run.ID,
			err:   fmt.Errorf("worker loop failed"),
		}, nil
	}

	if _, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "继续执行",
	}); err == nil {
		t.Fatalf("expected direct dispatch failure")
	}

	var after contracts.Ticket
	if err := p.DB.First(&after, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if after.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket queued after convergence, got=%s", after.WorkflowStatus)
	}

	var workerAfter contracts.Worker
	if err := p.DB.First(&workerAfter, w.ID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if workerAfter.RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got=%d", workerAfter.RetryCount)
	}

	var lost contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleExecutionLost).Order("sequence desc").First(&lost).Error; err != nil {
		t.Fatalf("expected execution_lost lifecycle event: %v", err)
	}
	if lost.TaskRunID == nil || *lost.TaskRunID != run.ID {
		t.Fatalf("expected execution_lost task_run_id=%d, got=%+v", run.ID, lost)
	}
	if got := strings.TrimSpace(lost.PayloadJSON.String()); !strings.Contains(got, `"observation_kind":"unexpected_exit"`) || !strings.Contains(got, `"failure_code":"worker_loop_failed"`) {
		t.Fatalf("unexpected execution_lost payload: %s", got)
	}
	var requeued contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleRequeued).Order("sequence desc").First(&requeued).Error; err != nil {
		t.Fatalf("expected requeued lifecycle event: %v", err)
	}
}

func TestDirectDispatchWorker_EscalatesAfterRetryBudgetExhausted(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-escalate-on-run-failure")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"retry_count": defaultZombieMaxRetries,
		"updated_at":  time.Now(),
	}).Error; err != nil {
		t.Fatalf("set retry_count failed: %v", err)
	}
	run := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "direct_dispatch_escalate_failure")
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{
			runID: run.ID,
			err:   fmt.Errorf("worker loop failed again"),
		}, nil
	}

	if _, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "继续执行",
	}); err == nil {
		t.Fatalf("expected direct dispatch failure")
	}

	var after contracts.Ticket
	if err := p.DB.First(&after, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if after.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked after escalation, got=%s", after.WorkflowStatus)
	}

	var escalated contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleExecutionEscalated).Order("sequence desc").First(&escalated).Error; err != nil {
		t.Fatalf("expected execution_escalated lifecycle event: %v", err)
	}
	if got := strings.TrimSpace(escalated.PayloadJSON.String()); !strings.Contains(got, `"observation_kind":"unexpected_exit"`) || !strings.Contains(got, `"blocked_reason":"system_incident"`) {
		t.Fatalf("unexpected execution_escalated payload: %s", got)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyWorkerIncident(w.ID, "execution_escalated"), contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected execution_escalated inbox: %v", err)
	}
}

func TestDirectDispatchWorker_WaitsCreatingWorkerReady(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	fakeWorkerCodex := filepath.Join(t.TempDir(), "worker-codex-wait")
	workerScript := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-direct-dispatch-wait"}'
echo '{"type":"item.completed","item":{"id":"msg-direct-wait","type":"agent_message","text":"direct dispatch wait ok"}}'
`
	if err := os.WriteFile(fakeWorkerCodex, []byte(workerScript), 0o755); err != nil {
		t.Fatalf("write fake worker codex failed: %v", err)
	}
	if err := os.Chmod(fakeWorkerCodex, 0o755); err != nil {
		t.Fatalf("chmod fake worker codex failed: %v", err)
	}
	p.Config.WorkerAgent = repo.AgentExecConfig{
		Provider: "codex",
		Mode:     "sdk",
		Command:  fakeWorkerCodex,
		Model:    "gpt-5.3-codex",
	}

	tk := createTicket(t, p.DB, "direct-dispatch-creating-wait")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerCreating,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set worker creating failed: %v", err)
	}

	svc.workerReadyTimeout = 800 * time.Millisecond
	svc.workerReadyPollInterval = 10 * time.Millisecond
	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = p.DB.Model(&contracts.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"status":     contracts.WorkerRunning,
			"log_path":   repo.WorkerStreamLogPath(p.WorkersDir, w.ID),
			"updated_at": time.Now(),
		}).Error
	}()

	out, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "继续执行这个 ticket",
	})
	if err != nil {
		t.Fatalf("DirectDispatchWorker should wait creating->running, got err=%v", err)
	}
	if out.WorkerID != w.ID {
		t.Fatalf("unexpected worker id: got=%d want=%d", out.WorkerID, w.ID)
	}
}

func TestDirectDispatchWorker_DefaultAutoStartWhenNotStarted(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		run := createWorkerTaskRun(t, p.DB, ticket.ID, worker.ID, "direct_dispatch_default_auto_start")
		makeSemanticReport(t, svc, run.ID, "done")
		return &fakeAgentRunHandle{runID: run.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	tk := createTicket(t, p.DB, "direct-dispatch-default-auto-start")
	out, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "继续执行",
	})
	if err != nil {
		t.Fatalf("DirectDispatchWorker should auto-start by default: %v", err)
	}
	if out.WorkerID == 0 {
		t.Fatalf("expected worker_id in direct dispatch result")
	}

	var w contracts.Worker
	if err := p.DB.First(&w, out.WorkerID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped {
		t.Fatalf("expected running/stopped worker after direct auto-start, got=%s", w.Status)
	}
}

func TestDirectDispatchWorker_GeneratesWorkerBootstrapFiles(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-bootstrap-files")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		run := createWorkerTaskRun(t, p.DB, ticket.ID, worker.ID, "direct_dispatch_bootstrap_files")
		makeSemanticReport(t, svc, run.ID, "done")
		return &fakeAgentRunHandle{runID: run.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	if _, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "先检查 bootstrap 文件",
	}); err != nil {
		t.Fatalf("DirectDispatchWorker failed: %v", err)
	}

	for _, path := range []string{
		filepath.Join(w.WorktreePath, ".dalek", "agent-kernel.md"),
		filepath.Join(w.WorktreePath, ".dalek", "PLAN.md"),
		filepath.Join(w.WorktreePath, ".dalek", "state.json"),
	} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("expected bootstrap file %s: %v", path, err)
		}
		if strings.Contains(string(raw), "{{") {
			t.Fatalf("bootstrap file should not keep placeholders: %s", path)
		}
	}
}

func TestDirectDispatchWorker_PreservesExistingPlan(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-preserve-plan")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	planPath := filepath.Join(w.WorktreePath, ".dalek", "PLAN.md")
	customPlan := "custom plan from previous worker run\n"
	if err := os.WriteFile(planPath, []byte(customPlan), 0o644); err != nil {
		t.Fatalf("write custom plan failed: %v", err)
	}
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		run := createWorkerTaskRun(t, p.DB, ticket.ID, worker.ID, "direct_dispatch_preserve_plan")
		makeSemanticReport(t, svc, run.ID, "done")
		return &fakeAgentRunHandle{runID: run.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	if _, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "继续执行任务",
	}); err != nil {
		t.Fatalf("DirectDispatchWorker failed: %v", err)
	}

	got, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatalf("read preserved plan failed: %v", err)
	}
	if string(got) != customPlan {
		t.Fatalf("expected existing plan preserved, got=%q", string(got))
	}
}

func TestDirectDispatchWorker_EmptyNextActionRetryExhaustedBlocksTicket(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-empty-report")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	run1 := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "direct_dispatch_empty_1")
	run2 := createWorkerTaskRun(t, p.DB, tk.ID, w.ID, "direct_dispatch_empty_2")

	var prompts []string
	var callCount atomic.Int32
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		prompts = append(prompts, prompt)
		if callCount.Add(1) == 1 {
			return &fakeAgentRunHandle{runID: run1.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
		}
		return &fakeAgentRunHandle{runID: run2.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	out, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "继续处理这个 ticket",
	})
	if err != nil {
		t.Fatalf("DirectDispatchWorker failed: %v", err)
	}
	if out.Stages != 2 {
		t.Fatalf("expected 2 stages, got=%d", out.Stages)
	}
	if out.LastNextAction != string(contracts.NextWaitUser) {
		t.Fatalf("expected next_action wait_user, got=%q", out.LastNextAction)
	}
	if len(prompts) != 2 || prompts[1] != emptyReportRetryPrompt {
		t.Fatalf("unexpected prompt sequence: %#v", prompts)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked, got=%s", ticket.WorkflowStatus)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyNeedsUser(w.ID), contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected needs_user inbox: %v", err)
	}
	if !strings.Contains(inbox.Body, "未提交 worker report") {
		t.Fatalf("unexpected inbox body: %q", inbox.Body)
	}
}

func TestDirectDispatchWorker_AutoStartFalsePreservesMissingSessionError(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-auto-start-off")
	if _, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		AutoStart: boolPtr(false),
	}); err == nil {
		t.Fatalf("expected direct dispatch fail when auto-start=false")
	} else if !strings.Contains(err.Error(), "尚未启动（没有 worker）") {
		t.Fatalf("unexpected error: %v", err)
	}

	var workers int64
	if err := p.DB.Model(&contracts.Worker{}).Where("ticket_id = ?", tk.ID).Count(&workers).Error; err != nil {
		t.Fatalf("count workers failed: %v", err)
	}
	if workers != 0 {
		t.Fatalf("expected auto-start=false does not create worker, got=%d", workers)
	}
}

func TestManagerTick_IgnoresContinueRedispatch(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	// 与 worker mode 无关：continue 信号不应驱动 ManagerTick 再次 dispatch。
	p.Config.WorkerAgent = repo.AgentExecConfig{
		Provider: "codex",
		Mode:     "cli",
	}

	tk := createTicket(t, p.DB, "manager-tick-skip-continue-redispatch")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	if err := svc.ApplyWorkerReport(context.Background(), contracts.WorkerReport{
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		Summary:    "继续执行",
		NextAction: string(contracts.NextContinue),
	}, "test"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	var beforeTicket contracts.Ticket
	if err := p.DB.First(&beforeTicket, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if beforeTicket.WorkflowStatus != contracts.TicketActive {
		t.Fatalf("expected ticket active before manager tick, got=%s", beforeTicket.WorkflowStatus)
	}

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	for _, id := range res.DispatchedTickets {
		if id == tk.ID {
			t.Fatalf("manager tick should not redispatch ticket by continue event: t%d", tk.ID)
		}
	}

	deadline := time.Now().Add(600 * time.Millisecond)
	for {
		var cnt int64
		if err := p.DB.Model(&contracts.PMDispatchJob{}).Where("ticket_id = ?", tk.ID).Count(&cnt).Error; err != nil {
			t.Fatalf("count pm dispatch jobs failed: %v", err)
		}
		if cnt > 0 {
			t.Fatalf("unexpected redispatch jobs created by continue event, count=%d", cnt)
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
}
