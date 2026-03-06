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

	"dalek/internal/contracts"
	"dalek/internal/services/channel/agentcli"
	"dalek/internal/services/core"

	"gorm.io/gorm"
)

type Service struct {
	p       *core.Project
	logger  *slog.Logger
	turnSem chan struct{}

	runningMu sync.Mutex
	running   *runningTurn

	actionExecMu sync.Mutex
	actionExec   *ActionExecutor

	depsMu sync.RWMutex

	chatRunners        ChatRunnerManager
	toolApprovalBridge *ToolApprovalBridge
}

func New(p *core.Project) *Service {
	logger := core.DiscardLogger()
	if p != nil {
		logger = core.EnsureLogger(p.Logger).With("service", "channel")
	}
	svc := &Service{
		p:                  p,
		logger:             logger,
		turnSem:            make(chan struct{}, 1),
		chatRunners:        newDefaultChatRunnerManager(nil),
		toolApprovalBridge: NewToolApprovalBridge(logger.With("component", "tool_approval")),
	}
	return svc
}

func (s *Service) actionExecutor() *ActionExecutor {
	if s == nil {
		return nil
	}
	s.actionExecMu.Lock()
	defer s.actionExecMu.Unlock()
	return s.actionExec
}

func (s *Service) SetActionExecutor(executor *ActionExecutor) {
	if s == nil {
		return
	}
	s.actionExecMu.Lock()
	defer s.actionExecMu.Unlock()
	s.actionExec = executor
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
	AgentSessionID    string
	AgentOutputMode   string
	AgentCommand      string
	AgentStdout       string
	AgentStderr       string
	AgentEvents       []AgentEvent
	PendingActions    []PendingActionView
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

func (s *Service) chatRunnerManagerSnapshot() ChatRunnerManager {
	if s == nil {
		return nil
	}
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.chatRunners
}

func (s *Service) ensureChatRunnerManager() ChatRunnerManager {
	if s == nil {
		return nil
	}
	s.depsMu.Lock()
	defer s.depsMu.Unlock()
	if s.chatRunners == nil {
		s.chatRunners = newDefaultChatRunnerManager(nil)
	}
	return s.chatRunners
}

func (s *Service) toolApprovalBridgeSnapshot() *ToolApprovalBridge {
	if s == nil {
		return nil
	}
	s.depsMu.RLock()
	defer s.depsMu.RUnlock()
	return s.toolApprovalBridge
}

func (s *Service) logInterrupt(phase string, attrs ...any) {
	all := []any{
		"cmd", "stop",
		"phase", phase,
	}
	all = append(all, attrs...)
	s.slog().Info("channel interrupt", all...)
}

func (s *Service) logReset(phase string, attrs ...any) {
	all := []any{
		"cmd", "reset",
		"phase", phase,
	}
	all = append(all, attrs...)
	s.slog().Info("channel reset", all...)
}

func (s *Service) Close() error {
	if s == nil {
		return nil
	}
	s.depsMu.Lock()
	chatRunners := s.chatRunners
	toolApprovalBridge := s.toolApprovalBridge
	s.chatRunners = nil
	s.toolApprovalBridge = nil
	s.depsMu.Unlock()

	var errs []error
	if chatRunners != nil {
		if err := chatRunners.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if toolApprovalBridge != nil {
		toolApprovalBridge.Close()
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
	if manager := s.chatRunnerManagerSnapshot(); manager != nil {
		runnerInterrupted, runnerErr = manager.InterruptConversation(ctx, fmt.Sprintf("%d", conv.ID))
	}
	// 无论 runner 是否声明 interrupt 成功，都尝试取消 turn context。
	// 某些阻塞路径只会响应 context cancel，不会响应 interrupt 信号。
	ctxCanceled := s.cancelRunningTurnByConversation(conv.ID)
	result := InterruptResult{
		ConversationID:    conv.ID,
		RunnerInterrupted: runnerInterrupted,
		ContextCanceled:   ctxCanceled,
	}
	if runnerErr != nil {
		result.RunnerError = runnerErr.Error()
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
		return false, fmt.Errorf("context 不能为空")
	}

	conv, found, err := s.resolvePeerConversation(ctx, channelType, adapter, peerConversationID)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}

	now := time.Now()
	if err := db.WithContext(ctx).Model(&contracts.ChannelConversation{}).
		Where("id = ?", conv.ID).
		Updates(map[string]any{
			"agent_session_id": "",
			"updated_at":       now,
		}).Error; err != nil {
		return false, err
	}
	s.cancelRunningTurnByConversation(conv.ID)
	if manager := s.chatRunnerManagerSnapshot(); manager != nil {
		manager.CloseConversation(fmt.Sprintf("%d", conv.ID))
	}
	return true, nil
}

func (s *Service) HardResetPeerConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (bool, error) {
	_, db, err := s.require()
	if err != nil {
		return false, err
	}
	if ctx == nil {
		return false, fmt.Errorf("context 不能为空")
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
	s.logReset("cmd_received",
		"channel_type", channelType,
		"adapter", adapter,
		"peer_conversation_id", peerConversationID,
	)

	conv, found, err := s.resolvePeerConversation(ctx, channelType, adapter, peerConversationID)
	if err != nil {
		s.logReset("locator_error",
			"channel_type", channelType,
			"adapter", adapter,
			"peer_conversation_id", peerConversationID,
			"error", err,
		)
		return false, err
	}
	if !found {
		s.logReset("locator_result",
			"channel_type", channelType,
			"adapter", adapter,
			"peer_conversation_id", peerConversationID,
			"locator", "miss",
		)
		return true, nil
	}
	s.logReset("locator_result",
		"channel_type", channelType,
		"adapter", adapter,
		"peer_conversation_id", peerConversationID,
		"locator", "hit",
		"conversation_id", conv.ID,
		"has_session", strings.TrimSpace(conv.AgentSessionID) != "",
	)

	contextCanceled := s.cancelRunningTurnByConversation(conv.ID)
	s.logReset("cancel_turn_result",
		"conversation_id", conv.ID,
		"context_canceled", contextCanceled,
	)

	var forceErr error
	managerPresent := false
	if manager := s.chatRunnerManagerSnapshot(); manager != nil {
		managerPresent = true
		forceErr = manager.ForceCloseConversation(fmt.Sprintf("%d", conv.ID))
	}
	s.logReset("force_close_result",
		"conversation_id", conv.ID,
		"manager_present", managerPresent,
		"error", forceErr,
	)

	now := time.Now()
	if err := db.WithContext(ctx).Model(&contracts.ChannelConversation{}).
		Where("id = ?", conv.ID).
		Updates(map[string]any{
			"agent_session_id": "",
			"updated_at":       now,
		}).Error; err != nil {
		s.logReset("persist_error",
			"conversation_id", conv.ID,
			"error", err,
		)
		if forceErr != nil {
			combinedErr := fmt.Errorf("%w；另外强制关闭 runner 失败: %v", err, forceErr)
			s.logReset("cmd_result",
				"conversation_id", conv.ID,
				"status", "error",
				"reset", false,
				"error", combinedErr,
			)
			return false, combinedErr
		}
		s.logReset("cmd_result",
			"conversation_id", conv.ID,
			"status", "error",
			"reset", false,
			"error", err,
		)
		return false, err
	}
	if forceErr != nil {
		s.logReset("cmd_result",
			"conversation_id", conv.ID,
			"status", "partial",
			"reset", true,
			"error", forceErr,
		)
		return true, forceErr
	}
	s.logReset("cmd_result",
		"conversation_id", conv.ID,
		"status", "ok",
		"reset", true,
	)
	return true, nil
}

func (s *Service) resolvePeerConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (contracts.ChannelConversation, bool, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.ChannelConversation{}, false, err
	}
	if ctx == nil {
		return contracts.ChannelConversation{}, false, fmt.Errorf("context 不能为空")
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
		return contracts.ChannelConversation{}, false, fmt.Errorf("peer_conversation_id 不能为空")
	}
	var binding contracts.ChannelBinding
	if err := db.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ?",
			channelType,
			adapter,
			"").
		First(&binding).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return contracts.ChannelConversation{}, false, nil
		}
		return contracts.ChannelConversation{}, false, err
	}

	var conv contracts.ChannelConversation
	if err := db.WithContext(ctx).
		Where("binding_id = ? AND peer_conversation_id = ?", binding.ID, peerConversationID).
		First(&conv).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return contracts.ChannelConversation{}, false, nil
		}
		return contracts.ChannelConversation{}, false, err
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
		return fmt.Errorf("context 不能为空")
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
		sessionID:      sessionID,
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
	if s.running != nil && s.running.jobID != currentJobID && s.running.sessionID == sessionID {
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
		return ProcessResult{}, fmt.Errorf("context 不能为空")
	}

	env.Normalize()
	env.ChannelType = contracts.ChannelType(strings.ToLower(string(env.ChannelType)))
	if env.ChannelType == "" {
		env.ChannelType = contracts.ChannelTypeCLI
	}
	if env.Adapter == "" {
		env.Adapter = defaultAdapter(string(env.ChannelType))
	}
	if env.PeerConversationID == "" {
		env.PeerConversationID = defaultConversationID(string(env.ChannelType))
	}
	if env.PeerMessageID == "" {
		env.PeerMessageID = newInboundMessageID()
	}
	if env.SenderID == "" {
		env.SenderID = "anonymous"
	}
	env.Normalize()
	if err := env.Validate(); err != nil {
		return ProcessResult{}, err
	}

	var binding contracts.ChannelBinding
	var conv contracts.ChannelConversation
	var inbound contracts.ChannelMessage
	var job contracts.ChannelTurnJob
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var txErr error
		binding, txErr = EnsureBindingTx(ctx, tx, EnsureBindingParams{
			ProjectName:    p.Name,
			PeerProjectKey: "",
			Env:            env,
			AutoUpdate:     false,
		})
		if txErr != nil {
			return txErr
		}
		conv, txErr = EnsureConversationTx(ctx, tx, binding.ID, env.PeerConversationID)
		if txErr != nil {
			return txErr
		}
		inbound, job, _, txErr = PersistInboundMessageTx(ctx, tx, PersistInboundParams{
			Conv:    conv,
			Env:     env,
			Project: p.Name,
		})
		return txErr
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

