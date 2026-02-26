package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"dalek/internal/agent/eventlog"
	"dalek/internal/contracts"
	"dalek/internal/services/channel/agentcli"
	"dalek/internal/services/core"
	"dalek/internal/store"

	"gorm.io/gorm"
)

const turnJobResultSchemaV1 = "dalek.channel_turn_result.v1"

type Service struct {
	p       *core.Project
	logger  *slog.Logger
	turnSem chan struct{}

	runningMu sync.Mutex
	running   *runningTurn

	chatRunners        ChatRunnerManager
	toolApprovalBridge *ToolApprovalBridge
}

func New(p *core.Project) *Service {
	logger := core.DiscardLogger()
	if p != nil {
		logger = core.EnsureLogger(p.Logger).With("service", "channel")
	}
	return &Service{
		p:                  p,
		logger:             logger,
		turnSem:            make(chan struct{}, 1),
		chatRunners:        newDefaultChatRunnerManager(nil),
		toolApprovalBridge: NewToolApprovalBridge(logger.With("component", "tool_approval")),
	}
}

type runningTurn struct {
	jobID          uint
	conversationID uint
	sessionID      string
	cancel         context.CancelFunc
}

type ProcessResult struct {
	BindingID        uint
	ConversationID   uint
	InboundMessageID uint
	JobID            uint
	RunID            string
	JobStatus        contracts.ChannelTurnJobStatus
	JobError         string
	JobErrorType     string

	OutboundMessageID uint
	OutboxID          uint
	ReplyText         string
	AgentProvider     string
	AgentModel        string
	AgentOutputMode   string
	AgentCommand      string
	AgentStdout       string
	AgentStderr       string
	AgentEvents       []AgentEvent
	PendingActions    []PendingActionView
}

type turnJobResult struct {
	Schema            string              `json:"schema"`
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
}

type pmAgentTurnResponse struct {
	Provider   string
	Model      string
	Text       string
	SessionID  string
	OutputMode agentcli.OutputMode
	Command    string
	Stdout     string
	Stderr     string
	Events     []agentcli.Event
}

func (s *Service) require() (*core.Project, *gorm.DB, error) {
	if s == nil || s.p == nil {
		return nil, nil, fmt.Errorf("channel service 缺少 project 上下文")
	}
	if s.p.DB == nil {
		return nil, nil, fmt.Errorf("channel service 缺少 DB")
	}
	return s.p, s.p.DB, nil
}

func (s *Service) slog() *slog.Logger {
	if s == nil || s.logger == nil {
		return core.DiscardLogger()
	}
	return s.logger
}

func (s *Service) logInterrupt(phase string, attrs ...any) {
	all := []any{
		"cmd", "stop",
		"phase", strings.TrimSpace(phase),
	}
	all = append(all, attrs...)
	s.slog().Info("channel interrupt", all...)
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	var errs []error
	if s.chatRunners != nil {
		if err := s.chatRunners.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if s.toolApprovalBridge != nil {
		s.toolApprovalBridge.Close()
		s.toolApprovalBridge = nil
	}
	return errors.Join(errs...)
}

func (s *Service) InterruptPeerConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (InterruptResult, error) {
	channelType = toStoreChannelType(channelType)
	if channelType == "" {
		channelType = contracts.ChannelTypeCLI
	}
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = defaultAdapter(string(channelType))
	}
	peerConversationID = strings.TrimSpace(peerConversationID)
	s.logInterrupt("locator_start",
		"channel_type", channelType,
		"adapter", adapter,
		"peer_conversation_id", peerConversationID,
	)

	conv, found, err := s.resolvePeerConversation(ctx, channelType, adapter, peerConversationID)
	if err != nil {
		s.logInterrupt("locator_error",
			"channel_type", channelType,
			"adapter", adapter,
			"peer_conversation_id", peerConversationID,
			"error", err,
		)
		return InterruptResult{}, err
	}
	if !found {
		result := InterruptResult{Status: InterruptStatusMiss}
		s.logInterrupt("locator_result",
			"channel_type", channelType,
			"adapter", adapter,
			"peer_conversation_id", peerConversationID,
			"locator", "miss",
			"status", result.Status,
		)
		return result, nil
	}
	s.logInterrupt("locator_result",
		"channel_type", channelType,
		"adapter", adapter,
		"peer_conversation_id", peerConversationID,
		"locator", "hit",
		"conversation_id", conv.ID,
	)

	runnerInterrupted := false
	var runnerErr error
	if s.chatRunners != nil {
		runnerInterrupted, runnerErr = s.chatRunners.InterruptConversation(ctx, fmt.Sprintf("%d", conv.ID))
	}
	ctxCanceled := false
	if !runnerInterrupted {
		ctxCanceled = s.cancelRunningTurnByConversation(conv.ID)
	}
	result := InterruptResult{
		ConversationID:    conv.ID,
		RunnerInterrupted: runnerInterrupted,
		ContextCanceled:   ctxCanceled,
	}
	if runnerErr != nil {
		result.RunnerError = strings.TrimSpace(runnerErr.Error())
	}
	switch {
	case runnerErr != nil && !ctxCanceled:
		result.Status = InterruptStatusExecutionFailure
	case runnerInterrupted || ctxCanceled:
		result.Status = InterruptStatusHit
	default:
		result.Status = InterruptStatusMiss
	}
	s.logInterrupt("runner_result",
		"channel_type", channelType,
		"adapter", adapter,
		"peer_conversation_id", peerConversationID,
		"conversation_id", conv.ID,
		"status", result.Status,
		"runner_hit", result.RunnerInterrupted,
		"context_canceled", result.ContextCanceled,
		"runner_error", result.RunnerErrorMessage(),
	)
	return result, nil
}

