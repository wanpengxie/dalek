package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/channel/inbounddb"
	"dalek/internal/store"

	"gorm.io/gorm"
)

const turnResultSchemaV2 = "dalek.channel_turn_result.v2"

type EnsureBindingParams struct {
	ProjectName    string
	PeerProjectKey string
	Env            contracts.InboundEnvelope
	AutoUpdate     bool
}

type PersistInboundParams struct {
	Conv    store.ChannelConversation
	Env     contracts.InboundEnvelope
	Project string
}

type PersistTurnResultParams struct {
	Binding            store.ChannelBinding
	Conv               store.ChannelConversation
	Inbound            store.ChannelMessage
	Job                store.ChannelTurnJob
	Adapter            string
	Result             ProcessResult
	RunErr             error
	RunnerID           string
	FinalizeJob        bool
	RequireRunnerMatch bool
}

type TurnResultRecord struct {
	Schema            string              `json:"schema"`
	BindingID         uint                `json:"binding_id,omitempty"`
	ConversationID    uint                `json:"conversation_id"`
	InboundMessageID  uint                `json:"inbound_message_id"`
	OutboundMessageID uint                `json:"outbound_message_id"`
	OutboxID          uint                `json:"outbox_id"`
	RunID             string              `json:"run_id,omitempty"`
	AgentReplyText    string              `json:"agent_reply_text,omitempty"`
	AgentProvider     string              `json:"agent_provider,omitempty"`
	AgentModel        string              `json:"agent_model,omitempty"`
	AgentSessionID    string              `json:"agent_session_id,omitempty"`
	AgentOutputMode   string              `json:"agent_output_mode,omitempty"`
	AgentCommand      string              `json:"agent_command,omitempty"`
	AgentStdout       string              `json:"agent_stdout,omitempty"`
	AgentStderr       string              `json:"agent_stderr,omitempty"`
	AgentEvents       []AgentEvent        `json:"agent_events,omitempty"`
	PendingActions    []PendingActionView `json:"pending_actions,omitempty"`
	JobStatus         string              `json:"job_status,omitempty"`
	JobError          string              `json:"job_error,omitempty"`
	JobErrorType      string              `json:"job_error_type,omitempty"`
}

type TurnResultOutput struct {
	Persisted  ProcessResult
	ResultJSON string
}

func EnsureBindingTx(ctx context.Context, tx *gorm.DB, p EnsureBindingParams) (store.ChannelBinding, error) {
	if tx == nil {
		return store.ChannelBinding{}, fmt.Errorf("tx 不能为空")
	}
	env := p.Env
	env.Normalize()
	if env.BindingID > 0 {
		var binding store.ChannelBinding
		if err := tx.WithContext(ctx).First(&binding, env.BindingID).Error; err != nil {
			return store.ChannelBinding{}, err
		}
		updates := map[string]any{}
		if p.AutoUpdate {
			projectName := p.ProjectName
			if projectName != "" && binding.ProjectName != projectName {
				updates["project_name"] = projectName
			}
			if !binding.Enabled {
				updates["enabled"] = true
			}
		} else if !binding.Enabled {
			return store.ChannelBinding{}, fmt.Errorf("channel binding 已禁用: %d", binding.ID)
		}
		if len(updates) > 0 {
			if err := tx.WithContext(ctx).Model(&store.ChannelBinding{}).
				Where("id = ?", binding.ID).
				Updates(updates).Error; err != nil {
				return store.ChannelBinding{}, err
			}
			if v, ok := updates["project_name"]; ok {
				binding.ProjectName = fmt.Sprint(v)
			}
			if _, ok := updates["enabled"]; ok {
				binding.Enabled = true
			}
		}
		return binding, nil
	}

	channelType := toStoreChannelType(env.ChannelType)
	adapter := env.Adapter
	peerProjectKey := p.PeerProjectKey

	var binding store.ChannelBinding
	err := tx.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ?", channelType, adapter, peerProjectKey).
		First(&binding).Error
	if err == nil {
		updates := map[string]any{}
		if p.AutoUpdate {
			projectName := p.ProjectName
			if projectName != "" && binding.ProjectName != projectName {
				updates["project_name"] = projectName
			}
			if !binding.Enabled {
				updates["enabled"] = true
			}
		} else if !binding.Enabled {
			return store.ChannelBinding{}, fmt.Errorf("channel binding 已禁用: %d", binding.ID)
		}
		if len(updates) > 0 {
			if err := tx.WithContext(ctx).Model(&store.ChannelBinding{}).
				Where("id = ?", binding.ID).
				Updates(updates).Error; err != nil {
				return store.ChannelBinding{}, err
			}
			if v, ok := updates["project_name"]; ok {
				binding.ProjectName = fmt.Sprint(v)
			}
			if _, ok := updates["enabled"]; ok {
				binding.Enabled = true
			}
		}
		return binding, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return store.ChannelBinding{}, err
	}

	binding = store.ChannelBinding{
		ProjectName:    p.ProjectName,
		ChannelType:    channelType,
		Adapter:        adapter,
		PeerProjectKey: peerProjectKey,
		RolePolicyJSON: "{}",
		Enabled:        true,
	}
	if err := tx.WithContext(ctx).Create(&binding).Error; err != nil {
		return store.ChannelBinding{}, err
	}
	return binding, nil
}