func (s *Service) runTurnJob(ctx context.Context, jobID uint) error {
	tctx, claimed, err := s.claimAndLoadTurnContext(ctx, jobID)
	if err != nil || !claimed {
		return err
	}
	defer tctx.closeEventLogger()

	agentResp, err := s.executeTurnAgent(ctx, tctx)
	if tctx.slotAcquired {
		defer s.releaseTurnSlot()
	}
	if tctx.runningTurnSet {
		defer func() {
			if tctx.turnCancel != nil {
				tctx.turnCancel()
			}
			s.clearRunningTurn(tctx.job.ID)
		}()
	}
	if err != nil {
		return s.failAndPersistTurn(ctx, tctx, agentResp, err)
	}

	effectiveReply, pendingActions, err := s.processTurnResponse(tctx.executionCtx(ctx), tctx, agentResp)
	if err != nil {
		return s.failAndPersistTurn(ctx, tctx, agentResp, err)
	}
	return s.finalizeTurn(ctx, tctx, effectiveReply, pendingActions)
}

func resolveGatewayAgentHints(cfg agentcli.ConfigOverride) (provider, model string) {
	resolved := agentcli.ResolveBackend(cfg)
	return strings.ToLower(resolved.Provider), resolved.Model
}

func (s *Service) claimTurnJob(ctx context.Context, jobID uint, runnerID string, leaseTTL time.Duration) (contracts.ChannelTurnJob, bool, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.ChannelTurnJob{}, false, err
	}
	if ctx == nil {
		return contracts.ChannelTurnJob{}, false, fmt.Errorf("context 不能为空")
	}
	if leaseTTL <= 0 {
		leaseTTL = 90 * time.Second
	}
	now := time.Now()
	lease := now.Add(leaseTTL)

	var out contracts.ChannelTurnJob
	claimed := false
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&contracts.ChannelTurnJob{}).
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
		return contracts.ChannelTurnJob{}, false, err
	}
	return out, claimed, nil
}

