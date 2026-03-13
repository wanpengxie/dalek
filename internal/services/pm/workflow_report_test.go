package pm

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
)

func TestApplyWorkerReport_DoesNotAdvanceWorkflowSynchronously(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "workflow-report-runtime-only")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "wait_user", "缺少生产环境 API token", []string{"请提供 FEISHU_APP_ID", "请提供 FEISHU_APP_SECRET"}, false, testWorkerDoneHeadSHA, "clean")

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID),
		Summary:    "开发与测试已完成",
		NeedsUser:  true,
		Blockers:   []string{"请提供 FEISHU_APP_ID"},
		NextAction: string(contracts.NextDone),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketQueued {
		t.Fatalf("expected ticket queued before closure, got=%s", ticket.WorkflowStatus)
	}
	var lifecycleCount int64
	if err := p.DB.Model(&contracts.TicketLifecycleEvent{}).
		Where("ticket_id = ? AND event_type IN (?, ?)", tk.ID, contracts.TicketLifecycleWaitUserReported, contracts.TicketLifecycleDoneReported).
		Count(&lifecycleCount).Error; err != nil {
		t.Fatalf("count lifecycle failed: %v", err)
	}
	if lifecycleCount != 0 {
		t.Fatalf("expected no terminal lifecycle before closure, got=%d", lifecycleCount)
	}
}

func TestApplyWorkerLoopTerminalReport_WaitUserCreatesInboxSynchronously(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "workflow-report-wait-user")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "开发与测试已完成", nil, true, testWorkerDoneHeadSHA, "clean")

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID),
		Summary:    "缺少生产环境 API token",
		NeedsUser:  true,
		Blockers:   []string{"请提供 FEISHU_APP_ID", "请提供 FEISHU_APP_SECRET"},
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.applyWorkerLoopTerminalReport(context.Background(), report, "pm.worker_loop.closure(test:wait_user)"); err != nil {
		t.Fatalf("applyWorkerLoopTerminalReport failed: %v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketBlocked {
		t.Fatalf("expected ticket blocked, got=%s", ticket.WorkflowStatus)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyNeedsUser(w.ID), contracts.InboxOpen).Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("wait_user should create inbox during closure: %v", err)
	}
	if inbox.Reason != contracts.InboxNeedsUser || inbox.Severity != contracts.InboxBlocker {
		t.Fatalf("unexpected inbox reason/severity: %s/%s", inbox.Reason, inbox.Severity)
	}
	if !strings.Contains(inbox.Body, "缺少生产环境 API token") || !strings.Contains(inbox.Body, "FEISHU_APP_ID") {
		t.Fatalf("unexpected inbox body: %q", inbox.Body)
	}

	var lifecycle contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleWaitUserReported).Order("sequence desc").First(&lifecycle).Error; err != nil {
		t.Fatalf("wait_user should append lifecycle event: %v", err)
	}
	if lifecycle.TaskRunID == nil || *lifecycle.TaskRunID == 0 {
		t.Fatalf("wait_user lifecycle event should record task_run_id, got=%+v", lifecycle)
	}
}

func TestApplyWorkerLoopTerminalReport_DoneFreezesTicketIntegrationSynchronously(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "workflow-report-done")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID),
		HeadSHA:    testWorkerDoneHeadSHA,
		Summary:    "开发与测试已完成",
		NextAction: string(contracts.NextDone),
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "开发与测试已完成", nil, true, report.HeadSHA, "clean")
	if err := svc.applyWorkerLoopTerminalReport(context.Background(), report, "pm.worker_loop.closure(test:done)"); err != nil {
		t.Fatalf("applyWorkerLoopTerminalReport failed: %v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus != contracts.TicketDone {
		t.Fatalf("expected ticket done, got=%s", ticket.WorkflowStatus)
	}
	if got := contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus); got != contracts.IntegrationNeedsMerge {
		t.Fatalf("expected integration_status needs_merge, got=%s", got)
	}
	if strings.TrimSpace(ticket.MergeAnchorSHA) != report.HeadSHA {
		t.Fatalf("expected merge_anchor_sha from report head, got=%q want=%q", ticket.MergeAnchorSHA, report.HeadSHA)
	}
	if strings.TrimSpace(ticket.TargetBranch) != "refs/heads/main" {
		t.Fatalf("expected target_branch frozen before done report, got=%q", ticket.TargetBranch)
	}

	var lifecycle contracts.TicketLifecycleEvent
	if err := p.DB.Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleDoneReported).Order("sequence desc").First(&lifecycle).Error; err != nil {
		t.Fatalf("done should append lifecycle event: %v", err)
	}
	if lifecycle.TaskRunID == nil || *lifecycle.TaskRunID == 0 {
		t.Fatalf("done lifecycle event should record task_run_id, got=%+v", lifecycle)
	}
}

