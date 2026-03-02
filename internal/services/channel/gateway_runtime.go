package channel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dalek/internal/contracts"
	"dalek/internal/services/core"

	"gorm.io/gorm"
)

var ErrGatewayStopped = errors.New("gateway stopped")

const channelBindingPolicyQuietModeKey = "quiet_mode"

type ProjectRuntime interface {
	ProcessInbound(ctx context.Context, env contracts.InboundEnvelope) (ProcessResult, error)
	GatewayTurnTimeout() time.Duration
}

type ProjectRuntimeInterrupter interface {
	InterruptConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (InterruptResult, error)
}

type ProjectRuntimeSessionResetter interface {
	ResetConversationSession(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (bool, error)
}

type ProjectRuntimeHardResetter interface {
	HardResetConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerConversationID string) (bool, error)
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

	activeTurnsMu sync.Mutex
	activeTurns   map[string]gatewayActiveTurn
	activeTurnSeq atomic.Uint64
}

type gatewayPersistState struct {
	binding contracts.ChannelBinding
	conv    contracts.ChannelConversation
	inbound contracts.ChannelMessage
	job     contracts.ChannelTurnJob
}

type gatewayActiveTurn struct {
	id                 uint64
	peerConversationID string
	cancel             context.CancelFunc
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
		activeTurns:        map[string]gatewayActiveTurn{},
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
		"phase", phase,
	}
	all = append(all, attrs...)
	g.slog().Info("channel interrupt", all...)
}