func EnsureConversationTx(ctx context.Context, tx *gorm.DB, bindingID uint, peerConversationID string) (store.ChannelConversation, error) {
	if tx == nil {
		return store.ChannelConversation{}, fmt.Errorf("tx 不能为空")
	}
	return inbounddb.EnsureConversationTx(ctx, tx, bindingID, peerConversationID)
}

func PersistInboundMessageTx(ctx context.Context, tx *gorm.DB, p PersistInboundParams) (store.ChannelMessage, store.ChannelTurnJob, bool, error) {
	if tx == nil {
		return store.ChannelMessage{}, store.ChannelTurnJob{}, false, fmt.Errorf("tx 不能为空")
	}
	env := p.Env
	env.Normalize()
	adapter := env.Adapter
	peerMessageID := env.PeerMessageID
	senderID := env.SenderID
	senderName := env.SenderName

	var inbound store.ChannelMessage
	err := tx.WithContext(ctx).
		Where("direction = ? AND conversation_id = ? AND adapter = ? AND peer_message_id = ?",
			contracts.ChannelMessageIn, p.Conv.ID, adapter, peerMessageID).
		First(&inbound).Error
	if err == nil {
		var job store.ChannelTurnJob
		jerr := tx.WithContext(ctx).Where("inbound_message_id = ?", inbound.ID).First(&job).Error
		if jerr == nil {
			return inbound, job, true, nil
		}
		if !errors.Is(jerr, gorm.ErrRecordNotFound) {
			return store.ChannelMessage{}, store.ChannelTurnJob{}, false, jerr
		}
		job = store.ChannelTurnJob{
			ConversationID:   inbound.ConversationID,
			InboundMessageID: inbound.ID,
			Status:           contracts.ChannelTurnPending,
		}
		if err := tx.WithContext(ctx).Create(&job).Error; err != nil {
			return store.ChannelMessage{}, store.ChannelTurnJob{}, false, err
		}
		return inbound, job, true, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return store.ChannelMessage{}, store.ChannelTurnJob{}, false, err
	}

	payload := map[string]any{
		"schema":      env.Schema,
		"attachments": env.Attachments,
		"received_at": env.ReceivedAt,
		"project":     p.Project,
	}
	inbound = store.ChannelMessage{
		ConversationID: p.Conv.ID,
		Direction:      contracts.ChannelMessageIn,
		Adapter:        adapter,
		PeerMessageID:  &peerMessageID,
		SenderID:       senderID,
		SenderName:     senderName,
		ContentText:    env.Text,
		PayloadJSON:    mustJSON(payload),
		Status:         contracts.ChannelMessageAccepted,
	}
	if err := tx.WithContext(ctx).Create(&inbound).Error; err != nil {
		return store.ChannelMessage{}, store.ChannelTurnJob{}, false, err
	}

	now := time.Now()
	if err := tx.WithContext(ctx).Model(&store.ChannelConversation{}).
		Where("id = ?", p.Conv.ID).
		Updates(map[string]any{
			"last_message_at": &now,
			"updated_at":      now,
		}).Error; err != nil {
		return store.ChannelMessage{}, store.ChannelTurnJob{}, false, err
	}

	job := store.ChannelTurnJob{
		ConversationID:   p.Conv.ID,
		InboundMessageID: inbound.ID,
		Status:           contracts.ChannelTurnPending,
	}
	if err := tx.WithContext(ctx).Create(&job).Error; err != nil {
		return store.ChannelMessage{}, store.ChannelTurnJob{}, false, err
	}
	return inbound, job, false, nil
}

