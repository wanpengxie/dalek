package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/store"

	"gorm.io/gorm"
)

func TestEnsureBindingTx_SupportsAutoUpdateAndBindingID(t *testing.T) {
	db := openChannelTestDB(t, "inbound-persistence-binding.db")
	ctx := context.Background()

	env := contracts.InboundEnvelope{
		ChannelType: contracts.ChannelTypeIM,
		Adapter:     "im.feishu",
	}
	var created store.ChannelBinding
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		created, err = EnsureBindingTx(ctx, tx, EnsureBindingParams{
			ProjectName:    "alpha",
			PeerProjectKey: "chat-a",
			Env:            env,
			AutoUpdate:     true,
		})
		return err
	}); err != nil {
		t.Fatalf("create binding failed: %v", err)
	}
	if created.ID == 0 {
		t.Fatalf("binding id should be created")
	}
	if strings.TrimSpace(created.ProjectName) != "alpha" {
		t.Fatalf("project_name mismatch: %q", created.ProjectName)
	}

	var updated store.ChannelBinding
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		updated, err = EnsureBindingTx(ctx, tx, EnsureBindingParams{
			ProjectName:    "beta",
			PeerProjectKey: "chat-a",
			Env:            env,
			AutoUpdate:     true,
		})
		return err
	}); err != nil {
		t.Fatalf("auto update binding failed: %v", err)
	}
	if updated.ID != created.ID {
		t.Fatalf("binding should be reused, got=%d want=%d", updated.ID, created.ID)
	}
	if strings.TrimSpace(updated.ProjectName) != "beta" {
		t.Fatalf("project_name should be updated to beta, got=%q", updated.ProjectName)
	}

	if err := db.Model(&store.ChannelBinding{}).Where("id = ?", created.ID).Update("enabled", false).Error; err != nil {
		t.Fatalf("disable binding failed: %v", err)
	}
	_, err := runEnsureBindingTx(ctx, db, EnsureBindingParams{
		ProjectName: "beta",
		Env: contracts.InboundEnvelope{
			BindingID: created.ID,
		},
		AutoUpdate: false,
	})
	if err == nil || !strings.Contains(err.Error(), "已禁用") {
		t.Fatalf("expect disabled error, got=%v", err)
	}

	got, err := runEnsureBindingTx(ctx, db, EnsureBindingParams{
		ProjectName: "gamma",
		Env: contracts.InboundEnvelope{
			BindingID: created.ID,
		},
		AutoUpdate: true,
	})
	if err != nil {
		t.Fatalf("enable disabled binding failed: %v", err)
	}
	if !got.Enabled {
		t.Fatalf("binding should be enabled")
	}
	if strings.TrimSpace(got.ProjectName) != "gamma" {
		t.Fatalf("project_name should update to gamma, got=%q", got.ProjectName)
	}
}

func TestPersistInboundMessageTx_UnifiesFieldsAndDedup(t *testing.T) {
	db := openChannelTestDB(t, "inbound-persistence-inbound.db")
	ctx := context.Background()

	binding, conv := prepareBindingConversation(t, db, "alpha", "chat-a", "chat-a")
	env := contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeIM,
		Adapter:            "im.feishu",
		PeerConversationID: "chat-a",
		PeerMessageID:      "msg-1",
		SenderID:           "u1",
		SenderName:         "Alice",
		Text:               "hello",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	}

	var inbound1 store.ChannelMessage
	var job1 store.ChannelTurnJob
	var duplicate bool
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		inbound1, job1, duplicate, err = PersistInboundMessageTx(ctx, tx, PersistInboundParams{
			Conv:    conv,
			Env:     env,
			Project: "alpha",
		})
		return err
	}); err != nil {
		t.Fatalf("first persist inbound failed: %v", err)
	}
	if duplicate {
		t.Fatalf("first persist should not be duplicate")
	}
	if inbound1.ID == 0 || job1.ID == 0 {
		t.Fatalf("inbound/job should be created, inbound=%d job=%d", inbound1.ID, job1.ID)
	}
	if strings.TrimSpace(inbound1.SenderName) != "Alice" {
		t.Fatalf("sender_name mismatch: %q", inbound1.SenderName)
	}

	payload := map[string]any(inbound1.PayloadJSON)
	if got := strings.TrimSpace(toString(payload["project"])); got != "alpha" {
		t.Fatalf("payload.project mismatch, got=%q", got)
	}

	var inbound2 store.ChannelMessage
	var job2 store.ChannelTurnJob
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		inbound2, job2, duplicate, err = PersistInboundMessageTx(ctx, tx, PersistInboundParams{
			Conv:    conv,
			Env:     env,
			Project: "alpha",
		})
		return err
	}); err != nil {
		t.Fatalf("second persist inbound failed: %v", err)
	}
	if !duplicate {
		t.Fatalf("second persist should be duplicate")
	}
	if inbound2.ID != inbound1.ID {
		t.Fatalf("duplicate inbound id mismatch: got=%d want=%d", inbound2.ID, inbound1.ID)
	}
	if job2.ID != job1.ID {
		t.Fatalf("duplicate job id mismatch: got=%d want=%d", job2.ID, job1.ID)
	}

	var inboundCount int64
	if err := db.Model(&store.ChannelMessage{}).
		Where("conversation_id = ? AND direction = ?", conv.ID, contracts.ChannelMessageIn).
		Count(&inboundCount).Error; err != nil {
		t.Fatalf("count inbound failed: %v", err)
	}
	if inboundCount != 1 {
		t.Fatalf("inbound should dedup to 1, got=%d", inboundCount)
	}

	var jobCount int64
	if err := db.Model(&store.ChannelTurnJob{}).Where("conversation_id = ?", conv.ID).Count(&jobCount).Error; err != nil {
		t.Fatalf("count job failed: %v", err)
	}
	if jobCount != 1 {
		t.Fatalf("job should dedup to 1, got=%d", jobCount)
	}

	_ = binding
}

