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
	focusBlockedReasonRestartExhausted             = "restart_exhausted"
	focusBlockedReasonSubmitFailed                 = "submit_failed"
	focusBlockedReasonStartFailed                  = "start_failed"
	focusBlockedReasonMergeFailed                  = "merge_failed"
	focusBlockedReasonHandoffWaitingMerge          = "handoff_waiting_merge"
	focusBlockedReasonHandoffRecursionRequiresUser = "handoff_recursion_requires_user"
)

const focusMaxAutoRestarts = 1

type focusTicketSnapshot struct {
	Ticket    contracts.Ticket
	Worker    *contracts.Worker
	ActiveRun *contracts.TaskRun
}

func (s *Service) AdvanceFocusController(ctx context.Context) error {
	return s.FocusTick(ctx)
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
		switch strings.TrimSpace(view.Run.DesiredState) {
		case contracts.FocusDesiredStopping:
			return s.focusSetRunTerminal(ctx, view.Run.ID, contracts.FocusStopped)
		case contracts.FocusDesiredCanceling:
			return s.focusSetRunTerminal(ctx, view.Run.ID, contracts.FocusCanceled)
		default:
			return s.focusTickPendingItem(ctx, view.Run, *pending)
		}
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
		return s.focusQueueItem(ctx, run.ID, item.ID, focusWorkerID(snapshot.Worker), focusNextAttempt(item.CurrentAttempt), "", "", nil)
	case contracts.TicketBlocked:
		return s.focusRestartOrBlock(ctx, run, item, snapshot, focusNextAttempt(item.CurrentAttempt))
	case contracts.TicketArchived:
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonStartFailed, "ticket 已归档")
	default:
		if _, err := s.StartTicket(ctx, item.TicketID); err != nil {
			return s.focusBlockItem(ctx, run, item, focusBlockedReasonStartFailed, err.Error())
		}
		snapshot, err = s.focusLoadTicketSnapshot(ctx, item.TicketID)
		if err != nil {
			return err
		}
		return s.focusQueueItem(ctx, run.ID, item.ID, focusWorkerID(snapshot.Worker), focusNextAttempt(item.CurrentAttempt), contracts.FocusEventItemStartRequested, "focus item start requested", map[string]any{
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
				return s.focusHandleBlockedExecution(ctx, run, item, snapshot)
			}
		}
		return nil
	}
}

func (s *Service) focusTickMergingItem(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem) error {
	if strings.TrimSpace(run.DesiredState) == contracts.FocusDesiredCanceling {
		s.gitMergeAbort(context.WithoutCancel(ctx))
		return s.focusTerminalizeItem(ctx, run.ID, item.ID, contracts.FocusCanceled, contracts.FocusItemCanceled)
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
	targetRef := s.targetBranchForTicket(ctx, item.TicketID)
	result, err := s.gitMergeTicketBranch(ctx, workerBranch, targetRef)
	if err != nil {
		s.gitMergeAbort(context.WithoutCancel(ctx))
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonMergeFailed, err.Error())
	}
	switch result {
	case mergeSuccess:
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
		return s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, map[string]any{
			"status":      contracts.FocusRunning,
			"finished_at": nil,
		}, map[string]any{
			"status":     contracts.FocusItemAwaitingMergeObservation,
			"updated_at": time.Now(),
		}, "", "", nil)
	case mergeConflict:
		conflictFiles := s.gitConflictFiles(ctx)
		s.gitMergeAbort(context.WithoutCancel(ctx))
		blockedReason := focusBlockedReasonMergeFailed
		lastError := "merge conflict"
		summary := "focus merge conflict aborted"
		if strings.EqualFold(strings.TrimSpace(snapshot.Ticket.Label), "integration") {
			blockedReason = focusBlockedReasonHandoffRecursionRequiresUser
			lastError = "integration ticket merge conflict requires user"
			summary = "integration merge conflict requires user"
		}
		if err := s.focusAppendEvent(ctx, run.ID, item.ID, contracts.FocusEventMergeAborted, summary, map[string]any{
			"ticket_id":      item.TicketID,
			"conflict_files": conflictFiles,
			"target_ref":     targetRef,
			"blocked_reason": blockedReason,
		}); err != nil {
			return err
		}
		return s.focusBlockItem(ctx, run, item, blockedReason, lastError)
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
		return s.focusTerminalizeItem(ctx, run.ID, item.ID, contracts.FocusStopped, contracts.FocusItemStopped)
	case contracts.FocusDesiredCanceling:
		return s.focusTerminalizeItem(ctx, run.ID, item.ID, contracts.FocusCanceled, contracts.FocusItemCanceled)
	}
	return nil
}

func (s *Service) focusHandleBlockedExecution(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, snapshot focusTicketSnapshot) error {
	if needsUser, err := s.focusHasNeedsUserInbox(ctx, item.TicketID, focusWorkerID(snapshot.Worker)); err != nil {
		return err
	} else if needsUser {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonNeedsUser, "ticket 需要用户介入")
	}
	return s.focusRestartOrBlock(ctx, run, item, snapshot, item.CurrentAttempt+1)
}

