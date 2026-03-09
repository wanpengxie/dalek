package pm

import (
	"context"
	"strings"
	"testing"

	"dalek/internal/contracts"
)

func TestApplyWorkerReport_WaitUserCreatesInboxSynchronously(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "workflow-report-wait-user")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		Summary:    "缺少生产环境 API token",
		NeedsUser:  true,
		Blockers:   []string{"请提供 FEISHU_APP_ID", "请提供 FEISHU_APP_SECRET"},
		NextAction: string(contracts.NextWaitUser),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
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
		t.Fatalf("wait_user should create inbox immediately: %v", err)
	}
	if inbox.Reason != contracts.InboxNeedsUser || inbox.Severity != contracts.InboxBlocker {
		t.Fatalf("unexpected inbox reason/severity: %s/%s", inbox.Reason, inbox.Severity)
	}
	if !strings.Contains(inbox.Body, "缺少生产环境 API token") || !strings.Contains(inbox.Body, "FEISHU_APP_ID") {
		t.Fatalf("unexpected inbox body: %q", inbox.Body)
	}
}

func TestApplyWorkerReport_DoneFreezesTicketIntegrationSynchronously(t *testing.T) {
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
		Summary:    "开发与测试已完成",
		NextAction: string(contracts.NextDone),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test"); err != nil {
		t.Fatalf("ApplyWorkerReport failed: %v", err)
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
	if strings.TrimSpace(ticket.TargetBranch) == "" {
		t.Fatalf("expected target_branch frozen on done report")
	}
}
