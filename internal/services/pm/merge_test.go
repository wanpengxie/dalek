package pm

import (
	"context"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

func TestDiscardMerge_FromProposedClosesApprovalInbox(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "discard-from-proposed")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	mi, err := svc.ProposeMerge(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("ProposeMerge failed: %v", err)
	}
	if mi.Status != store.MergeProposed {
		t.Fatalf("unexpected initial status: %s", mi.Status)
	}

	if err := svc.DiscardMerge(context.Background(), mi.ID, "需求变更"); err != nil {
		t.Fatalf("DiscardMerge failed: %v", err)
	}

	var cur store.MergeItem
	if err := p.DB.First(&cur, mi.ID).Error; err != nil {
		t.Fatalf("query merge item failed: %v", err)
	}
	if cur.Status != store.MergeDiscarded {
		t.Fatalf("expected discarded, got=%s", cur.Status)
	}
	if cur.MergedAt != nil {
		t.Fatalf("discarded item should not set merged_at")
	}

	var openInbox store.InboxItem
	if err := p.DB.Where("merge_item_id = ? AND status = ?", mi.ID, store.InboxOpen).First(&openInbox).Error; err == nil {
		t.Fatalf("approval inbox should be closed after discard")
	} else if err != gorm.ErrRecordNotFound {
		t.Fatalf("query open inbox failed: %v", err)
	}

	var doneInbox store.InboxItem
	if err := p.DB.Where("merge_item_id = ? AND status = ?", mi.ID, store.InboxDone).Order("id desc").First(&doneInbox).Error; err != nil {
		t.Fatalf("expected done inbox after discard: %v", err)
	}
	if doneInbox.ClosedAt == nil {
		t.Fatalf("done inbox should have closed_at")
	}
}

func TestDiscardMerge_FromApproved(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "discard-from-approved")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	mi, err := svc.ProposeMerge(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("ProposeMerge failed: %v", err)
	}
	if err := svc.ApproveMerge(context.Background(), mi.ID, "cto"); err != nil {
		t.Fatalf("ApproveMerge failed: %v", err)
	}
	if err := svc.DiscardMerge(context.Background(), mi.ID, "撤销审批"); err != nil {
		t.Fatalf("DiscardMerge failed: %v", err)
	}

	var cur store.MergeItem
	if err := p.DB.First(&cur, mi.ID).Error; err != nil {
		t.Fatalf("query merge item failed: %v", err)
	}
	if cur.Status != store.MergeDiscarded {
		t.Fatalf("expected discarded, got=%s", cur.Status)
	}
}

func TestDiscardMerge_Idempotent(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "discard-idempotent")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	mi, err := svc.ProposeMerge(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("ProposeMerge failed: %v", err)
	}
	if err := svc.DiscardMerge(context.Background(), mi.ID, "第一次"); err != nil {
		t.Fatalf("first discard failed: %v", err)
	}
	if err := svc.DiscardMerge(context.Background(), mi.ID, "第二次"); err != nil {
		t.Fatalf("second discard should be idempotent: %v", err)
	}

	var cur store.MergeItem
	if err := p.DB.First(&cur, mi.ID).Error; err != nil {
		t.Fatalf("query merge item failed: %v", err)
	}
	if cur.Status != store.MergeDiscarded {
		t.Fatalf("expected discarded, got=%s", cur.Status)
	}
}

func TestDiscardMerge_MergedCannotDiscard(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "discard-merged")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	mi, err := svc.ProposeMerge(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("ProposeMerge failed: %v", err)
	}
	if err := svc.MarkMergeMerged(context.Background(), mi.ID); err != nil {
		t.Fatalf("MarkMergeMerged failed: %v", err)
	}

	err = svc.DiscardMerge(context.Background(), mi.ID, "晚了")
	if err == nil {
		t.Fatalf("merged item should not be discardable")
	}
	if !strings.Contains(err.Error(), "已 merged") {
		t.Fatalf("unexpected error: %v", err)
	}

	var cur store.MergeItem
	if err := p.DB.First(&cur, mi.ID).Error; err != nil {
		t.Fatalf("query merge item failed: %v", err)
	}
	if cur.Status != store.MergeMerged {
		t.Fatalf("expected merged unchanged, got=%s", cur.Status)
	}
}