func (s *Service) ResetPeerConversationSession(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (bool, error) {
	_, db, err := s.require()
	if err != nil {
		return false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	conv, found, err := s.resolvePeerConversation(ctx, channelType, adapter, peerConversationID)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}

	now := time.Now()
	if err := db.WithContext(ctx).Model(&store.ChannelConversation{}).
		Where("id = ?", conv.ID).
		Updates(map[string]any{
			"agent_session_id": "",
			"updated_at":       now,
		}).Error; err != nil {
		return false, err
	}
	s.cancelRunningTurnByConversation(conv.ID)
	if s.chatRunners != nil {
		s.chatRunners.CloseConversation(fmt.Sprintf("%d", conv.ID))
	}
	return true, nil
}

func (s *Service) resolvePeerConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (store.ChannelConversation, bool, error) {
	_, db, err := s.require()
	if err != nil {
		return store.ChannelConversation{}, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	channelType = toStoreChannelType(channelType)
	if channelType == "" {
		channelType = contracts.ChannelTypeCLI
	}
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = defaultAdapter(string(channelType))
	}
	peerConversationID = strings.TrimSpace(peerConversationID)
	if peerConversationID == "" {
		return store.ChannelConversation{}, false, fmt.Errorf("peer_conversation_id 不能为空")
	}
	var binding store.ChannelBinding
	if err := db.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ?",
			channelType,
			adapter,
			"").
		First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return store.ChannelConversation{}, false, nil
		}
		return store.ChannelConversation{}, false, err
	}

	var conv store.ChannelConversation
	if err := db.WithContext(ctx).
		Where("binding_id = ? AND peer_conversation_id = ?", binding.ID, peerConversationID).
		First(&conv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return store.ChannelConversation{}, false, nil
		}
		return store.ChannelConversation{}, false, err
	}
	return conv, true, nil
}

func (s *Service) gatewayTurnTimeout() time.Duration {
	if s == nil || s.p == nil {
		return 0
	}
	ms := s.p.Config.WithDefaults().GatewayAgent.TurnTimeoutMS
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func (s *Service) acquireTurnSlot(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case s.turnSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Service) releaseTurnSlot() {
	select {
	case <-s.turnSem:
	default:
	}
}

func (s *Service) setRunningTurn(jobID uint, conversationID uint, sessionID string, cancel context.CancelFunc) {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	s.running = &runningTurn{
		jobID:          jobID,
		conversationID: conversationID,
		sessionID:      strings.TrimSpace(sessionID),
		cancel:         cancel,
	}
}

func (s *Service) clearRunningTurn(jobID uint) {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()
	if s.running != nil && s.running.jobID == jobID {
		s.running = nil
	}
}

func (s *Service) cancelConflictingTurn(sessionID string, currentJobID uint) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	var cancel context.CancelFunc
	s.runningMu.Lock()
	if s.running != nil && s.running.jobID != currentJobID && strings.TrimSpace(s.running.sessionID) == sessionID {
		cancel = s.running.cancel
	}
	s.runningMu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *Service) cancelRunningTurnByConversation(conversationID uint) bool {
	if conversationID == 0 {
		return false
	}
	var cancel context.CancelFunc
	s.runningMu.Lock()
	if s.running != nil && s.running.conversationID == conversationID {
		cancel = s.running.cancel
	}
	s.runningMu.Unlock()
	if cancel != nil {
		cancel()
		return true
	}
	return false
}

func (s *Service) ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (ProcessResult, error) {
	p, db, err := s.require()
	if err != nil {
		return ProcessResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	env.Normalize()
	env.ChannelType = contracts.ChannelType(strings.ToLower(strings.TrimSpace(string(env.ChannelType))))
	if env.ChannelType == "" {
		env.ChannelType = contracts.ChannelTypeCLI
	}
	if strings.TrimSpace(env.Adapter) == "" {
		env.Adapter = defaultAdapter(string(env.ChannelType))
	}
	if strings.TrimSpace(env.PeerConversationID) == "" {
		env.PeerConversationID = defaultConversationID(string(env.ChannelType))
	}
	if strings.TrimSpace(env.PeerMessageID) == "" {
		env.PeerMessageID = newInboundMessageID()
	}
	if strings.TrimSpace(env.SenderID) == "" {
		env.SenderID = "anonymous"
	}
	env.Normalize()
	if err := env.Validate(); err != nil {
		return ProcessResult{}, err
	}

	var binding store.ChannelBinding
	var conv store.ChannelConversation
	var inbound store.ChannelMessage
	var job store.ChannelTurnJob
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var txErr error
		binding, txErr = s.ensureBindingTx(ctx, tx, p, env)
		if txErr != nil {
			return txErr
		}
		conv, txErr = s.ensureConversationTx(ctx, tx, binding.ID, env.PeerConversationID)
		if txErr != nil {
			return txErr
		}

		if txErr = tx.
			Where("direction = ? AND conversation_id = ? AND adapter = ? AND peer_message_id = ?",
				contracts.ChannelMessageIn, conv.ID, strings.TrimSpace(env.Adapter), strings.TrimSpace(env.PeerMessageID)).
			First(&inbound).Error; txErr == nil {
			txErr = tx.Where("inbound_message_id = ?", inbound.ID).First(&job).Error
			if txErr == nil {
				return nil
			}
			if !errors.Is(txErr, gorm.ErrRecordNotFound) {
				return txErr
			}
			job = store.ChannelTurnJob{
				ConversationID:   inbound.ConversationID,
				InboundMessageID: inbound.ID,
				Status:           contracts.ChannelTurnPending,
			}
			return tx.Create(&job).Error
		}
		if txErr != nil && !errors.Is(txErr, gorm.ErrRecordNotFound) {
			return txErr
		}

		payload := map[string]any{
			"schema":      env.Schema,
			"attachments": env.Attachments,
			"received_at": env.ReceivedAt,
		}
		peerID := strings.TrimSpace(env.PeerMessageID)
		inbound = store.ChannelMessage{
			ConversationID: conv.ID,
			Direction:      contracts.ChannelMessageIn,
			Adapter:        strings.TrimSpace(env.Adapter),
			PeerMessageID:  &peerID,
			SenderID:       strings.TrimSpace(env.SenderID),
			SenderName:     strings.TrimSpace(env.SenderName),
			ContentText:    strings.TrimSpace(env.Text),
			PayloadJSON:    mustJSON(payload),
			Status:         contracts.ChannelMessageAccepted,
		}
		if txErr := tx.Create(&inbound).Error; txErr != nil {
			return txErr
		}

		now := time.Now()
		if txErr := tx.Model(&store.ChannelConversation{}).
			Where("id = ?", conv.ID).
			Updates(map[string]any{
				"last_message_at": &now,
				"updated_at":      now,
			}).Error; txErr != nil {
			return txErr
		}

		job = store.ChannelTurnJob{
			ConversationID:   conv.ID,
			InboundMessageID: inbound.ID,
			Status:           contracts.ChannelTurnPending,
		}
		return tx.Create(&job).Error
	}); err != nil {
		return ProcessResult{}, err
	}

	if err := s.runTurnJob(ctx, job.ID); err != nil {
		if result, collectErr := s.collectTurnResultWithTimeout(ctx, binding.ID, conv.ID, inbound.ID, job.ID); collectErr == nil {
			return result, nil
		}
		return ProcessResult{}, err
	}
	return s.collectTurnResultWithTimeout(ctx, binding.ID, conv.ID, inbound.ID, job.ID)
}

