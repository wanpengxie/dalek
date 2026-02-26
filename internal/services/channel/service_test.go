package channel

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/repo"
	"dalek/internal/services/channel/agentcli"
	"dalek/internal/services/core"
	"dalek/internal/store"

	"gorm.io/gorm"
)

func TestResolveGatewayAgentHints_DefaultProvider(t *testing.T) {
	provider, model := resolveGatewayAgentHints(agentcli.ConfigOverride{})
	if provider != agentcli.ProviderClaude {
		t.Fatalf("provider should default to claude, got=%q", provider)
	}
	if model != "" {
		t.Fatalf("model should be empty without override/env, got=%q", model)
	}
}

func TestResolveGatewayAgentHints_EnvOverride(t *testing.T) {
	t.Setenv("DALEK_GATEWAY_AGENT_PROVIDER", "codex")
	t.Setenv("DALEK_GATEWAY_AGENT_MODEL", "gpt-5-codex-env")

	provider, model := resolveGatewayAgentHints(agentcli.ConfigOverride{
		Provider: "claude",
		Model:    "claude-opus-4-6",
	})
	if provider != agentcli.ProviderCodex {
		t.Fatalf("provider should follow env override, got=%q", provider)
	}
	if model != "gpt-5-codex-env" {
		t.Fatalf("model should follow env override, got=%q", model)
	}
}

