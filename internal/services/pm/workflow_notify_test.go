package pm

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dalek/internal/contracts"
	gatewaysendsvc "dalek/internal/services/gatewaysend"
	"dalek/internal/store"
)

type testStatusChangeHook struct {
	ch  chan StatusChangeEvent
	err error
}

func (h *testStatusChangeHook) OnStatusChange(ctx context.Context, event StatusChangeEvent) error {
	_ = ctx
	if h != nil && h.ch != nil {
		select {
		case h.ch <- event:
		default:
		}
	}
	if h == nil {
		return nil
	}
	return h.err
}

type testBlockingStatusChangeHook struct {
	delay time.Duration
	done  chan struct{}
}

func (h *testBlockingStatusChangeHook) OnStatusChange(ctx context.Context, event StatusChangeEvent) error {
	_ = event
	if h == nil {
		return nil
	}
	if h.delay > 0 {
		select {
		case <-time.After(h.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if h.done != nil {
		select {
		case h.done <- struct{}{}:
		default:
		}
	}
	return nil
}

type sendCardCall struct {
	chatID   string
	title    string
	markdown string
}

type testMessageSender struct {
	mu    sync.Mutex
	calls []sendCardCall
}

func (s *testMessageSender) SendCard(ctx context.Context, chatID, title, markdown string) error {
	_ = ctx
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, sendCardCall{
		chatID:   strings.TrimSpace(chatID),
		title:    strings.TrimSpace(title),
		markdown: strings.TrimSpace(markdown),
	})
	return nil
}

func (s *testMessageSender) SendText(ctx context.Context, chatID, text string) error {
	return s.SendCard(ctx, chatID, "", text)
}

func (s *testMessageSender) snapshotCalls() []sendCardCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sendCardCall, len(s.calls))
	copy(out, s.calls)
	return out
}

func waitStatusEvent(t *testing.T, ch <-chan StatusChangeEvent) StatusChangeEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(2 * time.Second):
		t.Fatalf("等待状态通知超时")
	}
	return StatusChangeEvent{}
}

func assertNoStatusEvent(t *testing.T, ch <-chan StatusChangeEvent) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("不期望收到状态通知: %+v", ev)
	case <-time.After(250 * time.Millisecond):
	}
}

