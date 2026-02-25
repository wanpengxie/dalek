package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

const channelPendingActionExpireAfter = 24 * time.Hour

var ErrPendingActionNotFound = errors.New("pending action 不存在")

type PendingActionDecision string

const (
	PendingActionApprove PendingActionDecision = "approve"
	PendingActionReject  PendingActionDecision = "reject"
)

type PendingActionView struct {
	ID             uint                             `json:"id"`
	ConversationID uint                             `json:"conversation_id"`
	JobID          uint                             `json:"job_id"`
	Action         contracts.TurnAction             `json:"action"`
	Status         store.ChannelPendingActionStatus `json:"status"`
	Decider        string                           `json:"decider"`
	DecisionNote   string                           `json:"decision_note"`
	CreatedAt      time.Time                        `json:"created_at"`
	UpdatedAt      time.Time                        `json:"updated_at"`
	DecidedAt      *time.Time                       `json:"decided_at,omitempty"`
	ExecutedAt     *time.Time                       `json:"executed_at,omitempty"`
}

type PendingActionDecisionRequest struct {
	ChannelType        string
	Adapter            string
	PeerConversationID string
	PendingActionID    uint
	Decision           PendingActionDecision
	Decider            string
	Note               string
}

type PendingActionDecisionResult struct {
	Action            PendingActionView     `json:"action"`
	Decision          PendingActionDecision `json:"decision,omitempty"`
	Message           string                `json:"message"`
	ExecutionMessage  string                `json:"execution_message,omitempty"`
	AlreadyFinalState bool                  `json:"already_final_state,omitempty"`
}

type ProjectRuntimePendingActionDecider interface {
	DecidePendingAction(ctx context.Context, req PendingActionDecisionRequest) (PendingActionDecisionResult, error)
}

func (d PendingActionDecision) normalize() PendingActionDecision {
	return PendingActionDecision(strings.ToLower(strings.TrimSpace(string(d))))
}

func (d PendingActionDecision) valid() bool {
	switch d.normalize() {
	case PendingActionApprove, PendingActionReject:
		return true
	default:
		return false
	}
}

func (s *Service) CreatePendingActions(ctx context.Context, conversationID, jobID uint, actions []contracts.TurnAction) ([]PendingActionView, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	rows := []store.ChannelPendingAction{}
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		created, cerr := s.createPendingActionsTx(ctx, tx, conversationID, jobID, actions)
		if cerr != nil {
			return cerr
		}
		rows = append(rows, created...)
		return nil
	}); err != nil {
		return nil, err
	}
	return pendingActionViewsFromModels(rows), nil
}

