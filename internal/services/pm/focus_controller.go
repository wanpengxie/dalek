package pm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

const (
	focusBlockedReasonNeedsUser                    = "needs_user"
	focusBlockedReasonBudgetExhausted              = "budget_exhausted"
	focusBlockedReasonSubmitFailed                 = "submit_failed"
	focusBlockedReasonStartFailed                  = "start_failed"
	focusBlockedReasonMergeFailed                  = "merge_failed"
	focusBlockedReasonHandoffWaitingMerge          = "handoff_waiting_merge"
	focusBlockedReasonHandoffRecursionRequiresUser = "handoff_recursion_requires_user"
)

const (
	focusSubmitAcceptanceGrace = 30 * time.Second
)

type focusTicketSnapshot struct {
	Ticket    contracts.Ticket
	Worker    *contracts.Worker
	ActiveRun *contracts.TaskRun
}

type focusSubmitItemRunResult struct {
	WorkerID          uint
	RunID             uint
	NextAction        string
	UsedLocalFallback bool
}

func (s *Service) AdvanceFocusController(ctx context.Context) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	view, err := s.focusViewForDB(ctx, db, 0)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if view.Run.ID == 0 || view.Run.IsTerminal() {
		return nil
	}
	switch strings.TrimSpace(view.Run.Mode) {
	case contracts.FocusModeConvergent:
		return s.ConvergentTick(ctx, view)
	default:
		return s.FocusTick(ctx)
	}
}

func (s *Service) FocusTick(ctx context.Context) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	view, err := s.focusViewForDB(ctx, db, 0)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if view.Run.ID == 0 || view.Run.IsTerminal() {
		return nil
	}
	if view.ActiveItem != nil {
		return s.focusTickItem(ctx, view.Run, *view.ActiveItem)
	}
	if pending := focusFirstPendingItem(view.Items); pending != nil {
		if runStatus, itemStatus, ok := focusRunAndItemTerminalStatus(view.Run.DesiredState); ok {
			return s.focusSetRunTerminalWithOutstandingItems(ctx, view.Run.ID, runStatus, itemStatus)
		}
		return s.focusTickPendingItem(ctx, view.Run, *pending)
	}
	switch strings.TrimSpace(view.Run.DesiredState) {
	case contracts.FocusDesiredStopping:
		return s.focusSetRunTerminal(ctx, view.Run.ID, contracts.FocusStopped)
	case contracts.FocusDesiredCanceling:
		return s.focusSetRunTerminal(ctx, view.Run.ID, contracts.FocusCanceled)
	default:
		return s.focusSetRunTerminal(ctx, view.Run.ID, contracts.FocusCompleted)
	}
}

func (s *Service) focusTickItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem) error {
	switch strings.TrimSpace(item.Status) {
	case "", contracts.FocusItemPending, contracts.FocusItemQueued:
		return s.focusTickPendingItem(ctx, run, item)
	case contracts.FocusItemExecuting:
		return s.focusTickExecutingItem(ctx, run, item)
	case contracts.FocusItemMerging:
		return s.focusTickMergingItem(ctx, run, item)
	case contracts.FocusItemAwaitingMergeObservation:
		return s.focusTickAwaitingMergeObservation(ctx, run, item)
	case contracts.FocusItemBlocked:
		return s.focusTickBlockedItem(ctx, run, item)
	default:
		return nil
	}
}

func (s *Service) focusTickPendingItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem) error {
	switch strings.TrimSpace(run.DesiredState) {
	case contracts.FocusDesiredStopping:
		return s.focusSetRunTerminalWithOutstandingItems(ctx, run.ID, contracts.FocusStopped, contracts.FocusItemStopped)
	case contracts.FocusDesiredCanceling:
		return s.focusSetRunTerminalWithOutstandingItems(ctx, run.ID, contracts.FocusCanceled, contracts.FocusItemCanceled)
	}
	if err := s.focusAppendEvent(ctx, run.ID, item.ID, contracts.FocusEventItemSelected, "focus item selected", map[string]any{
		"ticket_id": item.TicketID,
		"seq":       item.Seq,
	}); err != nil {
		return err
	}
	snapshot, err := s.focusLoadTicketSnapshot(ctx, item.TicketID)
	if err != nil {
		return err
	}
	switch contracts.CanonicalTicketWorkflowStatus(snapshot.Ticket.WorkflowStatus) {
	case contracts.TicketDone:
		if contracts.CanonicalIntegrationStatus(snapshot.Ticket.IntegrationStatus) == contracts.IntegrationMerged {
			return s.focusCompleteItem(ctx, run, item, "")
		}
		return s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, map[string]any{
			"status":      contracts.FocusRunning,
			"finished_at": nil,
		}, map[string]any{
			"status":         contracts.FocusItemMerging,
			"started_at":     time.Now(),
			"updated_at":     time.Now(),
			"last_error":     "",
			"blocked_reason": "",
		}, "", "", nil)
	case contracts.TicketActive:
		return s.focusAdoptExecutingItem(ctx, run, item, snapshot)
	case contracts.TicketQueued:
		if snapshot.ActiveRun != nil {
			return s.focusAdoptExecutingItem(ctx, run, item, snapshot)
		}
		return s.focusQueueItem(ctx, run, item, focusWorkerID(snapshot.Worker), focusNextAttempt(item.CurrentAttempt), false, "", "", nil)
	case contracts.TicketBlocked:
		return s.focusRestartOrBlock(ctx, run, item, snapshot, focusNextAttempt(item.CurrentAttempt))
	case contracts.TicketArchived:
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonStartFailed, "ticket 已归档")
	default:
		baseBranch, berr := requiredWorkerBaseBranch(snapshot.Ticket)
		if berr != nil {
			return s.focusBlockItem(ctx, run, item, focusBlockedReasonStartFailed, berr.Error())
		}
		if _, err := s.StartTicketWithOptions(ctx, item.TicketID, StartOptions{BaseBranch: baseBranch}); err != nil {
			return s.focusBlockItem(ctx, run, item, focusBlockedReasonStartFailed, err.Error())
		}
		snapshot, err = s.focusLoadTicketSnapshot(ctx, item.TicketID)
		if err != nil {
			return err
		}
		return s.focusQueueItem(ctx, run, item, focusWorkerID(snapshot.Worker), focusNextAttempt(item.CurrentAttempt), false, contracts.FocusEventItemStartRequested, "focus item start requested", map[string]any{
			"ticket_id": item.TicketID,
			"worker_id": focusWorkerID(snapshot.Worker),
			"seq":       item.Seq,
			"attempt":   focusNextAttempt(item.CurrentAttempt),
		})
	}
}