func (g *Gateway) logReset(phase string, attrs ...any) {
	all := []any{
		"cmd", "reset",
		"phase", phase,
	}
	all = append(all, attrs...)
	g.slog().Info("channel reset", all...)
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
		return fmt.Errorf("context 不能为空")
	}

	g.stopOnce.Do(func() {
		g.stopping.Store(true)

		g.activeTurnsMu.Lock()
		cancels := make([]context.CancelFunc, 0, len(g.activeTurns))
		for key, item := range g.activeTurns {
			if item.cancel != nil {
				cancels = append(cancels, item.cancel)
			}
			delete(g.activeTurns, key)
		}
		g.activeTurnsMu.Unlock()
		for _, cancel := range cancels {
			cancel()
		}

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
		return fmt.Errorf("context 不能为空")
	}

	errMsg := fmt.Sprint(cause)
	if delivered {
		errMsg = ""
	} else if errMsg == "" || errMsg == "<nil>" {
		errMsg = "gateway delivery failed"
	}

	return g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var outbox contracts.ChannelOutbox
		if err := tx.WithContext(ctx).First(&outbox, outboxID).Error; err != nil {
			return err
		}

		var msg contracts.ChannelMessage
		if err := tx.WithContext(ctx).First(&msg, outbox.MessageID).Error; err != nil {
			return err
		}

		now := time.Now()
		outboxUpdates := map[string]any{
			"updated_at":    now,
			"next_retry_at": nil,
		}
		msgStatus := contracts.ChannelMessageFailed
		if delivered {
			outboxUpdates["status"] = contracts.ChannelOutboxSent
			outboxUpdates["last_error"] = ""
			msgStatus = contracts.ChannelMessageSent
		} else {
			outboxUpdates["status"] = contracts.ChannelOutboxFailed
			outboxUpdates["last_error"] = errMsg
		}

		if err := tx.WithContext(ctx).Model(&contracts.ChannelOutbox{}).
			Where("id = ?", outbox.ID).
			Updates(outboxUpdates).Error; err != nil {
			return err
		}
		if err := tx.WithContext(ctx).Model(&contracts.ChannelMessage{}).
			Where("id = ?", msg.ID).
			Updates(map[string]any{
				"status": msgStatus,
			}).Error; err != nil {
			return err
		}
		if delivered {
			if err := tx.WithContext(ctx).Model(&contracts.ChannelConversation{}).
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
		return fmt.Errorf("context 不能为空")
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
		env.PeerMessageID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}
	if env.SenderID == "" {
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
	if projectName == "" || ch == nil {
		return
	}

	g.workersMu.Lock()
	if existing, ok := g.workers[projectName]; ok && existing == ch {
		g.workersMu.Unlock()
		return
	}
	g.workers[projectName] = ch
	g.workersMu.Unlock()

	g.workerWg.Add(1)
	go func() {
		defer func() {
			g.workersMu.Lock()
			if current, ok := g.workers[projectName]; ok && current == ch {
				delete(g.workers, projectName)
			}
			g.workersMu.Unlock()
			g.workerWg.Done()
		}()
		for item := range ch {
			g.processInboundItem(item)
		}
	}()
}

func (g *Gateway) processInboundItem(item InboundItem) {
	// worker 为独立生命周期 goroutine，无上层请求上下文可继承。
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
				"project", item.ProjectName,
				"conversation", item.Envelope.PeerConversationID,
				"peer_msg", item.Envelope.PeerMessageID,
				"dedup_type", "peer_message_id",
				"job_id", cached.JobID,
				"status", string(cached.JobStatus),
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
	turnCtx := ctx
	cancel := func() {}
	if timeout > 0 {
		turnCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		turnCtx, cancel = context.WithCancel(ctx)
	}
	turnID := g.registerActiveTurn(item.ProjectName, item.Envelope.PeerConversationID, cancel)
	defer g.clearActiveTurn(item.ProjectName, turnID)
	var streamedAny atomic.Bool
	turnCtx = withStreamEventEmitter(turnCtx, func(ev AgentEvent) {
		streamedAny.Store(true)
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
		if !streamedAny.Load() {
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
	if !streamedAny.Load() {
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

func (g *Gateway) registerActiveTurn(projectName, peerConversationID string, cancel context.CancelFunc) uint64 {
	if g == nil || cancel == nil {
		return 0
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return 0
	}
	id := g.activeTurnSeq.Add(1)
	g.activeTurnsMu.Lock()
	g.activeTurns[projectName] = gatewayActiveTurn{
		id:                 id,
		peerConversationID: strings.TrimSpace(peerConversationID),
		cancel:             cancel,
	}
	g.activeTurnsMu.Unlock()
	return id
}

func (g *Gateway) clearActiveTurn(projectName string, id uint64) {
	if g == nil || id == 0 {
		return
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return
	}
	g.activeTurnsMu.Lock()
	current, ok := g.activeTurns[projectName]
	if ok && current.id == id {
		delete(g.activeTurns, projectName)
	}
	g.activeTurnsMu.Unlock()
}

func (g *Gateway) cancelActiveTurn(projectName, peerConversationID string, allowProjectFallback bool) bool {
	if g == nil {
		return false
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return false
	}
	peerConversationID = strings.TrimSpace(peerConversationID)

	g.activeTurnsMu.Lock()
	current, ok := g.activeTurns[projectName]
	if !ok || current.cancel == nil {
		g.activeTurnsMu.Unlock()
		return false
	}
	if peerConversationID != "" && current.peerConversationID != "" && current.peerConversationID != peerConversationID && !allowProjectFallback {
		g.activeTurnsMu.Unlock()
		return false
	}
	cancel := current.cancel
	g.activeTurnsMu.Unlock()
	cancel()
	return true
}

func (g *Gateway) replaceProjectWorker(projectName string) error {
	if g == nil || g.queue == nil {
		return fmt.Errorf("gateway queue 不可用")
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return fmt.Errorf("project name 不能为空")
	}
	oldCh, newCh, err := g.queue.Replace(projectName)
	if err != nil {
		if errors.Is(err, ErrInboundQueueClosed) {
			return ErrGatewayStopped
		}
		return err
	}
	g.startProjectWorker(projectName, newCh)
	if oldCh != nil {
		close(oldCh)
	}
	return nil
}

func (g *Gateway) persistInboundAccepted(ctx context.Context, item InboundItem) (gatewayPersistState, *ProcessResult, error) {
	var state gatewayPersistState
	var cached *ProcessResult
	err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		binding, err := EnsureBindingTx(ctx, tx, EnsureBindingParams{
			ProjectName:    item.ProjectName,
			PeerProjectKey: item.PeerProjectKey,
			Env:            item.Envelope,
			AutoUpdate:     true,
		})
		if err != nil {
			return err
		}
		state.binding = binding

		conv, err := EnsureConversationTx(ctx, tx, binding.ID, item.Envelope.PeerConversationID)
		if err != nil {
			return err
		}
		state.conv = conv

		inbound, job, duplicate, err := PersistInboundMessageTx(ctx, tx, PersistInboundParams{
			Conv:    conv,
			Env:     item.Envelope,
			Project: item.ProjectName,
		})
		if err != nil {
			return err
		}
		state.inbound = inbound
		state.job = job
		if duplicate {
			res := decodeTurnResult(job)
			if res.BindingID == 0 {
				res.BindingID = binding.ID
			}
			if res.ConversationID == 0 {
				res.ConversationID = conv.ID
			}
			if res.InboundMessageID == 0 {
				res.InboundMessageID = inbound.ID
			}
			cached = &res
			g.slog().Info("gateway dedup hit",
				"dedup_type", "peer_message_id",
				"dedup_key", item.Envelope.PeerMessageID,
				"inbound_id", inbound.ID,
				"job_id", job.ID,
				"status", string(job.Status),
				"action", "skip",
			)
		}
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
	adapter := env.Adapter
	if adapter == "" {
		adapter = state.binding.Adapter
	}
	var output TurnResultOutput
	if err := g.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var txErr error
		output, txErr = PersistTurnResultTx(ctx, tx, PersistTurnResultParams{
			Binding:     state.binding,
			Conv:        state.conv,
			Inbound:     state.inbound,
			Job:         state.job,
			Adapter:     adapter,
			Result:      result,
			RunErr:      runErr,
			RunnerID:    "gateway",
			FinalizeJob: true,
		})
		return txErr
	}); err != nil {
		return ProcessResult{}, err
	}
	return output.Persisted, nil
}

func (g *Gateway) publishError(projectName, conversationID, peerMessageID string, err error) {
	if g == nil || g.bus == nil || err == nil {
		return
	}
	msg := err.Error()
	g.bus.Publish(GatewayEvent{
		ProjectName:    projectName,
		ConversationID: conversationID,
		PeerMessageID:  peerMessageID,
		Type:           "error",
		Text:           msg,
		EventType:      "error",
		Stream:         "lifecycle",
		JobStatus:      contracts.ChannelTurnFailed,
		JobError:       msg,
		JobErrorType:   classifyJobErrorType(msg),
		At:             time.Now(),
	})
}

func (g *Gateway) publishStreamAgentEvent(projectName, conversationID, peerMessageID string, ev AgentEvent) {
	if g == nil || g.bus == nil {
		return
	}
	runID := ev.RunID
	stream := string(ev.Stream)
	eventType := deriveGatewayRuntimeEventType(stream, ev.Data.Phase)
	text := ev.Data.Text
	if text == "" {
		text = ev.Data.Error
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
		ToolName:       ev.Data.ToolName,
		ToolInput:      ev.Data.ToolInput,
		Detail:         ev.Data.Detail,
		JobStatus:      contracts.ChannelTurnRunning,
		At:             time.Now(),
	})
}

func (g *Gateway) publishFinalFromResult(projectName, conversationID, peerMessageID string, result ProcessResult) {
	if g == nil || g.bus == nil {
		return
	}
	reply := result.ReplyText
	if reply == "" && result.JobStatus != contracts.ChannelTurnSucceeded {
		reply = result.JobError
	}
	finalRunID := result.RunID
	finalSeq := 1
	for _, ev := range result.AgentEvents {
		if ev.Seq >= finalSeq {
			finalSeq = ev.Seq + 1
		}
		if finalRunID == "" && ev.RunID != "" {
			finalRunID = ev.RunID
		}
	}
	finalEventType := "end"
	if result.JobStatus != contracts.ChannelTurnSucceeded {
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
		AgentProvider:  result.AgentProvider,
		AgentModel:     result.AgentModel,
		JobStatus:      result.JobStatus,
		JobErrorType:   result.JobErrorType,
		JobError:       result.JobError,
		At:             time.Now(),
	})
}

func (g *Gateway) publishFromResult(projectName, conversationID, peerMessageID string, result ProcessResult) {
	if g == nil || g.bus == nil {
		return
	}
	reply := result.ReplyText
	if reply == "" && result.JobStatus != contracts.ChannelTurnSucceeded {
		reply = result.JobError
	}

	finalRunID := result.RunID
	lastSeq := 0
	finalSeq := 0
	finalEventType := "end"
	for _, ev := range result.AgentEvents {
		runID := ev.RunID
		if runID == "" {
			runID = finalRunID
		}
		if finalRunID == "" && runID != "" {
			finalRunID = runID
		}
		stream := string(ev.Stream)
		eventType := deriveGatewayRuntimeEventType(stream, ev.Data.Phase)
		text := ev.Data.Text
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
			ToolName:       ev.Data.ToolName,
			ToolInput:      ev.Data.ToolInput,
			Detail:         ev.Data.Detail,
			AgentProvider:  result.AgentProvider,
			AgentModel:     result.AgentModel,
			JobStatus:      result.JobStatus,
			JobErrorType:   result.JobErrorType,
			JobError:       result.JobError,
			At:             time.Now(),
		})
	}
	if finalSeq <= 0 {
		finalSeq = lastSeq + 1
	}
	if result.JobStatus != contracts.ChannelTurnSucceeded {
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
		AgentProvider:  result.AgentProvider,
		AgentModel:     result.AgentModel,
		JobStatus:      result.JobStatus,
		JobErrorType:   result.JobErrorType,
		JobError:       result.JobError,
		At:             time.Now(),
	})
}

