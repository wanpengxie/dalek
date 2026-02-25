package channel

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"dalek/internal/store"
)

func TestToolApprovalEventPayload_RoundTrip(t *testing.T) {
	pending := []PendingActionView{
		{
			ID: 7,
			Action: contracts.TurnAction{
				Name: "sdk_tool_permission",
				Args: map[string]any{
					"tool_name": "Bash",
					"command":   "git push origin main",
				},
			},
			Status: store.ChannelPendingActionPending,
		},
	}
	raw := EncodeToolApprovalEventPayload("请审批", pending)
	if strings.TrimSpace(raw) == "" {
		t.Fatalf("payload should not be empty")
	}
	decoded, ok := ParseToolApprovalEventPayload(raw)
	if !ok {
		t.Fatalf("parse payload failed")
	}
	if strings.TrimSpace(decoded.Message) != "请审批" {
		t.Fatalf("message mismatch: %q", decoded.Message)
	}
	if len(decoded.PendingActions) != 1 || decoded.PendingActions[0].ID != 7 {
		t.Fatalf("pending actions mismatch: %+v", decoded.PendingActions)
	}
}

func TestToolApprovalBridge_NotifyIfWaiting(t *testing.T) {
	bridge := NewToolApprovalBridge()
	actionID := uint(42)

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	decisionCh := make(chan PendingActionDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		decision, err := bridge.Wait(waitCtx, actionID)
		if err != nil {
			errCh <- err
			return
		}
		decisionCh <- decision
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if bridge.hasWaiter(actionID) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !bridge.hasWaiter(actionID) {
		t.Fatalf("waiter was not registered")
	}

	notified, hasWaiter := bridge.NotifyIfWaiting(actionID, PendingActionApprove)
	if !hasWaiter || !notified {
		t.Fatalf("notify should succeed: has_waiter=%v notified=%v", hasWaiter, notified)
	}

	select {
	case err := <-errCh:
		t.Fatalf("wait should succeed, got err=%v", err)
	case decision := <-decisionCh:
		if decision != PendingActionApprove {
			t.Fatalf("decision mismatch: %s", decision)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatalf("waiter did not receive decision")
	}

	notified, hasWaiter = bridge.NotifyIfWaiting(999, PendingActionReject)
	if hasWaiter || notified {
		t.Fatalf("notify on missing waiter should be false,false, got %v,%v", hasWaiter, notified)
	}
}

func TestApprovePendingAction_SDKToolApprovalNotifiesWaiter(t *testing.T) {
	svc := newToolApprovalTestService(t)
	created, err := svc.CreatePendingActions(context.Background(), 11, 22, []contracts.TurnAction{
		newSDKToolApprovalAction("Bash", map[string]any{"command": "git push origin main"}),
	})
	if err != nil {
		t.Fatalf("CreatePendingActions failed: %v", err)
	}
	if len(created) != 1 {
		t.Fatalf("expected one pending action, got=%d", len(created))
	}
	actionID := created[0].ID

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	decisionCh := make(chan PendingActionDecision, 1)
	waitErrCh := make(chan error, 1)
	go func() {
		decision, err := svc.toolApprovalBridge.Wait(waitCtx, actionID)
		if err != nil {
			waitErrCh <- err
			return
		}
		decisionCh <- decision
	}()
	waitBridgeReady(t, svc, actionID)

	decision, err := svc.ApprovePendingAction(context.Background(), actionID, "alice")
	if err != nil {
		t.Fatalf("ApprovePendingAction failed: %v", err)
	}
	if decision.Action.Status != store.ChannelPendingActionApproved {
		t.Fatalf("sdk tool approval should stay approved, got=%s", decision.Action.Status)
	}
	if !strings.Contains(decision.Message, "继续") {
		t.Fatalf("unexpected decision message: %q", decision.Message)
	}

	select {
	case err := <-waitErrCh:
		t.Fatalf("waiter should be notified, got err=%v", err)
	case got := <-decisionCh:
		if got != PendingActionApprove {
			t.Fatalf("waiter decision mismatch: %s", got)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatalf("waiter was not notified")
	}
}

func TestApprovePendingAction_SDKToolApprovalWithoutWaiterMarksFailed(t *testing.T) {
	svc := newToolApprovalTestService(t)
	created, err := svc.CreatePendingActions(context.Background(), 33, 44, []contracts.TurnAction{
		newSDKToolApprovalAction("Bash", map[string]any{"command": "git push origin main"}),
	})
	if err != nil {
		t.Fatalf("CreatePendingActions failed: %v", err)
	}
	actionID := created[0].ID

	decision, err := svc.ApprovePendingAction(context.Background(), actionID, "alice")
	if err != nil {
		t.Fatalf("ApprovePendingAction failed: %v", err)
	}
	if decision.Action.Status != store.ChannelPendingActionFailed {
		t.Fatalf("without waiter should mark failed, got=%s", decision.Action.Status)
	}
	if !strings.Contains(decision.Message, "会话已结束") {
		t.Fatalf("unexpected message: %q", decision.Message)
	}
}

func TestRejectPendingAction_SDKToolApprovalNotifiesWaiter(t *testing.T) {
	svc := newToolApprovalTestService(t)
	created, err := svc.CreatePendingActions(context.Background(), 55, 66, []contracts.TurnAction{
		newSDKToolApprovalAction("Bash", map[string]any{"command": "git push origin main"}),
	})
	if err != nil {
		t.Fatalf("CreatePendingActions failed: %v", err)
	}
	actionID := created[0].ID

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer waitCancel()
	decisionCh := make(chan PendingActionDecision, 1)
	waitErrCh := make(chan error, 1)
	go func() {
		decision, err := svc.toolApprovalBridge.Wait(waitCtx, actionID)
		if err != nil {
			waitErrCh <- err
			return
		}
		decisionCh <- decision
	}()
	waitBridgeReady(t, svc, actionID)

	decision, err := svc.RejectPendingAction(context.Background(), actionID, "bob", "拒绝")
	if err != nil {
		t.Fatalf("RejectPendingAction failed: %v", err)
	}
	if decision.Action.Status != store.ChannelPendingActionRejected {
		t.Fatalf("status should be rejected, got=%s", decision.Action.Status)
	}

	select {
	case err := <-waitErrCh:
		t.Fatalf("waiter should be notified, got err=%v", err)
	case got := <-decisionCh:
		if got != PendingActionReject {
			t.Fatalf("waiter decision mismatch: %s", got)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatalf("waiter was not notified")
	}
}

func TestSDKToolApprovalCommandSignature_CommandFamily(t *testing.T) {
	got := sdkToolApprovalCommandSignature("Bash", map[string]any{
		"command": "git push origin main --dry-run",
	})
	if got != "bash|git push" {
		t.Fatalf("signature mismatch: %q", got)
	}
}

func TestSDKToolApprovalHandler_RejectAutoDeniesCommandFamily(t *testing.T) {
	svc := newToolApprovalTestService(t)
	handler := svc.buildSDKToolApprovalHandler(context.Background(), 11, 22)
	if handler == nil {
		t.Fatalf("handler should not be nil")
	}

	type decideResult struct {
		allow bool
		err   error
	}
	firstCh := make(chan decideResult, 1)
	go func() {
		allow, err := handler(context.Background(), "Bash", map[string]any{
			"command": "git push origin main",
		})
		firstCh <- decideResult{allow: allow, err: err}
	}()

	var actionID uint
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pending, err := svc.ListPendingActions(context.Background(), 22)
		if err != nil {
			t.Fatalf("ListPendingActions failed: %v", err)
		}
		if len(pending) > 0 {
			actionID = pending[0].ID
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if actionID == 0 {
		t.Fatalf("pending action was not created")
	}
	waitBridgeReady(t, svc, actionID)

	if _, err := svc.RejectPendingAction(context.Background(), actionID, "tester", "reject"); err != nil {
		t.Fatalf("RejectPendingAction failed: %v", err)
	}
	select {
	case first := <-firstCh:
		if first.err != nil {
			t.Fatalf("first handler should not return error, got=%v", first.err)
		}
		if first.allow {
			t.Fatalf("first handler should be denied after reject")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("first handler did not return")
	}

	allow, err := handler(context.Background(), "Bash", map[string]any{
		"command": "git push origin main --dry-run",
	})
	if err == nil {
		t.Fatalf("follow-up command should be auto denied with error message")
	}
	if allow {
		t.Fatalf("follow-up command should not be allowed")
	}
	if !strings.Contains(err.Error(), "自动拒绝") {
		t.Fatalf("unexpected auto-deny error: %v", err)
	}

	pending, err := svc.ListPendingActions(context.Background(), 22)
	if err != nil {
		t.Fatalf("ListPendingActions failed: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("follow-up auto deny should not create new pending action, got=%d", len(pending))
	}
}

func waitBridgeReady(t *testing.T, svc *Service, actionID uint) {
	t.Helper()
	deadline := time.Now().Add(1500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if svc.toolApprovalBridge != nil && svc.toolApprovalBridge.hasWaiter(actionID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waiter did not register in time")
}

func newToolApprovalTestService(t *testing.T) *Service {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "tool-approval.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	repoRoot := t.TempDir()
	return New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		DB:         db,
	})
}