func (s *Service) focusTickExecutingItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem) error {
	if strings.TrimSpace(run.DesiredState) == contracts.FocusDesiredCanceling {
		return s.focusCancelExecutingItem(ctx, run, item)
	}
	snapshot, err := s.focusLoadTicketSnapshot(ctx, item.TicketID)
	if err != nil {
		return err
	}
	if snapshot.ActiveRun != nil {
		if err := s.focusRefreshExecutionBinding(ctx, run.ID, item.ID, snapshot); err != nil {
			return err
		}
	} else {
		waitingAccept, err := s.focusAwaitSubmittedExecutionAcceptance(ctx, item)
		if err != nil {
			return err
		}
		if waitingAccept {
			return nil
		}
	}
	switch contracts.CanonicalTicketWorkflowStatus(snapshot.Ticket.WorkflowStatus) {
	case contracts.TicketDone:
		if contracts.CanonicalIntegrationStatus(snapshot.Ticket.IntegrationStatus) == contracts.IntegrationMerged {
			return s.focusCompleteItem(ctx, run, item, "")
		}
		return s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, map[string]any{
			"status":      contracts.FocusRunning,
			"finished_at": nil,
		}, map[string]any{
			"status":     contracts.FocusItemMerging,
			"updated_at": time.Now(),
		}, "", "", nil)
	case contracts.TicketBlocked:
		return s.focusHandleBlockedExecution(ctx, run, item, snapshot)
	case contracts.TicketQueued:
		if snapshot.ActiveRun != nil {
			return s.focusAdoptExecutingItem(ctx, run, item, snapshot)
		}
		return s.focusHandleBlockedExecution(ctx, run, item, snapshot)
	case contracts.TicketArchived:
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonStartFailed, "ticket 已归档")
	default:
		if snapshot.ActiveRun == nil && snapshot.Worker != nil {
			switch snapshot.Worker.Status {
			case contracts.WorkerStopped, contracts.WorkerFailed:
				cause, cerr := s.focusLatestTaskCancelCause(ctx, item, snapshot)
				if cerr != nil {
					return cerr
				}
				if isUserInitiatedTaskCancelCause(cause) {
					return s.focusStopItem(ctx, run, item, userInitiatedTaskCancelSummary(cause))
				}
				return s.focusHandleBlockedExecution(ctx, run, item, snapshot)
			}
		}
		return nil
	}
}

func (s *Service) focusAwaitSubmittedExecutionAcceptance(ctx context.Context, item contracts.FocusRunItem) (bool, error) {
	if item.CurrentTaskRunID == nil || *item.CurrentTaskRunID == 0 {
		return false, nil
	}
	rt, err := s.taskRuntime()
	if err != nil {
		return false, err
	}
	run, err := rt.FindRunByID(ctx, *item.CurrentTaskRunID)
	if err != nil {
		return false, err
	}
	if run != nil {
		switch run.OrchestrationState {
		case contracts.TaskSucceeded, contracts.TaskFailed, contracts.TaskCanceled:
			return false, nil
		default:
			return true, nil
		}
	}
	observedAt := item.UpdatedAt
	if item.StartedAt != nil && !item.StartedAt.IsZero() {
		observedAt = *item.StartedAt
	}
	if observedAt.IsZero() {
		return true, nil
	}
	return time.Since(observedAt) < focusSubmitAcceptanceGrace, nil
}

func (s *Service) focusTickMergingItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem) error {
	if strings.TrimSpace(run.DesiredState) == contracts.FocusDesiredCanceling {
		s.gitMergeAbort(context.WithoutCancel(ctx))
		return s.focusSetRunTerminalWithOutstandingItems(ctx, run.ID, contracts.FocusCanceled, contracts.FocusItemCanceled)
	}
	snapshot, err := s.focusLoadTicketSnapshot(ctx, item.TicketID)
	if err != nil {
		return err
	}
	if contracts.CanonicalIntegrationStatus(snapshot.Ticket.IntegrationStatus) == contracts.IntegrationMerged {
		return s.focusCompleteItem(ctx, run, item, "")
	}
	if err := s.focusAppendEvent(ctx, run.ID, item.ID, contracts.FocusEventMergeStarted, "focus merge started", map[string]any{
		"ticket_id": item.TicketID,
	}); err != nil {
		return err
	}
	workerBranch, err := s.workerBranchForTicket(ctx, item.TicketID)
	if err != nil {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonMergeFailed, err.Error())
	}
	targetRef := strings.TrimSpace(snapshot.Ticket.TargetBranch)
	if targetRef == "" {
		targetRef = s.targetBranchForTicket(ctx, item.TicketID)
	}
	normalizedTargetRef, err := normalizeIntegrationTargetRefInput(targetRef)
	if err != nil {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonMergeFailed, err.Error())
	}
	conflictTargetHeadSHA, err := s.resolveRefCommit(ctx, normalizedTargetRef)
	if err != nil {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonMergeFailed, err.Error())
	}
	result, mergeSummary, err := s.gitMergeTicketBranch(ctx, workerBranch, normalizedTargetRef)
	if err != nil {
		s.gitMergeAbort(context.WithoutCancel(ctx))
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonMergeFailed, err.Error())
	}
	mergeSummary = focusSummarizeMergeOutput(mergeSummary)
	switch result {
	case mergeSuccess:
		if cleanErr := s.gitMergeCleanError(ctx); cleanErr != nil {
			return s.focusBlockItem(ctx, run, item, focusBlockedReasonMergeFailed, cleanErr.Error())
		}
		newSHA, err := s.resolveRefCommit(ctx, normalizedTargetRef)
		if err == nil {
			_, _ = s.SyncRef(ctx, normalizedTargetRef, "", newSHA)
		}
		snapshot, err = s.focusLoadTicketSnapshot(ctx, item.TicketID)
		if err != nil {
			return err
		}
		if contracts.CanonicalIntegrationStatus(snapshot.Ticket.IntegrationStatus) == contracts.IntegrationMerged {
			if err := s.focusAppendEvent(ctx, run.ID, item.ID, contracts.FocusEventMergeObserved, "focus merge observed", map[string]any{
				"ticket_id":  item.TicketID,
				"target_ref": normalizedTargetRef,
			}); err != nil {
				return err
			}
			return s.focusCompleteItem(ctx, run, item, "")
		}
		return s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, map[string]any{
			"status":      contracts.FocusRunning,
			"finished_at": nil,
		}, map[string]any{
			"status":     contracts.FocusItemAwaitingMergeObservation,
			"updated_at": time.Now(),
		}, "", "", nil)
	case mergeConflict:
		conflictFiles := s.gitConflictFiles(ctx)
		sourceAnchorSHAs := trimNonEmptyStrings([]string{strings.TrimSpace(snapshot.Ticket.MergeAnchorSHA)})
		evidenceRefs := s.focusConflictEvidenceRefs()
		s.gitMergeAbort(context.WithoutCancel(ctx))
		mergePayload := map[string]any{
			"ticket_id":                item.TicketID,
			"conflict_files":           conflictFiles,
			"target_ref":               normalizedTargetRef,
			"conflict_target_head_sha": strings.TrimSpace(conflictTargetHeadSHA),
			"source_anchor_shas":       sourceAnchorSHAs,
			"merge_summary":            strings.TrimSpace(mergeSummary),
			"evidence_refs":            evidenceRefs,
		}
		if strings.EqualFold(strings.TrimSpace(snapshot.Ticket.Label), "integration") {
			mergePayload["blocked_reason"] = focusBlockedReasonHandoffRecursionRequiresUser
			if err := s.focusAppendEvent(ctx, run.ID, item.ID, contracts.FocusEventMergeAborted, "integration merge conflict requires user", mergePayload); err != nil {
				return err
			}
			return s.focusBlockItem(ctx, run, item, focusBlockedReasonHandoffRecursionRequiresUser, "integration ticket merge conflict requires user")
		}
		replacement, createErr := s.CreateIntegrationTicket(ctx, contracts.CreateIntegrationTicketInput{
			SourceTicketIDs:       []uint{item.TicketID},
			TargetRef:             normalizedTargetRef,
			ConflictTargetHeadSHA: strings.TrimSpace(conflictTargetHeadSHA),
			SourceAnchorSHAs:      sourceAnchorSHAs,
			ConflictFiles:         conflictFiles,
			MergeSummary:          mergeSummary,
			EvidenceRefs:          evidenceRefs,
		})
		if createErr != nil {
			mergePayload["blocked_reason"] = focusBlockedReasonMergeFailed
			mergePayload["integration_ticket_error"] = createErr.Error()
			if err := s.focusAppendEvent(ctx, run.ID, item.ID, contracts.FocusEventMergeAborted, "focus merge conflict aborted", mergePayload); err != nil {
				return err
			}
			return s.focusBlockItem(ctx, run, item, focusBlockedReasonMergeFailed, fmt.Sprintf("create integration ticket failed: %v", createErr))
		}
		mergePayload["blocked_reason"] = focusBlockedReasonHandoffWaitingMerge
		mergePayload["handoff_ticket_id"] = replacement.TicketID
		integrationPayload := map[string]any{
			"ticket_id":                item.TicketID,
			"source_ticket_ids":        []uint{item.TicketID},
			"integration_ticket_id":    replacement.TicketID,
			"handoff_ticket_id":        replacement.TicketID,
			"target_ref":               normalizedTargetRef,
			"source_anchor_shas":       sourceAnchorSHAs,
			"conflict_files":           conflictFiles,
			"conflict_target_head_sha": strings.TrimSpace(conflictTargetHeadSHA),
			"merge_summary":            mergeSummary,
			"evidence_refs":            evidenceRefs,
		}
		return s.focusBlockItemWithHandoff(
			ctx,
			run,
			item,
			replacement.TicketID,
			fmt.Sprintf("merge conflict handed off to integration ticket t%d", replacement.TicketID),
			mergePayload,
			integrationPayload,
		)
	default:
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonMergeFailed, "merge result unknown")
	}
}

