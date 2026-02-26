package pm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
)

func TestConsumeTaskEvents_CreatesIncidentAndNeedsUserInbox(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "manager-tick-consume-events")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	rt, run := createWorkerRunForManagerTickTest(t, svc, p, tk.ID, w.ID, "consume-events")

	if err := rt.AppendEvent(context.Background(), contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "watch_error",
		Note:      "watch failed",
	}); err != nil {
		t.Fatalf("append watch_error event failed: %v", err)
	}
	if err := rt.AppendEvent(context.Background(), contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "runtime_observation",
		ToState: map[string]any{
			"needs_user": true,
			"summary":    "需要人工确认输入参数",
		},
	}); err != nil {
		t.Fatalf("append runtime_observation event failed: %v", err)
	}

	out := svc.consumeTaskEvents(context.Background(), rt, 0)
	if out.EventsConsumed != 2 {
		t.Fatalf("expected events consumed=2, got=%d", out.EventsConsumed)
	}
	if out.InboxUpserts != 2 {
		t.Fatalf("expected inbox upserts=2, got=%d", out.InboxUpserts)
	}
	if out.NewLastEventID == 0 {
		t.Fatalf("expected new last event id > 0")
	}
	if len(out.Errors) != 0 {
		t.Fatalf("expected no consume errors, got=%v", out.Errors)
	}

	var incidentInbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyWorkerIncident(w.ID, "watch_error"), contracts.InboxOpen).
		Order("id desc").First(&incidentInbox).Error; err != nil {
		t.Fatalf("expected watch_error inbox, err=%v", err)
	}
	var needsUserInbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyNeedsUser(w.ID), contracts.InboxOpen).
		Order("id desc").First(&needsUserInbox).Error; err != nil {
		t.Fatalf("expected needs_user inbox, err=%v", err)
	}
	if needsUserInbox.Reason != contracts.InboxNeedsUser {
		t.Fatalf("expected needs_user inbox reason, got=%s", needsUserInbox.Reason)
	}
}

func TestScanRunningWorkers_TracksBlockedAndProgressable(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	blockedTicket := createTicket(t, p.DB, "manager-tick-scan-blocked")
	blockedWorker, err := svc.StartTicket(context.Background(), blockedTicket.ID)
	if err != nil {
		t.Fatalf("start blocked worker failed: %v", err)
	}
	cleanTicket := createTicket(t, p.DB, "manager-tick-scan-clean")
	cleanWorker, err := svc.StartTicket(context.Background(), cleanTicket.ID)
	if err != nil {
		t.Fatalf("start clean worker failed: %v", err)
	}

	rt, run := createWorkerRunForManagerTickTest(t, svc, p, blockedTicket.ID, blockedWorker.ID, "scan-running")
	if err := rt.AppendRuntimeSample(context.Background(), contracts.TaskRuntimeSampleInput{
		TaskRunID:  run.ID,
		State:      contracts.TaskHealthWaitingUser,
		NeedsUser:  true,
		Summary:    "等待用户输入",
		Source:     "test",
		ObservedAt: time.Now(),
	}); err != nil {
		t.Fatalf("append runtime sample failed: %v", err)
	}

	out, err := svc.scanRunningWorkers(context.Background(), p.DB, rt)
	if err != nil {
		t.Fatalf("scanRunningWorkers failed: %v", err)
	}
	if out.Running != 2 {
		t.Fatalf("expected running=2, got=%d", out.Running)
	}
	if out.RunningBlocked != 1 {
		t.Fatalf("expected running_blocked=1, got=%d", out.RunningBlocked)
	}
	if out.Progressable != 1 {
		t.Fatalf("expected progressable=1, got=%d", out.Progressable)
	}
	if !out.RunningTicketIDs[blockedTicket.ID] || !out.RunningTicketIDs[cleanTicket.ID] {
		t.Fatalf("expected running ticket ids includes both tickets, got=%v", out.RunningTicketIDs)
	}
	if out.InboxUpserts != 1 {
		t.Fatalf("expected inbox upserts=1, got=%d", out.InboxUpserts)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("expected no scan errors, got=%v", out.Errors)
	}

	var inbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyNeedsUser(blockedWorker.ID), contracts.InboxOpen).
		Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected needs_user inbox for blocked worker, err=%v", err)
	}
	if inbox.TicketID != blockedTicket.ID {
		t.Fatalf("expected inbox ticket_id=%d, got=%d", blockedTicket.ID, inbox.TicketID)
	}
	if cleanWorker.ID == 0 {
		t.Fatalf("expected clean worker created")
	}
}

