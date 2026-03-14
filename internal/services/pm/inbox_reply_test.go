package pm

import (
	"context"
	"encoding/json"
	"fmt"
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
	if !strings.Contains(capturedPrompt, "当前动作：done") {
		t.Fatalf("expected done prompt declares explicit action, got:\n%s", capturedPrompt)
	}
	if !strings.Contains(capturedPrompt, "本轮只允许做最小收尾执行") {
		t.Fatalf("expected done prompt to enforce closeout-only semantics, got:\n%s", capturedPrompt)
	}
}

func TestReplyInboxItem_SingleTicketUsesSubmitterAndConsumesInboxOnAcceptedRun(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "reply-inbox-single-submitter")
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
		Summary:    "缺少配置说明",
		NeedsUser:  true,
		Blockers:   []string{"请提供 /tmp/reply.md"},
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.applyWorkerLoopTerminalReport(ctx, waitReport, "pm.worker_loop.closure(test:reply_single_submit_wait)"); err != nil {
		t.Fatalf("apply wait_user failed: %v", err)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("ticket_id = ? AND reason = ? AND status = ?", tk.ID, contracts.InboxNeedsUser, contracts.InboxOpen).First(&inbox).Error; err != nil {
		t.Fatalf("load open needs_user inbox failed: %v", err)
	}

	submitter := &stubWorkerRunSubmitter{runID: 88}
	svc.SetWorkerRunSubmitter(submitter)

	result, err := svc.ReplyInboxItem(ctx, inbox.ID, string(contracts.InboxReplyContinue), "资料已放到 /tmp/reply.md，请继续。")
	if err != nil {
		t.Fatalf("ReplyInboxItem failed: %v", err)
	}
	if result.Mode != inboxReplyModeSingle {
		t.Fatalf("expected mode=%s, got=%s", inboxReplyModeSingle, result.Mode)
	}
	if result.RunID != 88 {
		t.Fatalf("expected run_id=88, got=%d", result.RunID)
	}
	if result.WorkerID != w.ID {
		t.Fatalf("expected worker_id=%d, got=%d", w.ID, result.WorkerID)
	}
	prompts := submitter.Prompts()
	if len(prompts) != 1 {
		t.Fatalf("expected exactly one submitter prompt, got=%d", len(prompts))
	}
	if !strings.Contains(prompts[0], "当前动作：continue") {
		t.Fatalf("expected submitter prompt contains explicit action, got:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[0], "资料已放到 /tmp/reply.md，请继续。") {
		t.Fatalf("expected submitter prompt contains reply markdown, got:\n%s", prompts[0])
	}

	var inboxAfter contracts.InboxItem
	if err := p.DB.First(&inboxAfter, inbox.ID).Error; err != nil {
		t.Fatalf("reload inbox failed: %v", err)
	}
	if inboxAfter.Status != contracts.InboxDone {
		t.Fatalf("expected inbox done after accepted submit, got=%s", inboxAfter.Status)
	}
	if inboxAfter.ReplyConsumedAt == nil {
		t.Fatalf("expected reply consumed timestamp set after accepted submit")
	}
}

func TestReplyInboxItem_SingleTicketKeepsInboxOpenWhenRunSubmissionFails(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "reply-inbox-single-failure")
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
		Summary:    "缺少配置",
		NeedsUser:  true,
		Blockers:   []string{"请提供 /tmp/runtime.json"},
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.applyWorkerLoopTerminalReport(ctx, waitReport, "pm.worker_loop.closure(test:reply_single_failure_wait)"); err != nil {
		t.Fatalf("apply wait_user failed: %v", err)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("ticket_id = ? AND reason = ? AND status = ?", tk.ID, contracts.InboxNeedsUser, contracts.InboxOpen).First(&inbox).Error; err != nil {
		t.Fatalf("load open needs_user inbox failed: %v", err)
	}

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return nil, fmt.Errorf("launch failed")
	}

	_, err = svc.ReplyInboxItem(ctx, inbox.ID, string(contracts.InboxReplyContinue), "配置在 /tmp/runtime.json")
	if err == nil {
		t.Fatalf("expected reply failure")
	}
	if !strings.Contains(err.Error(), "launch failed") {
		t.Fatalf("expected launch failure, got=%v", err)
	}

	var inboxAfter contracts.InboxItem
	if err := p.DB.First(&inboxAfter, inbox.ID).Error; err != nil {
		t.Fatalf("reload inbox failed: %v", err)
	}
	if inboxAfter.Status != contracts.InboxOpen {
		t.Fatalf("expected inbox stay open on failure, got=%s", inboxAfter.Status)
	}
	if inboxAfter.ReplyConsumedAt != nil {
		t.Fatalf("expected reply not consumed on failure")
	}
	if inboxAfter.ReplyAction != contracts.InboxReplyContinue {
		t.Fatalf("expected reply action preserved, got=%s", inboxAfter.ReplyAction)
	}
	if strings.TrimSpace(inboxAfter.ReplyMarkdown) != "配置在 /tmp/runtime.json" {
		t.Fatalf("expected reply markdown preserved, got=%q", inboxAfter.ReplyMarkdown)
	}

	var ticketAfter contracts.Ticket
	if err := p.DB.First(&ticketAfter, tk.ID).Error; err != nil {
		t.Fatalf("reload ticket failed: %v", err)
	}
	if contracts.CanonicalTicketWorkflowStatus(ticketAfter.WorkflowStatus) != contracts.TicketBlocked {
		t.Fatalf("expected ticket stay blocked on failure, got=%s", ticketAfter.WorkflowStatus)
	}
}