func (s *Service) focusTickAwaitingMergeObservation(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem) error {
	snapshot, err := s.focusLoadTicketSnapshot(ctx, item.TicketID)
	if err != nil {
		return err
	}
	if contracts.CanonicalIntegrationStatus(snapshot.Ticket.IntegrationStatus) == contracts.IntegrationMerged {
		return s.focusCompleteItem(ctx, run, item, "")
	}
	targetRef := strings.TrimSpace(snapshot.Ticket.TargetBranch)
	if targetRef == "" {
		targetRef = s.targetBranchForTicket(ctx, item.TicketID)
	}
	newSHA, err := s.resolveRefCommit(ctx, targetRef)
	if err == nil {
		_, _ = s.SyncRef(ctx, targetRef, "", newSHA)
	}
	snapshot, err = s.focusLoadTicketSnapshot(ctx, item.TicketID)
	if err != nil {
		return err
	}
	if contracts.CanonicalIntegrationStatus(snapshot.Ticket.IntegrationStatus) == contracts.IntegrationMerged {
		if err := s.focusAppendEvent(ctx, run.ID, item.ID, contracts.FocusEventMergeObserved, "focus merge observed", map[string]any{
			"ticket_id":  item.TicketID,
			"target_ref": targetRef,
		}); err != nil {
			return err
		}
		return s.focusCompleteItem(ctx, run, item, "")
	}
	return nil
}

func (s *Service) focusTickBlockedItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem) error {
	switch strings.TrimSpace(run.DesiredState) {
	case contracts.FocusDesiredStopping:
		return s.focusSetRunTerminalWithOutstandingItems(ctx, run.ID, contracts.FocusStopped, contracts.FocusItemStopped)
	case contracts.FocusDesiredCanceling:
		return s.focusSetRunTerminalWithOutstandingItems(ctx, run.ID, contracts.FocusCanceled, contracts.FocusItemCanceled)
	}
	if strings.TrimSpace(item.BlockedReason) == focusBlockedReasonHandoffWaitingMerge {
		return s.focusResolveHandoffBlockedItem(ctx, run, item)
	}
	snapshot, err := s.focusLoadTicketSnapshot(ctx, item.TicketID)
	if err != nil {
		return err
	}
	if handled, err := s.focusConvergeBlockedItemToTicketTruth(ctx, run, item, snapshot); err != nil {
		return err
	} else if handled {
		return nil
	}
	// ticket 仍在 blocked → 保持 blocked，等 ReplyInboxItem 直接驱动恢复
	return nil
}

func (s *Service) focusConvergeBlockedItemToTicketTruth(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, snapshot focusTicketSnapshot) (bool, error) {
	switch contracts.CanonicalTicketWorkflowStatus(snapshot.Ticket.WorkflowStatus) {
	case contracts.TicketDone:
		switch contracts.CanonicalIntegrationStatus(snapshot.Ticket.IntegrationStatus) {
		case contracts.IntegrationNeedsMerge:
			now := time.Now()
			return true, s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, map[string]any{
				"status":      contracts.FocusRunning,
				"finished_at": nil,
				"updated_at":  now,
			}, map[string]any{
				"status":         contracts.FocusItemMerging,
				"started_at":     now,
				"updated_at":     now,
				"blocked_reason": "",
				"last_error":     "",
			}, "", "", nil)
		case contracts.IntegrationMerged:
			return true, s.focusCompleteItem(ctx, run, item, "")
		case contracts.IntegrationAbandoned:
			return true, s.focusCompleteItem(ctx, run, item, "ticket abandoned")
		}
	case contracts.TicketActive:
		return true, s.focusAdoptExecutingItem(ctx, run, item, snapshot)
	case contracts.TicketArchived:
		return true, s.focusCompleteItem(ctx, run, item, "ticket archived")
	}
	return false, nil
}

func (s *Service) focusResolveHandoffBlockedItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem) error {
	if item.HandoffTicketID == nil || *item.HandoffTicketID == 0 {
		return nil
	}
	replacementTicketID := *item.HandoffTicketID

	replacement, err := s.focusLoadTicketOnly(ctx, replacementTicketID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if contracts.CanonicalIntegrationStatus(replacement.IntegrationStatus) != contracts.IntegrationMerged {
		return nil
	}

	source, err := s.focusLoadTicketOnly(ctx, item.TicketID)
	if err != nil {
		return err
	}
	if source.SupersededByTicketID != nil && *source.SupersededByTicketID != 0 && *source.SupersededByTicketID != replacementTicketID {
		return fmt.Errorf("focus handoff mismatch: source ticket t%d 已被 t%d supersede，当前 handoff 指向 t%d", item.TicketID, *source.SupersededByTicketID, replacementTicketID)
	}
	if contracts.CanonicalIntegrationStatus(source.IntegrationStatus) != contracts.IntegrationAbandoned ||
		source.SupersededByTicketID == nil ||
		*source.SupersededByTicketID != replacementTicketID {
		reason := fmt.Sprintf("superseded by integration ticket t%d", replacementTicketID)
		if err := s.FinalizeTicketSuperseded(ctx, item.TicketID, replacementTicketID, reason); err != nil {
			return err
		}
	}

	return s.focusResolveHandoffItem(ctx, run, item, replacementTicketID, fmt.Sprintf("handoff resolved by integration ticket t%d", replacementTicketID))
}

func (s *Service) focusHandleBlockedExecution(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, snapshot focusTicketSnapshot) error {
	// ticket blocked = needs_user，直接 block focus item；其他情况尝试重启
	if contracts.CanonicalTicketWorkflowStatus(snapshot.Ticket.WorkflowStatus) == contracts.TicketBlocked {
		lifecycleSnapshot, err := s.RebuildTicketLifecycleSnapshot(ctx, item.TicketID)
		if err != nil {
			return err
		}
		if lifecycleSnapshot.Explanation != nil && isUserInitiatedBlockedReason(lifecycleSnapshot.Explanation.BlockedReason) {
			reason := strings.TrimSpace(lifecycleSnapshot.Explanation.Reason)
			if reason == "" {
				reason = "用户主动停止 ticket"
			}
			return s.focusStopItem(ctx, run, item, reason)
		}
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonNeedsUser, "ticket 需要用户介入")
	}
	return s.focusRestartOrBlock(ctx, run, item, snapshot, item.CurrentAttempt+1)
}

func (s *Service) focusLatestTaskCancelCause(ctx context.Context, item contracts.FocusRunItem, snapshot focusTicketSnapshot) (contracts.TaskCancelCause, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.TaskCancelCauseUnknown, err
	}
	taskRunID := focusTaskRunID(snapshot.ActiveRun)
	if taskRunID == 0 && item.CurrentTaskRunID != nil {
		taskRunID = *item.CurrentTaskRunID
	}
	return latestTaskCancelCause(ctx, db, focusWorkerID(snapshot.Worker), taskRunID)
}

func (s *Service) focusStopItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, reason string) error {
	now := time.Now()
	if err := s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, map[string]any{
		"status":      contracts.FocusRunning,
		"finished_at": nil,
		"updated_at":  now,
	}, map[string]any{
		"status":         contracts.FocusItemStopped,
		"finished_at":    &now,
		"updated_at":     now,
		"blocked_reason": "",
		"last_error":     strings.TrimSpace(reason),
	}, "", "", nil); err != nil {
		return err
	}
	return s.focusPromoteNextPendingItem(ctx, run.ID)
}

func (s *Service) focusRestartOrBlock(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, snapshot focusTicketSnapshot, nextAttempt int) error {
	if nextAttempt <= 0 {
		nextAttempt = 1
	}
	if focusRemainingBudget(run) <= 0 {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonBudgetExhausted, focusBudgetExhaustedMessage(run, nextAttempt))
	}
	baseBranch, berr := requiredWorkerBaseBranch(snapshot.Ticket)
	if berr != nil {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonStartFailed, berr.Error())
	}
	if _, err := s.StartTicketWithOptions(ctx, item.TicketID, StartOptions{BaseBranch: baseBranch}); err != nil {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonStartFailed, err.Error())
	}
	reloaded, loadErr := s.focusLoadTicketSnapshot(ctx, item.TicketID)
	if loadErr != nil {
		return loadErr
	}
	snapshot = reloaded
	return s.focusQueueItem(ctx, run, item, focusWorkerID(snapshot.Worker), nextAttempt, true, contracts.FocusEventItemRestarted, "focus item restarted", map[string]any{
		"ticket_id": item.TicketID,
		"worker_id": focusWorkerID(snapshot.Worker),
		"attempt":   nextAttempt,
	})
}