func TestProcessInbound_ListTicketsAndOutboxSent(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	svc := newChannelServiceWithFakeAgent(t, db)

	if err := db.Create(&store.Ticket{Title: "ticket-a", WorkflowStatus: contracts.TicketBacklog, Priority: 2}).Error; err != nil {
		t.Fatalf("seed ticket-a failed: %v", err)
	}
	if err := db.Create(&store.Ticket{Title: "ticket-b", WorkflowStatus: contracts.TicketActive, Priority: 1}).Error; err != nil {
		t.Fatalf("seed ticket-b failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-1",
		PeerMessageID:      "msg-1",
		SenderID:           "u1",
		SenderName:         "Alice",
		Text:               "请给我 ticket 列表",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if result.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("expected succeeded, got %s err=%s", result.JobStatus, result.JobError)
	}
	if strings.TrimSpace(result.ReplyText) == "" {
		t.Fatalf("reply should not be empty")
	}

	var inbound store.ChannelMessage
	if err := db.First(&inbound, result.InboundMessageID).Error; err != nil {
		t.Fatalf("query inbound failed: %v", err)
	}
	if inbound.Status != contracts.ChannelMessageProcessed {
		t.Fatalf("inbound status should be processed, got %s", inbound.Status)
	}
	if strings.TrimSpace(inbound.SenderName) != "Alice" {
		t.Fatalf("inbound sender_name should be Alice, got %q", inbound.SenderName)
	}

	var outbound store.ChannelMessage
	if err := db.First(&outbound, result.OutboundMessageID).Error; err != nil {
		t.Fatalf("query outbound failed: %v", err)
	}
	if outbound.Status != contracts.ChannelMessageSent {
		t.Fatalf("outbound status should be sent, got %s", outbound.Status)
	}

	var outbox store.ChannelOutbox
	if err := db.First(&outbox, result.OutboxID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if outbox.Status != contracts.ChannelOutboxSent {
		t.Fatalf("outbox status should be sent, got %s", outbox.Status)
	}
}

func TestProcessInbound_IdempotentByAdapterAndPeerMessageID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	svc := newChannelServiceWithFakeAgent(t, db)

	env := contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeWeb,
		Adapter:            "web.ws",
		PeerConversationID: "web-conv",
		PeerMessageID:      "web-msg-1",
		SenderID:           "u-web",
		Text:               "ticket 列表",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	r1, err := svc.ProcessInbound(ctx, env)
	if err != nil {
		t.Fatalf("first ProcessInbound failed: %v", err)
	}
	r2, err := svc.ProcessInbound(ctx, env)
	if err != nil {
		t.Fatalf("second ProcessInbound failed: %v", err)
	}

	if r1.JobID != r2.JobID {
		t.Fatalf("expected same job id, got %d and %d", r1.JobID, r2.JobID)
	}
	if r1.InboundMessageID != r2.InboundMessageID {
		t.Fatalf("expected same inbound message id, got %d and %d", r1.InboundMessageID, r2.InboundMessageID)
	}

	var msgCount int64
	if err := db.Model(&store.ChannelMessage{}).
		Where("direction = ? AND adapter = ? AND peer_message_id = ?", contracts.ChannelMessageIn, "web.ws", "web-msg-1").
		Count(&msgCount).Error; err != nil {
		t.Fatalf("count inbound messages failed: %v", err)
	}
	if msgCount != 1 {
		t.Fatalf("expected 1 inbound message, got %d", msgCount)
	}

	var jobCount int64
	if err := db.Model(&store.ChannelTurnJob{}).
		Where("inbound_message_id = ?", r1.InboundMessageID).
		Count(&jobCount).Error; err != nil {
		t.Fatalf("count turn jobs failed: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("expected 1 turn job, got %d", jobCount)
	}
}

func TestProcessInbound_IdempotentScopeIncludesConversation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	svc := newChannelServiceWithFakeAgent(t, db)

	base := contracts.InboundEnvelope{
		Schema:      contracts.ChannelInboundSchemaV1,
		ChannelType: contracts.ChannelTypeWeb,
		Adapter:     "web.ws",
		SenderID:    "u-web",
		Text:        "ticket 列表",
		ReceivedAt:  time.Now().UTC().Format(time.RFC3339),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	env1 := base
	env1.PeerConversationID = "web-conv-1"
	env1.PeerMessageID = "web-msg-dup"
	r1, err := svc.ProcessInbound(ctx, env1)
	if err != nil {
		t.Fatalf("first ProcessInbound failed: %v", err)
	}
	env2 := base
	env2.PeerConversationID = "web-conv-2"
	env2.PeerMessageID = "web-msg-dup"
	r2, err := svc.ProcessInbound(ctx, env2)
	if err != nil {
		t.Fatalf("second ProcessInbound failed: %v", err)
	}

	if r1.ConversationID == r2.ConversationID {
		t.Fatalf("expected different conversations, got %d and %d", r1.ConversationID, r2.ConversationID)
	}
	if r1.InboundMessageID == r2.InboundMessageID {
		t.Fatalf("expected different inbound message id, got %d and %d", r1.InboundMessageID, r2.InboundMessageID)
	}
	if r1.JobID == r2.JobID {
		t.Fatalf("expected different job id, got %d and %d", r1.JobID, r2.JobID)
	}

	var msgCount int64
	if err := db.Model(&store.ChannelMessage{}).
		Where("direction = ? AND adapter = ? AND peer_message_id = ?", contracts.ChannelMessageIn, "web.ws", "web-msg-dup").
		Count(&msgCount).Error; err != nil {
		t.Fatalf("count inbound messages failed: %v", err)
	}
	if msgCount != 2 {
		t.Fatalf("expected 2 inbound messages for different conversations, got %d", msgCount)
	}
}

func TestProcessInbound_TicketDetailActionSuccess(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	svc := newChannelServiceWithFakeAgent(t, db)

	ticket := store.Ticket{
		Title:          "detail-target",
		WorkflowStatus: contracts.TicketActive,
		Priority:       3,
		Description:    "for ticket_detail test",
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("seed ticket failed: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-ticket-detail",
		PeerMessageID:      "msg-ticket-detail-1",
		SenderID:           "u1",
		Text:               fmt.Sprintf("请查询 ticket %d", ticket.ID),
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if result.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("expected succeeded, got %s err=%s", result.JobStatus, result.JobError)
	}
	if !strings.Contains(result.ReplyText, fmt.Sprintf("t%d", ticket.ID)) {
		t.Fatalf("reply should include ticket id, got:\n%s", result.ReplyText)
	}
}

func TestProcessInbound_CreateTicketActionSuccess(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	svc := newChannelServiceWithFakeAgent(t, db)

	title := "ws e2e create ticket"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeWeb,
		Adapter:            "web.ws",
		PeerConversationID: "conv-create-ticket",
		PeerMessageID:      "msg-create-ticket-1",
		SenderID:           "u1",
		Text:               "请创建 ticket: " + title,
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if result.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("expected succeeded, got %s err=%s", result.JobStatus, result.JobError)
	}
	if !strings.Contains(result.ReplyText, title) {
		t.Fatalf("reply should include create ticket title, got:\n%s", result.ReplyText)
	}
}

func TestProcessInbound_StructuredTurnResponseCreatesPendingActions(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	turnResp := `{"schema":"dalek.turn_response.v1","reply_text":"请确认是否执行","requires_confirmation":true,"actions":[{"name":"create_ticket","args":{"title":"pending ticket","description":"pending desc"}}]}`
	installFakeClaudeBinaryWithMessageText(t, turnResp)

	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{Mode: "cli"},
		},
		DB: db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-pending",
		PeerMessageID:      "msg-pending-1",
		SenderID:           "u1",
		Text:               "请创建 ticket，需要审批",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if result.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("expected succeeded, got %s", result.JobStatus)
	}
	if strings.TrimSpace(result.ReplyText) != "请确认是否执行" {
		t.Fatalf("unexpected reply text: %q", result.ReplyText)
	}
	if len(result.PendingActions) != 1 {
		t.Fatalf("expected 1 pending action, got=%d", len(result.PendingActions))
	}
	got := result.PendingActions[0]
	if got.Status != contracts.ChannelPendingActionPending {
		t.Fatalf("pending action status should be pending, got=%s", got.Status)
	}
	if strings.TrimSpace(got.Action.Name) != contracts.ActionCreateTicket {
		t.Fatalf("pending action name mismatch: %q", got.Action.Name)
	}

	var rows []store.ChannelPendingAction
	if err := db.Where("job_id = ?", result.JobID).Order("id asc").Find(&rows).Error; err != nil {
		t.Fatalf("query pending actions failed: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 pending action row, got=%d", len(rows))
	}
	if rows[0].Status != contracts.ChannelPendingActionPending {
		t.Fatalf("db pending action status mismatch: %s", rows[0].Status)
	}
}

