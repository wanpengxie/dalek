package pm

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/fsm"
	"dalek/internal/services/ticketlifecycle"

	"gorm.io/gorm"
)

type workerLoopMissingReportError struct {
	Stages    int
	LastRunID uint
}

func (e *workerLoopMissingReportError) Error() string {
	if e == nil {
		return "worker 连续两轮执行完成但未提交 report next_action"
	}
	if e.LastRunID != 0 {
		return fmt.Sprintf("worker 连续两轮执行完成但未提交 report next_action（stages=%d last_run_id=%d）", e.Stages, e.LastRunID)
	}
	if e.Stages > 0 {
		return fmt.Sprintf("worker 连续两轮执行完成但未提交 report next_action（stages=%d）", e.Stages)
	}
	return "worker 连续两轮执行完成但未提交 report next_action"
}

type workerLoopStateSnapshot struct {
	NextAction string
	Summary    string
	Blockers   []string
}

type workerLoopStateFile struct {
	Phases struct {
		NextAction string `json:"next_action"`
		Summary    string `json:"summary"`
	} `json:"phases"`
	Blockers []string `json:"blockers"`
}

func (s *Service) applyWorkerLoopTerminalClosure(ctx context.Context, ticketID uint, w contracts.Worker, loopResult WorkerLoopResult, source string) error {
	next := strings.TrimSpace(strings.ToLower(loopResult.LastNextAction))
	switch next {
	case string(contracts.NextDone), string(contracts.NextWaitUser):
	default:
		return nil
	}
	report, err := s.loadWorkerLoopTerminalReport(ctx, ticketID, w, loopResult.LastRunID)
	if err != nil {
		return err
	}
	return s.applyWorkerLoopTerminalReport(ctx, report, workerLoopClosureSource(source, next))
}

func (s *Service) applyMissingWorkerReportWaitUser(ctx context.Context, ticketID uint, w contracts.Worker, loopResult WorkerLoopResult, source string) error {
	if ticketID == 0 {
		ticketID = w.TicketID
	}
	state := readWorkerLoopStateSnapshot(strings.TrimSpace(w.WorktreePath))
	stateNext := strings.TrimSpace(strings.ToLower(state.NextAction))
	summary := "worker 连续两轮执行完成但未提交 worker report，系统已自动阻塞并请求人工介入。"
	blockers := []string{
		"worker 未调用 dalek worker report 或 report 中缺少 next_action，请检查最近两轮执行日志与任务状态。",
	}
	closureKind := "missing_report"
	if stateNext != "" && !isValidWorkerNextAction(stateNext) {
		closureKind = "invalid_report"
		summary = fmt.Sprintf("worker state.json 中存在非法 next_action=%q，系统已自动阻塞并请求人工介入。", state.NextAction)
		blockers = []string{
			fmt.Sprintf("worker state.json phases.next_action=%q 非法，只允许 continue|done|wait_user。", state.NextAction),
		}
	} else if stateNext != "" {
		blockers = append(blockers, fmt.Sprintf("state.json 中记录的 phases.next_action=%s，但未同步成合法 worker report。", stateNext))
	}
	if loopResult.LastRunID != 0 {
		blockers = append(blockers, fmt.Sprintf("最后一次未收口的 run_id=%d。", loopResult.LastRunID))
	}
	if loopResult.Stages > 0 {
		blockers = append(blockers, fmt.Sprintf("本轮 worker loop 已执行 %d 个 stage，并在补报重试后仍未收口。", loopResult.Stages))
	}
	if strings.TrimSpace(state.Summary) != "" {
		blockers = append(blockers, fmt.Sprintf("state.json summary=%q。", strings.TrimSpace(state.Summary)))
	}
	if len(state.Blockers) > 0 {
		for _, blocker := range state.Blockers {
			blocker = strings.TrimSpace(blocker)
			if blocker == "" {
				continue
			}
			blockers = append(blockers, "state.json blocker: "+blocker)
		}
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
	next := strings.TrimSpace(strings.ToLower(r.NextAction))
	if next != string(contracts.NextDone) && next != string(contracts.NextWaitUser) {
		return nil
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
		return nil
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

func (s *Service) loadWorkerLoopTerminalReport(ctx context.Context, ticketID uint, w contracts.Worker, runID uint) (contracts.WorkerReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runID == 0 {
		return contracts.WorkerReport{}, fmt.Errorf("worker loop closure 缺少 task_run_id")
	}
	_, db, err := s.require()
	if err != nil {
		return contracts.WorkerReport{}, err
	}
	var row contracts.TaskSemanticReport
	if err := db.WithContext(ctx).
		Where("task_run_id = ?", runID).
		Order("reported_at desc").
		Order("id desc").
		First(&row).Error; err != nil {
		return contracts.WorkerReport{}, fmt.Errorf("读取 worker loop terminal report 失败: %w", err)
	}
	report := contracts.WorkerReport{
		Schema:     contracts.WorkerReportSchemaV1,
		WorkerID:   w.ID,
		TicketID:   ticketID,
		TaskRunID:  runID,
		Summary:    strings.TrimSpace(row.Summary),
		NextAction: strings.TrimSpace(row.NextAction),
		HeadSHA:    workerClosureJSONMapString(row.ReportPayloadJSON, "head_sha"),
		Dirty:      workerClosureJSONMapBool(row.ReportPayloadJSON, "dirty"),
		NeedsUser:  workerClosureJSONMapBool(row.ReportPayloadJSON, "needs_user"),
		Blockers:   workerClosureJSONMapStringSlice(row.ReportPayloadJSON, "blockers"),
	}
	report.Normalize()
	return report, nil
}

func readWorkerLoopStateSnapshot(worktreePath string) workerLoopStateSnapshot {
	worktreePath = strings.TrimSpace(worktreePath)
	if worktreePath == "" {
		return workerLoopStateSnapshot{}
	}
	statePath := filepath.Join(worktreePath, ".dalek", "state.json")
	raw, err := os.ReadFile(statePath)
	if err != nil {
		return workerLoopStateSnapshot{}
	}
	var snapshot workerLoopStateFile
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return workerLoopStateSnapshot{}
	}
	return workerLoopStateSnapshot{
		NextAction: strings.TrimSpace(snapshot.Phases.NextAction),
		Summary:    strings.TrimSpace(snapshot.Phases.Summary),
		Blockers:   cleanStringSlice(snapshot.Blockers),
	}
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