func (s *Service) ensureBindingTx(ctx context.Context, tx *gorm.DB, p *core.Project, env contracts.InboundEnvelope) (store.ChannelBinding, error) {
	if env.BindingID > 0 {
		var binding store.ChannelBinding
		if err := tx.WithContext(ctx).First(&binding, env.BindingID).Error; err != nil {
			return store.ChannelBinding{}, err
		}
		if !binding.Enabled {
			return store.ChannelBinding{}, fmt.Errorf("channel binding 已禁用: %d", binding.ID)
		}
		return binding, nil
	}

	channelType := contracts.ChannelType(strings.ToLower(strings.TrimSpace(string(env.ChannelType))))
	adapter := strings.TrimSpace(env.Adapter)
	peerProjectKey := ""

	var binding store.ChannelBinding
	err := tx.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ?", channelType, adapter, peerProjectKey).
		First(&binding).Error
	if err == nil {
		if !binding.Enabled {
			return store.ChannelBinding{}, fmt.Errorf("channel binding 已禁用: %d", binding.ID)
		}
		return binding, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return store.ChannelBinding{}, err
	}

	binding = store.ChannelBinding{
		ProjectName:    strings.TrimSpace(p.Name),
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

func (s *Service) ensureConversationTx(ctx context.Context, tx *gorm.DB, bindingID uint, peerConversationID string) (store.ChannelConversation, error) {
	var conv store.ChannelConversation
	err := tx.WithContext(ctx).
		Where("binding_id = ? AND peer_conversation_id = ?", bindingID, strings.TrimSpace(peerConversationID)).
		First(&conv).Error
	if err == nil {
		return conv, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return store.ChannelConversation{}, err
	}
	conv = store.ChannelConversation{
		BindingID:          bindingID,
		PeerConversationID: strings.TrimSpace(peerConversationID),
		Title:              "",
		Summary:            "",
	}
	if err := tx.WithContext(ctx).Create(&conv).Error; err != nil {
		return store.ChannelConversation{}, err
	}
	return conv, nil
}

func (s *Service) runTurnJob(ctx context.Context, jobID uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID == 0 {
		return fmt.Errorf("job_id 不能为空")
	}

	runnerID := newRunnerID()
	job, claimed, err := s.claimTurnJob(ctx, jobID, runnerID, 90*time.Second)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	runID := newRunID()
	startedAt := time.Now()
	gaCfg := s.p.Config.WithDefaults().GatewayAgent
	providerHint, modelHint := resolveGatewayAgentHints(agentcli.ConfigOverride{
		Provider:     strings.TrimSpace(gaCfg.Provider),
		Model:        strings.TrimSpace(gaCfg.Model),
		Command:      strings.TrimSpace(gaCfg.Command),
		Output:       strings.TrimSpace(gaCfg.Output),
		ResumeOutput: strings.TrimSpace(gaCfg.ResumeOutput),
	})
	collector := newTurnEventCollector(ctx, runID, startedAt, providerHint)
	collector.AppendLifecycleStart()

	// eventlog: 初始化 run 日志
	evLogProject := strings.TrimSpace(s.p.Name)
	if evLogProject == "" {
		evLogProject = strings.TrimSpace(s.p.Key)
	}
	if evLogProject == "" {
		evLogProject = "unknown"
	}
	evLogger, evLogErr := eventlog.Open(evLogProject, runID)
	if evLogErr != nil {
		s.slog().Warn("eventlog open failed",
			"project", evLogProject,
			"run_id", runID,
			"error", evLogErr,
		)
	}
	if evLogger != nil {
		defer func() { _ = evLogger.Close() }()
	}
	failTurn := func(cause error, resultJSON string) error {
		msg := strings.TrimSpace(fmt.Sprint(cause))
		if cause == nil || msg == "" || msg == "<nil>" {
			msg = "turn job failed"
		}
		if failErr := s.completeTurnJobFailed(context.Background(), job.ID, runnerID, msg, resultJSON); failErr != nil {
			if cause == nil {
				return failErr
			}
			return fmt.Errorf("%w；并且写入 turn_job failed 失败: %v", cause, failErr)
		}
		return nil
	}

	var inbound store.ChannelMessage
	if err := db.WithContext(ctx).First(&inbound, job.InboundMessageID).Error; err != nil {
		return failTurn(err, "")
	}
	var conv store.ChannelConversation
	if err := db.WithContext(ctx).First(&conv, inbound.ConversationID).Error; err != nil {
		return failTurn(err, "")
	}
	var binding store.ChannelBinding
	if err := db.WithContext(ctx).First(&binding, conv.BindingID).Error; err != nil {
		return failTurn(err, "")
	}

	s.cancelConflictingTurn(conv.AgentSessionID, job.ID)
	if err := s.acquireTurnSlot(ctx); err != nil {
		collector.AppendLifecycleEnd(err)
		payload := turnJobResult{
			Schema:           turnJobResultSchemaV1,
			ConversationID:   conv.ID,
			InboundMessageID: inbound.ID,
			RunID:            runID,
			AgentEvents:      collector.Snapshot(),
		}
		return failTurn(err, mustJSON(payload))
	}
	defer s.releaseTurnSlot()

	turnTimeout := s.gatewayTurnTimeout()
	turnCtx := ctx
	turnCancel := func() {}
	if turnTimeout > 0 {
		turnCtx, turnCancel = context.WithTimeout(ctx, turnTimeout)
	} else {
		turnCtx, turnCancel = context.WithCancel(ctx)
	}
	s.setRunningTurn(job.ID, conv.ID, conv.AgentSessionID, turnCancel)
	defer func() {
		turnCancel()
		s.clearRunningTurn(job.ID)
	}()

	// eventlog: 写入 header
	if evLogger != nil {
		_ = evLogger.WriteHeader(eventlog.RunMeta{
			RunID:          runID,
			Project:        evLogProject,
			ConversationID: fmt.Sprintf("%d", conv.ID),
			Provider:       providerHint,
			Model:          modelHint,
			WorkDir:        strings.TrimSpace(s.p.RepoRoot),
			Layer:          "chat_runner",
		})
	}
	var evLogSeq int
	agentResp, err := s.planTurnByPMAgent(turnCtx, inbound, conv, binding, job, func(ev agentcli.Event) {
		evLogSeq++
		if evLogger != nil {
			_ = evLogger.WriteEvent(evLogSeq, ev.Type, ev.RawJSON)
		}
		collector.AppendCLIEvent(ev)
	})
	// eventlog: 写入 footer
	if evLogger != nil {
		replyForLog := strings.TrimSpace(agentResp.Text)
		errForLog := ""
		if err != nil {
			errForLog = strings.TrimSpace(err.Error())
		}
		_ = evLogger.WriteFooter(eventlog.RunFooter{
			RunID:      runID,
			DurationMS: time.Since(startedAt).Milliseconds(),
			ReplyText:  replyForLog,
			Error:      errForLog,
			SessionID:  strings.TrimSpace(agentResp.SessionID),
		})
	}
	if err != nil {
		if collector.CLIEventCount() == 0 {
			for _, ev := range copyCLIEvents(agentResp.Events) {
				collector.AppendCLIEvent(ev)
			}
		}
		collector.AppendLifecycleEnd(err)
		payload := turnJobResult{
			Schema:            turnJobResultSchemaV1,
			ConversationID:    conv.ID,
			InboundMessageID:  inbound.ID,
			RunID:             runID,
			AgentProvider:     strings.TrimSpace(agentResp.Provider),
			AgentModel:        strings.TrimSpace(agentResp.Model),
			AgentOutputMode:   strings.TrimSpace(string(agentResp.OutputMode)),
			AgentCommand:      strings.TrimSpace(agentResp.Command),
			AgentStdout:       strings.TrimSpace(agentResp.Stdout),
			AgentStderr:       strings.TrimSpace(agentResp.Stderr),
			AgentSessionID:    strings.TrimSpace(agentResp.SessionID),
			AgentEvents:       collector.Snapshot(),
			OutboundMessageID: 0,
			OutboxID:          0,
		}
		return failTurn(err, mustJSON(payload))
	}

	if collector.CLIEventCount() == 0 {
		for _, ev := range copyCLIEvents(agentResp.Events) {
			collector.AppendCLIEvent(ev)
		}
	}

	replyText := strings.TrimSpace(agentResp.Text)
	effectiveReply := replyText
	pendingActionsToCreate := []contracts.TurnAction{}
	if turnResp, ok := parseTurnResponseFromAgent(agentResp); ok {
		if text := strings.TrimSpace(turnResp.ReplyText); text != "" {
			effectiveReply = text
		}
		if len(turnResp.Actions) > 0 {
			if turnResp.RequiresConfirmation {
				pendingActionsToCreate = append(pendingActionsToCreate, turnResp.Actions...)
				if strings.TrimSpace(effectiveReply) == "" {
					effectiveReply = "检测到待审批操作，请点击审批卡片确认。"
				}
			} else {
				results := make([]actionExecuteResult, 0, len(turnResp.Actions))
				for _, action := range turnResp.Actions {
					results = append(results, s.executeAction(turnCtx, action))
				}
				if summary := renderActionExecutionSummary(results); summary != "" {
					if strings.TrimSpace(effectiveReply) == "" {
						effectiveReply = summary
					} else {
						effectiveReply = strings.TrimSpace(effectiveReply + "\n\n" + summary)
					}
				}
			}
		}
	}
	if strings.TrimSpace(effectiveReply) == "" {
		noReplyErr := fmt.Errorf("project manager agent 无响应（reply_text 为空）")
		collector.AppendLifecycleEnd(noReplyErr)
		payload := turnJobResult{
			Schema:           turnJobResultSchemaV1,
			ConversationID:   conv.ID,
			InboundMessageID: inbound.ID,
			RunID:            runID,
			AgentProvider:    strings.TrimSpace(agentResp.Provider),
			AgentModel:       strings.TrimSpace(agentResp.Model),
			AgentOutputMode:  strings.TrimSpace(string(agentResp.OutputMode)),
			AgentCommand:     strings.TrimSpace(agentResp.Command),
			AgentStdout:      strings.TrimSpace(agentResp.Stdout),
			AgentStderr:      strings.TrimSpace(agentResp.Stderr),
			AgentSessionID:   strings.TrimSpace(agentResp.SessionID),
			AgentEvents:      collector.Snapshot(),
		}
		return failTurn(noReplyErr, mustJSON(payload))
	}

	collector.AppendAssistantText(effectiveReply)
	collector.AppendLifecycleEnd(nil)
	events := collector.Snapshot()
	payload := turnJobResult{
		Schema:           turnJobResultSchemaV1,
		ConversationID:   conv.ID,
		InboundMessageID: inbound.ID,
		RunID:            runID,
		AgentReplyText:   effectiveReply,
		AgentProvider:    strings.TrimSpace(agentResp.Provider),
		AgentModel:       strings.TrimSpace(agentResp.Model),
		AgentSessionID:   strings.TrimSpace(agentResp.SessionID),
		AgentOutputMode:  strings.TrimSpace(string(agentResp.OutputMode)),
		AgentCommand:     strings.TrimSpace(agentResp.Command),
		AgentStdout:      strings.TrimSpace(agentResp.Stdout),
		AgentStderr:      strings.TrimSpace(agentResp.Stderr),
		AgentEvents:      copyAgentEvents(events),
	}
	payloadJSON := mustJSON(payload)
	var outMsg store.ChannelMessage
	var outbox store.ChannelOutbox
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(pendingActionsToCreate) > 0 {
			pendingModels, err := s.createPendingActionsTx(ctx, tx, conv.ID, job.ID, pendingActionsToCreate)
			if err != nil {
				return err
			}
			payload.PendingActions = pendingActionViewsFromModels(pendingModels)
			payloadJSON = mustJSON(payload)
		}

		outPeerID := fmt.Sprintf("out_job_%d", job.ID)
		outMsg = store.ChannelMessage{
			ConversationID: conv.ID,
			Direction:      contracts.ChannelMessageOut,
			Adapter:        strings.TrimSpace(binding.Adapter),
			PeerMessageID:  &outPeerID,
			SenderID:       "pm",
			ContentText:    effectiveReply,
			PayloadJSON:    payloadJSON,
			Status:         contracts.ChannelMessageProcessed,
		}
		if err := tx.Create(&outMsg).Error; err != nil {
			return err
		}

		now := time.Now()
		convUpdates := map[string]any{
			"last_message_at": &now,
			"updated_at":      now,
		}
		if strings.TrimSpace(agentResp.SessionID) != "" {
			convUpdates["agent_session_id"] = strings.TrimSpace(agentResp.SessionID)
		}
		if err := tx.Model(&store.ChannelConversation{}).
			Where("id = ?", conv.ID).
			Updates(convUpdates).Error; err != nil {
			return err
		}
		if err := tx.Model(&store.ChannelMessage{}).
			Where("id = ?", inbound.ID).
			Updates(map[string]any{
				"status": contracts.ChannelMessageProcessed,
			}).Error; err != nil {
			return err
		}

		outbox = store.ChannelOutbox{
			MessageID:   outMsg.ID,
			Adapter:     strings.TrimSpace(binding.Adapter),
			PayloadJSON: payloadJSON,
			Status:      contracts.ChannelOutboxPending,
			RetryCount:  0,
			LastError:   "",
		}
		if err := tx.Create(&outbox).Error; err != nil {
			return err
		}

		payload.OutboundMessageID = outMsg.ID
		payload.OutboxID = outbox.ID
		payloadJSON = mustJSON(payload)
		return tx.Model(&store.ChannelMessage{}).
			Where("id = ?", outMsg.ID).
			Update("payload_json", payloadJSON).Error
	}); err != nil {
		return failTurn(err, payloadJSON)
	}

	if err := s.dispatchOutbox(ctx, outbox.ID); err != nil {
		prev := len(payload.AgentEvents)
		payload.AgentEvents = AppendLifecycleErrorEvent(runID, startedAt, payload.AgentEvents, err)
		if len(payload.AgentEvents) > prev {
			emitStreamAgentEvent(ctx, payload.AgentEvents[len(payload.AgentEvents)-1])
		}
		payloadJSON = mustJSON(payload)
		return s.completeTurnJobFailed(context.Background(), job.ID, runnerID, err.Error(), payloadJSON)
	}
	return s.completeTurnJobSuccess(context.Background(), job.ID, runnerID, payloadJSON)
}