func TestDecidePendingAction_ApproveExecutesAndMarksExecuted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	turnResp := `{"schema":"dalek.turn_response.v1","reply_text":"请审批","requires_confirmation":true,"actions":[{"name":"create_ticket","args":{"title":"approved ticket","description":"from approval"}}]}`
	installFakeClaudeBinaryWithMessageText(t, turnResp)

	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{Mode: "cli"},
		},
		DB: db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-approve",
		PeerMessageID:      "msg-approve-1",
		SenderID:           "u1",
		Text:               "发起审批",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if len(result.PendingActions) != 1 {
		t.Fatalf("expected 1 pending action, got=%d", len(result.PendingActions))
	}

	decision, err := svc.DecidePendingAction(context.Background(), PendingActionDecisionRequest{
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-approve",
		PendingActionID:    result.PendingActions[0].ID,
		Decision:           PendingActionApprove,
		Decider:            "alice",
	})
	if err != nil {
		t.Fatalf("DecidePendingAction approve failed: %v", err)
	}
	if decision.Action.Status != contracts.ChannelPendingActionExecuted {
		t.Fatalf("pending action status should be executed, got=%s", decision.Action.Status)
	}
	if strings.TrimSpace(decision.ExecutionMessage) == "" {
		t.Fatalf("execution message should not be empty")
	}

	var cnt int64
	if err := db.Model(&store.Ticket{}).Where("title = ?", "approved ticket").Count(&cnt).Error; err != nil {
		t.Fatalf("count approved ticket failed: %v", err)
	}
	if cnt != 1 {
		t.Fatalf("approved action should create ticket, count=%d", cnt)
	}
}

func TestDecidePendingAction_RejectMarksRejected(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	turnResp := `{"schema":"dalek.turn_response.v1","reply_text":"请审批","requires_confirmation":true,"actions":[{"name":"create_ticket","args":{"title":"rejected ticket","description":"from reject"}}]}`
	installFakeClaudeBinaryWithMessageText(t, turnResp)

	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{Mode: "cli"},
		},
		DB: db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-reject",
		PeerMessageID:      "msg-reject-1",
		SenderID:           "u1",
		Text:               "发起审批",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if len(result.PendingActions) != 1 {
		t.Fatalf("expected 1 pending action, got=%d", len(result.PendingActions))
	}

	decision, err := svc.DecidePendingAction(context.Background(), PendingActionDecisionRequest{
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-reject",
		PendingActionID:    result.PendingActions[0].ID,
		Decision:           PendingActionReject,
		Decider:            "bob",
		Note:               "不通过",
	})
	if err != nil {
		t.Fatalf("DecidePendingAction reject failed: %v", err)
	}
	if decision.Action.Status != contracts.ChannelPendingActionRejected {
		t.Fatalf("pending action status should be rejected, got=%s", decision.Action.Status)
	}

	var cnt int64
	if err := db.Model(&store.Ticket{}).Where("title = ?", "rejected ticket").Count(&cnt).Error; err != nil {
		t.Fatalf("count rejected ticket failed: %v", err)
	}
	if cnt != 0 {
		t.Fatalf("rejected action should not create ticket, count=%d", cnt)
	}
}

func TestProcessInbound_FailedWhenAgentNoResponse(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	installFakeClaudeBinary(t, true)
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{Mode: "cli"},
		},
		DB: db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-empty",
		PeerMessageID:      "msg-empty",
		SenderID:           "u1",
		Text:               "你好",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound should return job result instead of hard error, got: %v", err)
	}
	if result.JobStatus != contracts.ChannelTurnFailed {
		t.Fatalf("expected failed when agent has no response, got status=%s error=%s", result.JobStatus, result.JobError)
	}
	if !strings.Contains(result.JobError, "无响应") {
		t.Fatalf("job_error should include no response reason, got: %s", result.JobError)
	}
}

