package pm

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

func (s *Service) applyWorkerLoopTerminalClosure(ctx context.Context, ticketID uint, w contracts.Worker, loopResult WorkerLoopResult, source string) error {
	next := strings.TrimSpace(strings.ToLower(loopResult.LastNextAction))
	switch next {
	case string(contracts.NextDone), string(contracts.NextWaitUser):
	default:
		return nil
	}
	report, found, err := s.loadWorkerLoopCandidateReport(ctx, ticketID, w, loopResult.LastRunID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("worker loop closure 缺少 agent_report: run_id=%d", loopResult.LastRunID)
	}
	return s.applyWorkerLoopTerminalReport(ctx, report, workerLoopClosureSource(source, next))
}

func (s *Service) applyWorkerLoopClosureFallbackWaitUser(ctx context.Context, ticketID uint, w contracts.Worker, loopResult WorkerLoopResult, decision workerLoopStageClosureDecision, source string) error {
	if ticketID == 0 {
		ticketID = w.TicketID
	}
	summary := decision.fallbackSummary()
	blockers := decision.fallbackBlockers()
	closureKind := strings.TrimSpace(decision.ReasonCode)
	if closureKind == "" {
		closureKind = "closure_failed"
	}
	if loopResult.LastRunID != 0 {
		blockers = append(blockers, fmt.Sprintf("最后一次未收口的 run_id=%d。", loopResult.LastRunID))
	}
	if loopResult.Stages > 0 {
		blockers = append(blockers, fmt.Sprintf("本轮 worker loop 已执行 %d 个 stage，并在收口补救后仍未闭合。", loopResult.Stages))
	}
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   ticketID,
		TaskRunID:  loopResult.LastRunID,
		Summary:    summary,
		NeedsUser:  true,
		Blockers:   blockers,
		NextAction: string(contracts.NextWaitUser),
	}
	return s.applyWorkerLoopTerminalReport(ctx, report, workerLoopClosureSource(source, closureKind))
}