func resolveGatewayAgentHints(cfg agentcli.ConfigOverride) (provider, model string) {
	resolved := agentcli.ResolveBackend(cfg)
	return strings.TrimSpace(strings.ToLower(resolved.Provider)), strings.TrimSpace(resolved.Model)
}

func (s *Service) claimTurnJob(ctx context.Context, jobID uint, runnerID string, leaseTTL time.Duration) (store.ChannelTurnJob, bool, error) {
	_, db, err := s.require()
	if err != nil {
		return store.ChannelTurnJob{}, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if leaseTTL <= 0 {
		leaseTTL = 90 * time.Second
	}
	now := time.Now()
	lease := now.Add(leaseTTL)

	var out store.ChannelTurnJob
	claimed := false
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&store.ChannelTurnJob{}).
			Where("id = ? AND (status = ? OR (status = ? AND lease_expires_at IS NOT NULL AND lease_expires_at <= ?))",
				jobID, contracts.ChannelTurnPending, contracts.ChannelTurnRunning, now).
			Updates(map[string]any{
				"status":           contracts.ChannelTurnRunning,
				"runner_id":        runnerID,
				"lease_expires_at": &lease,
				"attempt":          gorm.Expr("attempt + 1"),
				"started_at":       &now,
				"finished_at":      nil,
				"error":            "",
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected > 0 {
			claimed = true
		}
		return tx.First(&out, jobID).Error
	}); err != nil {
		return store.ChannelTurnJob{}, false, err
	}
	return out, claimed, nil
}