func TestProposeMergesForDoneTickets_AvoidsDuplicateOpenItems(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "manager-tick-merge-proposal")
	worker, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	if worker == nil || worker.ID == 0 {
		t.Fatalf("expected started worker")
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketDone,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket done failed: %v", err)
	}

	first := svc.proposeMergesForDoneTickets(context.Background(), p.DB, false)
	if !containsTicketID(first.MergeProposed, tk.ID) {
		t.Fatalf("expected first proposal includes ticket t%d, got=%v", tk.ID, first.MergeProposed)
	}
	if len(first.Errors) != 0 {
		t.Fatalf("expected no merge proposal errors, got=%v", first.Errors)
	}

	var mergeItem contracts.MergeItem
	if err := p.DB.Where("ticket_id = ?", tk.ID).Order("id desc").First(&mergeItem).Error; err != nil {
		t.Fatalf("query merge item failed: %v", err)
	}
	var inbox contracts.InboxItem
	if err := p.DB.Where("key = ? AND status = ?", inboxKeyMergeApproval(mergeItem.ID), contracts.InboxOpen).
		Order("id desc").First(&inbox).Error; err != nil {
		t.Fatalf("expected merge approval inbox, err=%v", err)
	}

	second := svc.proposeMergesForDoneTickets(context.Background(), p.DB, false)
	if containsTicketID(second.MergeProposed, tk.ID) {
		t.Fatalf("expected second proposal skips duplicate open merge item, got=%v", second.MergeProposed)
	}

	var cnt int64
	if err := p.DB.Model(&contracts.MergeItem{}).
		Where("ticket_id = ? AND status NOT IN ?", tk.ID, mergeTerminalStatuses()).
		Count(&cnt).Error; err != nil {
		t.Fatalf("count open merge items failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected exactly one open merge item, got=%d", cnt)
	}
}

func TestScheduleQueuedTickets_StartsAndDispatchesWithSubmitter(t *testing.T) {
	svc, p, _, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "manager-tick-schedule-queued")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", tk.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set ticket queued failed: %v", err)
	}

	submitter := &stubDispatchSubmitter{}
	svc.SetDispatchSubmitter(submitter)

	runningTicketIDs := map[uint]bool{}
	out := svc.scheduleQueuedTickets(context.Background(), p.DB, scheduleOptions{
		Capacity:         1,
		RunningTicketIDs: runningTicketIDs,
	})
	if !containsTicketID(out.StartedTickets, tk.ID) {
		t.Fatalf("expected started ticket contains t%d, got=%v", tk.ID, out.StartedTickets)
	}
	if !containsTicketID(out.DispatchedTickets, tk.ID) {
		t.Fatalf("expected dispatched ticket contains t%d, got=%v", tk.ID, out.DispatchedTickets)
	}
	if len(out.Errors) != 0 {
		t.Fatalf("expected no schedule errors, got=%v", out.Errors)
	}
	callIDs := submitter.CallIDs()
	if len(callIDs) != 1 || callIDs[0] != tk.ID {
		t.Fatalf("expected submitter called once with t%d, got=%v", tk.ID, callIDs)
	}
	if !runningTicketIDs[tk.ID] {
		t.Fatalf("expected running ticket ids updated with t%d", tk.ID)
	}
}

func createWorkerRunForManagerTickTest(t *testing.T, svc *Service, p *core.Project, ticketID, workerID uint, prefix string) (core.TaskRuntime, contracts.TaskRun) {
	t.Helper()

	rt, err := svc.taskRuntimeForDB(p.DB)
	if err != nil {
		t.Fatalf("load task runtime failed: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	run, err := rt.CreateRun(context.Background(), contracts.TaskRunCreateInput{
		OwnerType:          contracts.TaskOwnerWorker,
		TaskType:           "deliver_ticket",
		ProjectKey:         p.Key,
		TicketID:           ticketID,
		WorkerID:           workerID,
		SubjectType:        "ticket",
		SubjectID:          fmt.Sprintf("%d", ticketID),
		RequestID:          fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano()),
		OrchestrationState: contracts.TaskRunning,
		StartedAt:          &now,
	})
	if err != nil {
		t.Fatalf("create worker task run failed: %v", err)
	}
	return rt, run
}