func (s *Service) focusSubmitItemRun(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, worker *contracts.Worker, eventKind, eventSummary string, attempt int, prompt string, consumeInbox *contracts.InboxItem) (focusSubmitItemRunResult, error) {
	submitter := s.getWorkerRunSubmitter()
	ticket, err := s.focusLoadTicketOnly(ctx, item.TicketID)
	if err != nil {
		return focusSubmitItemRunResult{}, s.focusBlockItem(ctx, run, item, focusBlockedReasonSubmitFailed, err.Error())
	}
	baseBranch, err := requiredWorkerBaseBranch(ticket)
	if err != nil {
		return focusSubmitItemRunResult{}, s.focusBlockItem(ctx, run, item, focusBlockedReasonSubmitFailed, err.Error())
	}
	if submitter == nil {
		autoStart := false
		runResult, err := s.RunTicketWorker(ctx, item.TicketID, WorkerRunOptions{
			EntryPrompt: strings.TrimSpace(prompt),
			AutoStart:   &autoStart,
			BaseBranch:  baseBranch,
		})
		if err != nil {
			return focusSubmitItemRunResult{}, err
		}
		workerID := runResult.WorkerID
		if workerID == 0 && worker != nil {
			workerID = worker.ID
		}
		if workerID == 0 && item.CurrentWorkerID != nil {
			workerID = *item.CurrentWorkerID
		}
		if strings.TrimSpace(eventKind) != "" {
			eventPayload := map[string]any{
				"ticket_id":   item.TicketID,
				"worker_id":   workerID,
				"task_run_id": runResult.RunID,
				"attempt":     attempt,
				"next_action": strings.TrimSpace(runResult.LastNextAction),
			}
			if consumeInbox != nil {
				for key, value := range inboxReplyAuditPayload(*consumeInbox, consumeInbox.ReplyAction, consumeInbox.ReplyMarkdown) {
					eventPayload[key] = value
				}
			}
			if err := s.focusAppendEvent(ctx, run.ID, item.ID, eventKind, eventSummary, eventPayload); err != nil {
				return focusSubmitItemRunResult{}, err
			}
		}
		return focusSubmitItemRunResult{
			WorkerID:          workerID,
			RunID:             runResult.RunID,
			NextAction:        strings.TrimSpace(runResult.LastNextAction),
			UsedLocalFallback: true,
		}, nil
	}
	submission, err := submitter.SubmitTicketWorkerRun(context.WithoutCancel(ctx), item.TicketID, WorkerRunSubmitOptions{
		BaseBranch: baseBranch,
		Prompt:     strings.TrimSpace(prompt),
	})
	if err != nil {
		return focusSubmitItemRunResult{}, s.focusBlockItem(ctx, run, item, focusBlockedReasonSubmitFailed, err.Error())
	}
	workerID := submission.WorkerID
	if workerID == 0 && worker != nil {
		workerID = worker.ID
	}
	now := time.Now()
	itemUpdates := map[string]any{
		"status":              contracts.FocusItemExecuting,
		"current_attempt":     attempt,
		"current_task_run_id": submission.TaskRunID,
		"blocked_reason":      "",
		"last_error":          "",
		"started_at":          now,
		"updated_at":          now,
	}
	if workerID != 0 {
		itemUpdates["current_worker_id"] = workerID
	}
	_, db, err := s.require()
	if err != nil {
		return focusSubmitItemRunResult{}, err
	}
	eventPayload := map[string]any{
		"ticket_id":   item.TicketID,
		"worker_id":   workerID,
		"task_run_id": submission.TaskRunID,
		"attempt":     attempt,
	}
	if consumeInbox != nil {
		for key, value := range inboxReplyAuditPayload(*consumeInbox, consumeInbox.ReplyAction, consumeInbox.ReplyMarkdown) {
			eventPayload[key] = value
		}
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":      contracts.FocusRunning,
			"finished_at": nil,
			"updated_at":  now,
		}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.FocusRunItem{}).Where("id = ? AND focus_run_id = ?", item.ID, run.ID).Updates(itemUpdates).Error; err != nil {
			return err
		}
		if consumeInbox != nil && consumeInbox.ID != 0 {
			if err := tx.WithContext(ctx).Model(&contracts.InboxItem{}).Where("id = ? AND reason = ?", consumeInbox.ID, contracts.InboxNeedsUser).Updates(map[string]any{
				"status":            contracts.InboxDone,
				"closed_at":         &now,
				"reply_consumed_at": &now,
				"updated_at":        now,
			}).Error; err != nil {
				return err
			}
		}
		if strings.TrimSpace(eventKind) == "" {
			return nil
		}
		_, err := appendFocusEventTx(ctx, tx, run.ID, &item.ID, eventKind, eventSummary, eventPayload, now)
		return err
	})
	if err != nil {
		return focusSubmitItemRunResult{}, err
	}
	s.projectWake()
	return focusSubmitItemRunResult{
		WorkerID: workerID,
		RunID:    submission.TaskRunID,
	}, nil
}

func (s *Service) focusAdoptExecutingItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, snapshot focusTicketSnapshot) error {
	now := time.Now()
	itemUpdates := map[string]any{
		"status":          contracts.FocusItemExecuting,
		"current_attempt": focusNextAttempt(item.CurrentAttempt),
		"blocked_reason":  "",
		"last_error":      "",
		"started_at":      now,
		"updated_at":      now,
	}
	if snapshot.Worker != nil {
		itemUpdates["current_worker_id"] = snapshot.Worker.ID
	}
	if snapshot.ActiveRun != nil {
		itemUpdates["current_task_run_id"] = snapshot.ActiveRun.ID
	}
	return s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, map[string]any{
		"status":      contracts.FocusRunning,
		"finished_at": nil,
	}, itemUpdates, contracts.FocusEventItemAdopted, "focus item adopted existing execution", map[string]any{
		"ticket_id":   item.TicketID,
		"worker_id":   focusWorkerID(snapshot.Worker),
		"task_run_id": focusTaskRunID(snapshot.ActiveRun),
	})
}

func (s *Service) focusQueueItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, workerID uint, attempt int, consumeRestartBudget bool, eventKind, eventSummary string, payload any) error {
	now := time.Now()
	runUpdates := map[string]any{
		"status":      contracts.FocusRunning,
		"finished_at": nil,
	}
	if consumeRestartBudget {
		runUpdates["agent_budget"] = gorm.Expr("CASE WHEN agent_budget > 0 THEN agent_budget - 1 ELSE 0 END")
	}
	itemUpdates := map[string]any{
		"status":              contracts.FocusItemQueued,
		"current_attempt":     focusNextAttempt(attempt),
		"current_task_run_id": nil,
		"blocked_reason":      "",
		"last_error":          "",
		"started_at":          now,
		"updated_at":          now,
	}
	if workerID != 0 {
		itemUpdates["current_worker_id"] = workerID
	}
	if err := s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, runUpdates, itemUpdates, eventKind, eventSummary, payload); err != nil {
		return err
	}
	s.KickQueueConsumer()
	return nil
}

func (s *Service) focusCompleteItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, reason string) error {
	runStatus := contracts.FocusRunning
	switch strings.TrimSpace(run.DesiredState) {
	case contracts.FocusDesiredStopping:
		runStatus = contracts.FocusStopped
	case contracts.FocusDesiredCanceling:
		runStatus = contracts.FocusCanceled
	}
	now := time.Now()
	runUpdates := map[string]any{
		"status":     runStatus,
		"updated_at": now,
	}
	if runStatus == contracts.FocusStopped || runStatus == contracts.FocusCanceled {
		runUpdates["finished_at"] = &now
	} else {
		runUpdates["finished_at"] = nil
	}
	if err := s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, runUpdates, map[string]any{
		"status":         contracts.FocusItemCompleted,
		"finished_at":    &now,
		"updated_at":     now,
		"blocked_reason": "",
		"last_error":     strings.TrimSpace(reason),
	}, contracts.FocusEventItemCompleted, "focus item completed", map[string]any{
		"ticket_id": item.TicketID,
		"reason":    strings.TrimSpace(reason),
	}); err != nil {
		return err
	}
	if runStatus != contracts.FocusRunning {
		return s.focusFinalizeRemainingItems(ctx, run.ID, focusTerminalItemStatusForRun(runStatus))
	}
	return s.focusPromoteNextPendingItem(ctx, run.ID)
}

