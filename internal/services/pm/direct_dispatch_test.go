package pm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
	"dalek/internal/store"
)

func TestDirectDispatchWorker_AllowsStoppedWorkerWithAliveSession(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

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

	// 模拟上一轮 loop 已退出：worker 状态变为 stopped，但 tmux session 仍在线。
	if err := p.DB.Model(&store.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
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
	svc, p, fTmux, _ := newServiceForTest(t)

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
	if err := p.DB.Model(&store.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status": contracts.WorkerStopped,
	}).Error; err != nil {
		t.Fatalf("mark worker stopped failed: %v", err)
	}
	delete(fTmux.Sessions, strings.TrimSpace(w.TmuxSession))

	out, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		EntryPrompt: "继续处理这个 ticket",
	})
	if err != nil {
		t.Fatalf("DirectDispatchWorker should auto-start stopped offline session: %v", err)
	}
	if out.WorkerID == 0 {
		t.Fatalf("expected worker_id in direct dispatch result")
	}
}

func TestDirectDispatchWorker_RollbackWorkflowOnLoopFailure(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-rollback-workflow")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&store.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
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

	var after store.Ticket
	if err := p.DB.First(&after, tk.ID).Error; err != nil {
		t.Fatalf("load ticket failed: %v", err)
	}
	if after.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("workflow should rollback to blocked, got=%s", after.WorkflowStatus)
	}
}

func TestDirectDispatchWorker_WaitsCreatingWorkerReady(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

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
	if err := p.DB.Model(&store.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
		"status":     contracts.WorkerCreating,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set worker creating failed: %v", err)
	}

	svc.workerReadyTimeout = 800 * time.Millisecond
	svc.workerReadyPollInterval = 10 * time.Millisecond
	go func() {
		time.Sleep(80 * time.Millisecond)
		_ = p.DB.Model(&store.Worker{}).Where("id = ?", w.ID).Updates(map[string]any{
			"status":     contracts.WorkerRunning,
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
	svc, p, _, _ := newServiceForTest(t)

	fakeWorkerCodex := filepath.Join(t.TempDir(), "worker-codex-auto-start")
	workerScript := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-direct-dispatch-auto-start"}'
echo '{"type":"item.completed","item":{"id":"msg-direct-auto-start","type":"agent_message","text":"direct dispatch auto-start ok"}}'
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

	var w store.Worker
	if err := p.DB.First(&w, out.WorkerID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped {
		t.Fatalf("expected running/stopped worker after direct auto-start, got=%s", w.Status)
	}
	if strings.TrimSpace(w.TmuxSession) == "" {
		t.Fatalf("expected worker session after direct auto-start")
	}
}

func TestDirectDispatchWorker_AutoStartFalsePreservesMissingSessionError(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "direct-dispatch-auto-start-off")
	if _, err := svc.DirectDispatchWorker(context.Background(), tk.ID, DirectDispatchOptions{
		AutoStart: boolPtr(false),
	}); err == nil {
		t.Fatalf("expected direct dispatch fail when auto-start=false")
	} else if !strings.Contains(err.Error(), "尚未启动（没有 worker/session）") {
		t.Fatalf("unexpected error: %v", err)
	}

	var workers int64
	if err := p.DB.Model(&store.Worker{}).Where("ticket_id = ?", tk.ID).Count(&workers).Error; err != nil {
		t.Fatalf("count workers failed: %v", err)
	}
	if workers != 0 {
		t.Fatalf("expected auto-start=false does not create worker, got=%d", workers)
	}
}

func TestManagerTick_IgnoresContinueRedispatch(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

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

	var beforeTicket store.Ticket
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
