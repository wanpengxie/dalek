package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"
	"dalek/internal/store"

	"gorm.io/gorm"
)

const gatewayTurnResultSchemaV1 = "dalek.channel_gateway_turn_result.v1"

var ErrGatewayStopped = errors.New("gateway stopped")

type ProjectRuntime interface {
	ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (ProcessResult, error)
	GatewayTurnTimeout() time.Duration
}

type ProjectRuntimeInterrupter interface {
	InterruptConversation(ctx context.Context, channelType, adapter, peerConversationID string) (InterruptResult, error)
}

type ProjectRuntimeSessionResetter interface {
	ResetConversationSession(ctx context.Context, channelType, adapter, peerConversationID string) (bool, error)
}

type ProjectRuntimePendingActionManager interface {
	ListPendingActions(ctx context.Context, jobID uint) ([]PendingActionView, error)
	ApprovePendingAction(ctx context.Context, actionID uint, decider string) (PendingActionDecisionResult, error)
	RejectPendingAction(ctx context.Context, actionID uint, decider, note string) (PendingActionDecisionResult, error)
}

type ProjectContext struct {
	Name     string
	RepoRoot string
	Runtime  ProjectRuntime
}

type ProjectResolver interface {
	Resolve(name string) (*ProjectContext, error)
	ListProjects() ([]string, error)
}

type GatewayOptions struct {
	QueueDepth         int
	DefaultTurnTimeout time.Duration
	Logger             *slog.Logger
}

type GatewayInboundRequest struct {
	ProjectName    string
	PeerProjectKey string
	Envelope       contracts.InboundEnvelope
	Callback       func(ProcessResult, error)
}

type InboundItem struct {
	ProjectName    string
	PeerProjectKey string
	Envelope       contracts.InboundEnvelope
	Callback       func(ProcessResult, error)
}

type Gateway struct {
	db       *gorm.DB
	resolver ProjectResolver
	logger   *slog.Logger

	queue *InboundQueue
	bus   *EventBus

	defaultTurnTimeout time.Duration

	workersMu sync.Mutex
	workers   map[string]chan InboundItem
	workerWg  sync.WaitGroup

	lifecycleMu sync.RWMutex
	stopping    atomic.Bool
	stopOnce    sync.Once
	stoppedCh   chan struct{}
}

type gatewayPersistState struct {
	binding store.ChannelBinding
	conv    store.ChannelConversation
	inbound store.ChannelMessage
	job     store.ChannelTurnJob
}