func TestProcessInbound_ClaudeResumeSessionFlow(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "claude-args.log")
	scriptPath := filepath.Join(binDir, "claude")
	script := `#!/usr/bin/env bash
set -euo pipefail
log="${DALEK_TEST_CLAUDE_LOG:-}"
if [[ -n "$log" ]]; then
  printf '%q\n' "$*" >> "$log"
fi
session_id=""
args=("$@")
for ((i=0;i<${#args[@]};i++)); do
  case "${args[$i]}" in
    --session-id|--resume)
      if (( i + 1 < ${#args[@]} )); then
        session_id="${args[$((i+1))]}"
      fi
      ;;
  esac
done
if [[ -z "$session_id" ]]; then
  session_id="sess-default"
fi
prompt=""
if (( ${#args[@]} > 0 )); then
  prompt="${args[$((${#args[@]}-1))]}"
fi
python3 - "$session_id" "$prompt" <<'PY'
import json
import sys

sid = (sys.argv[1] or "").strip()
prompt = (sys.argv[2] or "").strip()
print(json.dumps({"session_id": sid, "message": {"text": "echo:" + prompt}}, ensure_ascii=False))
PY
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude(resume) failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DALEK_TEST_CLAUDE_LOG", logPath)

	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{Mode: "cli"},
		},
		DB: db,
	})

	ctx1, cancel1 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel1()
	_, err = svc.ProcessInbound(ctx1, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-resume",
		PeerMessageID:      "msg-resume-1",
		SenderID:           "u1",
		Text:               "第一轮",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("first ProcessInbound failed: %v", err)
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel2()
	_, err = svc.ProcessInbound(ctx2, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-resume",
		PeerMessageID:      "msg-resume-2",
		SenderID:           "u1",
		Text:               "第二轮",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("second ProcessInbound failed: %v", err)
	}

	var conv store.ChannelConversation
	if err := db.Where("peer_conversation_id = ?", "conv-resume").First(&conv).Error; err != nil {
		t.Fatalf("query conversation failed: %v", err)
	}
	if strings.TrimSpace(conv.AgentSessionID) == "" {
		t.Fatalf("conversation agent_session_id should not be empty")
	}

	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read claude log failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected at least 2 claude calls, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "--session-id") {
		t.Fatalf("first call should include --session-id, got: %s", lines[0])
	}
	if strings.Contains(lines[0], "--resume") {
		t.Fatalf("first call should not include --resume, got: %s", lines[0])
	}
	if !strings.Contains(lines[1], "--resume") {
		t.Fatalf("second call should include --resume, got: %s", lines[1])
	}
}

func TestProcessInbound_SerializesCLIRunsAcrossConcurrentTurns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}

	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "claude")
	script := `#!/usr/bin/env bash
set -euo pipefail
sleep 0.4
session_id=""
args=("$@")
for ((i=0;i<${#args[@]};i++)); do
  case "${args[$i]}" in
    --session-id|--resume)
      if (( i + 1 < ${#args[@]} )); then
        session_id="${args[$((i+1))]}"
      fi
      ;;
  esac
done
if [[ -z "$session_id" ]]; then
  session_id="sess-serial"
fi
prompt=""
if (( ${#args[@]} > 0 )); then
  prompt="${args[$((${#args[@]}-1))]}"
fi
python3 - "$session_id" "$prompt" <<'PY'
import json
import sys

sid = (sys.argv[1] or "").strip()
prompt = (sys.argv[2] or "").strip()
print(json.dumps({"session_id": sid, "message": {"text": "echo:" + prompt}}, ensure_ascii=False))
PY
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude(serial) failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{Mode: "cli"},
		},
		DB: db,
	})

	start := time.Now()
	errCh := make(chan error, 2)
	run := func(convID, msgID, text string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, runErr := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
			Schema:             contracts.ChannelInboundSchemaV1,
			ChannelType:        contracts.ChannelTypeCLI,
			Adapter:            "cli.local",
			PeerConversationID: convID,
			PeerMessageID:      msgID,
			SenderID:           "u1",
			Text:               text,
			ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
		})
		errCh <- runErr
	}
	go run("conv-serial-1", "msg-serial-1", "one")
	go run("conv-serial-2", "msg-serial-2", "two")

	if e := <-errCh; e != nil {
		t.Fatalf("first concurrent ProcessInbound failed: %v", e)
	}
	if e := <-errCh; e != nil {
		t.Fatalf("second concurrent ProcessInbound failed: %v", e)
	}

	elapsed := time.Since(start)
	if elapsed < 700*time.Millisecond {
		t.Fatalf("expected serialized runs to take >=700ms, got %v", elapsed)
	}
}

func TestProcessInbound_TimeoutStillReturnsFailedJobResult(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	installFakeClaudeBinaryWithDelay(t, 2*time.Second)
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{Mode: "cli"},
		},
		DB: db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeCLI,
		Adapter:            "cli.local",
		PeerConversationID: "conv-timeout",
		PeerMessageID:      "msg-timeout-1",
		SenderID:           "u1",
		Text:               "你好",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound should return failed job result instead of hard error, got: %v", err)
	}
	if result.JobStatus != contracts.ChannelTurnFailed {
		t.Fatalf("expected failed on timeout, got status=%s error=%s", result.JobStatus, result.JobError)
	}
	if strings.TrimSpace(result.JobError) == "" {
		t.Fatalf("job_error should not be empty on timeout")
	}
	if strings.TrimSpace(result.JobErrorType) != "timeout" {
		t.Fatalf("job_error_type should be timeout, got=%q error=%q", result.JobErrorType, result.JobError)
	}
}