func TestMarkMergeMerged_DiscardedCannotMerge(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "merged-discarded")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	mi, err := svc.ProposeMerge(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("ProposeMerge failed: %v", err)
	}
	if err := svc.DiscardMerge(context.Background(), mi.ID, "放弃"); err != nil {
		t.Fatalf("DiscardMerge failed: %v", err)
	}

	err = svc.MarkMergeMerged(context.Background(), mi.ID)
	if err == nil {
		t.Fatalf("discarded item should not be markable as merged")
	}
	if !strings.Contains(err.Error(), "已 discarded") {
		t.Fatalf("unexpected error: %v", err)
	}

	var cur store.MergeItem
	if err := p.DB.First(&cur, mi.ID).Error; err != nil {
		t.Fatalf("query merge item failed: %v", err)
	}
	if cur.Status != store.MergeDiscarded {
		t.Fatalf("expected discarded unchanged, got=%s", cur.Status)
	}
}

func TestProposeMerge_AllowsReproposeAfterDiscarded(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "propose-after-discarded")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	first, err := svc.ProposeMerge(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("first ProposeMerge failed: %v", err)
	}
	if err := svc.DiscardMerge(context.Background(), first.ID, "暂不合并"); err != nil {
		t.Fatalf("DiscardMerge failed: %v", err)
	}

	second, err := svc.ProposeMerge(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("second ProposeMerge failed: %v", err)
	}
	if second.ID == first.ID {
		t.Fatalf("re-propose should create a new merge item")
	}
	if second.Status != store.MergeProposed {
		t.Fatalf("unexpected second status: %s", second.Status)
	}

	var cnt int64
	if err := p.DB.Model(&store.MergeItem{}).Where("ticket_id = ?", tk.ID).Count(&cnt).Error; err != nil {
		t.Fatalf("count merge items failed: %v", err)
	}
	if cnt != 2 {
		t.Fatalf("expected two merge items after re-propose, got=%d", cnt)
	}
}

func TestApplyWorkerReport_DoneRecreatesMergeProposalAfterDiscarded(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "report-done-after-discarded")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}

	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		ProjectKey: strings.TrimSpace(p.Key),
		WorkerID:   w.ID,
		TicketID:   tk.ID,
		Summary:    "首次 done",
		NextAction: string(contracts.NextDone),
	}
	if err := svc.ApplyWorkerReport(context.Background(), report, "test"); err != nil {
		t.Fatalf("first ApplyWorkerReport failed: %v", err)
	}

	var first store.MergeItem
	if err := p.DB.Where("ticket_id = ?", tk.ID).Order("id desc").First(&first).Error; err != nil {
		t.Fatalf("query first merge item failed: %v", err)
	}
	if err := svc.DiscardMerge(context.Background(), first.ID, "驳回"); err != nil {
		t.Fatalf("DiscardMerge failed: %v", err)
	}

	report.Summary = "二次 done"
	if err := svc.ApplyWorkerReport(context.Background(), report, "test"); err != nil {
		t.Fatalf("second ApplyWorkerReport failed: %v", err)
	}

	var items []store.MergeItem
	if err := p.DB.Where("ticket_id = ?", tk.ID).Order("id desc").Find(&items).Error; err != nil {
		t.Fatalf("list merge items failed: %v", err)
	}
	if len(items) < 2 {
		t.Fatalf("expected second merge proposal after discarded, got=%d", len(items))
	}
	if items[0].Status != store.MergeProposed {
		t.Fatalf("latest merge item should be proposed, got=%s", items[0].Status)
	}
	if items[0].ID == first.ID {
		t.Fatalf("latest merge item should be a new one")
	}
}

func TestManagerTick_ProposesWhenOnlyDiscardedMergeExists(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)
	tk := createTicket(t, p.DB, "manager-tick-discarded-only")
	if _, err := svc.StartTicket(context.Background(), tk.ID); err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if err := p.DB.Model(&store.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": store.TicketDone,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket done failed: %v", err)
	}

	mi, err := svc.ProposeMerge(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("ProposeMerge failed: %v", err)
	}
	if err := svc.DiscardMerge(context.Background(), mi.ID, "先不合并"); err != nil {
		t.Fatalf("DiscardMerge failed: %v", err)
	}

	res, err := svc.ManagerTick(context.Background(), ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if !containsTicketID(res.MergeProposed, tk.ID) {
		t.Fatalf("manager tick should re-propose when only discarded exists, merge_proposed=%v", res.MergeProposed)
	}

	var latest store.MergeItem
	if err := p.DB.Where("ticket_id = ?", tk.ID).Order("id desc").First(&latest).Error; err != nil {
		t.Fatalf("query latest merge item failed: %v", err)
	}
	if latest.Status != store.MergeProposed {
		t.Fatalf("latest merge status should be proposed, got=%s", latest.Status)
	}
	if latest.ID == mi.ID {
		t.Fatalf("manager tick should create a new merge item")
	}
}
