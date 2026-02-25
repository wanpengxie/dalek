package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"dalek/internal/contracts"
)

const (
	// ToolApprovalEventType is emitted to gateway event bus when SDK-level tool
	// permission approval is requested during an active turn.
	ToolApprovalEventType = "tool_approval_request"

	// sdkToolApprovalActionName is persisted in ChannelPendingAction.ActionJSON
	// to distinguish SDK-level tool approvals from app-level TurnResponse actions.
	sdkToolApprovalActionName = "sdk_tool_permission"
)

// ToolApprovalBridge coordinates between CanUseTool callbacks (waiters)
// and external decision sources (notifiers, e.g., Feishu card callbacks).
//
// Flow:
//  1. CanUseTool callback fires → creates PendingAction → calls Wait(actionID)
//  2. Feishu card callback → calls Notify(actionID, decision)
//  3. Wait unblocks → CanUseTool returns Allow/Deny to Claude SDK
type ToolApprovalBridge struct {
	mu      sync.Mutex
	waiters map[uint]chan PendingActionDecision
}

// NewToolApprovalBridge creates a new bridge.
func NewToolApprovalBridge() *ToolApprovalBridge {
	return &ToolApprovalBridge{
		waiters: make(map[uint]chan PendingActionDecision),
	}
}

// Wait blocks until a decision is received for the given action ID
// or the context is cancelled (e.g., turn timeout).
func (b *ToolApprovalBridge) Wait(ctx context.Context, actionID uint) (PendingActionDecision, error) {
	ch := b.register(actionID)
	defer b.unregister(actionID)

	select {
	case decision := <-ch:
		return decision, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// Notify sends a decision to the waiter for the given action ID.
// If no waiter exists, the notification is silently dropped.
func (b *ToolApprovalBridge) Notify(actionID uint, decision PendingActionDecision) {
	b.mu.Lock()
	ch, ok := b.waiters[actionID]
	b.mu.Unlock()
	if ok {
		select {
		case ch <- decision:
		default:
		}
	}
}

// HasWaiter returns true if there is an active SDK-level waiter for the given action.
// Used to distinguish SDK-level approvals (handled by Claude CLI) from
// app-level approvals (handled by ActionExecutor).
func (b *ToolApprovalBridge) HasWaiter(actionID uint) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.waiters[actionID]
	return ok
}

func (b *ToolApprovalBridge) register(actionID uint) <-chan PendingActionDecision {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan PendingActionDecision, 1)
	b.waiters[actionID] = ch
	return ch
}

func (b *ToolApprovalBridge) unregister(actionID uint) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.waiters, actionID)
}

// ToolApprovalNotifier sends tool approval cards to users.
// chatID is the external chat identifier (e.g., Feishu open_chat_id).
// Implemented by the daemon layer (Feishu-specific).
type ToolApprovalNotifier func(ctx context.Context, chatID string, actions []PendingActionView) error

type ToolApprovalEventPayload struct {
	Message        string              `json:"message,omitempty"`
	PendingActions []PendingActionView `json:"pending_actions,omitempty"`
}

func EncodeToolApprovalEventPayload(message string, pending []PendingActionView) string {
	msg := strings.TrimSpace(message)
	views := copyPendingActionViews(pending)
	if msg == "" && len(views) == 0 {
		return ""
	}
	payload := ToolApprovalEventPayload{
		Message:        msg,
		PendingActions: views,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func ParseToolApprovalEventPayload(raw string) (ToolApprovalEventPayload, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ToolApprovalEventPayload{}, false
	}
	var payload ToolApprovalEventPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ToolApprovalEventPayload{}, false
	}
	payload.Message = strings.TrimSpace(payload.Message)
	payload.PendingActions = copyPendingActionViews(payload.PendingActions)
	if payload.Message == "" && len(payload.PendingActions) == 0 {
		return ToolApprovalEventPayload{}, false
	}
	return payload, true
}

func newSDKToolApprovalAction(toolName string, input map[string]any) contracts.TurnAction {
	tool := strings.TrimSpace(toolName)
	if tool == "" {
		tool = "unknown"
	}
	args := map[string]any{
		"tool_name": tool,
	}
	if command := readToolApprovalCommand(input); command != "" {
		args["command"] = command
	}
	if cleanInput := sanitizeToolApprovalInput(input); len(cleanInput) > 0 {
		args["input"] = cleanInput
	}
	action := contracts.TurnAction{
		Name: sdkToolApprovalActionName,
		Args: args,
	}
	action.Normalize()
	return action
}

func isSDKToolApprovalAction(action contracts.TurnAction) bool {
	return strings.EqualFold(strings.TrimSpace(action.Name), sdkToolApprovalActionName)
}

func buildSDKToolApprovalMessage(toolName string, input map[string]any) string {
	tool := strings.TrimSpace(toolName)
	if tool == "" {
		tool = "unknown"
	}
	if cmd := readToolApprovalCommand(input); cmd != "" {
		return fmt.Sprintf("检测到工具调用请求：`%s`（`%s`），请审批。", tool, cmd)
	}
	return fmt.Sprintf("检测到工具调用请求：`%s`，请审批。", tool)
}

func readToolApprovalCommand(input map[string]any) string {
	if len(input) == 0 {
		return ""
	}
	for _, key := range []string{"command", "cmd"} {
		raw, ok := input[key]
		if !ok {
			continue
		}
		if cmd := strings.TrimSpace(fmt.Sprint(raw)); cmd != "" && cmd != "<nil>" {
			return cmd
		}
	}
	return ""
}

func sanitizeToolApprovalInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return map[string]any{
			"raw": strings.TrimSpace(fmt.Sprint(input)),
		}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{
			"raw": strings.TrimSpace(string(raw)),
		}
	}
	if out == nil {
		return map[string]any{}
	}
	return out
}
