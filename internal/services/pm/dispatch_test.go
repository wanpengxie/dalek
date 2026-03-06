package pm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
	"dalek/internal/services/agentexec"
)

func TestDispatchTicket_SingleModeRunsPMAgent(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "dispatch-single-mode")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	out, err := svc.DispatchTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("DispatchTicket failed: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.InjectedCmd), "sdk:") {
		t.Fatalf("expected sdk worker loop injected_cmd prefix, got=%q", out.InjectedCmd)
	}

	var job contracts.PMDispatchJob
	if err := p.DB.Order("id desc").First(&job).Error; err != nil {
		t.Fatalf("load job failed: %v", err)
	}
	if job.Status != contracts.PMDispatchSucceeded {
		t.Fatalf("unexpected job status: %s", job.Status)
	}
	if strings.TrimSpace(job.ResultJSON.Schema) != contracts.PMDispatchJobResultSchemaV1 {
		t.Fatalf("unexpected schema: %q", job.ResultJSON.Schema)
	}
}

func TestDispatchTicket_InvalidPMAgentConfigFails(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "dispatch-invalid-pm-agent-config")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	p.Config.PMAgent.Provider = "invalid-provider"
	if _, err := svc.DispatchTicket(context.Background(), tk.ID); err == nil {
		t.Fatalf("expected dispatch fail when pm_agent config is invalid")
	} else if !strings.Contains(err.Error(), "pm_agent 配置非法") {
		t.Fatalf("unexpected dispatch error: %v", err)
	}
}

func TestDispatchTicket_PromptContainsStructuredContext(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	dumpPath := filepath.Join(t.TempDir(), "prompt.txt")
	if err := os.Setenv("DALEK_TEST_PROMPT_PATH", dumpPath); err != nil {
		t.Fatalf("set env failed: %v", err)
	}
	defer os.Unsetenv("DALEK_TEST_PROMPT_PATH")

	tk := createTicket(t, p.DB, "dispatch-structured-context")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	override := "请先读取目录结构并拆分这次任务"
	if _, err := svc.DispatchTicketWithOptions(context.Background(), tk.ID, DispatchOptions{
		EntryPrompt: override,
	}); err != nil {
		t.Fatalf("DispatchTicketWithOptions failed: %v", err)
	}

	raw, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatalf("read prompt dump failed: %v", err)
	}
	prompt := string(raw)
	if !strings.Contains(prompt, "<pm_dispatch_single_mode>") {
		t.Fatalf("prompt missing builtin dispatch section: %s", prompt)
	}
	if !strings.Contains(prompt, fmt.Sprintf("\"id\": %d", tk.ID)) {
		t.Fatalf("prompt missing ticket id")
	}
	if !strings.Contains(prompt, strings.TrimSpace(tk.Title)) {
		t.Fatalf("prompt missing ticket title")
	}
	if !strings.Contains(prompt, strings.TrimSpace(tk.Description)) {
		t.Fatalf("prompt missing ticket description")
	}
	if !strings.Contains(prompt, strings.TrimSpace(w.WorktreePath)) {
		t.Fatalf("prompt missing worktree path")
	}
	if !strings.Contains(prompt, override) {
		t.Fatalf("prompt missing entry prompt override")
	}
}

func TestDispatchTicket_AllowsTicketWithoutDescription(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := contracts.Ticket{
		Title:          "dispatch-no-description",
		Description:    "",
		WorkflowStatus: contracts.TicketBacklog,
	}
	if err := p.DB.Create(&tk).Error; err != nil {
		t.Fatalf("create ticket failed: %v", err)
	}
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	if _, err := svc.DispatchTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("DispatchTicket failed when description empty: %v", err)
	}
}