func TestReplyInboxItem_SingleTicketWaitUserFallbackKeepsReopenedInboxOpen(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "reply-inbox-single-wait-fallback")
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
		Summary:    "第一次 wait_user",
		NeedsUser:  true,
		Blockers:   []string{"请提供 /tmp/retry.txt"},
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.applyWorkerLoopTerminalReport(ctx, waitReport, "pm.worker_loop.closure(test:reply_single_wait_fallback_wait1)"); err != nil {
		t.Fatalf("apply first wait_user failed: %v", err)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("ticket_id = ? AND reason = ? AND status = ?", tk.ID, contracts.InboxNeedsUser, contracts.InboxOpen).First(&inbox).Error; err != nil {
		t.Fatalf("load open needs_user inbox failed: %v", err)
	}

	runID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
	secondWaitReport := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  runID,
		Summary:    "第二次 wait_user",
		NeedsUser:  true,
		Blockers:   []string{"请补充 /tmp/retry.txt 的格式说明"},
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.ApplyWorkerReport(ctx, secondWaitReport, "test-runtime"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "wait_user", "第二次 wait_user", []string{"请补充 /tmp/retry.txt 的格式说明"}, false, testWorkerDoneHeadSHA, "clean")

	svc.sdkHandleLauncher = func(ctx context.Context, ticket contracts.Ticket, worker contracts.Worker, prompt string) (agentexec.AgentRunHandle, error) {
		return &fakeAgentRunHandle{
			runID:  runID,
			result: agentexec.AgentRunResult{ExitCode: 0},
		}, nil
	}

	result, err := svc.ReplyInboxItem(ctx, inbox.ID, string(contracts.InboxReplyContinue), "资料已放到 /tmp/retry.txt")
	if err != nil {
		t.Fatalf("ReplyInboxItem failed: %v", err)
	}
	if result.NextAction != string(contracts.NextWaitUser) {
		t.Fatalf("expected next_action=wait_user, got=%s", result.NextAction)
	}

	var inboxAfter contracts.InboxItem
	if err := p.DB.First(&inboxAfter, inbox.ID).Error; err != nil {
		t.Fatalf("reload inbox failed: %v", err)
	}
	if inboxAfter.Status != contracts.InboxOpen {
		t.Fatalf("expected reopened inbox stay open after sync wait_user fallback, got=%s", inboxAfter.Status)
	}
	if inboxAfter.ReplyConsumedAt != nil {
		t.Fatalf("expected reopened inbox not marked consumed")
	}
	if inboxAfter.WaitRoundCount != 2 {
		t.Fatalf("expected wait_round_count=2 after second wait_user, got=%d", inboxAfter.WaitRoundCount)
	}
	if inboxAfter.ReplyAction != contracts.InboxReplyNone {
		t.Fatalf("expected reply action reset after new wait_user, got=%s", inboxAfter.ReplyAction)
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
	if len(submitter.Prompts()) != 0 {
		t.Fatalf("expected reply api not submit prompt directly")
	}

	beforeController, err := svc.FocusGet(ctx, focusRes.FocusID)
	if err != nil {
		t.Fatalf("FocusGet before controller failed: %v", err)
	}
	beforeItem1 := focusItemByTicketID(beforeController.Items, tk1.ID)
	beforeItem2 := focusItemByTicketID(beforeController.Items, tk2.ID)
	if beforeController.Run.Status != contracts.FocusBlocked {
		t.Fatalf("expected focus stay blocked before controller consumes reply, got=%s", beforeController.Run.Status)
	}
	if beforeItem1 == nil || beforeItem1.Status != contracts.FocusItemBlocked {
		t.Fatalf("expected first item stay blocked before controller, got=%+v", beforeItem1)
	}
	if beforeItem2 == nil || beforeItem2.Status != contracts.FocusItemPending {
		t.Fatalf("expected second item stay pending before controller, got=%+v", beforeItem2)
	}

	var inboxAccepted contracts.InboxItem
	if err := p.DB.First(&inboxAccepted, inbox.ID).Error; err != nil {
		t.Fatalf("reload inbox before controller failed: %v", err)
	}
	if inboxAccepted.Status != contracts.InboxOpen {
		t.Fatalf("expected inbox stay open before controller consumes reply, got=%s", inboxAccepted.Status)
	}
	if inboxAccepted.ReplyAction != contracts.InboxReplyContinue {
		t.Fatalf("expected reply action stored before controller, got=%s", inboxAccepted.ReplyAction)
	}

	var acceptedEvent contracts.FocusEvent
	if err := p.DB.Where("focus_run_id = ? AND focus_item_id = ? AND kind = ?", focusRes.FocusID, item1.ID, contracts.FocusEventInboxReplyAccepted).First(&acceptedEvent).Error; err != nil {
		t.Fatalf("load focus reply accepted event failed: %v", err)
	}
	acceptedPayload := decodeJSONMapForTest(t, acceptedEvent.PayloadJSON)
	assertFocusReplyPayload(t, acceptedPayload, tk1.ID, inbox.ID, "continue", "资料已放到 /tmp/context.md，请继续执行。")

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
	if !strings.Contains(prompts[0], "当前动作：continue") {
		t.Fatalf("expected continue prompt declares explicit action, got:\n%s", prompts[0])
	}
	if !strings.Contains(prompts[0], "继续推进当前 ticket 的最小必要实现") {
		t.Fatalf("expected continue prompt semantics, got:\n%s", prompts[0])
	}

	var resumedEvent contracts.FocusEvent
	if err := p.DB.Where("focus_run_id = ? AND focus_item_id = ? AND kind = ?", focusRes.FocusID, item1.ID, contracts.FocusEventItemRestarted).First(&resumedEvent).Error; err != nil {
		t.Fatalf("load focus resumed event failed: %v", err)
	}
	resumedPayload := decodeJSONMapForTest(t, resumedEvent.PayloadJSON)
	assertFocusReplyPayload(t, resumedPayload, tk1.ID, inbox.ID, "continue", "资料已放到 /tmp/context.md，请继续执行。")
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

	var inboxAfter contracts.InboxItem
	if err := p.DB.First(&inboxAfter, inbox.ID).Error; err != nil {
		t.Fatalf("reload inbox failed: %v", err)
	}
	if !strings.Contains(inboxAfter.Title, "已达 3 轮上限") {
		t.Fatalf("expected inbox title mark manual handling, got=%q", inboxAfter.Title)
	}
	if !strings.Contains(inboxAfter.Body, "需要 PM/用户手工处理") {
		t.Fatalf("expected inbox body mark manual handling, got=%q", inboxAfter.Body)
	}
}

func TestApplyWorkerLoopTerminalReport_WaitUserRoundLimitMarksManualHandling(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "reply-inbox-round-limit-marker")
	w, err := svc.StartTicket(ctx, tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	for i := 1; i <= maxWaitUserRounds; i++ {
		runID := createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID)
		report := contracts.WorkerReport{
			Schema:     contracts.WorkerReportSchemaV1,
			ProjectKey: strings.TrimSpace(p.Key),
			WorkerID:   w.ID,
			TicketID:   tk.ID,
			TaskRunID:  runID,
			Summary:    fmt.Sprintf("第 %d 次 wait_user", i),
			NeedsUser:  true,
			Blockers:   []string{fmt.Sprintf("请补充第 %d 次资料", i)},
			NextAction: string(contracts.NextWaitUser),
		}
		if err := svc.applyWorkerLoopTerminalReport(ctx, report, fmt.Sprintf("pm.worker_loop.closure(test:wait_round_%d)", i)); err != nil {
			t.Fatalf("apply wait_user #%d failed: %v", i, err)
		}
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("ticket_id = ? AND reason = ? AND status = ?", tk.ID, contracts.InboxNeedsUser, contracts.InboxOpen).First(&inbox).Error; err != nil {
		t.Fatalf("load open needs_user inbox failed: %v", err)
	}
	if inbox.WaitRoundCount != maxWaitUserRounds {
		t.Fatalf("expected wait_round_count=%d, got=%d", maxWaitUserRounds, inbox.WaitRoundCount)
	}
	if !strings.Contains(inbox.Title, "已达 3 轮上限") {
		t.Fatalf("expected title mark manual handling, got=%q", inbox.Title)
	}
	if !strings.Contains(inbox.Body, "需要 PM/用户手工处理") {
		t.Fatalf("expected body mark manual handling, got=%q", inbox.Body)
	}
}

func TestCloseDuplicateNeedsUserInboxesTx_ResolvesDuplicateChain(t *testing.T) {
	_, p, _ := newServiceForTest(t)
	ctx := context.Background()

	tk := createTicket(t, p.DB, "reply-inbox-duplicate-chain")
	now := time.Now()
	keep := contracts.InboxItem{
		Key:              inboxKeyNeedsUserChain(tk.ID, 11),
		Status:           contracts.InboxOpen,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "保留链条",
		Body:             "keep",
		TicketID:         tk.ID,
		OriginTaskRunID:  11,
		CurrentTaskRunID: 12,
		WaitRoundCount:   2,
	}
	if err := p.DB.Create(&keep).Error; err != nil {
		t.Fatalf("create keep inbox failed: %v", err)
	}
	duplicate := contracts.InboxItem{
		Key:              inboxKeyNeedsUserChain(tk.ID, 11) + ":dup",
		Status:           contracts.InboxOpen,
		Severity:         contracts.InboxBlocker,
		Reason:           contracts.InboxNeedsUser,
		Title:            "重复链条",
		Body:             "duplicate",
		TicketID:         tk.ID,
		OriginTaskRunID:  11,
		CurrentTaskRunID: 13,
		WaitRoundCount:   2,
	}
	if err := p.DB.Create(&duplicate).Error; err != nil {
		t.Fatalf("create duplicate inbox failed: %v", err)
	}
	if err := p.DB.Model(&contracts.InboxItem{}).Where("id = ?", duplicate.ID).Update("updated_at", now.Add(time.Minute)).Error; err != nil {
		t.Fatalf("bump duplicate updated_at failed: %v", err)
	}

	if err := closeDuplicateNeedsUserInboxesTx(ctx, p.DB, tk.ID, keep.ID); err != nil {
		t.Fatalf("closeDuplicateNeedsUserInboxesTx failed: %v", err)
	}

	var duplicateAfter contracts.InboxItem
	if err := p.DB.First(&duplicateAfter, duplicate.ID).Error; err != nil {
		t.Fatalf("reload duplicate inbox failed: %v", err)
	}
	if duplicateAfter.Status != contracts.InboxDone {
		t.Fatalf("expected duplicate inbox done, got=%s", duplicateAfter.Status)
	}
	if duplicateAfter.ChainResolvedAt == nil {
		t.Fatalf("expected duplicate chain_resolved_at set")
	}

	active, err := loadActiveNeedsUserChainInboxByTicketWithDB(ctx, p.DB, tk.ID)
	if err != nil {
		t.Fatalf("load active needs_user chain failed: %v", err)
	}
	if active == nil || active.ID != keep.ID {
		t.Fatalf("expected active chain keep inbox#%d, got=%+v", keep.ID, active)
	}
}

func decodeJSONMapForTest(t *testing.T, raw string) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		t.Fatalf("unmarshal payload failed: %v raw=%s", err, raw)
	}
	return payload
}

func assertFocusReplyPayload(t *testing.T, payload map[string]any, ticketID, inboxID uint, action, reply string) {
	t.Helper()
	if got := uint(payload["ticket_id"].(float64)); got != ticketID {
		t.Fatalf("expected ticket_id=%d, got=%d payload=%+v", ticketID, got, payload)
	}
	if got := uint(payload["inbox_id"].(float64)); got != inboxID {
		t.Fatalf("expected inbox_id=%d, got=%d payload=%+v", inboxID, got, payload)
	}
	if got := strings.TrimSpace(payload["action"].(string)); got != action {
		t.Fatalf("expected action=%s, got=%s payload=%+v", action, got, payload)
	}
	if got := strings.TrimSpace(payload["reply"].(string)); got != reply {
		t.Fatalf("expected reply=%q, got=%q payload=%+v", reply, got, payload)
	}
	if got := strings.TrimSpace(payload["reply_excerpt"].(string)); got != reply {
		t.Fatalf("expected reply_excerpt=%q, got=%q payload=%+v", reply, got, payload)
	}
}