func TestWaitStatusChangeHooks_WaitsForAsyncHook(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-wait-hooks")
	hook := &testBlockingStatusChangeHook{
		delay: 120 * time.Millisecond,
		done:  make(chan struct{}, 1),
	}
	svc.SetStatusChangeHook(hook)

	if err := svc.SetTicketWorkflowStatus(context.Background(), tk.ID, contracts.TicketActive); err != nil {
		t.Fatalf("SetTicketWorkflowStatus failed: %v", err)
	}

	start := time.Now()
	if err := svc.WaitStatusChangeHooks(context.Background()); err != nil {
		t.Fatalf("WaitStatusChangeHooks failed: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("WaitStatusChangeHooks returned too early: %s", elapsed)
	}
	select {
	case <-hook.done:
	default:
		t.Fatalf("hook not finished")
	}
}

func TestWaitStatusChangeHooks_RespectsContextTimeout(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-wait-timeout")
	hook := &testBlockingStatusChangeHook{
		delay: 500 * time.Millisecond,
		done:  make(chan struct{}, 1),
	}
	svc.SetStatusChangeHook(hook)

	if err := svc.SetTicketWorkflowStatus(context.Background(), tk.ID, contracts.TicketActive); err != nil {
		t.Fatalf("SetTicketWorkflowStatus failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := svc.WaitStatusChangeHooks(ctx); err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
}

func TestSetTicketWorkflowStatus_Changed_EmitsHook(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-set-workflow")
	hook := &testStatusChangeHook{ch: make(chan StatusChangeEvent, 1)}
	svc.SetStatusChangeHook(hook)

	if err := svc.SetTicketWorkflowStatus(context.Background(), tk.ID, contracts.TicketActive); err != nil {
		t.Fatalf("SetTicketWorkflowStatus failed: %v", err)
	}

	ev := waitStatusEvent(t, hook.ch)
	if ev.TicketID != tk.ID {
		t.Fatalf("unexpected ticket_id: got=%d want=%d", ev.TicketID, tk.ID)
	}
	if ev.FromStatus != contracts.TicketBacklog || ev.ToStatus != contracts.TicketActive {
		t.Fatalf("unexpected transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.Source != "pm.set_workflow" {
		t.Fatalf("unexpected source: %s", ev.Source)
	}
}

func TestSetTicketWorkflowStatus_NoChange_DoesNotEmitHook(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-no-change")
	hook := &testStatusChangeHook{ch: make(chan StatusChangeEvent, 1)}
	svc.SetStatusChangeHook(hook)

	if err := svc.SetTicketWorkflowStatus(context.Background(), tk.ID, contracts.TicketBacklog); err != nil {
		t.Fatalf("SetTicketWorkflowStatus failed: %v", err)
	}
	assertNoStatusEvent(t, hook.ch)
}

func TestApplyWorkerReport_Changed_EmitsHook(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-apply-report")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	hook := &testStatusChangeHook{ch: make(chan StatusChangeEvent, 1)}
	svc.SetStatusChangeHook(hook)

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		NextAction: string(contracts.NextContinue),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test-source"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	ev := waitStatusEvent(t, hook.ch)
	if ev.TicketID != tk.ID {
		t.Fatalf("unexpected ticket_id: got=%d want=%d", ev.TicketID, tk.ID)
	}
	if ev.FromStatus != contracts.TicketQueued || ev.ToStatus != contracts.TicketActive {
		t.Fatalf("unexpected transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.Source != "pm.apply_worker_report(test-source)" {
		t.Fatalf("unexpected source: %s", ev.Source)
	}
}

func TestApplyWorkerReport_Done_EmitsHook(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-apply-report-done")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := svc.SetTicketWorkflowStatus(context.Background(), tk.ID, contracts.TicketActive); err != nil {
		t.Fatalf("SetTicketWorkflowStatus failed: %v", err)
	}
	hook := &testStatusChangeHook{ch: make(chan StatusChangeEvent, 1)}
	svc.SetStatusChangeHook(hook)

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		Summary:    "开发完成，准备进入 merge 队列",
		NextAction: string(contracts.NextDone),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test-source"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	ev := waitStatusEvent(t, hook.ch)
	if ev.TicketID != tk.ID {
		t.Fatalf("unexpected ticket_id: got=%d want=%d", ev.TicketID, tk.ID)
	}
	if ev.FromStatus != contracts.TicketActive || ev.ToStatus != contracts.TicketDone {
		t.Fatalf("unexpected transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.WorkerID != w.ID {
		t.Fatalf("unexpected worker_id: got=%d want=%d", ev.WorkerID, w.ID)
	}
	if !strings.Contains(ev.Detail, "开发完成") {
		t.Fatalf("detail missing summary: %q", ev.Detail)
	}
	if ev.Source != "pm.apply_worker_report(test-source)" {
		t.Fatalf("unexpected source: %s", ev.Source)
	}
}

func TestApplyWorkerReport_WaitUser_EmitsHookWithBlockers(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-apply-report-wait-user")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := svc.SetTicketWorkflowStatus(context.Background(), tk.ID, contracts.TicketActive); err != nil {
		t.Fatalf("SetTicketWorkflowStatus failed: %v", err)
	}
	hook := &testStatusChangeHook{ch: make(chan StatusChangeEvent, 1)}
	svc.SetStatusChangeHook(hook)

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		Summary:    "需要你确认 API 语义",
		Blockers:   []string{"是否允许破坏性变更？", "命名规范是否采用 v2？"},
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test-source"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	ev := waitStatusEvent(t, hook.ch)
	if ev.TicketID != tk.ID {
		t.Fatalf("unexpected ticket_id: got=%d want=%d", ev.TicketID, tk.ID)
	}
	if ev.FromStatus != contracts.TicketActive || ev.ToStatus != contracts.TicketBlocked {
		t.Fatalf("unexpected transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.WorkerID != w.ID {
		t.Fatalf("unexpected worker_id: got=%d want=%d", ev.WorkerID, w.ID)
	}
	if !strings.Contains(ev.Detail, "需要你确认") || !strings.Contains(ev.Detail, "是否允许破坏性变更") {
		t.Fatalf("detail missing blockers: %q", ev.Detail)
	}
	if ev.Source != "pm.apply_worker_report(test-source)" {
		t.Fatalf("unexpected source: %s", ev.Source)
	}
}

func TestClaimPMDispatchJob_Promote_EmitsHook(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-dispatch-claim")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketQueued).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}
	w := createDispatchWorker(t, p.DB, tk.ID)
	job, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, "")
	if err != nil {
		t.Fatalf("enqueuePMDispatchJob failed: %v", err)
	}
	hook := &testStatusChangeHook{ch: make(chan StatusChangeEvent, 1)}
	svc.SetStatusChangeHook(hook)

	if _, claimed, err := svc.claimPMDispatchJob(context.Background(), job.ID, "runner-notify-claim", 2*time.Minute); err != nil {
		t.Fatalf("claimPMDispatchJob failed: %v", err)
	} else if !claimed {
		t.Fatalf("expected claimed=true")
	}

	ev := waitStatusEvent(t, hook.ch)
	if ev.TicketID != tk.ID {
		t.Fatalf("unexpected ticket_id: got=%d want=%d", ev.TicketID, tk.ID)
	}
	if ev.FromStatus != contracts.TicketQueued || ev.ToStatus != contracts.TicketActive {
		t.Fatalf("unexpected transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.Source != "pm.dispatch.claim" {
		t.Fatalf("unexpected source: %s", ev.Source)
	}
}

func TestCompletePMDispatchJobFailed_Demote_EmitsHook(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-dispatch-failed")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Update("workflow_status", contracts.TicketActive).Error; err != nil {
		t.Fatalf("set ticket active failed: %v", err)
	}
	w := createDispatchWorker(t, p.DB, tk.ID)
	job, err := svc.enqueuePMDispatchJob(context.Background(), tk.ID, w.ID, "")
	if err != nil {
		t.Fatalf("enqueuePMDispatchJob failed: %v", err)
	}
	if _, claimed, err := svc.claimPMDispatchJob(context.Background(), job.ID, "runner-notify-failed", 2*time.Minute); err != nil {
		t.Fatalf("claimPMDispatchJob failed: %v", err)
	} else if !claimed {
		t.Fatalf("expected claimed=true")
	}
	hook := &testStatusChangeHook{ch: make(chan StatusChangeEvent, 1)}
	svc.SetStatusChangeHook(hook)

	if err := svc.completePMDispatchJobFailed(context.Background(), job.ID, "runner-notify-failed", "boom"); err != nil {
		t.Fatalf("completePMDispatchJobFailed failed: %v", err)
	}

	ev := waitStatusEvent(t, hook.ch)
	if ev.TicketID != tk.ID {
		t.Fatalf("unexpected ticket_id: got=%d want=%d", ev.TicketID, tk.ID)
	}
	if ev.FromStatus != contracts.TicketActive || ev.ToStatus != contracts.TicketBlocked {
		t.Fatalf("unexpected transition: %s -> %s", ev.FromStatus, ev.ToStatus)
	}
	if ev.Source != "pm.dispatch.fail" {
		t.Fatalf("unexpected source: %s", ev.Source)
	}
}

func TestGatewayStatusNotifier_OnStatusChange_SendsMessage(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "恢复 ticket 状态主动通知")

	gatewayDBPath := filepath.Join(t.TempDir(), "gateway.db")
	gatewayDB, err := store.OpenGatewayDB(gatewayDBPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	if err := gatewayDB.Create(&contracts.ChannelBinding{
		ProjectName:    strings.TrimSpace(p.Name),
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        gatewaysendsvc.AdapterFeishu,
		PeerProjectKey: "chat_demo",
		Enabled:        true,
	}).Error; err != nil {
		t.Fatalf("create channel binding failed: %v", err)
	}
	sender := &testMessageSender{}
	notifier := NewGatewayStatusNotifier(p.Name, p.DB, gatewayDB, nil, sender)
	_ = svc // 仅复用测试项目上下文

	event := StatusChangeEvent{
		TicketID:   tk.ID,
		FromStatus: contracts.TicketActive,
		ToStatus:   contracts.TicketDone,
		Source:     "pm.apply_worker_report(worker.report)",
		OccurredAt: time.Date(2026, 2, 25, 22, 30, 0, 0, time.Local),
	}
	if err := notifier.OnStatusChange(context.Background(), event); err != nil {
		t.Fatalf("OnStatusChange failed: %v", err)
	}

	calls := sender.snapshotCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one SendCard call, got=%d", len(calls))
	}
	md := calls[0].markdown
	if !strings.Contains(md, fmt.Sprintf("t%d", tk.ID)) {
		t.Fatalf("markdown missing ticket id: %q", md)
	}
	if !strings.Contains(md, "active -> done") {
		t.Fatalf("markdown missing transition: %q", md)
	}
	if !strings.Contains(md, strings.TrimSpace(tk.Title)) {
		t.Fatalf("markdown missing title: %q", md)
	}
	if !strings.Contains(md, "pm.apply_worker_report(worker.report)") {
		t.Fatalf("markdown missing source: %q", md)
	}
}

func TestOutboxEnqueueStatusNotifier_OnStatusChange_EnqueuesPending(t *testing.T) {
	_, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "outbox enqueue notify")

	gatewayDBPath := filepath.Join(t.TempDir(), "gateway.db")
	gatewayDB, err := store.OpenGatewayDB(gatewayDBPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	if err := gatewayDB.Create(&contracts.ChannelBinding{
		ProjectName:    strings.TrimSpace(p.Name),
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        gatewaysendsvc.AdapterFeishu,
		PeerProjectKey: "chat_enqueue_demo",
		Enabled:        true,
	}).Error; err != nil {
		t.Fatalf("create channel binding failed: %v", err)
	}
	notifier := NewOutboxEnqueueStatusNotifier(p.Name, p.DB, gatewayDB)

	event := StatusChangeEvent{
		TicketID:   tk.ID,
		FromStatus: contracts.TicketActive,
		ToStatus:   contracts.TicketDone,
		Source:     "pm.apply_worker_report(worker.report)",
		OccurredAt: time.Date(2026, 2, 25, 22, 30, 0, 0, time.Local),
	}
	if err := notifier.OnStatusChange(context.Background(), event); err != nil {
		t.Fatalf("OnStatusChange failed: %v", err)
	}

	var outbox contracts.ChannelOutbox
	if err := gatewayDB.Last(&outbox).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxPending {
		t.Fatalf("outbox status should be pending, got=%s", outbox.Status)
	}

	var msg contracts.ChannelMessage
	if err := gatewayDB.First(&msg, outbox.MessageID).Error; err != nil {
		t.Fatalf("query message failed: %v", err)
	}
	if msg.Status != contracts.ChannelMessageProcessed {
		t.Fatalf("message status should be processed, got=%s", msg.Status)
	}
	if !strings.Contains(msg.ContentText, fmt.Sprintf("t%d", tk.ID)) {
		t.Fatalf("message text missing ticket id: %q", msg.ContentText)
	}
	if !strings.Contains(msg.ContentText, "active -> done") {
		t.Fatalf("message text missing transition: %q", msg.ContentText)
	}
}

func TestGatewayStatusNotifier_IgnoresNonImportantTransition(t *testing.T) {
	_, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-filter")

	gatewayDBPath := filepath.Join(t.TempDir(), "gateway.db")
	gatewayDB, err := store.OpenGatewayDB(gatewayDBPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	if err := gatewayDB.Create(&contracts.ChannelBinding{
		ProjectName:    strings.TrimSpace(p.Name),
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        gatewaysendsvc.AdapterFeishu,
		PeerProjectKey: "chat_demo",
		Enabled:        true,
	}).Error; err != nil {
		t.Fatalf("create channel binding failed: %v", err)
	}
	sender := &testMessageSender{}
	notifier := NewGatewayStatusNotifier(p.Name, p.DB, gatewayDB, nil, sender)

	err = notifier.OnStatusChange(context.Background(), StatusChangeEvent{
		TicketID:   tk.ID,
		FromStatus: contracts.TicketQueued,
		ToStatus:   contracts.TicketActive,
		Source:     "pm.dispatch.claim",
		OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("OnStatusChange failed: %v", err)
	}
	if calls := sender.snapshotCalls(); len(calls) != 0 {
		t.Fatalf("expected no SendCard call, got=%d", len(calls))
	}
}

func TestGatewayStatusNotifier_NoBinding_ReturnsNil(t *testing.T) {
	_, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "notify-no-binding")

	gatewayDBPath := filepath.Join(t.TempDir(), "gateway.db")
	gatewayDB, err := store.OpenGatewayDB(gatewayDBPath)
	if err != nil {
		t.Fatalf("OpenGatewayDB failed: %v", err)
	}
	notifier := NewGatewayStatusNotifier(p.Name, p.DB, gatewayDB, nil, &testMessageSender{})

	err = notifier.OnStatusChange(context.Background(), StatusChangeEvent{
		TicketID:   tk.ID,
		FromStatus: contracts.TicketBlocked,
		ToStatus:   contracts.TicketDone,
		Source:     "pm.set_workflow",
		OccurredAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("OnStatusChange should ignore ErrBindingNotFound, got: %v", err)
	}
}