func PersistTurnResultTx(ctx context.Context, tx *gorm.DB, p PersistTurnResultParams) (TurnResultOutput, error) {
	if tx == nil {
		return TurnResultOutput{}, fmt.Errorf("tx 不能为空")
	}
	if p.Job.ID == 0 || p.Inbound.ID == 0 || p.Conv.ID == 0 {
		return TurnResultOutput{}, fmt.Errorf("turn 持久化入参不完整")
	}

	status := p.Result.JobStatus
	if status == "" {
		if p.RunErr != nil {
			status = contracts.ChannelTurnFailed
		} else {
			status = contracts.ChannelTurnSucceeded
		}
	}
	if p.RunErr != nil {
		status = contracts.ChannelTurnFailed
	}

	jobErr := p.Result.JobError
	if p.RunErr != nil {
		jobErr = p.RunErr.Error()
	}
	jobErrType := p.Result.JobErrorType
	if jobErrType == "" {
		jobErrType = classifyJobErrorType(jobErr)
	}
	if status == contracts.ChannelTurnSucceeded {
		jobErr = ""
		jobErrType = ""
	}

	adapter := p.Adapter
	if adapter == "" {
		adapter = p.Binding.Adapter
	}

	payload := TurnResultRecord{
		Schema:           turnResultSchemaV2,
		BindingID:        p.Binding.ID,
		ConversationID:   p.Conv.ID,
		InboundMessageID: p.Inbound.ID,
		RunID:            p.Result.RunID,
		AgentReplyText:   p.Result.ReplyText,
		AgentProvider:    p.Result.AgentProvider,
		AgentModel:       p.Result.AgentModel,
		AgentSessionID:   p.Result.AgentSessionID,
		AgentOutputMode:  p.Result.AgentOutputMode,
		AgentCommand:     p.Result.AgentCommand,
		AgentStdout:      p.Result.AgentStdout,
		AgentStderr:      p.Result.AgentStderr,
		AgentEvents:      copyAgentEvents(p.Result.AgentEvents),
		PendingActions:   copyPendingActionViews(p.Result.PendingActions),
		JobStatus:        string(status),
		JobError:         jobErr,
		JobErrorType:     jobErrType,
	}

	persisted := ProcessResult{
		BindingID:        p.Binding.ID,
		ConversationID:   p.Conv.ID,
		InboundMessageID: p.Inbound.ID,
		JobID:            p.Job.ID,
		RunID:            payload.RunID,
		JobStatus:        status,
		JobError:         jobErr,
		JobErrorType:     jobErrType,
		ReplyText:        payload.AgentReplyText,
		AgentProvider:    payload.AgentProvider,
		AgentModel:       payload.AgentModel,
		AgentSessionID:   payload.AgentSessionID,
		AgentOutputMode:  payload.AgentOutputMode,
		AgentCommand:     payload.AgentCommand,
		AgentStdout:      payload.AgentStdout,
		AgentStderr:      payload.AgentStderr,
		AgentEvents:      copyAgentEvents(payload.AgentEvents),
		PendingActions:   copyPendingActionViews(payload.PendingActions),
	}

	payloadJSON := mustJSON(payload)

	if payload.AgentReplyText != "" {
		outPeerID := fmt.Sprintf("out_job_%d", p.Job.ID)
		outboundStatus := contracts.ChannelMessageProcessed
		if status != contracts.ChannelTurnSucceeded {
			outboundStatus = contracts.ChannelMessageFailed
		}
		outbound := store.ChannelMessage{
			ConversationID: p.Conv.ID,
			Direction:      contracts.ChannelMessageOut,
			Adapter:        adapter,
			PeerMessageID:  &outPeerID,
			SenderID:       "pm",
			ContentText:    payload.AgentReplyText,
			PayloadJSON:    payloadJSON,
			Status:         outboundStatus,
		}
		if err := tx.WithContext(ctx).Create(&outbound).Error; err != nil {
			return TurnResultOutput{}, err
		}

		outboxStatus := contracts.ChannelOutboxPending
		lastError := ""
		if status != contracts.ChannelTurnSucceeded {
			outboxStatus = contracts.ChannelOutboxFailed
			lastError = jobErr
		}
		outbox := store.ChannelOutbox{
			MessageID:   outbound.ID,
			Adapter:     adapter,
			PayloadJSON: payloadJSON,
			Status:      outboxStatus,
			RetryCount:  0,
			LastError:   lastError,
		}
		if err := tx.WithContext(ctx).Create(&outbox).Error; err != nil {
			return TurnResultOutput{}, err
		}

		payload.OutboundMessageID = outbound.ID
		payload.OutboxID = outbox.ID
		payloadJSON = mustJSON(payload)

		if err := tx.WithContext(ctx).Model(&store.ChannelMessage{}).
			Where("id = ?", outbound.ID).
			Update("payload_json", payloadJSON).Error; err != nil {
			return TurnResultOutput{}, err
		}
		if err := tx.WithContext(ctx).Model(&store.ChannelOutbox{}).
			Where("id = ?", outbox.ID).
			Update("payload_json", payloadJSON).Error; err != nil {
			return TurnResultOutput{}, err
		}

		persisted.OutboundMessageID = outbound.ID
		persisted.OutboxID = outbox.ID
	}

	if err := tx.WithContext(ctx).Model(&store.ChannelMessage{}).
		Where("id = ?", p.Inbound.ID).
		Updates(map[string]any{
			"status":       inboundMessageStatusFromTurn(status),
			"payload_json": payloadJSON,
		}).Error; err != nil {
		return TurnResultOutput{}, err
	}

	now := time.Now()
	convUpdates := map[string]any{
		"updated_at": now,
	}
	if payload.AgentSessionID != "" {
		convUpdates["agent_session_id"] = payload.AgentSessionID
	}
	if payload.AgentReplyText != "" {
		convUpdates["last_message_at"] = &now
	}
	if err := tx.WithContext(ctx).Model(&store.ChannelConversation{}).
		Where("id = ?", p.Conv.ID).
		Updates(convUpdates).Error; err != nil {
		return TurnResultOutput{}, err
	}

	if p.FinalizeJob {
		jobUpdates := map[string]any{
			"status":           status,
			"result_json":      payloadJSON,
			"error":            jobErr,
			"runner_id":        "",
			"lease_expires_at": nil,
			"finished_at":      &now,
			"updated_at":       now,
		}
		if status == contracts.ChannelTurnSucceeded {
			jobUpdates["error"] = ""
		}
		query := tx.WithContext(ctx).Model(&store.ChannelTurnJob{}).Where("id = ?", p.Job.ID)
		if p.RequireRunnerMatch {
			query = query.Where("status = ? AND runner_id = ?", contracts.ChannelTurnRunning, p.RunnerID)
		}
		res := query.Updates(jobUpdates)
		if res.Error != nil {
			return TurnResultOutput{}, res.Error
		}
		if res.RowsAffected == 0 {
			return TurnResultOutput{}, fmt.Errorf("turn job 更新失败: id=%d", p.Job.ID)
		}
	}

	return TurnResultOutput{
		Persisted:  persisted,
		ResultJSON: payloadJSON,
	}, nil
}