func TestDispatchTicket_SDKModeRunsPMAgent(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	fakeCodex := filepath.Join(t.TempDir(), "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-sdk-1"}'
echo '{"type":"turn.started"}'
echo '{"type":"item.completed","item":{"id":"msg-1","type":"agent_message","text":"sdk dispatch ok"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}'
`
	if err := os.WriteFile(fakeCodex, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex failed: %v", err)
	}
	if err := os.Chmod(fakeCodex, 0o755); err != nil {
		t.Fatalf("chmod fake codex failed: %v", err)
	}
	p.Config.PMAgent = repo.AgentExecConfig{
		Provider: "codex",
		Mode:     "sdk",
		Command:  fakeCodex,
		Model:    "gpt-5.3-codex",
	}

	tk := createTicket(t, p.DB, "dispatch-sdk-mode")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if _, err := svc.DispatchTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("DispatchTicket(sdk) failed: %v", err)
	}
	w, err := svc.worker.LatestWorker(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("latest worker failed: %v", err)
	}
	if w == nil {
		t.Fatalf("expected latest worker")
	}
	logPath := repo.WorkerSDKStreamLogPath(p.WorkersDir, w.ID)
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read sdk stream log failed: %v", err)
	}
	content := strings.TrimSpace(string(b))
	if !strings.Contains(content, "sdk dispatch ok") {
		t.Fatalf("expected sdk stream log contains agent message, got=%q", content)
	}

	var streamEvents int64
	if err := p.DB.Model(&contracts.TaskEvent{}).Where("event_type = ?", "task_stream").Count(&streamEvents).Error; err != nil {
		t.Fatalf("count task_stream events failed: %v", err)
	}
	if streamEvents == 0 {
		t.Fatalf("expected sdk stream events > 0")
	}
}

func TestDispatchTicket_WorkerLoopRunsAndStops(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	fakeWorkerCodex := filepath.Join(t.TempDir(), "worker-codex")
	workerMarker := filepath.Join(t.TempDir(), "worker-ran")
	workerScript := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-worker-sdk-1"}'
echo '{"type":"item.completed","item":{"id":"msg-worker-1","type":"agent_message","text":"worker sdk ok"}}'
echo "ran" > "` + workerMarker + `"
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

	tk := createTicket(t, p.DB, "dispatch-worker-sdk-mode")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	out, err := svc.DispatchTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("DispatchTicket failed: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out.InjectedCmd), "sdk:") {
		t.Fatalf("expected sdk worker loop injected_cmd prefix, got=%q", out.InjectedCmd)
	}

	w, err := svc.worker.LatestWorker(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("latest worker failed: %v", err)
	}
	if w == nil {
		t.Fatalf("expected latest worker")
	}

	// worker loop 完成后 marker 应存在（worker 实际执行了）
	if _, err := os.Stat(workerMarker); err != nil {
		t.Fatalf("expected worker marker to exist after worker loop, err=%v", err)
	}
}

func TestDispatchTicket_DefaultAutoStartWhenNotStarted(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	svc.dispatchAgentExecutor = func(ctx context.Context, requestID string, ticket contracts.Ticket, worker contracts.Worker, entryPromptOverride string) (dispatchPromptBuildResult, error) {
		return dispatchPromptBuildResult{
			TemplatePath: "test://dispatch",
			EntryPrompt:  "dispatch auto-start prompt",
		}, nil
	}
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		run := createWorkerTaskRun(t, p.DB, ticket.ID, worker.ID, "dispatch_default_auto_start")
		makeSemanticReport(t, svc, run.ID, "done")
		return &fakeAgentRunHandle{runID: run.ID, result: agentexec.AgentRunResult{ExitCode: 0}}, nil
	}

	tk := createTicket(t, p.DB, "dispatch-default-auto-start")
	out, err := svc.DispatchTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("DispatchTicket should auto-start by default: %v", err)
	}
	if out.WorkerID == 0 {
		t.Fatalf("expected worker_id in dispatch result")
	}

	var w contracts.Worker
	if err := p.DB.First(&w, out.WorkerID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if w.Status != contracts.WorkerRunning && w.Status != contracts.WorkerStopped {
		t.Fatalf("expected worker running/stopped after auto-start dispatch, got=%s", w.Status)
	}

	var events []contracts.TicketWorkflowEvent
	if err := p.DB.Where("ticket_id = ? AND source IN ?", tk.ID, []string{"pm.start", "pm.dispatch"}).
		Order("id asc").
		Find(&events).Error; err != nil {
		t.Fatalf("load workflow events failed: %v", err)
	}
	var startEventID uint
	var dispatchEventID uint
	for _, ev := range events {
		switch strings.TrimSpace(ev.Source) {
		case "pm.start":
			if startEventID == 0 {
				startEventID = ev.ID
			}
		case "pm.dispatch":
			if dispatchEventID == 0 {
				dispatchEventID = ev.ID
			}
		}
	}
	if startEventID == 0 {
		t.Fatalf("expected pm.start event when dispatch auto-starts ticket")
	}
	if dispatchEventID == 0 {
		t.Fatalf("expected pm.dispatch event")
	}
	if startEventID > dispatchEventID {
		t.Fatalf("expected pm.start before pm.dispatch, start_id=%d dispatch_id=%d", startEventID, dispatchEventID)
	}
}

func TestDispatchTicket_AutoStartFalsePreservesMissingSessionError(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "dispatch-auto-start-off")
	if _, err := svc.DispatchTicketWithOptions(context.Background(), tk.ID, DispatchOptions{
		AutoStart: boolPtr(false),
	}); err == nil {
		t.Fatalf("expected dispatch fail when auto-start=false and ticket not started")
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

func TestSubmitDispatchTicket_AutoStartWhenNotStarted(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "submit-dispatch-auto-start")
	sub, err := svc.SubmitDispatchTicket(context.Background(), tk.ID, DispatchSubmitOptions{
		RequestID: "submit-auto-start-1",
	})
	if err != nil {
		t.Fatalf("SubmitDispatchTicket should auto-start by default: %v", err)
	}
	if sub.JobID == 0 || sub.WorkerID == 0 {
		t.Fatalf("unexpected submission: %+v", sub)
	}

	var w contracts.Worker
	if err := p.DB.First(&w, sub.WorkerID).Error; err != nil {
		t.Fatalf("load worker failed: %v", err)
	}
	if w.Status != contracts.WorkerRunning {
		t.Fatalf("expected running worker, got=%s", w.Status)
	}
	if strings.TrimSpace(w.LogPath) == "" {
		t.Fatalf("expected runtime log path after submit auto-start")
	}
}

func TestResolveDispatchTarget_WaitsCreatingWorkerReady(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "dispatch-wait-worker-ready")
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
			"updated_at": time.Now(),
		}).Error
	}()

	_, target, err := svc.resolveDispatchTarget(context.Background(), tk.ID, false)
	if err != nil {
		t.Fatalf("resolveDispatchTarget should wait creating->running, got err=%v", err)
	}
	if target == nil || target.Status != contracts.WorkerRunning {
		t.Fatalf("expected worker running after wait, got=%v", target)
	}
}

func TestResolveDispatchTarget_TimeoutWhenWorkerKeepsCreating(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "dispatch-wait-worker-timeout")
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

	svc.workerReadyTimeout = 80 * time.Millisecond
	svc.workerReadyPollInterval = 10 * time.Millisecond

	_, _, err = svc.resolveDispatchTarget(context.Background(), tk.ID, false)
	if err == nil {
		t.Fatalf("expected resolveDispatchTarget timeout error")
	}
	if !isWorkerReadyTimeout(err) {
		t.Fatalf("expected worker ready timeout error, got=%v", err)
	}
	if !strings.Contains(err.Error(), "等待 worker 就绪超时") {
		t.Fatalf("unexpected timeout message: %v", err)
	}
}

func boolPtr(v bool) *bool {
	return &v
}