func TestInterruptPeerConversation_CancelsRunningTurnWhenRunnerNotInterrupted(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		DB:         db,
	})

	binding := store.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        "im.feishu",
		PeerProjectKey: "",
		RolePolicyJSON: "{}",
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}
	conv := store.ChannelConversation{
		BindingID:          binding.ID,
		PeerConversationID: "chat-interrupt-1",
	}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	manager := &stubInterruptChatRunnerManager{}
	svc.chatRunners = manager

	canceled := false
	svc.setRunningTurn(1, conv.ID, "", func() { canceled = true })

	result, err := svc.InterruptPeerConversation(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-interrupt-1")
	if err != nil {
		t.Fatalf("InterruptPeerConversation failed: %v", err)
	}
	if result.Status != InterruptStatusHit {
		t.Fatalf("expected status=hit, got=%s", result.Status)
	}
	if !canceled {
		t.Fatalf("expected running turn context canceled")
	}
	if result.RunnerInterrupted {
		t.Fatalf("runner interrupted should be false when manager does not interrupt")
	}
	if !result.ContextCanceled {
		t.Fatalf("context canceled should be true")
	}
	if manager.interruptCalls != 1 {
		t.Fatalf("expected manager interrupt called once, got=%d", manager.interruptCalls)
	}
	if strings.TrimSpace(manager.lastConversationID) != fmt.Sprintf("%d", conv.ID) {
		t.Fatalf("unexpected conversation id: %q", manager.lastConversationID)
	}
}

func TestInterruptPeerConversation_UsesRunnerInterruptWithoutCancel(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		DB:         db,
	})

	binding := store.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        "im.feishu",
		PeerProjectKey: "",
		RolePolicyJSON: "{}",
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}
	conv := store.ChannelConversation{
		BindingID:          binding.ID,
		PeerConversationID: "chat-interrupt-2",
	}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	manager := &stubInterruptChatRunnerManager{interruptOK: true}
	svc.chatRunners = manager

	canceled := false
	svc.setRunningTurn(2, conv.ID, "", func() { canceled = true })

	result, err := svc.InterruptPeerConversation(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-interrupt-2")
	if err != nil {
		t.Fatalf("InterruptPeerConversation failed: %v", err)
	}
	if result.Status != InterruptStatusHit {
		t.Fatalf("expected status=hit, got=%s", result.Status)
	}
	if canceled {
		t.Fatalf("running turn context should not be canceled when runner interrupt succeeds")
	}
	if !result.RunnerInterrupted {
		t.Fatalf("runner interrupted should be true")
	}
	if result.ContextCanceled {
		t.Fatalf("context canceled should be false when runner interrupted")
	}
}

func TestResetPeerConversationSession_ClearsSessionAndClosesRunner(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		DB:         db,
	})

	binding := store.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeIM,
		Adapter:        "im.feishu",
		PeerProjectKey: "",
		RolePolicyJSON: "{}",
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}
	conv := store.ChannelConversation{
		BindingID:          binding.ID,
		PeerConversationID: "chat-reset-1",
		AgentSessionID:     "sess-old",
	}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}

	manager := &stubInterruptChatRunnerManager{}
	svc.chatRunners = manager

	changed, err := svc.ResetPeerConversationSession(context.Background(), contracts.ChannelTypeIM, "im.feishu", "chat-reset-1")
	if err != nil {
		t.Fatalf("ResetPeerConversationSession failed: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true")
	}
	if manager.closeCalls != 1 {
		t.Fatalf("close conversation should be called once, got=%d", manager.closeCalls)
	}
	if manager.lastClosedConversationID != fmt.Sprintf("%d", conv.ID) {
		t.Fatalf("unexpected closed conversation id: %q", manager.lastClosedConversationID)
	}

	var got store.ChannelConversation
	if err := db.First(&got, conv.ID).Error; err != nil {
		t.Fatalf("query conversation failed: %v", err)
	}
	if strings.TrimSpace(got.AgentSessionID) != "" {
		t.Fatalf("agent_session_id should be cleared, got=%q", got.AgentSessionID)
	}
}