func toStoreChannelType(channelType contracts.ChannelType) contracts.ChannelType {
	switch strings.ToLower(string(channelType)) {
	case string(contracts.ChannelTypeWeb):
		return contracts.ChannelTypeWeb
	case string(contracts.ChannelTypeIM):
		return contracts.ChannelTypeIM
	case string(contracts.ChannelTypeAPI):
		return contracts.ChannelTypeAPI
	case string(contracts.ChannelTypeCLI):
		fallthrough
	default:
		return contracts.ChannelTypeCLI
	}
}

func defaultPeerProjectKey(projectName string, env contracts.InboundEnvelope) string {
	if strings.EqualFold(string(env.ChannelType), string(contracts.ChannelTypeIM)) {
		if env.PeerConversationID != "" {
			return env.PeerConversationID
		}
	}
	return projectName
}

func inboundMessageStatusFromTurn(st contracts.ChannelTurnJobStatus) contracts.ChannelMessageStatus {
	if st == contracts.ChannelTurnSucceeded {
		return contracts.ChannelMessageProcessed
	}
	return contracts.ChannelMessageFailed
}

func outboundMessageStatusFromTurn(st contracts.ChannelTurnJobStatus) contracts.ChannelMessageStatus {
	if st == contracts.ChannelTurnSucceeded {
		return contracts.ChannelMessageSent
	}
	return contracts.ChannelMessageFailed
}