func decodeTurnResult(job store.ChannelTurnJob) ProcessResult {
	res := ProcessResult{
		JobID:        job.ID,
		JobStatus:    job.Status,
		JobError:     job.Error,
		JobErrorType: classifyJobErrorType(job.Error),
	}
	raw := job.ResultJSON
	if raw == "" {
		return res
	}

	var payload TurnResultRecord
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return res
	}

	res.BindingID = payload.BindingID
	res.ConversationID = payload.ConversationID
	res.InboundMessageID = payload.InboundMessageID
	res.OutboundMessageID = payload.OutboundMessageID
	res.OutboxID = payload.OutboxID
	res.RunID = payload.RunID
	res.ReplyText = payload.AgentReplyText
	res.AgentProvider = payload.AgentProvider
	res.AgentModel = payload.AgentModel
	res.AgentSessionID = payload.AgentSessionID
	res.AgentOutputMode = payload.AgentOutputMode
	res.AgentCommand = payload.AgentCommand
	res.AgentStdout = payload.AgentStdout
	res.AgentStderr = payload.AgentStderr
	res.AgentEvents = copyAgentEvents(payload.AgentEvents)
	res.PendingActions = copyPendingActionViews(payload.PendingActions)
	if st := payload.JobStatus; st != "" {
		res.JobStatus = contracts.ChannelTurnJobStatus(st)
	}
	if msg := payload.JobError; msg != "" {
		res.JobError = msg
	}
	if jt := payload.JobErrorType; jt != "" {
		res.JobErrorType = jt
	}
	if res.JobErrorType == "" {
		res.JobErrorType = classifyJobErrorType(res.JobError)
	}
	return res
}