func TestDispatchOutbox_EmptyAdapterPersistFailedStatus(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{Mode: "cli"},
		},
		DB: db,
	})

	binding := store.ChannelBinding{
		ProjectName:    "demo",
		ChannelType:    contracts.ChannelTypeWeb,
		Adapter:        "web.ws",
		PeerProjectKey: "",
		RolePolicyJSON: "{}",
		Enabled:        true,
	}
	if err := db.Create(&binding).Error; err != nil {
		t.Fatalf("create binding failed: %v", err)
	}
	conv := store.ChannelConversation{
		BindingID:          binding.ID,
		PeerConversationID: "conv-outbox-empty-adapter",
	}
	if err := db.Create(&conv).Error; err != nil {
		t.Fatalf("create conversation failed: %v", err)
	}
	outMsg := store.ChannelMessage{
		ConversationID: conv.ID,
		Direction:      contracts.ChannelMessageOut,
		Adapter:        "",
		SenderID:       "pm",
		ContentText:    "reply",
		PayloadJSON:    "{}",
		Status:         contracts.ChannelMessageProcessed,
	}
	if err := db.Create(&outMsg).Error; err != nil {
		t.Fatalf("create outbound message failed: %v", err)
	}
	outbox := store.ChannelOutbox{
		MessageID:   outMsg.ID,
		Adapter:     "",
		PayloadJSON: "{}",
		Status:      contracts.ChannelOutboxPending,
	}
	if err := db.Create(&outbox).Error; err != nil {
		t.Fatalf("create outbox failed: %v", err)
	}

	err = svc.dispatchOutbox(context.Background(), outbox.ID)
	if err == nil {
		t.Fatalf("dispatchOutbox should fail when adapter is empty")
	}

	var gotOutbox store.ChannelOutbox
	if err := db.First(&gotOutbox, outbox.ID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if gotOutbox.Status != contracts.ChannelOutboxFailed {
		t.Fatalf("outbox status should be failed, got %s", gotOutbox.Status)
	}
	if !strings.Contains(gotOutbox.LastError, "adapter 为空") {
		t.Fatalf("unexpected outbox last_error: %q", gotOutbox.LastError)
	}

	var gotMsg store.ChannelMessage
	if err := db.First(&gotMsg, outMsg.ID).Error; err != nil {
		t.Fatalf("query outbound message failed: %v", err)
	}
	if gotMsg.Status != contracts.ChannelMessageFailed {
		t.Fatalf("outbound message status should be failed, got %s", gotMsg.Status)
	}
}

func TestProcessInbound_CodexBackend_JSONLAndEvents(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	installFakeCodexBinary(t)
	t.Setenv("DALEK_GATEWAY_AGENT_PROVIDER", "codex")
	t.Setenv("DALEK_GATEWAY_AGENT_MODEL", "gpt-5-codex-test")

	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		DB:         db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeWeb,
		Adapter:            "web.ws",
		PeerConversationID: "conv-codex",
		PeerMessageID:      "msg-codex-1",
		SenderID:           "u1",
		Text:               "hello from codex backend",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if result.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("expected succeeded, got %s err=%s", result.JobStatus, result.JobError)
	}
	if !strings.Contains(result.ReplyText, "codex final reply") {
		t.Fatalf("unexpected reply: %q", result.ReplyText)
	}
	if result.AgentProvider != "codex" {
		t.Fatalf("unexpected provider: %q", result.AgentProvider)
	}
	if result.AgentModel != "gpt-5-codex-test" {
		t.Fatalf("unexpected model: %q", result.AgentModel)
	}
	if len(result.AgentEvents) == 0 {
		t.Fatalf("expected agent events from jsonl output")
	}
}

func TestProcessInbound_ResolveBackendFromProjectConfig(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	installFakeCodexBinary(t)
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{
				Provider: "codex",
				Mode:     "cli",
				Model:    "gpt-5-codex-config",
			},
		},
		DB: db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeWeb,
		Adapter:            "web.ws",
		PeerConversationID: "conv-codex-config",
		PeerMessageID:      "msg-codex-config-1",
		SenderID:           "u1",
		Text:               "hello from config codex backend",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if result.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("expected succeeded, got %s err=%s", result.JobStatus, result.JobError)
	}
	if result.AgentProvider != "codex" {
		t.Fatalf("unexpected provider: %q", result.AgentProvider)
	}
	if result.AgentModel != "gpt-5-codex-config" {
		t.Fatalf("unexpected model: %q", result.AgentModel)
	}
}