func TestPersistTurnResultTx_FinalizeSucceededAndFailure(t *testing.T) {
	db := openChannelTestDB(t, "inbound-persistence-turn.db")
	ctx := context.Background()

	binding, conv, inbound, job := prepareInboundState(t, db, "alpha", "chat-a", "chat-a", "msg-success")
	if err := db.Model(&store.ChannelTurnJob{}).
		Where("id = ?", job.ID).
		Updates(map[string]any{
			"status":    contracts.ChannelTurnRunning,
			"runner_id": "runner-1",
		}).Error; err != nil {
		t.Fatalf("set running job failed: %v", err)
	}

	var successOut TurnResultOutput
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		successOut, err = PersistTurnResultTx(ctx, tx, PersistTurnResultParams{
			Binding:            binding,
			Conv:               conv,
			Inbound:            inbound,
			Job:                job,
			Adapter:            "im.feishu",
			RunnerID:           "runner-1",
			FinalizeJob:        true,
			RequireRunnerMatch: true,
			Result: ProcessResult{
				RunID:           "run-1",
				JobStatus:       contracts.ChannelTurnSucceeded,
				ReplyText:       "done",
				AgentProvider:   "fake",
				AgentModel:      "test",
				AgentSessionID:  "sess-1",
				AgentOutputMode: "text",
			},
		})
		return err
	}); err != nil {
		t.Fatalf("persist success result failed: %v", err)
	}
	if successOut.Persisted.OutboundMessageID == 0 || successOut.Persisted.OutboxID == 0 {
		t.Fatalf("success should create outbound/outbox, outbound=%d outbox=%d", successOut.Persisted.OutboundMessageID, successOut.Persisted.OutboxID)
	}
	if successOut.Persisted.JobStatus != contracts.ChannelTurnSucceeded {
		t.Fatalf("success status mismatch: %s", successOut.Persisted.JobStatus)
	}

	var gotJob store.ChannelTurnJob
	if err := db.First(&gotJob, job.ID).Error; err != nil {
		t.Fatalf("query success job failed: %v", err)
	}
	if gotJob.Status != contracts.ChannelTurnSucceeded {
		t.Fatalf("job status should be succeeded, got=%s", gotJob.Status)
	}
	if strings.TrimSpace(gotJob.RunnerID) != "" {
		t.Fatalf("runner_id should be cleared, got=%q", gotJob.RunnerID)
	}
	var record TurnResultRecord
	{
		resultBytes, _ := json.Marshal(gotJob.ResultJSON)
		if err := json.Unmarshal(resultBytes, &record); err != nil {
			t.Fatalf("decode success result json failed: %v", err)
		}
	}
	if strings.TrimSpace(record.Schema) != turnResultSchemaV2 {
		t.Fatalf("schema mismatch, got=%q", record.Schema)
	}

	var gotConv store.ChannelConversation
	if err := db.First(&gotConv, conv.ID).Error; err != nil {
		t.Fatalf("query conversation failed: %v", err)
	}
	if strings.TrimSpace(gotConv.AgentSessionID) != "sess-1" {
		t.Fatalf("conversation agent_session_id mismatch: %q", gotConv.AgentSessionID)
	}

	var gotOutbox store.ChannelOutbox
	if err := db.First(&gotOutbox, successOut.Persisted.OutboxID).Error; err != nil {
		t.Fatalf("query outbox failed: %v", err)
	}
	if gotOutbox.Status != contracts.ChannelOutboxPending {
		t.Fatalf("outbox status should be pending, got=%s", gotOutbox.Status)
	}
	if gotOutbox.RetryCount != 0 {
		t.Fatalf("outbox retry_count should be 0, got=%d", gotOutbox.RetryCount)
	}

	var gotInbound store.ChannelMessage
	if err := db.First(&gotInbound, inbound.ID).Error; err != nil {
		t.Fatalf("query inbound failed: %v", err)
	}
	if gotInbound.Status != contracts.ChannelMessageProcessed {
		t.Fatalf("inbound status should be processed, got=%s", gotInbound.Status)
	}

	binding2, conv2, inbound2, job2 := prepareInboundState(t, db, "alpha", "chat-b", "chat-b", "msg-failed")
	var failedOut TurnResultOutput
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		failedOut, err = PersistTurnResultTx(ctx, tx, PersistTurnResultParams{
			Binding:     binding2,
			Conv:        conv2,
			Inbound:     inbound2,
			Job:         job2,
			Adapter:     "im.feishu",
			FinalizeJob: true,
			RunErr:      errors.New("context deadline exceeded"),
			Result: ProcessResult{
				RunID: "run-2",
			},
		})
		return err
	}); err != nil {
		t.Fatalf("persist failed result failed: %v", err)
	}
	if failedOut.Persisted.JobStatus != contracts.ChannelTurnFailed {
		t.Fatalf("failed status mismatch: %s", failedOut.Persisted.JobStatus)
	}
	if strings.TrimSpace(failedOut.Persisted.JobErrorType) != "timeout" {
		t.Fatalf("failed job_error_type should be timeout, got=%q", failedOut.Persisted.JobErrorType)
	}
	if failedOut.Persisted.OutboundMessageID != 0 || failedOut.Persisted.OutboxID != 0 {
		t.Fatalf("failed/no-reply should not create outbound/outbox, outbound=%d outbox=%d", failedOut.Persisted.OutboundMessageID, failedOut.Persisted.OutboxID)
	}

	var gotFailedJob store.ChannelTurnJob
	if err := db.First(&gotFailedJob, job2.ID).Error; err != nil {
		t.Fatalf("query failed job failed: %v", err)
	}
	if gotFailedJob.Status != contracts.ChannelTurnFailed {
		t.Fatalf("failed job status mismatch: %s", gotFailedJob.Status)
	}
	if !strings.Contains(gotFailedJob.Error, "deadline exceeded") {
		t.Fatalf("failed job error mismatch: %q", gotFailedJob.Error)
	}

	var failedInbound store.ChannelMessage
	if err := db.First(&failedInbound, inbound2.ID).Error; err != nil {
		t.Fatalf("query failed inbound failed: %v", err)
	}
	if failedInbound.Status != contracts.ChannelMessageFailed {
		t.Fatalf("failed inbound status mismatch: %s", failedInbound.Status)
	}
}

