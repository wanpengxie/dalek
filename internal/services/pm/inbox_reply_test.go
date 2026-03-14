package pm

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/agentexec"
)

func TestReplyInboxItem_SingleTicketInjectsReplyPrompt(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "reply-inbox-single")
	w, err := svc.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	waitRunID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
	waitReport := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  waitRunID,
		Summary:    "缺少收尾确认",
		NeedsUser:  true,
		Blockers:   []string{"请确认 /tmp/final.md 是否可读"},
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.applyWorkerLoopTerminalReport(ctx, waitReport, "pm.worker_loop.closure(test:reply_single_wait)"); err != nil {
		t.Fatalf("apply wait_user failed: %v", err)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("ticket_id = ? AND reason = ? AND status = ?", tk.ID, contracts.InboxNeedsUser, contracts.InboxOpen).First(&inbox).Error; err != nil {
		t.Fatalf("load open needs_user inbox failed: %v", err)
	}

	runID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
	doneReport := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  runID,
		HeadSHA:    testWorkerDoneHeadSHA,
		Summary:    "收尾完成",
		NextAction: string(contracts.NextDone),
	}
	if err := svc.ApplyWorkerReport(ctx, doneReport, "test-runtime"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "收尾完成", nil, true, testWorkerDoneHeadSHA, "clean")

	var capturedPrompt string
	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		capturedPrompt = prompt
		return &fakeAgentRunHandle{
			runID:  runID,
			result: agentexec.AgentRunResult{ExitCode: 0},
		}, nil
	}

	result, err := svc.ReplyInboxItem(ctx, inbox.ID, string(contracts.InboxReplyDone), "资料已放到 /tmp/final.md，请按最小收尾流程处理。")
	if err != nil {
		t.Fatalf("ReplyInboxItem failed: %v", err)
	}
	if result.Mode != inboxReplyModeSingle {
		t.Fatalf("expected mode=%s, got=%s", inboxReplyModeSingle, result.Mode)
	}
	if result.NextAction != string(contracts.NextDone) {
		t.Fatalf("expected next_action=done, got=%s", result.NextAction)
	}
	if !strings.Contains(capturedPrompt, "<context>") || !strings.Contains(capturedPrompt, "<reply>") || !strings.Contains(capturedPrompt, "<check>") {
		t.Fatalf("expected prompt contains context/reply/check blocks, got:\n%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "缺少收尾确认") || !strings.Contains(capturedPrompt, "请确认 /tmp/final.md 是否可读") {
		t.Fatalf("expected prompt contains inbox context, got:\n%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "资料已放到 /tmp/final.md") {
		t.Fatalf("expected prompt contains user reply, got:\n%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "本轮只允许做最小收尾执行") {
		t.Fatalf("expected done prompt to enforce closeout-only semantics, got:\n%s", capturedPrompt)
	}
}

func TestReplyInboxItem_FocusBatchUsesControllerAndKeepsSerialOrder(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk1 := createTicket(t, p.DB, "reply-inbox-focus-1")
	tk2 := createTicket(t, p.DB, "reply-inbox-focus-2")
	worker, err := svc.StartTicket(ctx, tk1.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk1.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketBlocked,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket blocked failed: %v", err)
	}

	focusRes, err := svc.FocusStart(ctx, contracts.FocusStartInput{
		Mode:           contracts.FocusModeBatch,
		ScopeTicketIDs: []uint{tk1.ID, tk2.ID},
	})
	if err != nil {
		t.Fatalf("FocusStart failed: %v", err)
	}
	view, err := svc.FocusGet(ctx, focusRes.FocusID)
	if err != nil {
		t.Fatalf("FocusGet failed: %v", err)
	}
	item1 := focusItemByTicketID(view.Items, tk1.ID)
	if item1 == nil {
		t.Fatalf("expected focus item for t%d", tk1.ID)
	}
	if err := p.DB.Model(&contracts.FocusRun{}).Where("id = ?", focusRes.FocusID).Updates(map[string]any{
		"status":     contracts.FocusBlocked,
		"updated_at": time.Now(),
	}).Error; err != nil {
		t.Fatalf("set focus blocked failed: %v", err)
	}
	if err := p.DB.Model(&contracts.FocusRunItem{}).Where("id = ?", item1.ID).Updates(map[string]any{
		"status":         contracts.FocusItemBlocked,
		"blocked_reason": focusBlockedReasonNeedsUser,
		"updated_at":     time.Now(),
	}).Error; err != nil {
		t.Fatalf("set focus item blocked failed: %v", err)
	}

	inbox := contracts.InboxItem{
		Key:              inboxKeyNeedsUser(worker.ID),
		Status:           contracts.InboxOpen,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "需要你输入",
		Body:             "请根据 /tmp/context.md 继续推进",
		TicketID:         tk1.ID,
		WorkerID:         worker.ID,
		OriginTaskRunID:  11,
		CurrentTaskRunID: 11,
		WaitRoundCount:   1,
	}
	if err := p.DB.Create(&inbox).Error; err != nil {
		t.Fatalf("create needs_user inbox failed: %v", err)
	}

	submitter := &stubWorkerRunSubmitter{runID: 99}
	svc.SetWorkerRunSubmitter(submitter)

	result, err := svc.ReplyInboxItem(ctx, inbox.ID, string(contracts.InboxReplyContinue), "资料已放到 /tmp/context.md，请继续执行。")
	if err != nil {
		t.Fatalf("ReplyInboxItem failed: %v", err)
	}
	if result.Mode != inboxReplyModeFocus {
		t.Fatalf("expected mode=%s, got=%s", inboxReplyModeFocus, result.Mode)
	}
	if result.FocusID != focusRes.FocusID {
		t.Fatalf("expected focus_id=%d, got=%d", focusRes.FocusID, result.FocusID)
	}

	if err := svc.AdvanceFocusController(ctx); err != nil {
		t.Fatalf("AdvanceFocusController after reply failed: %v", err)
	}

	prompts := submitter.Prompts()
	after, err := svc.FocusGet(ctx, focusRes.FocusID)
	if err != nil {
		t.Fatalf("FocusGet after reply failed: %v", err)
	}
	var inboxAfter contracts.InboxItem
	if err := p.DB.First(&inboxAfter, inbox.ID).Error; err != nil {
		t.Fatalf("reload inbox failed: %v", err)
	}
	afterItem1 := focusItemByTicketID(after.Items, tk1.ID)
	afterItem2 := focusItemByTicketID(after.Items, tk2.ID)
	if after.Run.Status != contracts.FocusRunning {
		t.Fatalf("expected focus running after controller resume, got=%s focus=%+v inbox=%+v", after.Run.Status, after, inboxAfter)
	}
	if afterItem1 == nil || afterItem1.Status != contracts.FocusItemExecuting {
		t.Fatalf("expected first item executing, got=%+v", afterItem1)
	}
	if afterItem2 == nil || afterItem2.Status != contracts.FocusItemPending {
		t.Fatalf("expected second item stays pending, got=%+v", afterItem2)
	}

	if inboxAfter.Status != contracts.InboxDone {
		t.Fatalf("expected inbox marked done after controller consumes reply, got=%s", inboxAfter.Status)
	}
	if inboxAfter.ReplyConsumedAt == nil {
		t.Fatalf("expected inbox reply marked consumed after controller submit")
	}
	if len(prompts) != 1 {
		t.Fatalf("expected exactly one submitted prompt, got=%d focus=%+v inbox=%+v", len(prompts), after, inboxAfter)
	}
	if !strings.Contains(prompts[0], "<context>") || !strings.Contains(prompts[0], "<reply>") || !strings.Contains(prompts[0], "<check>") {
		t.Fatalf("expected focus prompt contains context/reply/check blocks, got:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[0], "资料已放到 /tmp/context.md") {
		t.Fatalf("expected focus prompt contains reply markdown, got:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[0], "继续推进当前 ticket 的最小必要实现") {
		t.Fatalf("expected continue prompt semantics, got:\n%s", prompts[0])
	}
}

func TestReplyInboxItem_RejectsWhenWaitUserRoundsExhausted(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "reply-inbox-round-limit")
	inbox := contracts.InboxItem{
		Key:              inboxKeyNeedsUserChain(tk.ID, 42),
		Status:           contracts.InboxOpen,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "需要更多输入",
		Body:             "wait_user 已经达到上限",
		TicketID:         tk.ID,
		OriginTaskRunID:  42,
		CurrentTaskRunID: 45,
		WaitRoundCount:   maxWaitUserRounds,
	}
	if err := p.DB.Create(&inbox).Error; err != nil {
		t.Fatalf("create needs_user inbox failed: %v", err)
	}

	_, err := svc.ReplyInboxItem(ctx, inbox.ID, string(contracts.InboxReplyContinue), "继续尝试")
	if err == nil {
		t.Fatalf("expected round limit error")
	}
	if !strings.Contains(err.Error(), "wait_user 链已达到") {
		t.Fatalf("expected round limit error, got=%v", err)
	}
}