func TestProcessInbound_CodexBackend_SDKMode(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dalek.sqlite3")
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("OpenAndMigrate failed: %v", err)
	}
	installFakeCodexBinaryForSDK(t)
	repoRoot := t.TempDir()
	svc := New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{
				Provider: "codex",
				Mode:     "sdk",
				Model:    "gpt-5-codex-sdk",
			},
		},
		DB: db,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := svc.ProcessInbound(ctx, contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeWeb,
		Adapter:            "web.ws",
		PeerConversationID: "conv-codex-sdk",
		PeerMessageID:      "msg-codex-sdk-1",
		SenderID:           "u1",
		Text:               "hello from codex sdk backend",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("ProcessInbound failed: %v", err)
	}
	if result.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("expected succeeded, got %s err=%s", result.JobStatus, result.JobError)
	}
	if !strings.Contains(result.ReplyText, "codex sdk final reply") {
		t.Fatalf("unexpected reply: %q", result.ReplyText)
	}
	if result.AgentProvider != "codex" {
		t.Fatalf("unexpected provider: %q", result.AgentProvider)
	}
	if result.AgentModel != "gpt-5-codex-sdk" {
		t.Fatalf("unexpected model: %q", result.AgentModel)
	}
	if result.AgentOutputMode != "jsonl" {
		t.Fatalf("unexpected output mode: %q", result.AgentOutputMode)
	}
	if len(result.AgentEvents) == 0 {
		t.Fatalf("expected non-empty agent events")
	}

	var conv store.ChannelConversation
	if err := db.First(&conv, result.ConversationID).Error; err != nil {
		t.Fatalf("query conversation failed: %v", err)
	}
	if strings.TrimSpace(conv.AgentSessionID) != "thread-sdk-chan-1" {
		t.Fatalf("unexpected session id: %q", conv.AgentSessionID)
	}
}