func (s *Service) focusRestartOrBlock(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, _ focusTicketSnapshot, nextAttempt int) error {
	if nextAttempt <= 0 {
		nextAttempt = 1
	}
	if nextAttempt > focusMaxAutoRestarts+1 {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonRestartExhausted, "focus restart exhausted")
	}
	if _, err := s.StartTicket(ctx, item.TicketID); err != nil {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonStartFailed, err.Error())
	}
	snapshot, err := s.focusLoadTicketSnapshot(ctx, item.TicketID)
	if err != nil {
		return err
	}
	return s.focusQueueItem(ctx, run.ID, item.ID, focusWorkerID(snapshot.Worker), nextAttempt, contracts.FocusEventItemRestarted, "focus item restarted", map[string]any{
		"ticket_id": item.TicketID,
		"worker_id": focusWorkerID(snapshot.Worker),
		"attempt":   nextAttempt,
	})
}

func (s *Service) focusSubmitItemRun(ctx context.Context, run contracts.FocusRun, item contracts.FocusRunItem, worker *contracts.Worker, eventKind, eventSummary string, attempt int) error {
	submitter := s.getWorkerRunSubmitter()
	if submitter == nil {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonSubmitFailed, "worker run submitter 未配置")
	}
	submission, err := submitter.SubmitTicketWorkerRun(context.WithoutCancel(ctx), item.TicketID)
	if err != nil {
		return s.focusBlockItem(ctx, run, item, focusBlockedReasonSubmitFailed, err.Error())
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
	return s.focusUpdateRunAndItem(ctx, run.ID, &item.ID, map[string]any{
		"status":      contracts.FocusRunning,
		"finished_at": nil,
	}, itemUpdates, eventKind, eventSummary, map[string]any{
		"ticket_id":   item.TicketID,
		"worker_id":   workerID,
		"task_run_id": submission.TaskRunID,
		"attempt":     attempt,
	})
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

func (s *Service) focusQueueItem(ctx context.Context, runID, itemID, workerID uint, attempt int, eventKind, eventSummary string, payload any) error {
	now := time.Now()
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
	if err := s.focusUpdateRunAndItem(ctx, runID, &itemID, map[string]any{
		"status":      contracts.FocusRunning,
		"finished_at": nil,
	}, itemUpdates, eventKind, eventSummary, payload); err != nil {
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

func (s *Service) focusTerminalizeItem(ctx context.Context, runID, itemID uint, runStatus, itemStatus string) error {
	now := time.Now()
	return s.focusUpdateRunAndItem(ctx, runID, &itemID, map[string]any{
		"status":      strings.TrimSpace(runStatus),
		"updated_at":  now,
		"finished_at": &now,
	}, map[string]any{
		"status":      strings.TrimSpace(itemStatus),
		"updated_at":  now,
		"finished_at": &now,
	}, "", "", nil)
}

func (s *Service) focusSetRunTerminal(ctx context.Context, runID uint, status string) error {
	now := time.Now()
	return s.focusUpdateRunAndItem(ctx, runID, nil, map[string]any{
		"status":      strings.TrimSpace(status),
		"updated_at":  now,
		"finished_at": &now,
	}, nil, "", "", nil)
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

func (s *Service) focusHasNeedsUserInbox(ctx context.Context, ticketID, workerID uint) (bool, error) {
	_, db, err := s.require()
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var cnt int64
	query := db.WithContext(ctx).Model(&contracts.InboxItem{}).
		Where("status = ? AND reason = ?", contracts.InboxOpen, contracts.InboxNeedsUser)
	if workerID != 0 {
		query = query.Where("(ticket_id = ? OR worker_id = ?)", ticketID, workerID)
	} else {
		query = query.Where("ticket_id = ?", ticketID)
	}
	if err := query.Count(&cnt).Error; err != nil {
		return false, err
	}
	return cnt > 0, nil
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
		canceled, err := s.focusTaskRunCanceled(ctx, *item.CurrentTaskRunID)
		if err != nil {
			return err
		}
		if !canceled {
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
		_ = ctrl.CancelTicketLoop(context.WithoutCancel(ctx), item.TicketID)
	}
	if item.CurrentTaskRunID != nil && *item.CurrentTaskRunID != 0 {
		if ctrl != nil {
			_ = ctrl.CancelTaskRun(context.WithoutCancel(ctx), *item.CurrentTaskRunID)
		}
		if err := s.focusMarkTaskRunCanceled(ctx, *item.CurrentTaskRunID, "focus cancel requested"); err != nil {
			return err
		}
	}
	if err := s.StopTicket(context.WithoutCancel(ctx), item.TicketID); err != nil && !strings.Contains(err.Error(), "尚无可停止的 worker") {
		return err
	}
	return s.focusRepairTicketWorkflow(context.WithoutCancel(ctx), item.TicketID, contracts.TicketBacklog, "focus cancel requested")
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

func (s *Service) focusTaskRunCanceled(ctx context.Context, runID uint) (bool, error) {
	rt, err := s.taskRuntime()
	if err != nil {
		return false, err
	}
	run, err := rt.FindRunByID(ctx, runID)
	if err != nil || run == nil {
		return false, err
	}
	return run.OrchestrationState == contracts.TaskCanceled, nil
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

func focusNextAttempt(current int) int {
	if current <= 0 {
		return 1
	}
	return current
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
