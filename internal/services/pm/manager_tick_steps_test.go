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
	svc, p, _ := newServiceForTest(t)
	st, err := svc.getOrInitPMState(context.Background())
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	initialWakeVersion := st.PlannerWakeVersion

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

	out := svc.consumeTaskEvents(context.Background(), rt, st, 0)
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
	if !st.PlannerDirty {
		t.Fatalf("expected planner dirty after inbox upsert")
	}
	if st.PlannerWakeVersion != initialWakeVersion+uint(out.InboxUpserts) {
		t.Fatalf("expected planner wake version incremented by inbox upserts, got=%d", st.PlannerWakeVersion)
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
	svc, p, _ := newServiceForTest(t)
	st, err := svc.getOrInitPMState(context.Background())
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	initialWakeVersion := st.PlannerWakeVersion

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

	out, err := svc.scanRunningWorkers(context.Background(), p.DB, rt, st)
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
	if !st.PlannerDirty {
		t.Fatalf("expected planner dirty after blocked worker inbox upsert")
	}
	if st.PlannerWakeVersion != initialWakeVersion+1 {
		t.Fatalf("expected planner wake version +1, got=%d", st.PlannerWakeVersion)
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
	svc, p, _ := newServiceForTest(t)
	st, err := svc.getOrInitPMState(context.Background())
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	initialWakeVersion := st.PlannerWakeVersion

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

	first := svc.proposeMergesForDoneTickets(context.Background(), p.DB, st, false)
	if !containsTicketID(first.MergeProposed, tk.ID) {
		t.Fatalf("expected first proposal includes ticket t%d, got=%v", tk.ID, first.MergeProposed)
	}
	if len(first.Errors) != 0 {
		t.Fatalf("expected no merge proposal errors, got=%v", first.Errors)
	}
	if !st.PlannerDirty {
		t.Fatalf("expected planner dirty after merge proposal")
	}
	if st.PlannerWakeVersion != initialWakeVersion+1 {
		t.Fatalf("expected planner wake version +1 after merge proposal, got=%d", st.PlannerWakeVersion)
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

	st.PlannerDirty = false
	second := svc.proposeMergesForDoneTickets(context.Background(), p.DB, st, false)
	if containsTicketID(second.MergeProposed, tk.ID) {
		t.Fatalf("expected second proposal skips duplicate open merge item, got=%v", second.MergeProposed)
	}
	if len(second.Errors) != 0 {
		t.Fatalf("expected no merge proposal errors on second call, got=%v", second.Errors)
	}
	if !st.PlannerDirty {
		t.Fatalf("expected planner dirty after second call with existing open merge item")
	}
	if st.PlannerWakeVersion != initialWakeVersion+2 {
		t.Fatalf("expected planner wake version +2 after second call, got=%d", st.PlannerWakeVersion)
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

func TestProposeMergesForDoneTickets_ExistingOpenItemsMarkDirtyForNonTerminalStatuses(t *testing.T) {
	nonTerminalStatuses := []contracts.MergeStatus{
		contracts.MergeProposed,
		contracts.MergeChecksRunning,
		contracts.MergeReady,
		contracts.MergeApproved,
		contracts.MergeBlocked,
	}

	for _, status := range nonTerminalStatuses {
		status := status
		t.Run(string(status), func(t *testing.T) {
			svc, p, _ := newServiceForTest(t)
			st, err := svc.getOrInitPMState(context.Background())
			if err != nil {
				t.Fatalf("getOrInitPMState failed: %v", err)
			}
			initialWakeVersion := st.PlannerWakeVersion

			tk := createTicket(t, p.DB, fmt.Sprintf("manager-tick-merge-existing-%s", status))
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

			existing := contracts.MergeItem{
				Status:   status,
				TicketID: tk.ID,
				WorkerID: worker.ID,
				Branch:   worker.Branch,
			}
			if err := p.DB.Create(&existing).Error; err != nil {
				t.Fatalf("create existing merge item failed: %v", err)
			}

			st.PlannerDirty = false
			out := svc.proposeMergesForDoneTickets(context.Background(), p.DB, st, false)
			if containsTicketID(out.MergeProposed, tk.ID) {
				t.Fatalf("expected no new merge proposal for existing status=%s, got=%v", status, out.MergeProposed)
			}
			if len(out.Errors) != 0 {
				t.Fatalf("expected no merge proposal errors, got=%v", out.Errors)
			}
			if !st.PlannerDirty {
				t.Fatalf("expected planner dirty for existing status=%s", status)
			}
			if st.PlannerWakeVersion != initialWakeVersion+1 {
				t.Fatalf("expected planner wake version +1 for existing status=%s, got=%d", status, st.PlannerWakeVersion)
			}

			var cnt int64
			if err := p.DB.Model(&contracts.MergeItem{}).
				Where("ticket_id = ? AND status NOT IN ?", tk.ID, mergeTerminalStatuses()).
				Count(&cnt).Error; err != nil {
				t.Fatalf("count open merge items failed: %v", err)
			}
			if cnt != 1 {
				t.Fatalf("expected exactly one open merge item for status=%s, got=%d", status, cnt)
			}
		})
	}
}

func TestScheduleQueuedTickets_StartsAndDispatchesWithSubmitter(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

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

func TestMarkPlannerDirty_SetsDirtyAndIncrementsWakeVersion(t *testing.T) {
	svc, _, _ := newServiceForTest(t)
	st := &contracts.PMState{
		PlannerDirty:       false,
		PlannerWakeVersion: 7,
	}

	svc.markPlannerDirty(st)
	if !st.PlannerDirty {
		t.Fatalf("expected planner dirty true after first call")
	}
	if st.PlannerWakeVersion != 8 {
		t.Fatalf("expected wake version=8, got=%d", st.PlannerWakeVersion)
	}

	svc.markPlannerDirty(st)
	if st.PlannerWakeVersion != 9 {
		t.Fatalf("expected wake version=9 after second call, got=%d", st.PlannerWakeVersion)
	}
}

func TestMaybeSchedulePlannerRun_SchedulesAndPreventsDuplicateInSameState(t *testing.T) {
	svc, p, _ := newServiceForTest(t)
	st, err := svc.getOrInitPMState(context.Background())
	if err != nil {
		t.Fatalf("getOrInitPMState failed: %v", err)
	}
	st.PlannerDirty = true
	st.PlannerWakeVersion = 11

	now := time.Now().UTC().Truncate(time.Second)
	scheduled, err := svc.maybeSchedulePlannerRun(context.Background(), p.DB, st, now)
	if err != nil {
		t.Fatalf("maybeSchedulePlannerRun failed: %v", err)
	}
	if !scheduled {
		t.Fatalf("expected planner run scheduled")
	}
	if st.PlannerDirty {
		t.Fatalf("expected planner dirty cleared after scheduling")
	}
	if st.PlannerActiveTaskRunID == nil {
		t.Fatalf("expected planner active task run id set")
	}

	var run contracts.TaskRun
	if err := p.DB.First(&run, *st.PlannerActiveTaskRunID).Error; err != nil {
		t.Fatalf("load planner task run failed: %v", err)
	}
	if run.OwnerType != contracts.TaskOwnerPM {
		t.Fatalf("expected owner_type=pm, got=%s", run.OwnerType)
	}
	if run.TaskType != contracts.TaskTypePMPlannerRun {
		t.Fatalf("expected task_type=%s, got=%s", contracts.TaskTypePMPlannerRun, run.TaskType)
	}
	if run.OrchestrationState != contracts.TaskPending {
		t.Fatalf("expected orchestration_state=pending, got=%s", run.OrchestrationState)
	}
	if run.ProjectKey != p.Key {
		t.Fatalf("expected project_key=%s, got=%s", p.Key, run.ProjectKey)
	}

	scheduledAgain, err := svc.maybeSchedulePlannerRun(context.Background(), p.DB, st, now.Add(time.Second))
	if err != nil {
		t.Fatalf("second maybeSchedulePlannerRun failed: %v", err)
	}
	if scheduledAgain {
		t.Fatalf("expected second scheduling attempt skipped")
	}

	var cnt int64
	if err := p.DB.Model(&contracts.TaskRun{}).
		Where("owner_type = ? AND task_type = ?", contracts.TaskOwnerPM, contracts.TaskTypePMPlannerRun).
		Count(&cnt).Error; err != nil {
		t.Fatalf("count planner task runs failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected exactly one planner run, got=%d", cnt)
	}
}

func TestMaybeSchedulePlannerRun_SkipsWhenConditionsNotMet(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(st *contracts.PMState, now time.Time)
	}{
		{
			name: "planner not dirty",
			mutate: func(st *contracts.PMState, _ time.Time) {
				st.PlannerDirty = false
			},
		},
		{
			name: "planner already active",
			mutate: func(st *contracts.PMState, _ time.Time) {
				activeID := uint(123)
				st.PlannerActiveTaskRunID = &activeID
			},
		},
		{
			name: "cooldown not elapsed",
			mutate: func(st *contracts.PMState, now time.Time) {
				cooldown := now.Add(5 * time.Minute)
				st.PlannerCooldownUntil = &cooldown
			},
		},
		{
			name: "cooldown equals now",
			mutate: func(st *contracts.PMState, now time.Time) {
				cooldown := now
				st.PlannerCooldownUntil = &cooldown
			},
		},
		{
			name: "autopilot disabled",
			mutate: func(st *contracts.PMState, _ time.Time) {
				st.AutopilotEnabled = false
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, p, _ := newServiceForTest(t)
			st, err := svc.getOrInitPMState(context.Background())
			if err != nil {
				t.Fatalf("getOrInitPMState failed: %v", err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			st.AutopilotEnabled = true
			st.PlannerDirty = true
			st.PlannerActiveTaskRunID = nil
			st.PlannerCooldownUntil = nil

			tc.mutate(st, now)

			scheduled, err := svc.maybeSchedulePlannerRun(context.Background(), p.DB, st, now)
			if err != nil {
				t.Fatalf("maybeSchedulePlannerRun failed: %v", err)
			}
			if scheduled {
				t.Fatalf("expected planner schedule skipped")
			}

			var cnt int64
			if err := p.DB.Model(&contracts.TaskRun{}).
				Where("owner_type = ? AND task_type = ?", contracts.TaskOwnerPM, contracts.TaskTypePMPlannerRun).
				Count(&cnt).Error; err != nil {
				t.Fatalf("count planner task runs failed: %v", err)
			}
			if cnt != 0 {
				t.Fatalf("expected no planner task run created, got=%d", cnt)
			}
		})
	}
}

func TestManagerTick_SchedulesPlannerRunOnceAfterDirtyEvent(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	tk := createTicket(t, p.DB, "manager-tick-planner-schedule-once")
	w, err := svc.StartTicket(context.Background(), tk.ID)
	if err != nil {
		t.Fatalf("StartTicket failed: %v", err)
	}
	rt, run := createWorkerRunForManagerTickTest(t, svc, p, tk.ID, w.ID, "planner-run")
	if err := rt.AppendEvent(context.Background(), contracts.TaskEventInput{
		TaskRunID: run.ID,
		EventType: "watch_error",
		Note:      "watch failed and created inbox",
	}); err != nil {
		t.Fatalf("append watch_error event failed: %v", err)
	}

	first, err := svc.ManagerTick(context.Background(), ManagerTickOptions{})
	if err != nil {
		t.Fatalf("first ManagerTick failed: %v", err)
	}
	if !first.PlannerRunScheduled {
		t.Fatalf("expected first tick schedules planner run")
	}

	st, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if st.PlannerDirty {
		t.Fatalf("expected planner dirty cleared after schedule")
	}
	if st.PlannerActiveTaskRunID == nil {
		t.Fatalf("expected active planner task run id set after schedule")
	}

	var plannerRun contracts.TaskRun
	if err := p.DB.First(&plannerRun, *st.PlannerActiveTaskRunID).Error; err != nil {
		t.Fatalf("load planner run failed: %v", err)
	}
	if plannerRun.TaskType != contracts.TaskTypePMPlannerRun {
		t.Fatalf("expected planner run task type=%s, got=%s", contracts.TaskTypePMPlannerRun, plannerRun.TaskType)
	}

	second, err := svc.ManagerTick(context.Background(), ManagerTickOptions{})
	if err != nil {
		t.Fatalf("second ManagerTick failed: %v", err)
	}
	if second.PlannerRunScheduled {
		t.Fatalf("expected second tick not scheduling planner run again")
	}

	var cnt int64
	if err := p.DB.Model(&contracts.TaskRun{}).
		Where("owner_type = ? AND task_type = ?", contracts.TaskOwnerPM, contracts.TaskTypePMPlannerRun).
		Count(&cnt).Error; err != nil {
		t.Fatalf("count planner runs failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("expected exactly one planner run across two ticks, got=%d", cnt)
	}
}

type cancelingDispatchSubmitter struct {
	cancel context.CancelFunc
	called bool
}

func (s *cancelingDispatchSubmitter) SubmitTicketDispatch(_ context.Context, _ uint) error {
	if s == nil {
		return nil
	}
	s.called = true
	if s.cancel != nil {
		s.cancel()
	}
	return nil
}

func TestManagerTick_SchedulesPlannerRunAfterMergeDirtyWhenParentContextCanceled(t *testing.T) {
	svc, p, _ := newServiceForTest(t)

	doneTicket := createTicket(t, p.DB, "manager-tick-merge-dirty-context-canceled")
	doneWorker, err := svc.StartTicket(context.Background(), doneTicket.ID)
	if err != nil {
		t.Fatalf("start done ticket worker failed: %v", err)
	}
	if doneWorker == nil || doneWorker.ID == 0 {
		t.Fatalf("expected done worker created")
	}
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", doneTicket.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketDone,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set done ticket status failed: %v", err)
	}

	queuedTicket := createTicket(t, p.DB, "manager-tick-queued-cancel-parent")
	if err := p.DB.Model(&contracts.Ticket{}).Where("id = ?", queuedTicket.ID).Updates(map[string]any{
		"workflow_status": contracts.TicketQueued,
		"updated_at":      time.Now(),
	}).Error; err != nil {
		t.Fatalf("set queued ticket status failed: %v", err)
	}

	tickCtx, cancelTick := context.WithCancel(context.Background())
	defer cancelTick()
	submitter := &cancelingDispatchSubmitter{cancel: cancelTick}
	svc.SetDispatchSubmitter(submitter)

	res, err := svc.ManagerTick(tickCtx, ManagerTickOptions{})
	if err != nil {
		t.Fatalf("ManagerTick failed: %v", err)
	}
	if !submitter.called {
		t.Fatalf("expected dispatch submitter called and parent context canceled")
	}
	if !containsTicketID(res.MergeProposed, doneTicket.ID) {
		t.Fatalf("expected merge proposal for done ticket t%d, got=%v", doneTicket.ID, res.MergeProposed)
	}
	if !res.PlannerRunScheduled {
		t.Fatalf("expected planner run scheduled after merge dirty even with parent context canceled, errors=%v", res.Errors)
	}

	st, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState failed: %v", err)
	}
	if st.PlannerWakeVersion == 0 {
		t.Fatalf("expected planner wake version incremented")
	}
	if st.PlannerActiveTaskRunID == nil {
		t.Fatalf("expected planner active task run id persisted")
	}

	var plannerRun contracts.TaskRun
	if err := p.DB.First(&plannerRun, *st.PlannerActiveTaskRunID).Error; err != nil {
		t.Fatalf("load planner run failed: %v", err)
	}
	if plannerRun.TaskType != contracts.TaskTypePMPlannerRun {
		t.Fatalf("expected planner run task type=%s, got=%s", contracts.TaskTypePMPlannerRun, plannerRun.TaskType)
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