func (s *Service) completeTurnJobSuccess(ctx context.Context, jobID uint, runnerID, resultJSON string) error {
	_, db, err := s.require()
	if err != nil {
		return err
	}
	if ctx == nil {
		return fmt.Errorf("context 不能为空")
	}
	now := time.Now()
	res := db.WithContext(ctx).Model(&contracts.ChannelTurnJob{}).
		Where("id = ? AND status = ? AND runner_id = ?", jobID, contracts.ChannelTurnRunning, runnerID).
		Updates(map[string]any{
			"status":           contracts.ChannelTurnSucceeded,
			"result_json":      resultJSON,
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
		return fmt.Errorf("context 不能为空")
	}
	now := time.Now()
	res := db.WithContext(ctx).Model(&contracts.ChannelTurnJob{}).
		Where("id = ? AND status = ? AND runner_id = ?", jobID, contracts.ChannelTurnRunning, runnerID).
		Updates(map[string]any{
			"status":           contracts.ChannelTurnFailed,
			"result_json":      resultJSON,
			"error":            errMsg,
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

func (s *Service) waitTurnJob(ctx context.Context, jobID uint, pollInterval time.Duration) (contracts.ChannelTurnJob, error) {
	_, db, err := s.require()
	if err != nil {
		return contracts.ChannelTurnJob{}, err
	}
	if ctx == nil {
		return contracts.ChannelTurnJob{}, fmt.Errorf("context 不能为空")
	}
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		var job contracts.ChannelTurnJob
		if err := db.WithContext(ctx).First(&job, jobID).Error; err != nil {
			return contracts.ChannelTurnJob{}, err
		}
		if isTurnTerminal(job.Status) {
			return job, nil
		}
		select {
		case <-ctx.Done():
			return contracts.ChannelTurnJob{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (s *Service) collectTurnResultWithTimeout(ctx context.Context, bindingID, conversationID, inboundMessageID, jobID uint) (ProcessResult, error) {
	if ctx == nil {
		return ProcessResult{}, fmt.Errorf("context 不能为空")
	}
	job, err := s.waitTurnJob(ctx, jobID, 80*time.Millisecond)
	if err == nil {
		return s.buildProcessResult(ctx, bindingID, conversationID, inboundMessageID, job)
	}

	// Best-effort 兜底查询用于读取已落盘的终态，独立于上层请求取消语义。
	fallbackCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var latest contracts.ChannelTurnJob
	_, db, requireErr := s.require()
	if requireErr != nil {
		return ProcessResult{}, requireErr
	}
	if dbErr := db.WithContext(fallbackCtx).First(&latest, jobID).Error; dbErr == nil && isTurnTerminal(latest.Status) {
		return s.buildProcessResult(fallbackCtx, bindingID, conversationID, inboundMessageID, latest)
	}
	return ProcessResult{}, err
}

func (s *Service) buildProcessResult(ctx context.Context, bindingID, conversationID, inboundMessageID uint, job contracts.ChannelTurnJob) (ProcessResult, error) {
	_, db, err := s.require()
	if err != nil {
		return ProcessResult{}, err
	}
	res := decodeTurnResult(job)
	if res.BindingID == 0 {
		res.BindingID = bindingID
	}
	if res.ConversationID == 0 {
		res.ConversationID = conversationID
	}
	if res.InboundMessageID == 0 {
		res.InboundMessageID = inboundMessageID
	}
	if res.JobID == 0 {
		res.JobID = job.ID
	}
	if res.JobStatus == "" {
		res.JobStatus = job.Status
	}
	if res.JobError == "" {
		res.JobError = job.Error
	}
	if res.JobErrorType == "" {
		res.JobErrorType = classifyJobErrorType(res.JobError)
	}

	if res.OutboundMessageID > 0 {
		var outMsg contracts.ChannelMessage
		if err := db.WithContext(ctx).First(&outMsg, res.OutboundMessageID).Error; err == nil {
			res.ReplyText = outMsg.ContentText
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
		return fmt.Errorf("context 不能为空")
	}
	var outbox contracts.ChannelOutbox
	if err := db.WithContext(ctx).First(&outbox, outboxID).Error; err != nil {
		return err
	}
	if outbox.Adapter == "" {
		errMsg := "adapter 为空，无法发送"
		now := time.Now()
		if err := db.WithContext(ctx).Model(&contracts.ChannelOutbox{}).
			Where("id = ?", outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxFailed,
				"last_error":    errMsg,
				"updated_at":    now,
				"next_retry_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("%s: %w", errMsg, err)
		}
		if err := db.WithContext(ctx).Model(&contracts.ChannelMessage{}).
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
		if err := tx.Model(&contracts.ChannelOutbox{}).
			Where("id = ? AND status IN ?", outbox.ID, []contracts.ChannelOutboxStatus{contracts.ChannelOutboxPending, contracts.ChannelOutboxFailed}).
			Updates(map[string]any{
				"status":      contracts.ChannelOutboxSending,
				"retry_count": gorm.Expr("retry_count + 1"),
				"updated_at":  now,
				"last_error":  "",
			}).Error; err != nil {
			return err
		}

		if err := tx.Model(&contracts.ChannelOutbox{}).
			Where("id = ?", outbox.ID).
			Updates(map[string]any{
				"status":        contracts.ChannelOutboxSent,
				"updated_at":    time.Now(),
				"last_error":    "",
				"next_retry_at": nil,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&contracts.ChannelMessage{}).
			Where("id = ?", outbox.MessageID).
			Update("status", contracts.ChannelMessageSent).Error; err != nil {
			return err
		}
		return nil
	})
}

func (s *Service) planTurnByPMAgent(ctx context.Context, inbound contracts.ChannelMessage, conv contracts.ChannelConversation, binding contracts.ChannelBinding, job contracts.ChannelTurnJob, onEvent func(agentcli.Event)) (pmAgentTurnResponse, error) {
	p, _, err := s.require()
	if err != nil {
		return pmAgentTurnResponse{}, err
	}
	if ctx == nil {
		return pmAgentTurnResponse{}, fmt.Errorf("context 不能为空")
	}

	repoRoot := p.RepoRoot
	if repoRoot == "" {
		repoRoot = p.Layout.RepoRoot
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
		Provider:     gaCfg.Provider,
		Model:        gaCfg.Model,
		Command:      gaCfg.Command,
		Output:       gaCfg.Output,
		ResumeOutput: gaCfg.ResumeOutput,
	})
	mode := strings.ToLower(gaCfg.Mode)
	if envMode := strings.ToLower(os.Getenv("DALEK_GATEWAY_AGENT_MODE")); envMode != "" {
		mode = envMode
	}
	if mode == "" {
		mode = "sdk"
	}
	if mode != "sdk" && resolved.Provider == agentcli.ProviderGemini {
		return pmAgentTurnResponse{}, fmt.Errorf("gateway agent provider=gemini 仅支持 sdk mode")
	}
	var runResult agentcli.Result
	if mode == "sdk" {
		approvalHandler := s.buildSDKToolApprovalHandler(ctx, conv.ID, job.ID)
		runResult, err = s.runAgentSDK(ctx, runAgentSDKRequest{
			ConversationID: fmt.Sprintf("%d", conv.ID),
			Provider:       resolved.Provider,
			Model:          resolved.Model,
			Command:        resolved.Backend.Command,
			WorkDir:        repoRoot,
			Prompt:         prompt,
			SessionID:      conv.AgentSessionID,
			OnToolApproval: approvalHandler,
			OnEvent:        onEvent,
		})
	} else {
		runResult, err = s.runAgentCLI(ctx, fmt.Sprintf("%d", conv.ID), resolved.Backend, agentcli.RunRequest{
			WorkDir:   repoRoot,
			Prompt:    prompt,
			Model:     resolved.Model,
			SessionID: conv.AgentSessionID,
		})
	}
	if err != nil {
		return pmAgentTurnResponse{
			Provider:   resolved.Provider,
			Model:      resolved.Model,
			Text:       runResult.Text,
			SessionID:  runResult.SessionID,
			OutputMode: runResult.OutputMode,
			Command:    runResult.Command,
			Stdout:     runResult.Stdout,
			Stderr:     runResult.Stderr,
			Events:     copyCLIEvents(runResult.Events),
		}, err
	}
	replyText := runResult.Text
	if replyText == "" {
		return pmAgentTurnResponse{
			Provider:   resolved.Provider,
			Model:      resolved.Model,
			Text:       "",
			SessionID:  runResult.SessionID,
			OutputMode: runResult.OutputMode,
			Command:    runResult.Command,
			Stdout:     runResult.Stdout,
			Stderr:     runResult.Stderr,
			Events:     copyCLIEvents(runResult.Events),
		}, fmt.Errorf("project manager agent 无响应（stdout 为空）")
	}
	return pmAgentTurnResponse{
		Provider:   resolved.Provider,
		Model:      resolved.Model,
		Text:       replyText,
		SessionID:  runResult.SessionID,
		OutputMode: runResult.OutputMode,
		Command:    runResult.Command,
		Stdout:     runResult.Stdout,
		Stderr:     runResult.Stderr,
		Events:     copyCLIEvents(runResult.Events),
	}, nil
}

func buildPMAgentPrompt(inbound contracts.ChannelMessage) string {
	content := inbound.ContentText

	// Extract image attachments from PayloadJSON and append local paths to prompt.
	var attachmentLines []string
	if raw, ok := inbound.PayloadJSON["attachments"]; ok {
		var attachments []contracts.InboundAttachment
		if b, err := json.Marshal(raw); err == nil {
			if json.Unmarshal(b, &attachments) == nil {
				for i, a := range attachments {
					if a.Type == "image" && a.URL != "" {
						attachmentLines = append(attachmentLines, fmt.Sprintf("[图片%d] %s", i+1, a.URL))
					}
				}
			}
		}
	}

	if content == "" && len(attachmentLines) == 0 {
		return ""
	}

	var parts []string
	if content != "" {
		parts = append(parts, content)
	}
	if len(attachmentLines) > 0 {
		parts = append(parts, strings.Join(attachmentLines, "\n"))
	}
	body := strings.Join(parts, "\n")

	senderID := inbound.SenderID
	if senderID == "" || strings.EqualFold(senderID, "anonymous") {
		return body
	}

	senderName := inbound.SenderName
	if senderName == "" {
		return fmt.Sprintf("[sender: %s]\n%s", senderID, body)
	}
	return fmt.Sprintf("[sender: %s (%s)]\n%s", senderName, senderID, body)
}

func copyCLIEvents(in []agentcli.Event) []agentcli.Event {
	if len(in) == 0 {
		return nil
	}
	out := make([]agentcli.Event, 0, len(in))
	for _, ev := range in {
		typ := ev.Type
		text := ev.Text
		raw := ev.RawJSON
		sid := ev.SessionID
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
	switch strings.ToLower(channelType) {
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
	switch strings.ToLower(channelType) {
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
	return string(b)
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
	raw := msg
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
		if strings.Contains(s, strings.ToLower(t)) {
			return true
		}
	}
	return false
}