type gatewayTurnResult struct {
	Schema            string              `json:"schema"`
	BindingID         uint                `json:"binding_id"`
	ConversationID    uint                `json:"conversation_id"`
	InboundMessageID  uint                `json:"inbound_message_id"`
	OutboundMessageID uint                `json:"outbound_message_id"`
	OutboxID          uint                `json:"outbox_id"`
	RunID             string              `json:"run_id,omitempty"`
	AgentReplyText    string              `json:"agent_reply_text,omitempty"`
	AgentProvider     string              `json:"agent_provider,omitempty"`
	AgentModel        string              `json:"agent_model,omitempty"`
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

func NewGateway(db *gorm.DB, resolver ProjectResolver, opt GatewayOptions) (*Gateway, error) {
	if db == nil {
		return nil, fmt.Errorf("gateway db 为空")
	}
	if resolver == nil {
		return nil, fmt.Errorf("project resolver 为空")
	}
	queueDepth := opt.QueueDepth
	if queueDepth <= 0 {
		queueDepth = 32
	}
	turnTimeout := opt.DefaultTurnTimeout
	if turnTimeout < 0 {
		turnTimeout = 0
	}
	logger := core.EnsureLogger(opt.Logger).With("service", "channel_gateway")
	return &Gateway{
		db:                 db,
		resolver:           resolver,
		logger:             logger,
		queue:              NewInboundQueue(queueDepth),
		bus:                NewEventBusWithAudit(db),
		defaultTurnTimeout: turnTimeout,
		workers:            map[string]chan InboundItem{},
		stoppedCh:          make(chan struct{}),
	}, nil
}

func (g *Gateway) slog() *slog.Logger {
	if g == nil || g.logger == nil {
		return core.DiscardLogger()
	}
	return g.logger
}

func (g *Gateway) logInterrupt(phase string, attrs ...any) {
	all := []any{
		"cmd", "stop",
		"phase", strings.TrimSpace(phase),
	}
	all = append(all, attrs...)
	g.slog().Info("channel interrupt", all...)
}

func (g *Gateway) EventBus() *EventBus {
	if g == nil {
		return nil
	}
	return g.bus
}

func (g *Gateway) QueueDepth() int {
	if g == nil || g.queue == nil {
		return 32
	}
	return g.queue.Depth()
}

func (g *Gateway) Stop(ctx context.Context) error {
	if g == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	g.stopOnce.Do(func() {
		g.stopping.Store(true)

		g.lifecycleMu.Lock()
		if g.queue != nil {
			g.queue.Close()
		}
		g.workersMu.Lock()
		g.workers = map[string]chan InboundItem{}
		g.workersMu.Unlock()
		g.lifecycleMu.Unlock()

		go func() {
			g.workerWg.Wait()
			if g.bus != nil {
				g.bus.Close()
			}
			close(g.stoppedCh)
		}()
	})

	select {
	case <-g.stoppedCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// MarkOutboxDelivery 根据外部通道真实投递结果回写 outbox/message/conversation 状态。
func (g *Gateway) MarkOutboxDelivery(ctx context.Context, outboxID uint, delivered bool, cause error) error {
	if g == nil || g.db == nil {
		return fmt.Errorf("gateway db 为空")
	}
	if outboxID == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	errMsg := strings.TrimSpace(fmt.Sprint(cause))
	if delivered {
		errMsg = ""
	} else if errMsg == "" || errMsg == "<nil>" {
		errMsg = "gateway delivery failed"
	}

	return g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var outbox store.ChannelOutbox
		if err := tx.WithContext(ctx).First(&outbox, outboxID).Error; err != nil {
			return err
		}

		var msg store.ChannelMessage
		if err := tx.WithContext(ctx).First(&msg, outbox.MessageID).Error; err != nil {
			return err
		}

		now := time.Now()
		outboxUpdates := map[string]any{
			"updated_at":    now,
			"next_retry_at": nil,
		}
		msgStatus := store.ChannelMessageFailed
		if delivered {
			outboxUpdates["status"] = store.ChannelOutboxSent
			outboxUpdates["last_error"] = ""
			msgStatus = store.ChannelMessageSent
		} else {
			outboxUpdates["status"] = store.ChannelOutboxFailed
			outboxUpdates["last_error"] = errMsg
		}

		if err := tx.WithContext(ctx).Model(&store.ChannelOutbox{}).
			Where("id = ?", outbox.ID).
			Updates(outboxUpdates).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&store.ChannelMessage{}).
			Where("id = ?", msg.ID).
			Updates(map[string]any{
				"status": msgStatus,
			}).Error; err != nil {
			return err
		}
		if delivered {
			if err := tx.WithContext(ctx).Model(&store.ChannelConversation{}).
				Where("id = ?", msg.ConversationID).
				Updates(map[string]any{
					"last_message_at": &now,
					"updated_at":      now,
				}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

func (g *Gateway) Submit(ctx context.Context, req GatewayInboundRequest) error {
	if g == nil {
		return fmt.Errorf("gateway 为空")
	}
	if g.stopping.Load() {
		return ErrGatewayStopped
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	g.lifecycleMu.RLock()
	defer g.lifecycleMu.RUnlock()
	if g.stopping.Load() {
		return ErrGatewayStopped
	}

	item, err := g.normalizeInboundRequest(req)
	if err != nil {
		return err
	}
	ch, created, err := g.queue.GetOrCreate(item.ProjectName)
	if err != nil {
		if errors.Is(err, ErrInboundQueueClosed) {
			return ErrGatewayStopped
		}
		return err
	}
	if created {
		g.startProjectWorker(item.ProjectName, ch)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case ch <- item:
		return nil
	default:
		return ErrInboundQueueFull
	}
}

func (g *Gateway) normalizeInboundRequest(req GatewayInboundRequest) (InboundItem, error) {
	projectName := strings.TrimSpace(req.ProjectName)
	if projectName == "" {
		return InboundItem{}, fmt.Errorf("project name 不能为空")
	}
	env := req.Envelope
	env.Normalize()
	env.ChannelType = strings.ToLower(strings.TrimSpace(env.ChannelType))
	if env.ChannelType == "" {
		env.ChannelType = contracts.ChannelTypeCLI
	}
	if strings.TrimSpace(env.Adapter) == "" {
		env.Adapter = defaultAdapter(env.ChannelType)
	}
	if strings.TrimSpace(env.PeerConversationID) == "" {
		env.PeerConversationID = defaultConversationID(env.ChannelType)
	}
	if strings.TrimSpace(env.PeerMessageID) == "" {
		env.PeerMessageID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}
	if strings.TrimSpace(env.SenderID) == "" {
		env.SenderID = "anonymous"
	}
	env.Normalize()
	if err := env.Validate(); err != nil {
		return InboundItem{}, err
	}

	peerProjectKey := strings.TrimSpace(req.PeerProjectKey)
	if peerProjectKey == "" {
		peerProjectKey = defaultPeerProjectKey(projectName, env)
	}

	return InboundItem{
		ProjectName:    projectName,
		PeerProjectKey: peerProjectKey,
		Envelope:       env,
		Callback:       req.Callback,
	}, nil
}

func (g *Gateway) startProjectWorker(projectName string, ch chan InboundItem) {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" || ch == nil {
		return
	}

	g.workersMu.Lock()
	if _, ok := g.workers[projectName]; ok {
		g.workersMu.Unlock()
		return
	}
	g.workers[projectName] = ch
	g.workersMu.Unlock()

	g.workerWg.Add(1)
	go func() {
		defer func() {
			g.workersMu.Lock()
			delete(g.workers, projectName)
			g.workersMu.Unlock()
			g.workerWg.Done()
		}()
		for item := range ch {
			g.processInboundItem(item)
		}
	}()
}

func (g *Gateway) processInboundItem(item InboundItem) {
	ctx := context.Background()
	state, cached, err := g.persistInboundAccepted(ctx, item)
	if err != nil {
		g.callback(item, ProcessResult{}, err)
		g.publishError(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, err)
		return
	}
	if cached != nil {
		g.callback(item, *cached, nil)
		if isTurnTerminal(cached.JobStatus) {
			g.publishFromResult(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, *cached)
		} else {
			g.slog().Info("gateway dedup skip runtime",
				"project", strings.TrimSpace(item.ProjectName),
				"conversation", strings.TrimSpace(item.Envelope.PeerConversationID),
				"peer_msg", strings.TrimSpace(item.Envelope.PeerMessageID),
				"dedup_type", "peer_message_id",
				"job_id", cached.JobID,
				"status", strings.TrimSpace(string(cached.JobStatus)),
			)
		}
		return
	}

	projectCtx, err := g.resolver.Resolve(item.ProjectName)
	if err != nil {
		persisted, pErr := g.persistFailure(ctx, state, ProcessResult{}, fmt.Errorf("resolve project 失败: %w", err))
		if pErr == nil {
			g.callback(item, persisted, nil)
			g.publishFromResult(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, persisted)
			return
		}
		g.callback(item, ProcessResult{}, err)
		g.publishError(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, err)
		return
	}
	if projectCtx == nil || projectCtx.Runtime == nil {
		err := fmt.Errorf("project runtime 不可用: %s", item.ProjectName)
		persisted, pErr := g.persistFailure(ctx, state, ProcessResult{}, err)
		if pErr == nil {
			g.callback(item, persisted, nil)
			g.publishFromResult(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, persisted)
			return
		}
		g.callback(item, ProcessResult{}, err)
		g.publishError(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, err)
		return
	}

	timeout := projectCtx.Runtime.GatewayTurnTimeout()
	if timeout <= 0 {
		timeout = g.defaultTurnTimeout
	}
	turnCtx := context.Background()
	cancel := func() {}
	if timeout > 0 {
		turnCtx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		turnCtx, cancel = context.WithCancel(context.Background())
	}
	streamedAny := false
	turnCtx = withStreamEventEmitter(turnCtx, func(ev AgentEvent) {
		streamedAny = true
		g.publishStreamAgentEvent(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, ev)
	})
	result, runErr := projectCtx.Runtime.ProcessInbound(turnCtx, item.Envelope)
	cancel()
	if runErr != nil {
		persisted, pErr := g.persistFailure(ctx, state, result, runErr)
		if pErr != nil {
			g.callback(item, ProcessResult{}, fmt.Errorf("gateway 失败落盘失败: %w", pErr))
			g.publishError(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, pErr)
			return
		}
		g.callback(item, persisted, nil)
		if !streamedAny {
			g.publishFromResult(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, persisted)
		} else {
			g.publishFinalFromResult(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, persisted)
		}
		return
	}

	persisted, err := g.persistSuccess(ctx, state, result, item.Envelope)
	if err != nil {
		g.callback(item, ProcessResult{}, err)
		g.publishError(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, err)
		return
	}
	g.callback(item, persisted, nil)
	if !streamedAny {
		g.publishFromResult(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, persisted)
	} else {
		g.publishFinalFromResult(item.ProjectName, item.Envelope.PeerConversationID, item.Envelope.PeerMessageID, persisted)
	}
}

func (g *Gateway) callback(item InboundItem, result ProcessResult, err error) {
	if item.Callback == nil {
		return
	}
	item.Callback(result, err)
}

func (g *Gateway) persistInboundAccepted(ctx context.Context, item InboundItem) (gatewayPersistState, *ProcessResult, error) {
	var state gatewayPersistState
	var cached *ProcessResult
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		binding, err := ensureGatewayBindingTx(ctx, tx, item.ProjectName, item.PeerProjectKey, item.Envelope)
		if err != nil {
			return err
		}
		state.binding = binding

		conv, err := ensureGatewayConversationTx(ctx, tx, binding.ID, item.Envelope.PeerConversationID)
		if err != nil {
			return err
		}
		state.conv = conv

		var inbound store.ChannelMessage
		err = tx.WithContext(ctx).
			Where("direction = ? AND conversation_id = ? AND adapter = ? AND peer_message_id = ?",
				store.ChannelMessageIn,
				conv.ID,
				strings.TrimSpace(item.Envelope.Adapter),
				strings.TrimSpace(item.Envelope.PeerMessageID)).
			First(&inbound).Error
		if err == nil {
			state.inbound = inbound
			var existingJob store.ChannelTurnJob
			jerr := tx.WithContext(ctx).Where("inbound_message_id = ?", inbound.ID).First(&existingJob).Error
			if jerr == nil {
				state.job = existingJob
				res := decodeGatewayTurnResult(existingJob)
				res.BindingID = binding.ID
				res.ConversationID = conv.ID
				res.InboundMessageID = inbound.ID
				cached = &res
				g.slog().Info("gateway dedup hit",
					"dedup_type", "peer_message_id",
					"dedup_key", strings.TrimSpace(item.Envelope.PeerMessageID),
					"inbound_id", inbound.ID,
					"job_id", existingJob.ID,
					"status", strings.TrimSpace(string(existingJob.Status)),
					"action", "skip",
				)
				if isTurnTerminal(existingJob.Status) {
					return nil
				}
				return nil
			}
			if errors.Is(jerr, gorm.ErrRecordNotFound) {
				job := store.ChannelTurnJob{
					ConversationID:   inbound.ConversationID,
					InboundMessageID: inbound.ID,
					Status:           store.ChannelTurnPending,
				}
				if jerr := tx.WithContext(ctx).Create(&job).Error; jerr != nil {
					return jerr
				}
				state.job = job
				return nil
			}
			return jerr
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		payload := map[string]any{
			"schema":      item.Envelope.Schema,
			"attachments": item.Envelope.Attachments,
			"received_at": item.Envelope.ReceivedAt,
			"project":     item.ProjectName,
		}
		peerID := strings.TrimSpace(item.Envelope.PeerMessageID)
		inbound = store.ChannelMessage{
			ConversationID: conv.ID,
			Direction:      store.ChannelMessageIn,
			Adapter:        strings.TrimSpace(item.Envelope.Adapter),
			PeerMessageID:  &peerID,
			SenderID:       strings.TrimSpace(item.Envelope.SenderID),
			ContentText:    strings.TrimSpace(item.Envelope.Text),
			PayloadJSON:    mustJSON(payload),
			Status:         store.ChannelMessageAccepted,
		}
		if err := tx.WithContext(ctx).Create(&inbound).Error; err != nil {
			return err
		}
		state.inbound = inbound

		now := time.Now()
		if err := tx.WithContext(ctx).Model(&store.ChannelConversation{}).
			Where("id = ?", conv.ID).
			Updates(map[string]any{"last_message_at": &now, "updated_at": now}).Error; err != nil {
			return err
		}

		job := store.ChannelTurnJob{
			ConversationID:   conv.ID,
			InboundMessageID: inbound.ID,
			Status:           store.ChannelTurnPending,
		}
		if err := tx.WithContext(ctx).Create(&job).Error; err != nil {
			return err
		}
		state.job = job
		return nil
	})
	if err != nil {
		return gatewayPersistState{}, nil, err
	}
	return state, cached, nil
}

func (g *Gateway) persistSuccess(ctx context.Context, state gatewayPersistState, result ProcessResult, env contracts.InboundEnvelope) (ProcessResult, error) {
	return g.persistTurnResult(ctx, state, result, env, nil)
}

func (g *Gateway) persistFailure(ctx context.Context, state gatewayPersistState, result ProcessResult, runErr error) (ProcessResult, error) {
	if runErr == nil {
		runErr = errors.New("unknown gateway error")
	}
	env := contracts.InboundEnvelope{
		Adapter:            state.binding.Adapter,
		PeerConversationID: state.conv.PeerConversationID,
	}
	return g.persistTurnResult(ctx, state, result, env, runErr)
}

func (g *Gateway) persistTurnResult(ctx context.Context, state gatewayPersistState, result ProcessResult, env contracts.InboundEnvelope, runErr error) (ProcessResult, error) {
	if state.job.ID == 0 || state.inbound.ID == 0 || state.conv.ID == 0 || state.binding.ID == 0 {
		return ProcessResult{}, fmt.Errorf("gateway 落盘状态不完整")
	}
	status := result.JobStatus
	if status == "" {
		if runErr != nil {
			status = store.ChannelTurnFailed
		} else {
			status = store.ChannelTurnSucceeded
		}
	}
	if runErr != nil {
		status = store.ChannelTurnFailed
	}
	jobErr := strings.TrimSpace(result.JobError)
	if runErr != nil {
		jobErr = strings.TrimSpace(runErr.Error())
	}
	jobErrType := strings.TrimSpace(result.JobErrorType)
	if jobErrType == "" {
		jobErrType = classifyJobErrorType(jobErr)
	}
	if status == store.ChannelTurnSucceeded {
		jobErr = ""
		jobErrType = ""
	}

	payload := gatewayTurnResult{
		Schema:           gatewayTurnResultSchemaV1,
		BindingID:        state.binding.ID,
		ConversationID:   state.conv.ID,
		InboundMessageID: state.inbound.ID,
		RunID:            strings.TrimSpace(result.RunID),
		AgentReplyText:   strings.TrimSpace(result.ReplyText),
		AgentProvider:    strings.TrimSpace(result.AgentProvider),
		AgentModel:       strings.TrimSpace(result.AgentModel),
		AgentOutputMode:  strings.TrimSpace(result.AgentOutputMode),
		AgentCommand:     strings.TrimSpace(result.AgentCommand),
		AgentStdout:      strings.TrimSpace(result.AgentStdout),
		AgentStderr:      strings.TrimSpace(result.AgentStderr),
		AgentEvents:      copyAgentEvents(result.AgentEvents),
		PendingActions:   copyPendingActionViews(result.PendingActions),
		JobStatus:        strings.TrimSpace(string(status)),
		JobError:         jobErr,
		JobErrorType:     jobErrType,
	}

	persisted := ProcessResult{
		BindingID:        state.binding.ID,
		ConversationID:   state.conv.ID,
		InboundMessageID: state.inbound.ID,
		JobID:            state.job.ID,
		RunID:            payload.RunID,
		JobStatus:        status,
		JobError:         jobErr,
		JobErrorType:     jobErrType,
		ReplyText:        payload.AgentReplyText,
		AgentProvider:    payload.AgentProvider,
		AgentModel:       payload.AgentModel,
		AgentOutputMode:  payload.AgentOutputMode,
		AgentCommand:     payload.AgentCommand,
		AgentStdout:      payload.AgentStdout,
		AgentStderr:      payload.AgentStderr,
		AgentEvents:      copyAgentEvents(payload.AgentEvents),
		PendingActions:   copyPendingActionViews(payload.PendingActions),
	}

	payloadJSON := mustJSON(payload)

	if err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		if err := tx.Model(&store.ChannelMessage{}).
			Where("id = ?", state.inbound.ID).
			Updates(map[string]any{"status": inboundMessageStatusFromTurn(status), "payload_json": payloadJSON}).Error; err != nil {
			return err
		}

		convUpdates := map[string]any{
			"updated_at": now,
		}
		if status == store.ChannelTurnSucceeded {
			convUpdates["last_message_at"] = &now
		}
		if err := tx.Model(&store.ChannelConversation{}).
			Where("id = ?", state.conv.ID).
			Updates(convUpdates).Error; err != nil {
			return err
		}

		if strings.TrimSpace(payload.AgentReplyText) != "" {
			outPeerID := fmt.Sprintf("gateway_out_job_%d", state.job.ID)
			outbound := store.ChannelMessage{
				ConversationID: state.conv.ID,
				Direction:      store.ChannelMessageOut,
				Adapter:        strings.TrimSpace(env.Adapter),
				PeerMessageID:  &outPeerID,
				SenderID:       "pm",
				ContentText:    strings.TrimSpace(payload.AgentReplyText),
				PayloadJSON:    payloadJSON,
				Status:         outboundMessageStatusFromTurn(status),
			}
			if err := tx.Create(&outbound).Error; err != nil {
				return err
			}
			persisted.OutboundMessageID = outbound.ID

			outbox := store.ChannelOutbox{
				MessageID:   outbound.ID,
				Adapter:     strings.TrimSpace(env.Adapter),
				PayloadJSON: payloadJSON,
				Status:      outboxStatusFromTurn(status),
				RetryCount:  1,
				LastError:   "",
			}
			if status != store.ChannelTurnSucceeded {
				outbox.LastError = jobErr
			}
			if err := tx.Create(&outbox).Error; err != nil {
				return err
			}
			persisted.OutboxID = outbox.ID
		}

		payload.OutboundMessageID = persisted.OutboundMessageID
		payload.OutboxID = persisted.OutboxID
		payloadJSON = mustJSON(payload)
		persisted.ReplyText = strings.TrimSpace(payload.AgentReplyText)

		updates := map[string]any{
			"status":           status,
			"result_json":      payloadJSON,
			"error":            jobErr,
			"runner_id":        "gateway",
			"lease_expires_at": nil,
			"finished_at":      &now,
			"updated_at":       now,
		}
		if status == store.ChannelTurnSucceeded {
			updates["error"] = ""
		}
		return tx.Model(&store.ChannelTurnJob{}).
			Where("id = ?", state.job.ID).
			Updates(updates).Error
	}); err != nil {
		return ProcessResult{}, err
	}
	return persisted, nil
}

func ensureGatewayBindingTx(ctx context.Context, tx *gorm.DB, projectName, peerProjectKey string, env contracts.InboundEnvelope) (store.ChannelBinding, error) {
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return store.ChannelBinding{}, fmt.Errorf("project name 不能为空")
	}
	channelType := toStoreChannelType(env.ChannelType)
	adapter := strings.TrimSpace(env.Adapter)
	peerProjectKey = strings.TrimSpace(peerProjectKey)

	var binding store.ChannelBinding
	err := tx.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ?", channelType, adapter, peerProjectKey).
		First(&binding).Error
	if err == nil {
		updates := map[string]any{}
		if strings.TrimSpace(binding.ProjectName) != projectName {
			updates["project_name"] = projectName
		}
		if !binding.Enabled {
			updates["enabled"] = true
		}
		if len(updates) > 0 {
			if uErr := tx.WithContext(ctx).Model(&store.ChannelBinding{}).
				Where("id = ?", binding.ID).
				Updates(updates).Error; uErr != nil {
				return store.ChannelBinding{}, uErr
			}
			if updates["project_name"] != nil {
				binding.ProjectName = projectName
			}
			if updates["enabled"] != nil {
				binding.Enabled = true
			}
		}
		return binding, nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return store.ChannelBinding{}, err
	}

	binding = store.ChannelBinding{
		ProjectName:    projectName,
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

func ensureGatewayConversationTx(ctx context.Context, tx *gorm.DB, bindingID uint, peerConversationID string) (store.ChannelConversation, error) {
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
	}
	if err := tx.WithContext(ctx).Create(&conv).Error; err != nil {
		return store.ChannelConversation{}, err
	}
	return conv, nil
}

func decodeGatewayTurnResult(job store.ChannelTurnJob) ProcessResult {
	res := ProcessResult{
		JobID:        job.ID,
		JobStatus:    job.Status,
		JobError:     strings.TrimSpace(job.Error),
		JobErrorType: classifyJobErrorType(strings.TrimSpace(job.Error)),
	}
	raw := strings.TrimSpace(job.ResultJSON)
	if raw == "" {
		return res
	}
	var payload gatewayTurnResult
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return res
	}
	res.BindingID = payload.BindingID
	res.ConversationID = payload.ConversationID
	res.InboundMessageID = payload.InboundMessageID
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
	if st := strings.TrimSpace(payload.JobStatus); st != "" {
		res.JobStatus = store.ChannelTurnJobStatus(st)
	}
	if msg := strings.TrimSpace(payload.JobError); msg != "" {
		res.JobError = msg
	}
	if jt := strings.TrimSpace(payload.JobErrorType); jt != "" {
		res.JobErrorType = jt
	}
	if res.JobErrorType == "" {
		res.JobErrorType = classifyJobErrorType(res.JobError)
	}
	return res
}

func (g *Gateway) publishError(projectName, conversationID, peerMessageID string, err error) {
	if g == nil || g.bus == nil || err == nil {
		return
	}
	msg := strings.TrimSpace(err.Error())
	g.bus.Publish(GatewayEvent{
		ProjectName:    strings.TrimSpace(projectName),
		ConversationID: strings.TrimSpace(conversationID),
		PeerMessageID:  strings.TrimSpace(peerMessageID),
		Type:           "error",
		Text:           msg,
		EventType:      "error",
		Stream:         "lifecycle",
		JobStatus:      store.ChannelTurnFailed,
		JobError:       msg,
		JobErrorType:   classifyJobErrorType(msg),
		At:             time.Now(),
	})
}

func (g *Gateway) publishStreamAgentEvent(projectName, conversationID, peerMessageID string, ev AgentEvent) {
	if g == nil || g.bus == nil {
		return
	}
	projectName = strings.TrimSpace(projectName)
	conversationID = strings.TrimSpace(conversationID)
	peerMessageID = strings.TrimSpace(peerMessageID)
	runID := strings.TrimSpace(ev.RunID)
	stream := strings.TrimSpace(string(ev.Stream))
	eventType := deriveGatewayRuntimeEventType(stream, strings.TrimSpace(ev.Data.Phase))
	text := strings.TrimSpace(ev.Data.Text)
	if text == "" {
		text = strings.TrimSpace(ev.Data.Error)
	}
	if stream == "" && eventType == "" && text == "" {
		return
	}
	g.bus.Publish(GatewayEvent{
		ProjectName:    projectName,
		ConversationID: conversationID,
		PeerMessageID:  peerMessageID,
		Type:           "assistant_event",
		RunID:          runID,
		Seq:            ev.Seq,
		Stream:         stream,
		Text:           text,
		EventType:      eventType,
		JobStatus:      store.ChannelTurnRunning,
		At:             time.Now(),
	})
}

func (g *Gateway) publishFinalFromResult(projectName, conversationID, peerMessageID string, result ProcessResult) {
	if g == nil || g.bus == nil {
		return
	}
	reply := strings.TrimSpace(result.ReplyText)
	if reply == "" && result.JobStatus != store.ChannelTurnSucceeded {
		reply = strings.TrimSpace(result.JobError)
	}
	finalRunID := strings.TrimSpace(result.RunID)
	finalSeq := 1
	for _, ev := range result.AgentEvents {
		if ev.Seq >= finalSeq {
			finalSeq = ev.Seq + 1
		}
		if finalRunID == "" && strings.TrimSpace(ev.RunID) != "" {
			finalRunID = strings.TrimSpace(ev.RunID)
		}
	}
	finalEventType := "end"
	if result.JobStatus != store.ChannelTurnSucceeded {
		finalEventType = "error"
	}
	g.bus.Publish(GatewayEvent{
		ProjectName:    strings.TrimSpace(projectName),
		ConversationID: strings.TrimSpace(conversationID),
		PeerMessageID:  strings.TrimSpace(peerMessageID),
		Type:           "assistant_message",
		RunID:          finalRunID,
		Seq:            finalSeq,
		Stream:         "lifecycle",
		Text:           reply,
		EventType:      finalEventType,
		AgentProvider:  strings.TrimSpace(result.AgentProvider),
		AgentModel:     strings.TrimSpace(result.AgentModel),
		JobStatus:      result.JobStatus,
		JobErrorType:   strings.TrimSpace(result.JobErrorType),
		JobError:       strings.TrimSpace(result.JobError),
		At:             time.Now(),
	})
}

func (g *Gateway) publishFromResult(projectName, conversationID, peerMessageID string, result ProcessResult) {
	if g == nil || g.bus == nil {
		return
	}
	projectName = strings.TrimSpace(projectName)
	conversationID = strings.TrimSpace(conversationID)
	peerMessageID = strings.TrimSpace(peerMessageID)
	reply := strings.TrimSpace(result.ReplyText)
	if reply == "" && result.JobStatus != store.ChannelTurnSucceeded {
		reply = strings.TrimSpace(result.JobError)
	}

	finalRunID := strings.TrimSpace(result.RunID)
	lastSeq := 0
	finalSeq := 0
	finalEventType := "end"
	for _, ev := range result.AgentEvents {
		runID := strings.TrimSpace(ev.RunID)
		if runID == "" {
			runID = finalRunID
		}
		if finalRunID == "" && runID != "" {
			finalRunID = runID
		}
		stream := strings.TrimSpace(string(ev.Stream))
		eventType := deriveGatewayRuntimeEventType(stream, strings.TrimSpace(ev.Data.Phase))
		text := strings.TrimSpace(ev.Data.Text)
		if ev.Seq > lastSeq {
			lastSeq = ev.Seq
		}
		if stream == "lifecycle" && (eventType == "end" || eventType == "error") {
			finalSeq = ev.Seq
			finalEventType = eventType
			continue
		}
		if stream == "" && eventType == "" && text == "" {
			continue
		}
		if text != "" && text == reply {
			continue
		}
		g.bus.Publish(GatewayEvent{
			ProjectName:    projectName,
			ConversationID: conversationID,
			PeerMessageID:  peerMessageID,
			Type:           "assistant_event",
			RunID:          runID,
			Seq:            ev.Seq,
			Stream:         stream,
			Text:           text,
			EventType:      eventType,
			AgentProvider:  strings.TrimSpace(result.AgentProvider),
			AgentModel:     strings.TrimSpace(result.AgentModel),
			JobStatus:      result.JobStatus,
			JobErrorType:   strings.TrimSpace(result.JobErrorType),
			JobError:       strings.TrimSpace(result.JobError),
			At:             time.Now(),
		})
	}
	if finalSeq <= 0 {
		finalSeq = lastSeq + 1
	}
	if result.JobStatus != store.ChannelTurnSucceeded {
		finalEventType = "error"
	}

	g.bus.Publish(GatewayEvent{
		ProjectName:    projectName,
		ConversationID: conversationID,
		PeerMessageID:  peerMessageID,
		Type:           "assistant_message",
		RunID:          finalRunID,
		Seq:            finalSeq,
		Stream:         "lifecycle",
		Text:           reply,
		EventType:      finalEventType,
		AgentProvider:  strings.TrimSpace(result.AgentProvider),
		AgentModel:     strings.TrimSpace(result.AgentModel),
		JobStatus:      result.JobStatus,
		JobErrorType:   strings.TrimSpace(result.JobErrorType),
		JobError:       strings.TrimSpace(result.JobError),
		At:             time.Now(),
	})
}

func toStoreChannelType(channelType string) store.ChannelType {
	switch strings.ToLower(strings.TrimSpace(channelType)) {
	case contracts.ChannelTypeWeb:
		return store.ChannelWeb
	case contracts.ChannelTypeIM:
		return store.ChannelIM
	case contracts.ChannelTypeAPI:
		return store.ChannelAPI
	case contracts.ChannelTypeCLI:
		fallthrough
	default:
		return store.ChannelCLI
	}
}

func defaultPeerProjectKey(projectName string, env contracts.InboundEnvelope) string {
	if strings.EqualFold(strings.TrimSpace(env.ChannelType), contracts.ChannelTypeIM) {
		if strings.TrimSpace(env.PeerConversationID) != "" {
			return strings.TrimSpace(env.PeerConversationID)
		}
	}
	return strings.TrimSpace(projectName)
}

func inboundMessageStatusFromTurn(st store.ChannelTurnJobStatus) store.ChannelMessageStatus {
	if st == store.ChannelTurnSucceeded {
		return store.ChannelMessageProcessed
	}
	return store.ChannelMessageFailed
}

func outboundMessageStatusFromTurn(st store.ChannelTurnJobStatus) store.ChannelMessageStatus {
	if st == store.ChannelTurnSucceeded {
		return store.ChannelMessageSent
	}
	return store.ChannelMessageFailed
}

func outboxStatusFromTurn(st store.ChannelTurnJobStatus) store.ChannelOutboxStatus {
	if st == store.ChannelTurnSucceeded {
		return store.ChannelOutboxSent
	}
	return store.ChannelOutboxFailed
}

func deriveGatewayRuntimeEventType(stream, phase string) string {
	stream = strings.TrimSpace(stream)
	phase = strings.TrimSpace(phase)
	if stream == "lifecycle" {
		if phase != "" {
			return phase
		}
		return "lifecycle"
	}
	if stream == "assistant" {
		return "assistant"
	}
	if stream == "error" {
		return "error"
	}
	if stream == "tool" {
		return "tool"
	}
	return phase
}

func (g *Gateway) InterruptBoundConversation(ctx context.Context, channelType, adapter, peerProjectKey, peerConversationID string) (string, InterruptResult, error) {
	if g == nil || g.db == nil {
		return "", InterruptResult{}, fmt.Errorf("gateway db 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	channelType = strings.TrimSpace(strings.ToLower(channelType))
	if channelType == "" {
		channelType = contracts.ChannelTypeCLI
	}
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = defaultAdapter(channelType)
	}
	peerProjectKey = strings.TrimSpace(peerProjectKey)
	peerConversationID = strings.TrimSpace(peerConversationID)
	if peerConversationID == "" {
		peerConversationID = peerProjectKey
	}
	g.logInterrupt("cmd_received",
		"channel_type", channelType,
		"adapter", adapter,
		"peer_project_key", peerProjectKey,
		"peer_conversation_id", peerConversationID,
	)
	if peerProjectKey == "" || peerConversationID == "" {
		return "", InterruptResult{}, fmt.Errorf("peer project key / conversation id 不能为空")
	}

	projectName, err := g.LookupBoundProject(ctx, channelType, adapter, peerProjectKey)
	if err != nil {
		g.logInterrupt("locator_error",
			"channel_type", channelType,
			"adapter", adapter,
			"peer_project_key", peerProjectKey,
			"error", err,
		)
		return "", InterruptResult{}, err
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		g.logInterrupt("locator_result",
			"channel_type", channelType,
			"adapter", adapter,
			"peer_project_key", peerProjectKey,
			"locator", "miss",
		)
		return "", InterruptResult{}, nil
	}
	g.logInterrupt("locator_result",
		"channel_type", channelType,
		"adapter", adapter,
		"peer_project_key", peerProjectKey,
		"locator", "hit",
		"project", projectName,
	)
	projectCtx, err := g.resolver.Resolve(projectName)
	if err != nil {
		return projectName, InterruptResult{}, err
	}
	if projectCtx == nil || projectCtx.Runtime == nil {
		return projectName, InterruptResult{}, fmt.Errorf("project runtime 不可用: %s", projectName)
	}
	interrupter, ok := projectCtx.Runtime.(ProjectRuntimeInterrupter)
	if !ok || interrupter == nil {
		result := InterruptResult{Status: InterruptStatusMiss}
		g.logInterrupt("runner_result",
			"project", projectName,
			"status", result.Status,
			"runner_hit", result.RunnerInterrupted,
			"context_canceled", result.ContextCanceled,
			"runner_error", result.RunnerErrorMessage(),
		)
		return projectName, result, nil
	}
	result, err := interrupter.InterruptConversation(ctx, channelType, adapter, peerConversationID)
	if err != nil {
		g.logInterrupt("runner_error",
			"project", projectName,
			"error", err,
		)
		return projectName, result, err
	}
	g.logInterrupt("runner_result",
		"project", projectName,
		"status", result.Status,
		"runner_hit", result.RunnerInterrupted,
		"context_canceled", result.ContextCanceled,
		"runner_error", result.RunnerErrorMessage(),
	)
	return projectName, result, nil
}

func (g *Gateway) ResetBoundConversationSession(ctx context.Context, channelType, adapter, peerProjectKey, peerConversationID string) (string, bool, error) {
	if g == nil || g.db == nil {
		return "", false, fmt.Errorf("gateway db 为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	channelType = strings.TrimSpace(strings.ToLower(channelType))
	if channelType == "" {
		channelType = contracts.ChannelTypeCLI
	}
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = defaultAdapter(channelType)
	}
	peerProjectKey = strings.TrimSpace(peerProjectKey)
	peerConversationID = strings.TrimSpace(peerConversationID)
	if peerConversationID == "" {
		peerConversationID = peerProjectKey
	}
	if peerProjectKey == "" || peerConversationID == "" {
		return "", false, fmt.Errorf("peer project key / conversation id 不能为空")
	}

	projectName, err := g.LookupBoundProject(ctx, channelType, adapter, peerProjectKey)
	if err != nil {
		return "", false, err
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return "", false, nil
	}
	projectCtx, err := g.resolver.Resolve(projectName)
	if err != nil {
		return projectName, false, err
	}
	if projectCtx == nil || projectCtx.Runtime == nil {
		return projectName, false, fmt.Errorf("project runtime 不可用: %s", projectName)
	}
	resetter, ok := projectCtx.Runtime.(ProjectRuntimeSessionResetter)
	if !ok || resetter == nil {
		return projectName, false, nil
	}
	reset, err := resetter.ResetConversationSession(ctx, channelType, adapter, peerConversationID)
	if err != nil {
		return projectName, reset, err
	}
	return projectName, reset, nil
}

func (g *Gateway) LookupBoundProject(ctx context.Context, channelType, adapter, peerProjectKey string) (string, error) {
	if g == nil || g.db == nil {
		return "", fmt.Errorf("gateway db 为空")
	}
	var binding store.ChannelBinding
	err := g.db.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ? AND enabled = 1",
			toStoreChannelType(channelType),
			strings.TrimSpace(adapter),
			strings.TrimSpace(peerProjectKey)).
		First(&binding).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimSpace(binding.ProjectName), nil
}

func (g *Gateway) BindProject(ctx context.Context, channelType, adapter, peerProjectKey, projectName string) (string, error) {
	if g == nil || g.db == nil {
		return "", fmt.Errorf("gateway db 为空")
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return "", fmt.Errorf("project name 不能为空")
	}
	adapter = strings.TrimSpace(adapter)
	peerProjectKey = strings.TrimSpace(peerProjectKey)
	if adapter == "" || peerProjectKey == "" {
		return "", fmt.Errorf("adapter/chat_id 不能为空")
	}
	var prevProject string
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var binding store.ChannelBinding
		err := tx.WithContext(ctx).
			Where("channel_type = ? AND adapter = ? AND peer_project_key = ?",
				toStoreChannelType(channelType),
				adapter,
				peerProjectKey).
			First(&binding).Error
		if err == nil {
			prevProject = strings.TrimSpace(binding.ProjectName)
			return tx.WithContext(ctx).Model(&store.ChannelBinding{}).
				Where("id = ?", binding.ID).
				Updates(map[string]any{
					"project_name": projectName,
					"enabled":      true,
				}).Error
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		binding = store.ChannelBinding{
			ProjectName:    projectName,
			ChannelType:    toStoreChannelType(channelType),
			Adapter:        adapter,
			PeerProjectKey: peerProjectKey,
			RolePolicyJSON: "{}",
			Enabled:        true,
		}
		return tx.WithContext(ctx).Create(&binding).Error
	})
	if err != nil {
		return "", err
	}
	return prevProject, nil
}

func (g *Gateway) UnbindProject(ctx context.Context, channelType, adapter, peerProjectKey string) (bool, error) {
	if g == nil || g.db == nil {
		return false, fmt.Errorf("gateway db 为空")
	}
	res := g.db.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ?",
			toStoreChannelType(channelType),
			strings.TrimSpace(adapter),
			strings.TrimSpace(peerProjectKey)).
		Delete(&store.ChannelBinding{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