func (s *Service) focusResolveHandoffItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, replacementTicketID uint, reason string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	runStatus := contracts.FocusRunning
	switch strings.TrimSpace(run.DesiredState) {
	case contracts.FocusDesiredStopping:
		runStatus = contracts.FocusStopped
	case contracts.FocusDesiredCanceling:
		runStatus = contracts.FocusCanceled
	}
	now := time.Now()
	runUpdates := map[string]any{
		"status":     runStatus,
		"updated_at": now,
	}
	if runStatus == contracts.FocusStopped || runStatus == contracts.FocusCanceled {
		runUpdates["finished_at"] = &now
	} else {
		runUpdates["finished_at"] = nil
	}
	itemUpdates := map[string]any{
		"status":         contracts.FocusItemCompleted,
		"finished_at":    &now,
		"updated_at":     now,
		"blocked_reason": "",
		"last_error":     strings.TrimSpace(reason),
	}
	itemID := item.ID
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(runUpdates).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.FocusRunItem{}).Where("id = ? AND focus_run_id = ?", item.ID, run.ID).Updates(itemUpdates).Error; err != nil {
			return err
		}
		if runStatus != contracts.FocusRunning {
			if err := focusFinalizeRemainingItemsTx(ctx, tx, run.ID, item.ID, focusTerminalItemStatusForRun(runStatus), now); err != nil {
				return err
			}
		}
		if _, err := appendFocusEventTx(ctx, tx, run.ID, &itemID, contracts.FocusEventHandoffResolved, "focus handoff resolved", map[string]any{
			"ticket_id":             item.TicketID,
			"handoff_ticket_id":     replacementTicketID,
			"replacement_ticket_id": replacementTicketID,
			"reason":                strings.TrimSpace(reason),
		}, now); err != nil {
			return err
		}
		if _, err := appendFocusEventTx(ctx, tx, run.ID, &itemID, contracts.FocusEventItemCompleted, "focus item completed", map[string]any{
			"ticket_id": item.TicketID,
			"reason":    strings.TrimSpace(reason),
		}, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.projectWake()
	if runStatus != contracts.FocusRunning {
		return nil
	}
	return s.focusPromoteNextPendingItem(ctx, run.ID)
}

func (s *Service) focusBlockItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, reason, lastError string) error {
	now := time.Now()
	return s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, map[string]any{
		"status":     contracts.FocusBlocked,
		"updated_at": now,
	}, map[string]any{
		"status":         contracts.FocusItemBlocked,
		"blocked_reason": strings.TrimSpace(reason),
		"last_error":     strings.TrimSpace(lastError),
		"updated_at":     now,
	}, contracts.FocusEventItemBlocked, "focus item blocked", map[string]any{
		"ticket_id":      item.TicketID,
		"blocked_reason": strings.TrimSpace(reason),
		"error":          strings.TrimSpace(lastError),
	})
}

func (s *Service) focusBlockItemWithHandoff(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, replacementTicketID uint, lastError string, mergePayload, integrationPayload map[string]any) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if replacementTicketID == 0 {
		return fmt.Errorf("replacement ticket_id 不能为空")
	}
	now := time.Now()
	itemID := item.ID
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", run.ID).Updates(map[string]any{
			"status":     contracts.FocusBlocked,
			"updated_at": now,
		}).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.FocusRunItem{}).Where("id = ? AND focus_run_id = ?", item.ID, run.ID).Updates(map[string]any{
			"status":            contracts.FocusItemBlocked,
			"blocked_reason":    focusBlockedReasonHandoffWaitingMerge,
			"last_error":        strings.TrimSpace(lastError),
			"handoff_ticket_id": replacementTicketID,
			"updated_at":        now,
		}).Error; err != nil {
			return err
		}
		if _, err := appendFocusEventTx(ctx, tx, run.ID, &itemID, contracts.FocusEventMergeAborted, "focus merge conflict handed off", mergePayload, now); err != nil {
			return err
		}
		if _, err := appendFocusEventTx(ctx, tx, run.ID, &itemID, contracts.FocusEventIntegrationCreated, "focus integration ticket created", integrationPayload, now); err != nil {
			return err
		}
		if _, err := appendFocusEventTx(ctx, tx, run.ID, &itemID, contracts.FocusEventItemBlocked, "focus item blocked", map[string]any{
			"ticket_id":         item.TicketID,
			"blocked_reason":    focusBlockedReasonHandoffWaitingMerge,
			"error":             strings.TrimSpace(lastError),
			"handoff_ticket_id": replacementTicketID,
		}, now); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

func (s *Service) focusTerminalizeItem(ctx context.Context, runID, itemID uint, runStatus, itemStatus string) error {
	return s.focusTerminateRun(ctx, runID, 0, runStatus, itemStatus)
}

func (s *Service) focusSetRunTerminal(ctx context.Context, runID uint, status string) error {
	itemStatus := focusTerminalItemStatusForRun(status)
	if itemStatus == "" {
		now := time.Now()
		return s.focusUpdateRunAndItem(ctx, runID, nil, map[string]any{
			"status":      strings.TrimSpace(status),
			"updated_at":  now,
			"finished_at": &now,
		}, nil, "", "", nil)
	}
	return s.focusTerminateRun(ctx, runID, 0, status, itemStatus)
}

func (s *Service) focusSetRunTerminalWithOutstandingItems(ctx context.Context, runID uint, runStatus, itemStatus string) error {
	return s.focusTerminateRun(ctx, runID, 0, runStatus, itemStatus)
}

func (s *Service) focusTerminateRun(ctx context.Context, runID, itemID uint, runStatus, itemStatus string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", runID).Updates(map[string]any{
			"status":      strings.TrimSpace(runStatus),
			"updated_at":  now,
			"finished_at": &now,
		}).Error; err != nil {
			return err
		}
		if itemID != 0 {
			if err := tx.WithContext(ctx).
				Model(&contracts.FocusRunItem{}).
				Where("id = ? AND focus_run_id = ?", itemID, runID).
				Updates(map[string]any{
					"status":      strings.TrimSpace(itemStatus),
					"updated_at":  now,
					"finished_at": &now,
				}).Error; err != nil {
				return err
			}
		}
		return focusFinalizeRemainingItemsTx(ctx, tx, runID, itemID, itemStatus, now)
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