func TestApplyWorkerLoopTerminalReport_DoneRejectsDirtyWorktree(t *testing.T) {
	svc, p, git := newServiceForTest(t)
	git.WorktreeDirtyValue = true

	tk := createTicket(t, p.DB, "workflow-report-done-dirty")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "开发与测试已完成", nil, true, testWorkerDoneHeadSHA, "dirty")

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID),
		HeadSHA:    testWorkerDoneHeadSHA,
		Summary:    "开发与测试已完成",
		NextAction: string(contracts.NextDone),
	}
	err = svc.applyWorkerLoopTerminalReport(context.Background(), report, "pm.worker_loop.closure(test:done_dirty)")
	if err == nil {
		t.Fatalf("expected dirty done closure rejected")
	}
	if !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("expected dirty rejection, got=%v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("query ticket failed: %v", err)
	}
	if ticket.WorkflowStatus == contracts.TicketDone {
		t.Fatalf("dirty worktree should not promote ticket to done")
	}
}

func TestApplyWorkerLoopTerminalReport_WaitUserDuplicateDoesNotDuplicateLifecycleOrInbox(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "workflow-report-wait-user-duplicate")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "wait_user", "缺少审批结果", []string{"请确认发布窗口"}, false, testWorkerDoneHeadSHA, "clean")

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID),
		Summary:    "缺少审批结果",
		NeedsUser:  true,
		Blockers:   []string{"请确认发布窗口"},
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.applyWorkerLoopTerminalReport(context.Background(), report, "pm.worker_loop.closure(test:wait_user_dup)"); err != nil {
		t.Fatalf("first applyWorkerLoopTerminalReport failed: %v", err)
	}
	if err := svc.applyWorkerLoopTerminalReport(context.Background(), report, "pm.worker_loop.closure(test:wait_user_dup)"); err != nil {
		t.Fatalf("second applyWorkerLoopTerminalReport failed: %v", err)
	}

	var lifecycleCount int64
	if err := p.DB.Model(&contracts.TicketLifecycleEvent{}).
		Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleWaitUserReported).
		Count(&lifecycleCount).Error; err != nil {
		t.Fatalf("count wait_user lifecycle events failed: %v", err)
	}
	if lifecycleCount != 1 {
		t.Fatalf("expected exactly 1 wait_user lifecycle event, got=%d", lifecycleCount)
	}

	var inboxCount int64
	if err := p.DB.Model(&contracts.InboxItem{}).
		Where("key = ? AND status = ?", inboxKeyNeedsUser(w.ID), contracts.InboxOpen).
		Count(&inboxCount).Error; err != nil {
		t.Fatalf("count wait_user inbox failed: %v", err)
	}
	if inboxCount != 1 {
		t.Fatalf("expected exactly 1 open needs_user inbox, got=%d", inboxCount)
	}
}

func TestApplyWorkerLoopTerminalReport_DoneDuplicateDoesNotReopenNeedsMergeAfterAbandon(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "workflow-report-done-duplicate")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	writeWorkerLoopStateForTest(t, w.WorktreePath, "done", "开发完成", nil, true, testWorkerDoneHeadSHA, "clean")

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		TaskRunID:  createBoundPMWorkerRunForReport(t, svc, strings.TrimSpace(p.Key), tk.ID, w.ID),
		HeadSHA:    testWorkerDoneHeadSHA,
		Summary:    "开发完成",
		NextAction: string(contracts.NextDone),
	}
	if err := svc.applyWorkerLoopTerminalReport(context.Background(), report, "pm.worker_loop.closure(test:done_dup)"); err != nil {
		t.Fatalf("first applyWorkerLoopTerminalReport failed: %v", err)
	}
	if err := svc.AbandonTicketIntegration(context.Background(), tk.ID, "需求取消"); err != nil {
		t.Fatalf("AbandonTicketIntegration failed: %v", err)
	}
	if err := svc.applyWorkerLoopTerminalReport(context.Background(), report, "pm.worker_loop.closure(test:done_dup)"); err != nil {
		t.Fatalf("second applyWorkerLoopTerminalReport failed: %v", err)
	}

	var ticket contracts.Ticket
	if err := p.DB.First(&ticket, tk.ID).Error; err != nil {
		t.Fatalf("reload ticket failed: %v", err)
	}
	if got := contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus); got != contracts.IntegrationAbandoned {
		t.Fatalf("expected integration_status abandoned after duplicate done report, got=%s", got)
	}

	var lifecycleCount int64
	if err := p.DB.Model(&contracts.TicketLifecycleEvent{}).
		Where("ticket_id = ? AND event_type = ?", tk.ID, contracts.TicketLifecycleDoneReported).
		Count(&lifecycleCount).Error; err != nil {
		t.Fatalf("count done lifecycle events failed: %v", err)
	}
	if lifecycleCount != 1 {
		t.Fatalf("expected exactly 1 done lifecycle event, got=%d", lifecycleCount)
	}
}

func createBoundPMWorkerRunForReport(t *testing.T, svc *Service, projectKey string, ticketID, workerID uint) uint {
	t.Helper()
	rt, err := svc.taskRuntime()
	if err != nil {
		t.Fatalf("taskRuntime failed: %v", err)
	}
	now := time.Now()
	run, err := rt.CreateRun(context.Background(), contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           contracts.TaskTypeDeliverTicket,
		ProjectKey:         strings.TrimSpace(projectKey),
		TicketID:           ticketID,
		WorkerID:           workerID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", ticketID),
		RequestID:          fmt.Sprintf("test_report_t%d_w%d_%d", ticketID, workerID, now.UnixNano()),
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("create deliver_ticket run failed: %v", err)
	}
	return run.ID
}