func outboxStatusFromTurn(st contracts.ChannelTurnJobStatus) contracts.ChannelOutboxStatus {
	if st == contracts.ChannelTurnSucceeded {
		return contracts.ChannelOutboxSent
	}
	return contracts.ChannelOutboxFailed
}

func deriveGatewayRuntimeEventType(stream, phase string) string {
	if stream == "lifecycle" {
		if phase != "" {
			return phase
		}
		return "lifecycle"
	}
	if stream == "assistant" {
		if phase != "" {
			return phase
		}
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

func (g *Gateway) InterruptBoundConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerProjectKey, peerConversationID string) (string, InterruptResult, error) {
	if g == nil || g.db == nil {
		return "", InterruptResult{}, fmt.Errorf("gateway db 为空")
	}
	if ctx == nil {
		return "", InterruptResult{}, fmt.Errorf("context 不能为空")
	}
	channelType = toStoreChannelType(channelType)
	if channelType == "" {
		channelType = contracts.ChannelTypeCLI
	}
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = defaultAdapter(string(channelType))
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
	gatewayCanceled := g.cancelActiveTurn(projectName, peerConversationID, false)
	if !gatewayCanceled {
		// 容灾兜底：同项目串行 worker 只跑一个 turn，允许退化为项目级取消。
		gatewayCanceled = g.cancelActiveTurn(projectName, "", true)
	}
	projectCtx, err := g.resolver.Resolve(projectName)
	if err != nil {
		return projectName, InterruptResult{}, err
	}
	if projectCtx == nil || projectCtx.Runtime == nil {
		return projectName, InterruptResult{}, fmt.Errorf("project runtime 不可用: %s", projectName)
	}
	interrupter, ok := projectCtx.Runtime.(ProjectRuntimeInterrupter)
	if !ok || interrupter == nil {
		status := InterruptStatusMiss
		if gatewayCanceled {
			status = InterruptStatusHit
		}
		result := InterruptResult{
			Status:          status,
			ContextCanceled: gatewayCanceled,
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
	result, err := interrupter.InterruptConversation(ctx, channelType, adapter, peerConversationID)
	if err != nil {
		g.logInterrupt("runner_error",
			"project", projectName,
			"error", err,
		)
		return projectName, result, err
	}
	if gatewayCanceled {
		result.ContextCanceled = true
		if result.Status != InterruptStatusHit {
			result.Status = InterruptStatusHit
		}
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

func (g *Gateway) ResetBoundConversationSession(ctx context.Context, channelType contracts.ChannelType, adapter, peerProjectKey, peerConversationID string) (string, bool, error) {
	if g == nil || g.db == nil {
		return "", false, fmt.Errorf("gateway db 为空")
	}
	if ctx == nil {
		return "", false, fmt.Errorf("context 不能为空")
	}
	channelType = toStoreChannelType(channelType)
	if channelType == "" {
		channelType = contracts.ChannelTypeCLI
	}
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = defaultAdapter(string(channelType))
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

func (g *Gateway) HardResetBoundConversation(ctx context.Context, channelType contracts.ChannelType, adapter, peerProjectKey, peerConversationID string) (string, bool, error) {
	if g == nil || g.db == nil {
		return "", false, fmt.Errorf("gateway db 为空")
	}
	if ctx == nil {
		return "", false, fmt.Errorf("context 不能为空")
	}
	channelType = toStoreChannelType(channelType)
	if channelType == "" {
		channelType = contracts.ChannelTypeCLI
	}
	adapter = strings.TrimSpace(adapter)
	if adapter == "" {
		adapter = defaultAdapter(string(channelType))
	}
	peerProjectKey = strings.TrimSpace(peerProjectKey)
	peerConversationID = strings.TrimSpace(peerConversationID)
	if peerConversationID == "" {
		peerConversationID = peerProjectKey
	}
	g.logReset("cmd_received",
		"channel_type", channelType,
		"adapter", adapter,
		"peer_project_key", peerProjectKey,
		"peer_conversation_id", peerConversationID,
	)
	if peerProjectKey == "" || peerConversationID == "" {
		g.logReset("cmd_invalid",
			"channel_type", channelType,
			"adapter", adapter,
			"peer_project_key", peerProjectKey,
			"peer_conversation_id", peerConversationID,
		)
		return "", false, fmt.Errorf("peer project key / conversation id 不能为空")
	}

	projectName, err := g.LookupBoundProject(ctx, channelType, adapter, peerProjectKey)
	if err != nil {
		g.logReset("locator_error",
			"channel_type", channelType,
			"adapter", adapter,
			"peer_project_key", peerProjectKey,
			"error", err,
		)
		return "", false, err
	}
	if projectName == "" {
		g.logReset("locator_result",
			"channel_type", channelType,
			"adapter", adapter,
			"peer_project_key", peerProjectKey,
			"locator", "miss",
		)
		return "", false, nil
	}
	g.logReset("locator_result",
		"channel_type", channelType,
		"adapter", adapter,
		"peer_project_key", peerProjectKey,
		"locator", "hit",
		"project", projectName,
	)

	// /reset 以恢复可用性为优先：先做项目级取消，确保上层 turn ctx 收敛。
	cancelByConversation := g.cancelActiveTurn(projectName, peerConversationID, false)
	cancelByProjectFallback := false
	activeTurnCanceled := cancelByConversation
	if !activeTurnCanceled {
		cancelByProjectFallback = g.cancelActiveTurn(projectName, "", true)
		activeTurnCanceled = cancelByProjectFallback
	}
	g.logReset("cancel_active_turn",
		"project", projectName,
		"peer_conversation_id", peerConversationID,
		"conversation_canceled", cancelByConversation,
		"project_fallback_canceled", cancelByProjectFallback,
		"active_turn_canceled", activeTurnCanceled,
	)

	projectCtx, err := g.resolver.Resolve(projectName)
	if err != nil {
		g.logReset("project_resolve_error",
			"project", projectName,
			"error", err,
		)
		return projectName, false, err
	}
	if projectCtx == nil || projectCtx.Runtime == nil {
		runtimeErr := fmt.Errorf("project runtime 不可用: %s", projectName)
		g.logReset("runtime_unavailable",
			"project", projectName,
			"error", runtimeErr,
		)
		return projectName, false, runtimeErr
	}
	resetter, ok := projectCtx.Runtime.(ProjectRuntimeHardResetter)
	if !ok || resetter == nil {
		runtimeErr := fmt.Errorf("project runtime 不支持 hard reset: %s", projectName)
		g.logReset("runtime_unsupported",
			"project", projectName,
			"error", runtimeErr,
		)
		return projectName, false, runtimeErr
	}
	reset, resetErr := resetter.HardResetConversation(ctx, channelType, adapter, peerConversationID)
	g.logReset("runtime_reset_result",
		"project", projectName,
		"reset", reset,
		"error", resetErr,
	)
	replaceErr := g.replaceProjectWorker(projectName)
	g.logReset("worker_replace_result",
		"project", projectName,
		"replaced", replaceErr == nil,
		"error", replaceErr,
	)
	if resetErr != nil && replaceErr != nil {
		combinedErr := fmt.Errorf("%w；另外 worker 重建失败: %v", resetErr, replaceErr)
		g.logReset("cmd_result",
			"project", projectName,
			"status", "error",
			"reset", reset,
			"error", combinedErr,
		)
		return projectName, reset, combinedErr
	}
	if resetErr != nil {
		g.logReset("cmd_result",
			"project", projectName,
			"status", "error",
			"reset", reset,
			"error", resetErr,
		)
		return projectName, reset, resetErr
	}
	if replaceErr != nil {
		g.logReset("cmd_result",
			"project", projectName,
			"status", "error",
			"reset", reset,
			"error", replaceErr,
		)
		return projectName, reset, replaceErr
	}
	if !reset {
		reset = true
	}
	g.logReset("cmd_result",
		"project", projectName,
		"status", "ok",
		"reset", reset,
		"active_turn_canceled", activeTurnCanceled,
	)
	return projectName, reset, nil
}

func (g *Gateway) LookupBoundProject(ctx context.Context, channelType contracts.ChannelType, adapter, peerProjectKey string) (string, error) {
	projectName, _, err := g.LookupBindingDetail(ctx, channelType, adapter, peerProjectKey)
	return projectName, err
}

func (g *Gateway) LookupBindingDetail(ctx context.Context, channelType contracts.ChannelType, adapter, peerProjectKey string) (string, bool, error) {
	if g == nil || g.db == nil {
		return "", false, fmt.Errorf("gateway db 为空")
	}
	if ctx == nil {
		return "", false, fmt.Errorf("context 不能为空")
	}
	adapter = strings.TrimSpace(adapter)
	peerProjectKey = strings.TrimSpace(peerProjectKey)
	var binding contracts.ChannelBinding
	err := g.db.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ? AND enabled = 1",
			toStoreChannelType(channelType),
			adapter,
			peerProjectKey).
		First(&binding).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	return binding.ProjectName, readChannelBindingQuietMode(binding.RolePolicyJSON), nil
}

func (g *Gateway) GetBindingQuietMode(ctx context.Context, channelType contracts.ChannelType, adapter, peerProjectKey string) (bool, error) {
	_, quietMode, err := g.LookupBindingDetail(ctx, channelType, adapter, peerProjectKey)
	if err != nil {
		return false, err
	}
	return quietMode, nil
}

func (g *Gateway) SetBindingQuietMode(ctx context.Context, channelType contracts.ChannelType, adapter, peerProjectKey string, quiet bool) error {
	if g == nil || g.db == nil {
		return fmt.Errorf("gateway db 为空")
	}
	if ctx == nil {
		return fmt.Errorf("context 不能为空")
	}
	adapter = strings.TrimSpace(adapter)
	peerProjectKey = strings.TrimSpace(peerProjectKey)

	var binding contracts.ChannelBinding
	err := g.db.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ? AND enabled = 1",
			toStoreChannelType(channelType),
			adapter,
			peerProjectKey).
		First(&binding).Error
	if err != nil {
		return err
	}

	policy := contracts.JSONMapFromAny(binding.RolePolicyJSON)
	policy[channelBindingPolicyQuietModeKey] = quiet

	return g.db.WithContext(ctx).Model(&contracts.ChannelBinding{}).
		Where("id = ?", binding.ID).
		Update("role_policy_json", policy).Error
}

func readChannelBindingQuietMode(policy contracts.JSONMap) bool {
	raw, ok := policy[channelBindingPolicyQuietModeKey]
	if !ok {
		return false
	}
	switch x := raw.(type) {
	case bool:
		return x
	case string:
		s := strings.ToLower(strings.TrimSpace(x))
		if s == "1" || s == "true" || s == "yes" || s == "y" || s == "on" {
			return true
		}
		if s == "0" || s == "false" || s == "no" || s == "n" || s == "off" {
			return false
		}
	case float64:
		return x != 0
	case float32:
		return x != 0
	case int:
		return x != 0
	case int64:
		return x != 0
	case int32:
		return x != 0
	case uint:
		return x != 0
	case uint64:
		return x != 0
	case uint32:
		return x != 0
	}
	return false
}

func (g *Gateway) BindProject(ctx context.Context, channelType contracts.ChannelType, adapter, peerProjectKey, projectName string) (string, error) {
	if g == nil || g.db == nil {
		return "", fmt.Errorf("gateway db 为空")
	}
	if ctx == nil {
		return "", fmt.Errorf("context 不能为空")
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
		var binding contracts.ChannelBinding
		err := tx.WithContext(ctx).
			Where("channel_type = ? AND adapter = ? AND peer_project_key = ?",
				toStoreChannelType(channelType),
				adapter,
				peerProjectKey).
			First(&binding).Error
		if err == nil {
			prevProject = binding.ProjectName
			return tx.WithContext(ctx).Model(&contracts.ChannelBinding{}).
				Where("id = ?", binding.ID).
				Updates(map[string]any{
					"project_name": projectName,
					"enabled":      true,
				}).Error
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		binding = contracts.ChannelBinding{
			ProjectName:    projectName,
			ChannelType:    toStoreChannelType(channelType),
			Adapter:        adapter,
			PeerProjectKey: peerProjectKey,
			RolePolicyJSON: contracts.JSONMap{},
			Enabled:        true,
		}
		return tx.WithContext(ctx).Create(&binding).Error
	})
	if err != nil {
		return "", err
	}
	return prevProject, nil
}

func (g *Gateway) UnbindProject(ctx context.Context, channelType contracts.ChannelType, adapter, peerProjectKey string) (bool, error) {
	if g == nil || g.db == nil {
		return false, fmt.Errorf("gateway db 为空")
	}
	if ctx == nil {
		return false, fmt.Errorf("context 不能为空")
	}
	adapter = strings.TrimSpace(adapter)
	peerProjectKey = strings.TrimSpace(peerProjectKey)
	res := g.db.WithContext(ctx).
		Where("channel_type = ? AND adapter = ? AND peer_project_key = ?",
			toStoreChannelType(channelType),
			adapter,
			peerProjectKey).
		Delete(&contracts.ChannelBinding{})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}