func TestBuildPMAgentPrompt_WithSenderName(t *testing.T) {
	got := buildPMAgentPrompt(store.ChannelMessage{
		SenderID:    "ou_test_1",
		SenderName:  "张三",
		ContentText: "请列出我的 ticket",
	})
	want := "[sender: 张三 (ou_test_1)]\n请列出我的 ticket"
	if got != want {
		t.Fatalf("unexpected prompt:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestBuildPMAgentPrompt_WithSenderIDOnly(t *testing.T) {
	got := buildPMAgentPrompt(store.ChannelMessage{
		SenderID:    "ou_test_2",
		ContentText: "status",
	})
	want := "[sender: ou_test_2]\nstatus"
	if got != want {
		t.Fatalf("unexpected prompt:\nwant: %q\ngot:  %q", want, got)
	}
}

func TestBuildPMAgentPrompt_AnonymousNoPrefix(t *testing.T) {
	got := buildPMAgentPrompt(store.ChannelMessage{
		SenderID:    "anonymous",
		ContentText: "hello",
	})
	if got != "hello" {
		t.Fatalf("anonymous sender should not add prefix, got=%q", got)
	}
}

type stubInterruptChatRunnerManager struct {
	interruptOK    bool
	interruptErr   error
	interruptCalls int

	lastConversationID       string
	closeCalls               int
	lastClosedConversationID string
}

func (m *stubInterruptChatRunnerManager) RunTurn(ctx context.Context, req ChatRunRequest, onEvent ChatEventHandler) (ChatRunResult, error) {
	_ = ctx
	_ = req
	_ = onEvent
	return ChatRunResult{}, nil
}

func (m *stubInterruptChatRunnerManager) InterruptConversation(ctx context.Context, conversationID string) (bool, error) {
	_ = ctx
	m.interruptCalls++
	m.lastConversationID = strings.TrimSpace(conversationID)
	if m.interruptErr != nil {
		return false, m.interruptErr
	}
	return m.interruptOK, nil
}

func (m *stubInterruptChatRunnerManager) CloseConversation(conversationID string) {
	m.closeCalls++
	m.lastClosedConversationID = strings.TrimSpace(conversationID)
}

func (m *stubInterruptChatRunnerManager) Close() error { return nil }

func newChannelServiceWithFakeAgent(t *testing.T, db *gorm.DB) *Service {
	t.Helper()
	installFakeClaudeBinary(t, false)
	repoRoot := t.TempDir()
	return New(&core.Project{
		Name:       "demo",
		RepoRoot:   repoRoot,
		ProjectDir: repoRoot,
		Config: repo.Config{
			GatewayAgent: repo.GatewayAgentConfig{
				Mode: "cli",
			},
		},
		DB: db,
	})
}

func installFakeClaudeBinary(t *testing.T, emptyOutput bool) {
	t.Helper()
	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "claude")
	if emptyOutput {
		script := `#!/usr/bin/env bash
set -euo pipefail
exit 0
`
		if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
			t.Fatalf("write fake claude(empty) failed: %v", err)
		}
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		return
	}

	script := `#!/usr/bin/env bash
set -euo pipefail
session_id=""
args=("$@")
for ((i=0;i<${#args[@]};i++)); do
  case "${args[$i]}" in
    --session-id|--resume)
      if (( i + 1 < ${#args[@]} )); then
        session_id="${args[$((i+1))]}"
      fi
      ;;
  esac
done
prompt=""
if (( ${#args[@]} > 0 )); then
  prompt="${args[$((${#args[@]}-1))]}"
fi
python3 - "$prompt" "$session_id" <<'PY'
import json
import re
import sys

text = (sys.argv[1] or "").strip()
session_id = (sys.argv[2] or "").strip()
if not session_id:
    session_id = "sess-test-1"
lower = text.lower()
if ("创建" in text) or ("新建" in text) or ("新增" in text) or ("create" in lower):
    title = text
    m = re.search(r'(?:创建|新建|新增|create)\s*(?:ticket|工单|任务)?\s*[:：]\s*([^"\n]+)', text, flags=re.I)
    if m:
        title = m.group(1).strip()
    if not title:
        title = "未命名 ticket"
    reply = f"正在创建 ticket：{title}。"
elif ("merge" in lower) or ("合并" in text):
    reply = "正在查询 merge 列表。"
else:
    m = re.search(r'(?:ticket|t|工单|任务)\s*#?\s*(\d+)', text, flags=re.I)
    if m:
        tid = int(m.group(1))
        reply = f"正在查询 t{tid} 的详情。"
    elif ("ticket" in lower) or ("工单" in text) or ("任务" in text) or ("列表" in text):
        reply = "正在查询 ticket 列表。"
    else:
        reply = "你好，我是 project manager agent。"
obj = {"session_id": session_id, "message": {"text": reply}}
print(json.dumps(obj, ensure_ascii=False))
PY
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude script failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installFakeClaudeBinaryWithDelay(t *testing.T, delay time.Duration) {
	t.Helper()
	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "claude")
	delaySeconds := int(delay / time.Second)
	if delaySeconds <= 0 {
		delaySeconds = 1
	}
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
sleep %d
echo "late reply"
`, delaySeconds)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude(delay) failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installFakeClaudeBinaryWithMessageText(t *testing.T, messageText string) {
	t.Helper()
	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "claude")
	script := `#!/usr/bin/env bash
set -euo pipefail
msg="${DALEK_TEST_CLAUDE_REPLY:-}"
python3 - "$msg" <<'PY'
import json
import sys

msg = (sys.argv[1] or "").strip()
print(json.dumps({"session_id": "sess-structured", "message": {"text": msg}}, ensure_ascii=False))
PY
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake claude(structured) failed: %v", err)
	}
	t.Setenv("DALEK_TEST_CLAUDE_REPLY", strings.TrimSpace(messageText))
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installFakeCodexBinary(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail
prompt="${@: -1}"
python3 - "$prompt" <<'PY'
import json
import sys

text = (sys.argv[1] or "").strip()
events = [
    {"thread_id": "thread-codex-1", "type": "message.delta", "item": {"type": "message", "text": "codex thinking"}},
    {"type": "message", "item": {"type": "message", "text": f"codex final reply: {text}"}},
]
for ev in events:
    print(json.dumps(ev, ensure_ascii=False))
PY
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex script failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installFakeCodexBinaryForSDK(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	scriptPath := filepath.Join(binDir, "codex")
	script := `#!/usr/bin/env bash
set -euo pipefail
cat >/dev/null
echo '{"type":"thread.started","thread_id":"thread-sdk-chan-1"}'
echo '{"type":"turn.started"}'
echo '{"type":"item.completed","item":{"id":"msg-1","type":"agent_message","text":"codex sdk final reply"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":1,"cached_input_tokens":0,"output_tokens":1}}'
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake codex sdk script failed: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestClassifyJobErrorType(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want string
	}{
		{
			name: "network",
			msg:  "project manager agent 调用失败: stream disconnected before completion",
			want: "network",
		},
		{
			name: "auth",
			msg:  "project manager agent 调用失败: unauthorized (status code: 401)",
			want: "auth",
		},
		{
			name: "config",
			msg:  "project manager agent 调用失败: command not found: codex",
			want: "config",
		},
		{
			name: "timeout",
			msg:  "project manager agent 调用失败: context deadline exceeded",
			want: "timeout",
		},
		{
			name: "agent",
			msg:  "project manager agent 无响应（stdout 为空）",
			want: "agent",
		},
		{
			name: "unknown",
			msg:  "some unrecognized error",
			want: "unknown",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyJobErrorType(tc.msg)
			if got != tc.want {
				t.Fatalf("classifyJobErrorType(%q)=%q, want=%q", tc.msg, got, tc.want)
			}
		})
	}
}