func (s *Service) completeTurnJobSuccess(ctx context.Context, jobID uint, runnerID, resultJSON string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	res := db.WithContext(ctx).Model(&store.ChannelTurnJob{}).
		Where("id = ? AND status = ? AND runner_id = ?", jobID, contracts.ChannelTurnRunning, strings.TrimSpace(runnerID)).
		Updates(map[string]any{
			"status":           contracts.ChannelTurnSucceeded,
			"result_json":      strings.TrimSpace(resultJSON),
			"error":            "",
			"runner_id":        "",
			"lease_expires_at": nil,
			"finished_at":      &now,
			"updated_at":       now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("turn job success 提交失败: id=%d runner=%s", jobID, runnerID)
	}
	return nil
}

func (s *Service) completeTurnJobFailed(ctx context.Context, jobID uint, runnerID, errMsg, resultJSON string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	res := db.WithContext(ctx).Model(&store.ChannelTurnJob{}).
		Where("id = ? AND status = ? AND runner_id = ?", jobID, contracts.ChannelTurnRunning, strings.TrimSpace(runnerID)).
		Updates(map[string]any{
			"status":           contracts.ChannelTurnFailed,
			"result_json":      strings.TrimSpace(resultJSON),
			"error":            strings.TrimSpace(errMsg),
			"runner_id":        "",
			"lease_expires_at": nil,
			"finished_at":      &now,
			"updated_at":       now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("turn job failed 提交失败: id=%d runner=%s", jobID, runnerID)
	}
	return nil
}

func (s *Service) waitTurnJob(ctx context.Context, jobID uint, pollInterval time.Duration) (store.ChannelTurnJob, error) {
	_, db, err := s.require()
	if err != nil {
		return store.ChannelTurnJob{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		var job store.ChannelTurnJob
		if err := db.WithContext(ctx).First(&job, jobID).Error; err != nil {
			return store.ChannelTurnJob{}, err
		}
		if isTurnTerminal(job.Status) {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return store.ChannelTurnJob{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) collectTurnResultWithTimeout(ctx context.Context, bindingID, conversationID, inboundMessageID, jobID uint) (ProcessResult, error) {
	job, err := s.waitTurnJob(ctx, jobID, 80*time.Millisecond)
	if err == nil {
		return s.buildProcessResult(ctx, bindingID, conversationID, inboundMessageID, job)
	}

	fallbackCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var latest store.ChannelTurnJob
	_, db, requireErr := s.require()
	if requireErr != nil {
		return ProcessResult{}, requireErr
	}
	if dbErr := db.WithContext(fallbackCtx).First(&latest, jobID).Error; dbErr == nil && isTurnTerminal(latest.Status) {
		return s.buildProcessResult(fallbackCtx, bindingID, conversationID, inboundMessageID, latest)
	}
	return ProcessResult{}, err
}

func (s *Service) buildProcessResult(ctx context.Context, bindingID, conversationID, inboundMessageID uint, job store.ChannelTurnJob) (ProcessResult, error) {
	_, db, err := s.require()
	if err != nil {
		return ProcessResult{}, err
	}
	res := ProcessResult{
		BindingID:        bindingID,
		ConversationID:   conversationID,
		InboundMessageID: inboundMessageID,
		JobID:            job.ID,
		JobStatus:        job.Status,
		JobError:         strings.TrimSpace(job.Error),
	}
	res.JobErrorType = classifyJobErrorType(res.JobError)
	raw := strings.TrimSpace(job.ResultJSON)
	if raw == "" {
		return res, nil
	}
	var payload turnJobResult
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return res, nil
	}
	res.OutboundMessageID = payload.OutboundMessageID
	res.OutboxID = payload.OutboxID
	res.RunID = strings.TrimSpace(payload.RunID)
	res.ReplyText = strings.TrimSpace(payload.AgentReplyText)
	res.AgentProvider = strings.TrimSpace(payload.AgentProvider)
	res.AgentModel = strings.TrimSpace(payload.AgentModel)
	res.AgentOutputMode = strings.TrimSpace(payload.AgentOutputMode)
	res.AgentCommand = strings.TrimSpace(payload.AgentCommand)
	res.AgentStdout = strings.TrimSpace(payload.AgentStdout)
	res.AgentStderr = strings.TrimSpace(payload.AgentStderr)
	res.AgentEvents = copyAgentEvents(payload.AgentEvents)
	res.PendingActions = copyPendingActionViews(payload.PendingActions)

	if payload.OutboundMessageID > 0 {
		var outMsg store.ChannelMessage
		if err := db.WithContext(ctx).First(&outMsg, payload.OutboundMessageID).Error; err == nil {
			res.ReplyText = strings.TrimSpace(outMsg.ContentText)
		}
	}
	return res, nil
}

func (s *Service) dispatchOutbox(ctx context.Context, outboxID uint) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var outbox store.ChannelOutbox
	if err := db.WithContext(ctx).First(&outbox, outboxID).Error; err != nil {
		return err
	}
	if strings.TrimSpace(outbox.Adapter) == "" {
		errMsg := "adapter 为空，无法发送"
		now := time.Now()
		if err := db.WithContext(ctx).Model(&store.ChannelOutbox{}).
			Where("id = ?", outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxFailed,
				"last_error":    errMsg,
				"updated_at":    now,
				"next_retry_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("%s: %w", errMsg, err)
		}
		if err := db.WithContext(ctx).Model(&store.ChannelMessage{}).
			Where("id = ?", outbox.MessageID).
			Update("status", contracts.ChannelMessageFailed).Error; err != nil {
			return fmt.Errorf("%s: %w", errMsg, err)
		}
		return errors.New(errMsg)
	}

	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.First(&outbox, outboxID).Error; err != nil {
			return err
		}
		now := time.Now()
		if err := tx.Model(&store.ChannelOutbox{}).
			Where("id = ? AND status IN ?", outbox.ID, []contracts.ChannelOutboxStatus{contracts.ChannelOutboxPending, contracts.ChannelOutboxFailed}).
			Updates(map[string]any{
				"status":      contracts.ChannelOutboxSending,
				"retry_count": gorm.Expr("retry_count + 1"),
				"updated_at":  now,
				"last_error":  "",
			}).Error; err != nil {
			return err
		}

		if err := tx.Model(&store.ChannelOutbox{}).
			Where("id = ?", outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxSent,
				"updated_at":    time.Now(),
				"last_error":    "",
				"next_retry_at": nil,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&store.ChannelMessage{}).
			Where("id = ?", outbox.MessageID).
			Update("status", contracts.ChannelMessageSent).Error; err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) planTurnByPMAgent(ctx context.Context, inbound store.ChannelMessage, conv store.ChannelConversation, binding store.ChannelBinding, job store.ChannelTurnJob, onEvent func(agentcli.Event)) (pmAgentTurnResponse, error) {
	p, _, err := s.require()
	if err != nil {
		return pmAgentTurnResponse{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	repoRoot := strings.TrimSpace(p.RepoRoot)
	if repoRoot == "" {
		repoRoot = strings.TrimSpace(p.Layout.RepoRoot)
	}
	if repoRoot == "" {
		return pmAgentTurnResponse{}, fmt.Errorf("repo_root 为空，无法调用 project manager agent")
	}

	_ = binding
	prompt := buildPMAgentPrompt(inbound)
	if prompt == "" {
		return pmAgentTurnResponse{}, fmt.Errorf("用户消息为空，无法调用 project manager agent")
	}
	gaCfg := p.Config.WithDefaults().GatewayAgent
	resolved := agentcli.ResolveBackend(agentcli.ConfigOverride{
		Provider:     strings.TrimSpace(gaCfg.Provider),
		Model:        strings.TrimSpace(gaCfg.Model),
		Command:      strings.TrimSpace(gaCfg.Command),
		Output:       strings.TrimSpace(gaCfg.Output),
		ResumeOutput: strings.TrimSpace(gaCfg.ResumeOutput),
	})
	mode := strings.TrimSpace(strings.ToLower(gaCfg.Mode))
	if envMode := strings.TrimSpace(strings.ToLower(os.Getenv("DALEK_GATEWAY_AGENT_MODE"))); envMode != "" {
		mode = envMode
	}
	if mode == "" {
		mode = "sdk"
	}
	var runResult agentcli.Result
	if mode == "sdk" {
		approvalHandler := s.buildSDKToolApprovalHandler(ctx, conv.ID, job.ID)
		runResult, err = s.runAgentSDK(ctx, runAgentSDKRequest{
			ConversationID: fmt.Sprintf("%d", conv.ID),
			Provider:       strings.TrimSpace(resolved.Provider),
			Model:          strings.TrimSpace(resolved.Model),
			Command:        strings.TrimSpace(resolved.Backend.Command),
			WorkDir:        repoRoot,
			Prompt:         prompt,
			SessionID:      strings.TrimSpace(conv.AgentSessionID),
			OnToolApproval: approvalHandler,
			OnEvent:        onEvent,
		})
	} else {
		runResult, err = s.runAgentCLI(ctx, resolved.Backend, agentcli.RunRequest{
			WorkDir:   repoRoot,
			Prompt:    prompt,
			Model:     strings.TrimSpace(resolved.Model),
			SessionID: strings.TrimSpace(conv.AgentSessionID),
		})
	}
	if err != nil {
		return pmAgentTurnResponse{
			Provider:   strings.TrimSpace(resolved.Provider),
			Model:      strings.TrimSpace(resolved.Model),
			Text:       strings.TrimSpace(runResult.Text),
			SessionID:  strings.TrimSpace(runResult.SessionID),
			OutputMode: runResult.OutputMode,
			Command:    strings.TrimSpace(runResult.Command),
			Stdout:     strings.TrimSpace(runResult.Stdout),
			Stderr:     strings.TrimSpace(runResult.Stderr),
			Events:     copyCLIEvents(runResult.Events),
		}, err
	}
	replyText := strings.TrimSpace(runResult.Text)
	if replyText == "" {
		return pmAgentTurnResponse{
			Provider:   strings.TrimSpace(resolved.Provider),
			Model:      strings.TrimSpace(resolved.Model),
			Text:       "",
			SessionID:  strings.TrimSpace(runResult.SessionID),
			OutputMode: runResult.OutputMode,
			Command:    strings.TrimSpace(runResult.Command),
			Stdout:     strings.TrimSpace(runResult.Stdout),
			Stderr:     strings.TrimSpace(runResult.Stderr),
			Events:     copyCLIEvents(runResult.Events),
		}, fmt.Errorf("project manager agent 无响应（stdout 为空）")
	}
	return pmAgentTurnResponse{
		Provider:   strings.TrimSpace(resolved.Provider),
		Model:      strings.TrimSpace(resolved.Model),
		Text:       replyText,
		SessionID:  strings.TrimSpace(runResult.SessionID),
		OutputMode: runResult.OutputMode,
		Command:    strings.TrimSpace(runResult.Command),
		Stdout:     strings.TrimSpace(runResult.Stdout),
		Stderr:     strings.TrimSpace(runResult.Stderr),
		Events:     copyCLIEvents(runResult.Events),
	}, nil
}

func buildPMAgentPrompt(inbound store.ChannelMessage) string {
	content := strings.TrimSpace(inbound.ContentText)
	if content == "" {
		return ""
	}

	senderID := strings.TrimSpace(inbound.SenderID)
	if senderID == "" || strings.EqualFold(senderID, "anonymous") {
		return content
	}

	senderName := strings.TrimSpace(inbound.SenderName)
	if senderName == "" {
		return fmt.Sprintf("[sender: %s]\n%s", senderID, content)
	}
	return fmt.Sprintf("[sender: %s (%s)]\n%s", senderName, senderID, content)
}

func copyCLIEvents(in []agentcli.Event) []agentcli.Event {
	if len(in) == 0 {
		return nil
	}
	out := make([]agentcli.Event, 0, len(in))
	for _, ev := range in {
		typ := strings.TrimSpace(ev.Type)
		text := strings.TrimSpace(ev.Text)
		raw := strings.TrimSpace(ev.RawJSON)
		sid := strings.TrimSpace(ev.SessionID)
		if typ == "" && text == "" && raw == "" && sid == "" {
			continue
		}
		out = append(out, agentcli.Event{
			Type:      typ,
			Text:      text,
			RawJSON:   raw,
			SessionID: sid,
		})
	}
	return out
}

func defaultAdapter(channelType string) string {
	switch strings.ToLower(strings.TrimSpace(channelType)) {
	case string(contracts.ChannelTypeWeb):
		return "web.ws"
	case string(contracts.ChannelTypeCLI):
		return "cli.local"
	case string(contracts.ChannelTypeAPI):
		return "api.http"
	case string(contracts.ChannelTypeIM):
		return "im.unknown"
	default:
		return "cli.local"
	}
}

func defaultConversationID(channelType string) string {
	switch strings.ToLower(strings.TrimSpace(channelType)) {
	case string(contracts.ChannelTypeCLI):
		return "cli.default"
	case string(contracts.ChannelTypeWeb):
		return "web.default"
	default:
		return "default"
	}
}

func newRunnerID() string {
	return "gateway-runner-" + randomHex("r", 8)
}

func newInboundMessageID() string {
	return "msg_" + randomHex("m", 8)
}

func newRunID() string {
	return "run-" + randomHex("run", 8)
}

func randomHex(prefix string, nbytes int) string {
	buf := make([]byte, nbytes)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return strings.TrimSpace(string(b))
}

func isTurnTerminal(st contracts.ChannelTurnJobStatus) bool {
	switch st {
	case contracts.ChannelTurnSucceeded, contracts.ChannelTurnFailed:
		return true
	default:
		return false
	}
}

func classifyJobErrorType(msg string) string {
	raw := strings.TrimSpace(msg)
	if raw == "" {
		return ""
	}
	s := strings.ToLower(raw)

	if containsAny(s, []string{
		"context deadline exceeded",
		"deadline exceeded",
		"timed out",
		"timeout",
	}) {
		return "timeout"
	}
	if containsAny(s, []string{
		"unauthorized",
		"authentication failed",
		"authentication error",
		"forbidden",
		"access denied",
		"invalid api key",
		"invalid token",
		"token expired",
		"not logged in",
		"login required",
		"http 401",
		"http 403",
		"status code: 401",
		"status code: 403",
	}) {
		return "auth"
	}
	if containsAny(s, []string{
		"stream disconnected",
		"connection reset",
		"connection refused",
		"connection closed",
		"broken pipe",
		"dial tcp",
		"no such host",
		"temporary failure",
		"network",
		"reconnecting",
		"tls handshake",
		"x509",
		"proxyconnect",
	}) {
		return "network"
	}
	if containsAny(s, []string{
		"agent backend command 为空",
		"repo_root 为空",
		"用户消息为空",
		"missing env",
		"missing prompt",
		"command not found",
		"no such file or directory",
		"unknown flag",
		"invalid option",
		"pm_agent 配置非法",
		"invalid argument",
		"exit code=127",
	}) {
		return "config"
	}
	if containsAny(s, []string{
		"无响应",
		"empty reply",
		"stdout 为空",
	}) {
		return "agent"
	}
	return "unknown"
}

func containsAny(s string, tokens []string) bool {
	for _, t := range tokens {
		if strings.Contains(s, strings.ToLower(strings.TrimSpace(t))) {
			return true
		}
	}
	return false
}