func (s *Service) createPendingActionsTx(ctx context.Context, tx *gorm.DB, conversationID, jobID uint, actions []contracts.TurnAction) ([]store.ChannelPendingAction, error) {
	if tx == nil {
		return nil, fmt.Errorf("tx 不能为空")
	}
	if conversationID == 0 || jobID == 0 {
		return nil, fmt.Errorf("conversation_id/job_id 不能为空")
	}
	if len(actions) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	rows := make([]store.ChannelPendingAction, 0, len(actions))
	for _, action := range actions {
		action.Normalize()
		if err := action.Validate(); err != nil {
			return nil, err
		}
		actionJSON, err := json.Marshal(action)
		if err != nil {
			return nil, fmt.Errorf("序列化 pending action 失败: %w", err)
		}
		row := store.ChannelPendingAction{
			ConversationID: conversationID,
			JobID:          jobID,
			ActionJSON:     strings.TrimSpace(string(actionJSON)),
			Status:         store.ChannelPendingActionPending,
			Decider:        "",
			DecisionNote:   "",
		}
		if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *Service) ListPendingActions(ctx context.Context, jobID uint) ([]PendingActionView, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID == 0 {
		return []PendingActionView{}, nil
	}
	var rows []store.ChannelPendingAction
	if err := db.WithContext(ctx).
		Where("job_id = ?", jobID).
		Order("id ASC").
		Find(&rows).Error; err != nil {
		return nil, err
	}
	return pendingActionViewsFromModels(rows), nil
}

func (s *Service) DecidePendingAction(ctx context.Context, req PendingActionDecisionRequest) (PendingActionDecisionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.PendingActionID == 0 {
		return PendingActionDecisionResult{}, fmt.Errorf("pending_action_id 不能为空")
	}
	decision := req.Decision.normalize()
	if !decision.valid() {
		return PendingActionDecisionResult{}, fmt.Errorf("decision 非法: %s", strings.TrimSpace(string(req.Decision)))
	}

	channelType := strings.ToLower(strings.TrimSpace(req.ChannelType))
	if channelType == "" {
		channelType = contracts.ChannelTypeIM
	}
	adapter := strings.TrimSpace(req.Adapter)
	if adapter == "" {
		adapter = defaultAdapter(channelType)
	}
	peerConversationID := strings.TrimSpace(req.PeerConversationID)
	if peerConversationID == "" {
		return PendingActionDecisionResult{}, fmt.Errorf("peer_conversation_id 不能为空")
	}
	conv, found, err := s.resolvePeerConversation(ctx, channelType, adapter, peerConversationID)
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	if !found {
		return PendingActionDecisionResult{}, fmt.Errorf("%w: 会话不存在", ErrPendingActionNotFound)
	}

	row, err := s.getPendingActionByID(ctx, req.PendingActionID)
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	if row.ConversationID != conv.ID {
		return PendingActionDecisionResult{}, fmt.Errorf("%w: action 不属于当前会话", ErrPendingActionNotFound)
	}

	switch decision {
	case PendingActionApprove:
		res, err := s.ApprovePendingAction(ctx, req.PendingActionID, strings.TrimSpace(req.Decider))
		if err != nil {
			return PendingActionDecisionResult{}, err
		}
		res.Decision = decision
		return res, nil
	case PendingActionReject:
		res, err := s.RejectPendingAction(ctx, req.PendingActionID, strings.TrimSpace(req.Decider), strings.TrimSpace(req.Note))
		if err != nil {
			return PendingActionDecisionResult{}, err
		}
		res.Decision = decision
		return res, nil
	default:
		return PendingActionDecisionResult{}, fmt.Errorf("decision 非法: %s", decision)
	}
}

func (s *Service) ApprovePendingAction(ctx context.Context, actionID uint, decider string) (PendingActionDecisionResult, error) {
	_, db, err := s.require()
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if actionID == 0 {
		return PendingActionDecisionResult{}, fmt.Errorf("action_id 不能为空")
	}
	decider = strings.TrimSpace(decider)
	if decider == "" {
		decider = "unknown"
	}

	row, err := s.getPendingActionByID(ctx, actionID)
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	if row.Status != store.ChannelPendingActionPending {
		return finalizeAlreadyDecidedResult(row), nil
	}

	now := time.Now()
	if row.CreatedAt.Add(channelPendingActionExpireAfter).Before(now) {
		if err := db.WithContext(ctx).Model(&store.ChannelPendingAction{}).
			Where("id = ? AND status = ?", row.ID, store.ChannelPendingActionPending).
			Updates(map[string]any{
				"status":        store.ChannelPendingActionFailed,
				"decider":       decider,
				"decision_note": "审批已过期",
				"decided_at":    &now,
				"executed_at":   &now,
				"updated_at":    now,
			}).Error; err != nil {
			return PendingActionDecisionResult{}, err
		}
		expiredRow, err := s.getPendingActionByID(ctx, actionID)
		if err != nil {
			return PendingActionDecisionResult{}, err
		}
		out := finalizeAlreadyDecidedResult(expiredRow)
		out.Decision = PendingActionApprove
		out.Message = "该审批已过期，请重新发起。"
		return out, nil
	}

	updateRes := db.WithContext(ctx).Model(&store.ChannelPendingAction{}).
		Where("id = ? AND status = ?", row.ID, store.ChannelPendingActionPending).
		Updates(map[string]any{
			"status":        store.ChannelPendingActionApproved,
			"decider":       decider,
			"decision_note": "已批准",
			"decided_at":    &now,
			"updated_at":    now,
		})
	if updateRes.Error != nil {
		return PendingActionDecisionResult{}, updateRes.Error
	}
	if updateRes.RowsAffected == 0 {
		latest, err := s.getPendingActionByID(ctx, actionID)
		if err != nil {
			return PendingActionDecisionResult{}, err
		}
		out := finalizeAlreadyDecidedResult(latest)
		out.Decision = PendingActionApprove
		return out, nil
	}

	approvedRow, err := s.getPendingActionByID(ctx, actionID)
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	action, err := decodePendingTurnAction(approvedRow.ActionJSON)
	if err != nil {
		failMsg := "action 数据损坏，无法执行"
		if uerr := s.finishPendingActionExecution(ctx, actionID, store.ChannelPendingActionFailed, failMsg); uerr != nil {
			return PendingActionDecisionResult{}, uerr
		}
		failedRow, ferr := s.getPendingActionByID(ctx, actionID)
		if ferr != nil {
			return PendingActionDecisionResult{}, ferr
		}
		return PendingActionDecisionResult{
			Action:           pendingActionRowToView(failedRow),
			Decision:         PendingActionApprove,
			Message:          failMsg,
			ExecutionMessage: failMsg,
		}, nil
	}
	if isSDKToolApprovalAction(action) {
		if s.toolApprovalBridge != nil {
			notified, hasWaiter := s.toolApprovalBridge.NotifyIfWaiting(actionID, PendingActionApprove)
			if hasWaiter {
				if !notified {
					log.Printf("tool_approval approve notify skipped: action=%d", actionID)
				}
				return PendingActionDecisionResult{
					Action:           pendingActionRowToView(approvedRow),
					Decision:         PendingActionApprove,
					Message:          "已批准，已通知会话继续执行该工具请求。",
					ExecutionMessage: "已批准，等待会话执行结果。",
				}, nil
			}
		}
		log.Printf("tool_approval approve waiter missing: action=%d", actionID)
		failMsg := "当前会话已结束，该工具审批已失效。"
		if err := s.finishPendingActionExecution(ctx, actionID, store.ChannelPendingActionFailed, failMsg); err != nil {
			return PendingActionDecisionResult{}, err
		}
		finalRow, err := s.getPendingActionByID(ctx, actionID)
		if err != nil {
			return PendingActionDecisionResult{}, err
		}
		return PendingActionDecisionResult{
			Action:           pendingActionRowToView(finalRow),
			Decision:         PendingActionApprove,
			Message:          failMsg,
			ExecutionMessage: failMsg,
		}, nil
	}

	execResult := s.executeAction(ctx, action)
	finalStatus := store.ChannelPendingActionExecuted
	finalMsg := strings.TrimSpace(execResult.Message)
	if !execResult.Success {
		finalStatus = store.ChannelPendingActionFailed
	}
	if finalMsg == "" {
		if finalStatus == store.ChannelPendingActionExecuted {
			finalMsg = "操作执行成功"
		} else {
			finalMsg = "操作执行失败"
		}
	}
	if err := s.finishPendingActionExecution(ctx, actionID, finalStatus, finalMsg); err != nil {
		return PendingActionDecisionResult{}, err
	}
	finalRow, err := s.getPendingActionByID(ctx, actionID)
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	return PendingActionDecisionResult{
		Action:           pendingActionRowToView(finalRow),
		Decision:         PendingActionApprove,
		Message:          finalMsg,
		ExecutionMessage: finalMsg,
	}, nil
}

func (s *Service) RejectPendingAction(ctx context.Context, actionID uint, decider, note string) (PendingActionDecisionResult, error) {
	_, db, err := s.require()
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if actionID == 0 {
		return PendingActionDecisionResult{}, fmt.Errorf("action_id 不能为空")
	}
	decider = strings.TrimSpace(decider)
	if decider == "" {
		decider = "unknown"
	}
	note = strings.TrimSpace(note)
	if note == "" {
		note = "用户拒绝"
	}

	row, err := s.getPendingActionByID(ctx, actionID)
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	if row.Status != store.ChannelPendingActionPending {
		out := finalizeAlreadyDecidedResult(row)
		out.Decision = PendingActionReject
		return out, nil
	}

	now := time.Now()
	updateRes := db.WithContext(ctx).Model(&store.ChannelPendingAction{}).
		Where("id = ? AND status = ?", row.ID, store.ChannelPendingActionPending).
		Updates(map[string]any{
			"status":        store.ChannelPendingActionRejected,
			"decider":       decider,
			"decision_note": rejectDecisionNote(note),
			"decided_at":    &now,
			"updated_at":    now,
		})
	if updateRes.Error != nil {
		return PendingActionDecisionResult{}, updateRes.Error
	}
	if updateRes.RowsAffected == 0 {
		latest, err := s.getPendingActionByID(ctx, actionID)
		if err != nil {
			return PendingActionDecisionResult{}, err
		}
		out := finalizeAlreadyDecidedResult(latest)
		out.Decision = PendingActionReject
		return out, nil
	}
	rejectedRow, err := s.getPendingActionByID(ctx, actionID)
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	if s.toolApprovalBridge != nil {
		notified, hasWaiter := s.toolApprovalBridge.NotifyIfWaiting(actionID, PendingActionReject)
		if hasWaiter && !notified {
			log.Printf("tool_approval reject notify skipped: action=%d", actionID)
		}
	}
	return PendingActionDecisionResult{
		Action:   pendingActionRowToView(rejectedRow),
		Decision: PendingActionReject,
		Message:  "已拒绝该操作。",
	}, nil
}

func (s *Service) finishPendingActionExecution(ctx context.Context, actionID uint, status store.ChannelPendingActionStatus, message string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	return db.WithContext(ctx).Model(&store.ChannelPendingAction{}).
		Where("id = ?", actionID).
		Updates(map[string]any{
			"status":        status,
			"decision_note": strings.TrimSpace(message),
			"executed_at":   &now,
			"updated_at":    now,
		}).Error
}

func (s *Service) getPendingActionByID(ctx context.Context, actionID uint) (store.ChannelPendingAction, error) {
	_, db, err := s.require()
	if err != nil {
		return store.ChannelPendingAction{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var row store.ChannelPendingAction
	if err := db.WithContext(ctx).First(&row, actionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return store.ChannelPendingAction{}, fmt.Errorf("%w: %d", ErrPendingActionNotFound, actionID)
		}
		return store.ChannelPendingAction{}, err
	}
	return row, nil
}

func rejectDecisionNote(note string) string {
	if strings.TrimSpace(note) == "" {
		return "已拒绝"
	}
	return strings.TrimSpace(note)
}

func finalizeAlreadyDecidedResult(row store.ChannelPendingAction) PendingActionDecisionResult {
	view := pendingActionRowToView(row)
	note := strings.TrimSpace(view.DecisionNote)
	out := PendingActionDecisionResult{
		Action:            view,
		AlreadyFinalState: true,
	}
	switch view.Status {
	case store.ChannelPendingActionExecuted:
		if note == "" {
			note = "该操作已执行完成。"
		}
		out.Message = note
	case store.ChannelPendingActionRejected:
		if note == "" {
			note = "该操作已被拒绝。"
		}
		out.Message = note
	case store.ChannelPendingActionFailed:
		if note == "" {
			note = "该操作执行失败。"
		}
		out.Message = note
	case store.ChannelPendingActionApproved:
		if note == "" {
			note = "该操作已批准，正在执行。"
		}
		out.Message = note
	default:
		if note == "" {
			note = "该操作状态已更新。"
		}
		out.Message = note
	}
	return out
}

func pendingActionRowToView(row store.ChannelPendingAction) PendingActionView {
	action, err := decodePendingTurnAction(row.ActionJSON)
	if err != nil {
		action = contracts.TurnAction{
			Name: "invalid_action",
			Args: map[string]any{
				"raw_action_json": strings.TrimSpace(row.ActionJSON),
			},
		}
	}
	return PendingActionView{
		ID:             row.ID,
		ConversationID: row.ConversationID,
		JobID:          row.JobID,
		Action:         action,
		Status:         row.Status,
		Decider:        strings.TrimSpace(row.Decider),
		DecisionNote:   strings.TrimSpace(row.DecisionNote),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		DecidedAt:      row.DecidedAt,
		ExecutedAt:     row.ExecutedAt,
	}
}

func pendingActionViewsFromModels(rows []store.ChannelPendingAction) []PendingActionView {
	if len(rows) == 0 {
		return nil
	}
	views := make([]PendingActionView, 0, len(rows))
	for _, row := range rows {
		views = append(views, pendingActionRowToView(row))
	}
	return views
}

func copyPendingActionViews(in []PendingActionView) []PendingActionView {
	if len(in) == 0 {
		return nil
	}
	out := make([]PendingActionView, 0, len(in))
	for _, item := range in {
		copied := item
		copied.Action.Normalize()
		if len(item.Action.Args) > 0 {
			copied.Action.Args = make(map[string]any, len(item.Action.Args))
			for k, v := range item.Action.Args {
				copied.Action.Args[strings.TrimSpace(k)] = v
			}
		}
		if item.DecidedAt != nil {
			t := *item.DecidedAt
			copied.DecidedAt = &t
		}
		if item.ExecutedAt != nil {
			t := *item.ExecutedAt
			copied.ExecutedAt = &t
		}
		out = append(out, copied)
	}
	return out
}

func decodePendingTurnAction(raw string) (contracts.TurnAction, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return contracts.TurnAction{}, fmt.Errorf("action_json 为空")
	}
	var action contracts.TurnAction
	if err := json.Unmarshal([]byte(raw), &action); err != nil {
		return contracts.TurnAction{}, err
	}
	action.Normalize()
	if err := action.Validate(); err != nil {
		return contracts.TurnAction{}, err
	}
	return action, nil
}

type actionExecuteResult struct {
	Action  contracts.TurnAction
	Success bool
	Message string
}

func (s *Service) executeAction(ctx context.Context, action contracts.TurnAction) actionExecuteResult {
	action.Normalize()
	result := actionExecuteResult{Action: action}
	if s == nil || s.p == nil {
		result.Message = "channel service 缺少 project 上下文"
		return result
	}
	execRes, err := newActionExecutor(s.p).Execute(ctx, action)
	if err != nil {
		result.Success = false
		result.Message = strings.TrimSpace(err.Error())
		return result
	}
	result.Success = execRes.Success
	result.Message = strings.TrimSpace(execRes.Message)
	if result.Message == "" {
		if result.Success {
			result.Message = "操作执行成功"
		} else {
			result.Message = "操作执行失败"
		}
	}
	return result
}

func renderActionExecutionSummary(results []actionExecuteResult) string {
	if len(results) == 0 {
		return ""
	}
	lines := make([]string, 0, len(results)+1)
	lines = append(lines, "Action 执行结果：")
	for _, res := range results {
		prefix := "[OK]"
		if !res.Success {
			prefix = "[FAIL]"
		}
		msg := strings.TrimSpace(res.Message)
		desc := describePendingAction(res.Action)
		if msg == "" {
			lines = append(lines, fmt.Sprintf("- %s %s", prefix, desc))
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s %s -> %s", prefix, desc, msg))
	}
	return strings.Join(lines, "\n")
}

func describePendingAction(action contracts.TurnAction) string {
	action.Normalize()
	name := strings.TrimSpace(action.Name)
	if name == "" {
		name = "unknown_action"
	}
	if len(action.Args) == 0 {
		return name
	}
	parts := make([]string, 0, len(action.Args))
	for k, v := range action.Args {
		key := strings.TrimSpace(k)
		if key == "" {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v", key, v))
	}
	if len(parts) == 0 {
		return name
	}
	return name + "(" + strings.Join(parts, ", ") + ")"
}