func (s *Service) applyWorkerLoopTerminalReport(ctx context.Context, r contracts.WorkerReport, source string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	r.Normalize()
	if err := r.Validate(); err != nil {
		return err
	}
	r.Blockers = cleanStringSlice(r.Blockers)
	next := strings.TrimSpace(strings.ToLower(r.NextAction))
	if next != string(contracts.NextDone) && next != string(contracts.NextWaitUser) {
		return nil
	}
	switch next {
	case string(contracts.NextDone):
		guarded, err := s.guardWorkerLoopTerminalReport(ctx, r)
		if err != nil {
			return err
		}
		r = guarded
	case string(contracts.NextWaitUser):
		if !meaningfulWorkerSummary(r.Summary) {
			return fmt.Errorf("wait_user closure 缺少能解释阻塞原因的 summary")
		}
		if len(r.Blockers) == 0 {
			return fmt.Errorf("wait_user closure 缺少 blockers")
		}
	}
	ticketID := r.TicketID
	if ticketID == 0 {
		if w, err := s.worker.WorkerByID(ctx, r.WorkerID); err == nil && w != nil {
			ticketID = w.TicketID
			if r.WorkerID == 0 {
				r.WorkerID = w.ID
			}
		}
	}
	if ticketID == 0 {
		return fmt.Errorf("worker loop closure 缺少 ticket_id")
	}
	_, db, err := s.require()
	if err != nil {
		return err
	}
	now := time.Now()
	var statusEvent *StatusChangeEvent
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var t contracts.Ticket
		if err := tx.WithContext(ctx).First(&t, ticketID).Error; err != nil {
			return err
		}
		if !fsm.ShouldApplyWorkerReport(t.WorkflowStatus) {
			return nil
		}
		var promoteTo contracts.TicketWorkflowStatus
		switch next {
		case string(contracts.NextDone):
			promoteTo = contracts.TicketDone
		case string(contracts.NextWaitUser):
			promoteTo = contracts.TicketBlocked
		}
		if !fsm.CanReportPromoteTo(t.WorkflowStatus, promoteTo) {
			return nil
		}
		taskRunID := r.TaskRunID
		switch next {
		case string(contracts.NextWaitUser):
			lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
				TicketID:       t.ID,
				EventType:      contracts.TicketLifecycleWaitUserReported,
				Source:         source,
				ActorType:      contracts.TicketLifecycleActorWorker,
				WorkerID:       r.WorkerID,
				TaskRunID:      taskRunID,
				IdempotencyKey: ticketlifecycle.WaitUserReportedIdempotencyKey(t.ID, taskRunID, r.WorkerID),
				Payload: map[string]any{
					"ticket_id":   t.ID,
					"worker_id":   r.WorkerID,
					"task_run_id": taskRunID,
					"next_action": next,
					"source":      source,
					"summary":     strings.TrimSpace(r.Summary),
					"blockers":    r.Blockers,
				},
				CreatedAt: now,
			})
			if err != nil {
				return err
			}
			if !lifecycleResult.Inserted {
				return nil
			}
			if lifecycleResult.WorkflowChanged() {
				if err := s.appendTicketWorkflowEventTx(ctx, tx, t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, source, "worker loop closure 推进 workflow", map[string]any{
					"worker_id":   r.WorkerID,
					"ticket_id":   t.ID,
					"task_run_id": taskRunID,
					"next_action": next,
				}, now); err != nil {
					return err
				}
				statusEvent = s.buildStatusChangeEvent(t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, source, now)
				if statusEvent != nil {
					statusEvent.WorkerID = r.WorkerID
					statusEvent.Detail = buildNeedsUserInboxBodyFromReport(r)
				}
			}
			_, err = s.upsertOpenInboxTx(ctx, tx, contracts.InboxItem{
				Key:      inboxKeyNeedsUser(r.WorkerID),
				Status:   contracts.InboxOpen,
				Severity: contracts.InboxBlocker,
				Reason:   contracts.InboxNeedsUser,
				Title:    fmt.Sprintf("需要你输入：t%d w%d", t.ID, r.WorkerID),
				Body:     buildNeedsUserInboxBodyFromReport(r),
				TicketID: t.ID,
				WorkerID: r.WorkerID,
			})
			return err
		case string(contracts.NextDone):
			freeze, err := s.resolveDoneIntegrationFreezeTx(ctx, tx, t.ID, r.WorkerID, taskRunID, r.HeadSHA)
			if err != nil {
				return err
			}
			lifecycleResult, err := s.appendTicketLifecycleEventAndProjectSnapshotTx(ctx, tx, ticketlifecycle.AppendInput{
				TicketID:       t.ID,
				EventType:      contracts.TicketLifecycleDoneReported,
				Source:         source,
				ActorType:      contracts.TicketLifecycleActorWorker,
				WorkerID:       r.WorkerID,
				TaskRunID:      taskRunID,
				IdempotencyKey: ticketlifecycle.DoneReportedIdempotencyKey(t.ID, taskRunID, r.WorkerID),
				Payload: map[string]any{
					"ticket_id":          t.ID,
					"worker_id":          r.WorkerID,
					"task_run_id":        taskRunID,
					"next_action":        next,
					"source":             source,
					"summary":            strings.TrimSpace(r.Summary),
					"head_sha":           freeze.AnchorSHA,
					"anchor_sha":         freeze.AnchorSHA,
					"target_ref":         freeze.TargetRef,
					"integration_status": string(contracts.IntegrationNeedsMerge),
					"workflow_status":    string(contracts.TicketDone),
				},
				CreatedAt: now,
			})
			if err != nil {
				return err
			}
			if !lifecycleResult.Inserted {
				return nil
			}
			if err := s.applyDoneIntegrationFreezeTx(ctx, tx, t.ID, freeze, now); err != nil {
				return err
			}
			if !lifecycleResult.WorkflowChanged() {
				return nil
			}
			if err := s.appendTicketWorkflowEventTx(ctx, tx, t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, source, "worker loop closure 推进 workflow", map[string]any{
				"worker_id":   r.WorkerID,
				"ticket_id":   t.ID,
				"task_run_id": taskRunID,
				"next_action": next,
			}, now); err != nil {
				return err
			}
			statusEvent = s.buildStatusChangeEvent(t.ID, lifecycleResult.Before.WorkflowStatus, lifecycleResult.After.WorkflowStatus, source, now)
			if statusEvent != nil {
				statusEvent.WorkerID = r.WorkerID
				summary := strings.TrimSpace(r.Summary)
				if summary != "" && summary != "-" {
					statusEvent.Detail = summary
				}
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.emitStatusChangeHookAsync(statusEvent)
	return nil
}

func workerLoopClosureSource(source, kind string) string {
	base := "pm.worker_loop.closure"
	source = strings.TrimSpace(source)
	kind = strings.TrimSpace(kind)
	switch {
	case source == "" && kind == "":
		return base
	case source == "":
		return fmt.Sprintf("%s(%s)", base, kind)
	case kind == "":
		return fmt.Sprintf("%s(%s)", base, source)
	default:
		return fmt.Sprintf("%s(%s:%s)", base, source, kind)
	}
}

func isValidWorkerNextAction(next string) bool {
	switch strings.TrimSpace(strings.ToLower(next)) {
	case string(contracts.NextContinue), string(contracts.NextDone), string(contracts.NextWaitUser):
		return true
	default:
		return false
	}
}

func cleanStringSlice(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	out := make([]string, 0, len(src))
	for _, item := range src {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		out = append(out, item)
	}
	return out
}

func workerClosureJSONMapString(payload contracts.JSONMap, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	out := strings.TrimSpace(fmt.Sprint(value))
	if out == "<nil>" {
		return ""
	}
	return out
}

func workerClosureJSONMapBool(payload contracts.JSONMap, key string) bool {
	if payload == nil {
		return false
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.TrimSpace(strings.ToLower(v)) {
		case "1", "true", "yes", "on":
			return true
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	}
	return false
}

func workerClosureJSONMapStringSlice(payload contracts.JSONMap, key string) []string {
	if payload == nil {
		return nil
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return cleanStringSlice(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text == "" || text == "<nil>" {
				continue
			}
			out = append(out, text)
		}
		return out
	default:
		text := strings.TrimSpace(fmt.Sprint(v))
		if text == "" || text == "<nil>" {
			return nil
		}
		return []string{text}
	}
}
