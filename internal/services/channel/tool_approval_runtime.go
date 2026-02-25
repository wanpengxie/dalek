package channel

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"dalek/internal/contracts"
)

func (s *Service) buildSDKToolApprovalHandler(turnCtx context.Context, conversationID, jobID uint) func(ctx context.Context, toolName string, input map[string]any) (bool, error) {
	if s == nil || conversationID == 0 || jobID == 0 {
		return nil
	}
	var (
		denyMu       sync.Mutex
		deniedSigSet = map[string]struct{}{}
	)
	return func(cbCtx context.Context, toolName string, input map[string]any) (bool, error) {
		waitCtx := cbCtx
		if waitCtx == nil {
			waitCtx = turnCtx
		}
		if waitCtx == nil {
			waitCtx = context.Background()
		}
		signature := sdkToolApprovalCommandSignature(toolName, input)
		denyMu.Lock()
		_, autoDenied := deniedSigSet[signature]
		denyMu.Unlock()
		if autoDenied {
			if cmd := strings.TrimSpace(readToolApprovalCommand(input)); cmd != "" {
				return false, fmt.Errorf("该轮会话中用户已拒绝同类命令（%s），本次自动拒绝", cmd)
			}
			return false, fmt.Errorf("该轮会话中用户已拒绝同类工具操作，本次自动拒绝")
		}

		action := newSDKToolApprovalAction(toolName, input)
		created, err := s.CreatePendingActions(waitCtx, conversationID, jobID, []contracts.TurnAction{action})
		if err != nil {
			return false, fmt.Errorf("创建工具审批记录失败: %w", err)
		}
		if len(created) == 0 {
			return false, fmt.Errorf("创建工具审批记录失败: empty pending action")
		}
		pending := created[0]

		payloadText := EncodeToolApprovalEventPayload(buildSDKToolApprovalMessage(toolName, input), []PendingActionView{pending})
		eventCtx := turnCtx
		if eventCtx == nil {
			eventCtx = waitCtx
		}
		if payloadText != "" {
			emitStreamAgentEvent(eventCtx, AgentEvent{
				Stream: "",
				Data: AgentEventData{
					Phase:    ToolApprovalEventType,
					Text:     payloadText,
					ToolName: strings.TrimSpace(toolName),
					ToolInput: strings.TrimSpace(
						readToolApprovalCommand(input),
					),
				},
			})
		}

		if s.toolApprovalBridge == nil {
			return false, fmt.Errorf("工具审批桥接未初始化")
		}
		decision, err := s.toolApprovalBridge.Wait(waitCtx, pending.ID)
		if err != nil {
			return false, fmt.Errorf("等待工具审批失败: %w", err)
		}
		switch decision.normalize() {
		case PendingActionApprove:
			return true, nil
		case PendingActionReject:
			denyMu.Lock()
			deniedSigSet[signature] = struct{}{}
			denyMu.Unlock()
			return false, nil
		default:
			return false, fmt.Errorf("未知审批结果: %s", strings.TrimSpace(string(decision)))
		}
	}
}

func sdkToolApprovalCommandSignature(toolName string, input map[string]any) string {
	tool := strings.ToLower(strings.TrimSpace(toolName))
	if tool == "" {
		tool = "unknown"
	}
	cmd := strings.ToLower(strings.TrimSpace(readToolApprovalCommand(input)))
	if cmd == "" {
		return tool
	}
	parts := strings.Fields(cmd)
	switch len(parts) {
	case 0:
		return tool
	case 1:
		return tool + "|" + parts[0]
	default:
		return tool + "|" + parts[0] + " " + parts[1]
	}
}
