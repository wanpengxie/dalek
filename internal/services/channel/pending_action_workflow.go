package channel

import (
	"context"
	"fmt"
	"strings"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"
)

func (s *Service) DecidePendingAction(ctx context.Context, req PendingActionDecisionRequest) (PendingActionDecisionResult, error) {
	if ctx == nil {
		return PendingActionDecisionResult{}, fmt.Errorf("context 不能为空")
	}
	if req.PendingActionID == 0 {
		return PendingActionDecisionResult{}, fmt.Errorf("pending_action_id 不能为空")
	}
	decision := req.Decision.normalize()
	if !decision.valid() {
		return PendingActionDecisionResult{}, fmt.Errorf("decision 非法: %s", string(req.Decision))
	}

	channelType := toStoreChannelType(contracts.ChannelType(strings.ToLower(string(req.ChannelType))))
	if channelType == "" {
		channelType = contracts.ChannelTypeIM
	}
	adapter := req.Adapter
	if adapter == "" {
		adapter = defaultAdapter(string(channelType))
	}
	peerConversationID := req.PeerConversationID
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
		res, err := s.ApprovePendingAction(ctx, req.PendingActionID, req.Decider)
		if err != nil {
			return PendingActionDecisionResult{}, err
		}
		res.Decision = decision
		return res, nil
	case PendingActionReject:
		res, err := s.RejectPendingAction(ctx, req.PendingActionID, req.Decider, req.Note)
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
	if ctx == nil {
		return PendingActionDecisionResult{}, fmt.Errorf("context 不能为空")
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
	if row.Status != contracts.ChannelPendingActionPending {
		return finalizeAlreadyDecidedResult(row), nil
	}

	now := time.Now()
	if row.CreatedAt.Add(channelPendingActionExpireAfter).Before(now) {
		if _, err := s.updatePendingAction(ctx, row.ID, contracts.ChannelPendingActionPending, map[string]any{
			"status":        contracts.ChannelPendingActionFailed,
			"decider":       decider,
			"decision_note": "审批已过期",
			"decided_at":    &now,
			"executed_at":   &now,
			"updated_at":    now,
		}); err != nil {
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

	rowsAffected, err := s.updatePendingAction(ctx, row.ID, contracts.ChannelPendingActionPending, map[string]any{
		"status":        contracts.ChannelPendingActionApproved,
		"decider":       decider,
		"decision_note": "已批准",
		"decided_at":    &now,
		"updated_at":    now,
	})
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	if rowsAffected == 0 {
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
		if uerr := s.finishPendingActionExecution(ctx, actionID, contracts.ChannelPendingActionFailed, failMsg); uerr != nil {
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
		if bridge := s.toolApprovalBridgeSnapshot(); bridge != nil {
			notified, hasWaiter := bridge.NotifyIfWaiting(actionID, PendingActionApprove)
			if hasWaiter {
				if !notified {
					s.slog().Warn("tool approval approve notify skipped",
						"action_id", actionID,
						"decision", PendingActionApprove,
					)
				}
				return PendingActionDecisionResult{
					Action:           pendingActionRowToView(approvedRow),
					Decision:         PendingActionApprove,
					Message:          "已批准，已通知会话继续执行该工具请求。",
					ExecutionMessage: "已批准，等待会话执行结果。",
				}, nil
			}
		}
		s.slog().Warn("tool approval approve waiter missing",
			"action_id", actionID,
			"decision", PendingActionApprove,
		)
		failMsg := "当前会话已结束，该工具审批已失效。"
		if err := s.finishPendingActionExecution(ctx, actionID, contracts.ChannelPendingActionFailed, failMsg); err != nil {
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
	finalStatus := contracts.ChannelPendingActionExecuted
	finalMsg := execResult.Message
	if !execResult.Success {
		finalStatus = contracts.ChannelPendingActionFailed
	}
	if finalMsg == "" {
		if finalStatus == contracts.ChannelPendingActionExecuted {
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
	if ctx == nil {
		return PendingActionDecisionResult{}, fmt.Errorf("context 不能为空")
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
	if row.Status != contracts.ChannelPendingActionPending {
		out := finalizeAlreadyDecidedResult(row)
		out.Decision = PendingActionReject
		return out, nil
	}

	now := time.Now()
	rowsAffected, err := s.updatePendingAction(ctx, row.ID, contracts.ChannelPendingActionPending, map[string]any{
		"status":        contracts.ChannelPendingActionRejected,
		"decider":       decider,
		"decision_note": rejectDecisionNote(note),
		"decided_at":    &now,
		"updated_at":    now,
	})
	if err != nil {
		return PendingActionDecisionResult{}, err
	}
	if rowsAffected == 0 {
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
	if bridge := s.toolApprovalBridgeSnapshot(); bridge != nil {
		notified, hasWaiter := bridge.NotifyIfWaiting(actionID, PendingActionReject)
		if hasWaiter && !notified {
			s.slog().Warn("tool approval reject notify skipped",
				"action_id", actionID,
				"decision", PendingActionReject,
			)
		}
	}
	return PendingActionDecisionResult{
		Action:   pendingActionRowToView(rejectedRow),
		Decision: PendingActionReject,
		Message:  "已拒绝该操作。",
	}, nil
}

func (s *Service) finishPendingActionExecution(ctx context.Context, actionID uint, status contracts.ChannelPendingActionStatus, message string) error {
	if ctx == nil {
		return fmt.Errorf("context 不能为空")
	}
	now := time.Now()
	_, err := s.updatePendingAction(ctx, actionID, "", map[string]any{
		"status":        status,
		"decision_note": message,
		"executed_at":   &now,
		"updated_at":    now,
	})
	return err
}

func rejectDecisionNote(note string) string {
	if note == "" {
		return "已拒绝"
	}
	return note
}

func finalizeAlreadyDecidedResult(row store.ChannelPendingAction) PendingActionDecisionResult {
	view := pendingActionRowToView(row)
	note := view.DecisionNote
	out := PendingActionDecisionResult{
		Action:            view,
		AlreadyFinalState: true,
	}
	switch view.Status {
	case contracts.ChannelPendingActionExecuted:
		if note == "" {
			note = "该操作已执行完成。"
		}
		out.Message = note
	case contracts.ChannelPendingActionRejected:
		if note == "" {
			note = "该操作已被拒绝。"
		}
		out.Message = note
	case contracts.ChannelPendingActionFailed:
		if note == "" {
			note = "该操作执行失败。"
		}
		out.Message = note
	case contracts.ChannelPendingActionApproved:
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