func openChannelTestDB(t *testing.T, name string) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), name)
	db, err := store.OpenAndMigrate(dbPath)
	if err != nil {
		t.Fatalf("open test db failed: %v", err)
	}
	return db
}

func runEnsureBindingTx(ctx context.Context, db *gorm.DB, p EnsureBindingParams) (store.ChannelBinding, error) {
	var out store.ChannelBinding
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		out, err = EnsureBindingTx(ctx, tx, p)
		return err
	})
	if err != nil {
		return store.ChannelBinding{}, err
	}
	return out, nil
}

func prepareBindingConversation(t *testing.T, db *gorm.DB, project, peerProjectKey, peerConversationID string) (store.ChannelBinding, store.ChannelConversation) {
	t.Helper()
	ctx := context.Background()
	var binding store.ChannelBinding
	var conv store.ChannelConversation
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		binding, err = EnsureBindingTx(ctx, tx, EnsureBindingParams{
			ProjectName:    strings.TrimSpace(project),
			PeerProjectKey: strings.TrimSpace(peerProjectKey),
			Env: contracts.InboundEnvelope{
				ChannelType: contracts.ChannelTypeIM,
				Adapter:     "im.feishu",
			},
			AutoUpdate: true,
		})
		if err != nil {
			return err
		}
		conv, err = EnsureConversationTx(ctx, tx, binding.ID, peerConversationID)
		return err
	})
	if err != nil {
		t.Fatalf("prepare binding+conversation failed: %v", err)
	}
	return binding, conv
}

func prepareInboundState(t *testing.T, db *gorm.DB, project, peerProjectKey, peerConversationID, peerMessageID string) (store.ChannelBinding, store.ChannelConversation, store.ChannelMessage, store.ChannelTurnJob) {
	t.Helper()
	ctx := context.Background()
	binding, conv := prepareBindingConversation(t, db, project, peerProjectKey, peerConversationID)
	env := contracts.InboundEnvelope{
		Schema:             contracts.ChannelInboundSchemaV1,
		ChannelType:        contracts.ChannelTypeIM,
		Adapter:            "im.feishu",
		PeerConversationID: peerConversationID,
		PeerMessageID:      peerMessageID,
		SenderID:           "u1",
		SenderName:         "Alice",
		Text:               "hello",
		ReceivedAt:         time.Now().UTC().Format(time.RFC3339),
	}
	var inbound store.ChannelMessage
	var job store.ChannelTurnJob
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		inbound, job, _, err = PersistInboundMessageTx(ctx, tx, PersistInboundParams{
			Conv:    conv,
			Env:     env,
			Project: project,
		})
		return err
	})
	if err != nil {
		t.Fatalf("prepare inbound state failed: %v", err)
	}
	return binding, conv, inbound, job
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(x)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}