func (s *Service) focusFinalizeRemainingItems(ctx context.Context, runID uint, itemStatus string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return focusFinalizeRemainingItemsTx(ctx, tx, runID, 0, itemStatus, now)
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

func focusFinalizeRemainingItemsTx(ctx context.Context, tx *gorm.DB, runID, excludeItemID uint, itemStatus string, now time.Time) error {
	if tx == nil || strings.TrimSpace(itemStatus) == "" {
		return nil
	}
	query := tx.WithContext(ctx).
		Model(&contracts.FocusRunItem{}).
		Where("focus_run_id = ? AND status NOT IN ?", runID, focusTerminalItemStatuses())
	if excludeItemID != 0 {
		query = query.Where("id <> ?", excludeItemID)
	}
	return query.Updates(map[string]any{
		"status":         strings.TrimSpace(itemStatus),
		"updated_at":     now,
		"finished_at":    &now,
		"blocked_reason": "",
	}).Error
}

func focusTerminalItemStatusForRun(runStatus string) string {
	switch strings.TrimSpace(runStatus) {
	case contracts.FocusStopped:
		return contracts.FocusItemStopped
	case contracts.FocusCanceled:
		return contracts.FocusItemCanceled
	default:
		return ""
	}
}

func focusRunAndItemTerminalStatus(desiredState string) (string, string, bool) {
	switch strings.TrimSpace(desiredState) {
	case contracts.FocusDesiredStopping:
		return contracts.FocusStopped, contracts.FocusItemStopped, true
	case contracts.FocusDesiredCanceling:
		return contracts.FocusCanceled, contracts.FocusItemCanceled, true
	default:
		return "", "", false
	}
}

func focusTerminalItemStatuses() []string {
	return []string{
		contracts.FocusItemCompleted,
		contracts.FocusItemFailed,
		contracts.FocusItemStopped,
		contracts.FocusItemCanceled,
	}
}

func (s *Service) focusUpdateRunAndItem(ctx context.Context, runID uint, itemID *uint, runUpdates, itemUpdates map[string]any, eventKind, eventSummary string, payload any) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	if runUpdates != nil {
		if _, ok := runUpdates["updated_at"]; !ok {
			runUpdates["updated_at"] = now
		}
	}
	if itemUpdates != nil {
		if _, ok := itemUpdates["updated_at"]; !ok {
			itemUpdates["updated_at"] = now
		}
	}
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if runUpdates != nil {
			if err := tx.WithContext(ctx).Model(&contracts.FocusRun{}).Where("id = ?", runID).Updates(runUpdates).Error; err != nil {
				return err
			}
		}
		if itemID != nil && itemUpdates != nil {
			if err := tx.WithContext(ctx).Model(&contracts.FocusRunItem{}).Where("id = ? AND focus_run_id = ?", *itemID, runID).Updates(itemUpdates).Error; err != nil {
				return err
			}
		}
		if strings.TrimSpace(eventKind) != "" {
			_, err := appendFocusEventTx(ctx, tx, runID, itemID, strings.TrimSpace(eventKind), strings.TrimSpace(eventSummary), payload, now)
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.projectWake()
	return nil
}

func (s *Service) focusAppendEvent(ctx context.Context, runID, itemID uint, kind, summary string, payload any) error {
	var itemPtr *uint
	if itemID != 0 {
		itemPtr = &itemID
	}
	return s.focusUpdateRunAndItem(ctx, runID, itemPtr, nil, nil, kind, summary, payload)
}

func (s *Service) focusRefreshExecutionBinding(ctx context.Context, runID, itemID uint, snapshot focusTicketSnapshot) error {
	itemUpdates := map[string]any{
		"updated_at": time.Now(),
	}
	if snapshot.Worker != nil {
		itemUpdates["current_worker_id"] = snapshot.Worker.ID
	}
	if snapshot.ActiveRun != nil {
		itemUpdates["current_task_run_id"] = snapshot.ActiveRun.ID
	}
	return s.focusUpdateRunAndItem(ctx, runID, &itemID, nil, itemUpdates, "", "", nil)
}

func (s *Service) focusLoadTicketSnapshot(ctx context.Context, ticketID uint) (focusTicketSnapshot, error) {
	_, db, err := s.require()
	if err != nil {
		return focusTicketSnapshot{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var ticket contracts.Ticket
	if err := db.WithContext(ctx).First(&ticket, ticketID).Error; err != nil {
		return focusTicketSnapshot{}, err
	}
	var workerPtr *contracts.Worker
	workerRef, err := s.worker.LatestWorker(ctx, ticketID)
	if err != nil {
		return focusTicketSnapshot{}, err
	}
	if workerRef != nil {
		workerCopy := *workerRef
		workerPtr = &workerCopy
	}
	var activeRun *contracts.TaskRun
	if workerPtr != nil {
		rt, err := s.taskRuntimeForDB(db)
		if err != nil {
			return focusTicketSnapshot{}, err
		}
		run, err := rt.LatestActiveWorkerRun(ctx, workerPtr.ID)
		if err != nil {
			return focusTicketSnapshot{}, err
		}
		activeRun = run
	}
	return focusTicketSnapshot{
		Ticket:    ticket,
		Worker:    workerPtr,
		ActiveRun: activeRun,
	}, nil
}

func (s *Service) focusLoadTicketOnly(ctx context.Context, ticketID uint) (contracts.Ticket, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.Ticket{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var ticket contracts.Ticket
	if err := db.WithContext(ctx).First(&ticket, ticketID).Error; err != nil {
		return contracts.Ticket{}, err
	}
	return ticket, nil
}

func (s *Service) focusCancelExecutingItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem) error {
	if err := s.focusRequestCancel(ctx, item); err != nil {
		return err
	}
	snapshot, err := s.focusLoadTicketSnapshot(ctx, item.TicketID)
	if err != nil {
		return err
	}
	if item.CurrentTaskRunID != nil && *item.CurrentTaskRunID != 0 {
		terminal, err := s.focusTaskRunTerminal(ctx, *item.CurrentTaskRunID)
		if err != nil {
			return err
		}
		if !terminal {
			return nil
		}
	}
	if snapshot.ActiveRun != nil {
		return nil
	}
	if snapshot.Worker != nil && snapshot.Worker.Status == contracts.WorkerRunning {
		return nil
	}
	return s.focusTerminalizeItem(ctx, run.ID, item.ID, contracts.FocusCanceled, contracts.FocusItemCanceled)
}

func (s *Service) focusRequestCancel(ctx context.Context, item contracts.FocusRunItem) error {
	ctrl := s.getFocusLoopControl()
	if ctrl != nil {
		if err := ctrl.CancelTicketLoop(context.WithoutCancel(ctx), item.TicketID); err != nil {
			return err
		}
	}
	if item.CurrentTaskRunID != nil && *item.CurrentTaskRunID != 0 {
		if ctrl != nil {
			if err := ctrl.CancelTaskRun(context.WithoutCancel(ctx), *item.CurrentTaskRunID, contracts.TaskCancelCauseFocusCancel); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) focusMarkTaskRunCanceled(ctx context.Context, runID uint, reason string) error {
	rt, err := s.taskRuntime()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := rt.FindRunByID(ctx, runID)
	if err != nil || run == nil {
		return err
	}
	switch run.OrchestrationState {
	case contracts.TaskSucceeded, contracts.TaskFailed, contracts.TaskCanceled:
		return nil
	}
	now := time.Now()
	if err := rt.MarkRunCanceled(ctx, runID, "focus_cancel", strings.TrimSpace(reason), now); err != nil {
		return err
	}
	return rt.AppendEvent(ctx, contracts.TaskEventInput{
		TaskRunID: runID,
		EventType: "task_canceled",
		FromState: map[string]any{
			"orchestration_state": run.OrchestrationState,
		},
		ToState: map[string]any{
			"orchestration_state": contracts.TaskCanceled,
		},
		Note:      strings.TrimSpace(reason),
		Payload:   map[string]any{"source": "pm.focus"},
		CreatedAt: now,
	})
}

func (s *Service) focusTaskRunTerminal(ctx context.Context, runID uint) (bool, error) {
	rt, err := s.taskRuntime()
	if err != nil {
		return false, err
	}
	run, err := rt.FindRunByID(ctx, runID)
	if err != nil || run == nil {
		return false, err
	}
	switch run.OrchestrationState {
	case contracts.TaskSucceeded, contracts.TaskFailed, contracts.TaskCanceled:
		return true, nil
	default:
		return false, nil
	}
}

func (s *Service) focusRepairTicketWorkflow(ctx context.Context, ticketID uint, target contracts.TicketWorkflowStatus, reason string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ticketID == 0 || target == "" {
		return nil
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "focus workflow repair"
	}
	var statusEvent *StatusChangeEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ticket contracts.Ticket
		if err := tx.WithContext(ctx).First(&ticket, ticketID).Error; err != nil {
			return err
		}
		from := contracts.CanonicalTicketWorkflowStatus(ticket.WorkflowStatus)
		target = contracts.CanonicalTicketWorkflowStatus(target)
		if from == target {
			return nil
		}
		now := time.Now()
		lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
			TicketID:       ticketID,
			EventType:      contracts.TicketLifecycleRepaired,
			Source:         "pm.focus",
			ActorType:      contracts.TicketLifecycleActorSystem,
			IdempotencyKey: ticketlifecycle.RepairedIdempotencyKey(ticketID, "pm.focus."+string(target), now),
			Payload: lifecycleRepairPayload(target, contracts.CanonicalIntegrationStatus(ticket.IntegrationStatus), map[string]any{
				"ticket_id": ticketID,
				"reason":    reason,
			}),
			CreatedAt: now,
		})
		if err != nil {
			return err
		}
		if !lifecycleResult.WorkflowChanged() {
			return nil
		}
		if err := s.appendTicketWorkflowEventTx(ctx, tx, ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.focus", reason, map[string]any{
			"ticket_id": ticketID,
			"reason":    reason,
		}, now); err != nil {
			return err
		}
		statusEvent = s.buildStatusChangeEvent(ticketID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, "pm.focus", now)
		return nil
	})
	if err != nil {
		return err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return nil
}

func (s *Service) focusManagedTicketIDs(ctx context.Context, db *gorm.DB) (map[uint]struct{}, error) {
	if db == nil {
		return map[uint]struct{}{}, fmt.Errorf("db 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	view, err := s.focusViewForDB(ctx, db, 0)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return map[uint]struct{}{}, nil
	case err != nil:
		return nil, err
	case view.Run.ID == 0 || view.Run.IsTerminal():
		return map[uint]struct{}{}, nil
	}
	allowedQueuedTicketID := uint(0)
	if strings.TrimSpace(view.Run.DesiredState) == contracts.FocusDesiredRunning &&
		view.ActiveItem != nil &&
		strings.TrimSpace(view.ActiveItem.Status) == contracts.FocusItemQueued {
		allowedQueuedTicketID = view.ActiveItem.TicketID
	}
	out := make(map[uint]struct{}, len(view.Items))
	for _, item := range view.Items {
		if item.TicketID == 0 || focusItemTerminalStatus(item.Status) {
			continue
		}
		if allowedQueuedTicketID != 0 && item.TicketID == allowedQueuedTicketID {
			continue
		}
		out[item.TicketID] = struct{}{}
	}
	return out, nil
}

func (s *Service) focusAllowsQueuedActivation(ctx context.Context, db *gorm.DB, ticketID uint) (bool, bool, error) {
	if db == nil || ticketID == 0 {
		return true, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	view, err := s.focusViewForDB(ctx, db, 0)
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return true, false, nil
	case err != nil:
		return false, false, err
	case view.Run.ID == 0 || view.Run.IsTerminal():
		return true, false, nil
	}
	inScope := false
	for _, item := range view.Items {
		if item.TicketID == ticketID && !focusItemTerminalStatus(item.Status) {
			inScope = true
			break
		}
	}
	if !inScope {
		return true, false, nil
	}
	if strings.TrimSpace(view.Run.DesiredState) != contracts.FocusDesiredRunning {
		return false, true, nil
	}
	if view.ActiveItem != nil &&
		view.ActiveItem.TicketID == ticketID &&
		strings.TrimSpace(view.ActiveItem.Status) == contracts.FocusItemQueued {
		return true, false, nil
	}
	return false, true, nil
}

func (s *Service) focusPromoteNextPendingItem(ctx context.Context, runID uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	view, err := s.focusViewForDB(ctx, db, runID)
	if err != nil {
		return err
	}
	if view.Run.IsTerminal() || strings.TrimSpace(view.Run.DesiredState) != contracts.FocusDesiredRunning || view.ActiveItem != nil {
		return nil
	}
	next := focusFirstPendingItem(view.Items)
	if next == nil {
		return nil
	}
	return s.focusTickPendingItem(ctx, view.Run, *next)
}

func focusFirstPendingItem(items []contracts.FocusRunItem) *contracts.FocusRunItem {
	for i := range items {
		if strings.TrimSpace(items[i].Status) == contracts.FocusItemPending {
			item := items[i]
			return &item
		}
	}
	return nil
}

func focusSummarizeMergeOutput(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	compact := strings.Join(strings.Fields(raw), " ")
	if len(compact) <= 240 {
		return compact
	}
	return compact[:237] + "..."
}

func focusNextAttempt(current int) int {
	if current <= 0 {
		return 1
	}
	return current
}

func focusRemainingBudget(run contracts.FocusRun) int {
	if run.AgentBudget > 0 {
		return run.AgentBudget
	}
	return 0
}

func focusMaxBudget(run contracts.FocusRun) int {
	if run.AgentBudgetMax > 0 {
		return run.AgentBudgetMax
	}
	if run.AgentBudget > 0 {
		return run.AgentBudget
	}
	return defaultAgentBudget
}

func focusBudgetExhaustedMessage(run contracts.FocusRun, nextAttempt int) string {
	return fmt.Sprintf(
		"focus budget exhausted: next_attempt=%d remaining_budget=%d budget_max=%d",
		nextAttempt,
		focusRemainingBudget(run),
		focusMaxBudget(run),
	)
}

func focusItemTerminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case contracts.FocusItemCompleted, contracts.FocusItemStopped, contracts.FocusItemFailed, contracts.FocusItemCanceled:
		return true
	default:
		return false
	}
}

func focusWorkerID(worker *contracts.Worker) uint {
	if worker == nil {
		return 0
	}
	return worker.ID
}

func focusTaskRunID(run *contracts.TaskRun) uint {
	if run == nil {
		return 0
	}
	return run.ID
}
