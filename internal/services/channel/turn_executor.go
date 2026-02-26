package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"dalek/internal/agent/eventlog"
	"dalek/internal/contracts"
	"dalek/internal/services/channel/agentcli"
	"dalek/internal/store"

	"gorm.io/gorm"
)

type turnContext struct {
	runnerID  string
	runID     string
	startedAt time.Time

	job     store.ChannelTurnJob
	inbound store.ChannelMessage
	conv    store.ChannelConversation
	binding store.ChannelBinding

	collector    *turnEventCollector
	evLogger     *eventlog.RunLogger
	evLogProject string
	provider     string
	model        string
	gaCfg        agentcli.ConfigOverride

	turnCtx        context.Context
	turnCancel     context.CancelFunc
	slotAcquired   bool
	runningTurnSet bool
	agentResp      pmAgentTurnResponse
}

func (t *turnContext) closeEventLogger() {
	if t == nil || t.evLogger == nil {
		return
	}
	_ = t.evLogger.Close()
	t.evLogger = nil
}

func (t *turnContext) executionCtx(fallback context.Context) context.Context {
	if t != nil && t.turnCtx != nil {
		return t.turnCtx
	}
	if fallback != nil {
		return fallback
	}
	return context.Background()
}

func (t *turnContext) failTurn(s *Service, cause error, resultJSON string) error {
	if t == nil {
		return cause
	}
	msg := strings.TrimSpace(fmt.Sprint(cause))
	if cause == nil || msg == "" || msg == "<nil>" {
		msg = "turn job failed"
	}
	if failErr := s.completeTurnJobFailed(context.Background(), t.job.ID, t.runnerID, msg, resultJSON); failErr != nil {
		if cause == nil {
			return failErr
		}
		return fmt.Errorf("%w；并且写入 turn_job failed 失败: %v", cause, failErr)
	}
	return nil
}

func (t *turnContext) failureProcessResult() ProcessResult {
	agentProvider := strings.TrimSpace(t.agentResp.Provider)
	if agentProvider == "" {
		agentProvider = strings.TrimSpace(t.provider)
	}
	agentModel := strings.TrimSpace(t.agentResp.Model)
	if agentModel == "" {
		agentModel = strings.TrimSpace(t.model)
	}
	return ProcessResult{
		RunID:           t.runID,
		JobStatus:       contracts.ChannelTurnFailed,
		AgentProvider:   agentProvider,
		AgentModel:      agentModel,
		AgentSessionID:  strings.TrimSpace(t.agentResp.SessionID),
		AgentOutputMode: strings.TrimSpace(string(t.agentResp.OutputMode)),
		AgentCommand:    strings.TrimSpace(t.agentResp.Command),
		AgentStdout:     strings.TrimSpace(t.agentResp.Stdout),
		AgentStderr:     strings.TrimSpace(t.agentResp.Stderr),
		AgentEvents:     t.collector.Snapshot(),
	}
}

func (s *Service) claimAndLoadTurnContext(ctx context.Context, jobID uint) (*turnContext, bool, error) {
	_, db, err := s.require()
	if err != nil {
		return nil, false, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if jobID == 0 {
		return nil, false, fmt.Errorf("job_id 不能为空")
	}

	runnerID := newRunnerID()
	job, claimed, err := s.claimTurnJob(ctx, jobID, runnerID, 90*time.Second)
	if err != nil {
		return nil, false, err
	}
	if !claimed {
		return nil, false, nil
	}

	tctx := &turnContext{
		runnerID:  runnerID,
		runID:     newRunID(),
		startedAt: time.Now(),
		job:       job,
	}

	gaCfg := s.p.Config.WithDefaults().GatewayAgent
	tctx.gaCfg = agentcli.ConfigOverride{
		Provider:     strings.TrimSpace(gaCfg.Provider),
		Model:        strings.TrimSpace(gaCfg.Model),
		Command:      strings.TrimSpace(gaCfg.Command),
		Output:       strings.TrimSpace(gaCfg.Output),
		ResumeOutput: strings.TrimSpace(gaCfg.ResumeOutput),
	}
	tctx.provider, tctx.model = resolveGatewayAgentHints(tctx.gaCfg)
	tctx.collector = newTurnEventCollector(ctx, tctx.runID, tctx.startedAt, tctx.provider)
	tctx.collector.AppendLifecycleStart()

	tctx.evLogProject = strings.TrimSpace(s.p.Name)
	if tctx.evLogProject == "" {
		tctx.evLogProject = strings.TrimSpace(s.p.Key)
	}
	if tctx.evLogProject == "" {
		tctx.evLogProject = "unknown"
	}

	evLogger, evLogErr := eventlog.Open(tctx.evLogProject, tctx.runID)
	if evLogErr != nil {
		s.slog().Warn("eventlog open failed",
			"project", tctx.evLogProject,
			"run_id", tctx.runID,
			"error", evLogErr,
		)
	}
	tctx.evLogger = evLogger

	if err := db.WithContext(ctx).First(&tctx.inbound, tctx.job.InboundMessageID).Error; err != nil {
		tctx.closeEventLogger()
		return nil, true, tctx.failTurn(s, err, "")
	}
	if err := db.WithContext(ctx).First(&tctx.conv, tctx.inbound.ConversationID).Error; err != nil {
		tctx.closeEventLogger()
		return nil, true, tctx.failTurn(s, err, "")
	}
	if err := db.WithContext(ctx).First(&tctx.binding, tctx.conv.BindingID).Error; err != nil {
		tctx.closeEventLogger()
		return nil, true, tctx.failTurn(s, err, "")
	}
	return tctx, true, nil
}

func (s *Service) executeTurnAgent(ctx context.Context, tctx *turnContext) (pmAgentTurnResponse, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if tctx == nil {
		return pmAgentTurnResponse{}, fmt.Errorf("turn context 不能为空")
	}

	s.cancelConflictingTurn(tctx.conv.AgentSessionID, tctx.job.ID)
	if err := s.acquireTurnSlot(ctx); err != nil {
		return pmAgentTurnResponse{}, err
	}
	tctx.slotAcquired = true

	turnTimeout := s.gatewayTurnTimeout()
	if turnTimeout > 0 {
		tctx.turnCtx, tctx.turnCancel = context.WithTimeout(ctx, turnTimeout)
	} else {
		tctx.turnCtx, tctx.turnCancel = context.WithCancel(ctx)
	}
	s.setRunningTurn(tctx.job.ID, tctx.conv.ID, tctx.conv.AgentSessionID, tctx.turnCancel)
	tctx.runningTurnSet = true

	if tctx.evLogger != nil {
		_ = tctx.evLogger.WriteHeader(eventlog.RunMeta{
			RunID:          tctx.runID,
			Project:        tctx.evLogProject,
			ConversationID: fmt.Sprintf("%d", tctx.conv.ID),
			Provider:       tctx.provider,
			Model:          tctx.model,
			WorkDir:        strings.TrimSpace(s.p.RepoRoot),
			Layer:          "chat_runner",
		})
	}
	var evLogSeq int
	agentResp, err := s.planTurnByPMAgent(tctx.turnCtx, tctx.inbound, tctx.conv, tctx.binding, tctx.job, func(ev agentcli.Event) {
		evLogSeq++
		if tctx.evLogger != nil {
			_ = tctx.evLogger.WriteEvent(evLogSeq, ev.Type, ev.RawJSON)
		}
		tctx.collector.AppendCLIEvent(ev)
	})
	tctx.agentResp = agentResp

	if tctx.evLogger != nil {
		replyForLog := strings.TrimSpace(agentResp.Text)
		errForLog := ""
		if err != nil {
			errForLog = strings.TrimSpace(err.Error())
		}
		_ = tctx.evLogger.WriteFooter(eventlog.RunFooter{
			RunID:      tctx.runID,
			DurationMS: time.Since(tctx.startedAt).Milliseconds(),
			ReplyText:  replyForLog,
			Error:      errForLog,
			SessionID:  strings.TrimSpace(agentResp.SessionID),
		})
	}
	return agentResp, err
}

func (s *Service) processTurnResponse(ctx context.Context, tctx *turnContext, agentResp pmAgentTurnResponse) (string, []contracts.TurnAction, error) {
	if tctx == nil {
		return "", nil, fmt.Errorf("turn context 不能为空")
	}
	if tctx.collector.CLIEventCount() == 0 {
		for _, ev := range copyCLIEvents(agentResp.Events) {
			tctx.collector.AppendCLIEvent(ev)
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
					results = append(results, s.executeAction(ctx, action))
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
		return "", nil, fmt.Errorf("project manager agent 无响应（reply_text 为空）")
	}
	return effectiveReply, pendingActionsToCreate, nil
}

func (s *Service) finalizeTurn(ctx context.Context, tctx *turnContext, effectiveReply string, pendingActions []contracts.TurnAction) error {
	if tctx == nil {
		return fmt.Errorf("turn context 不能为空")
	}
	tctx.collector.AppendAssistantText(effectiveReply)
	tctx.collector.AppendLifecycleEnd(nil)
	events := tctx.collector.Snapshot()

	output, err := s.persistTurnJobResult(ctx, tctx, nil, ProcessResult{
		RunID:           tctx.runID,
		JobStatus:       contracts.ChannelTurnSucceeded,
		ReplyText:       effectiveReply,
		AgentProvider:   strings.TrimSpace(tctx.agentResp.Provider),
		AgentModel:      strings.TrimSpace(tctx.agentResp.Model),
		AgentSessionID:  strings.TrimSpace(tctx.agentResp.SessionID),
		AgentOutputMode: strings.TrimSpace(string(tctx.agentResp.OutputMode)),
		AgentCommand:    strings.TrimSpace(tctx.agentResp.Command),
		AgentStdout:     strings.TrimSpace(tctx.agentResp.Stdout),
		AgentStderr:     strings.TrimSpace(tctx.agentResp.Stderr),
		AgentEvents:     copyAgentEvents(events),
	}, pendingActions)
	if err != nil {
		return tctx.failTurn(s, err, "")
	}

	payloadJSON := output.ResultJSON
	if output.Persisted.OutboxID > 0 {
		if err := s.dispatchOutbox(ctx, output.Persisted.OutboxID); err != nil {
			var payload TurnResultRecord
			if uErr := json.Unmarshal([]byte(payloadJSON), &payload); uErr == nil {
				prev := len(payload.AgentEvents)
				payload.AgentEvents = AppendLifecycleErrorEvent(tctx.runID, tctx.startedAt, payload.AgentEvents, err)
				if len(payload.AgentEvents) > prev {
					emitStreamAgentEvent(ctx, payload.AgentEvents[len(payload.AgentEvents)-1])
				}
				payloadJSON = mustJSON(payload)
			}
			return s.completeTurnJobFailed(context.Background(), tctx.job.ID, tctx.runnerID, err.Error(), payloadJSON)
		}
	}
	return s.completeTurnJobSuccess(context.Background(), tctx.job.ID, tctx.runnerID, payloadJSON)
}

func (s *Service) persistTurnJobResult(ctx context.Context, tctx *turnContext, runErr error, result ProcessResult, pendingActions []contracts.TurnAction) (TurnResultOutput, error) {
	_, db, err := s.require()
	if err != nil {
		return TurnResultOutput{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if tctx == nil {
		return TurnResultOutput{}, fmt.Errorf("turn context 不能为空")
	}

	var output TurnResultOutput
	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if len(pendingActions) > 0 {
			pendingModels, err := s.createPendingActionsTx(ctx, tx, tctx.conv.ID, tctx.job.ID, pendingActions)
			if err != nil {
				return err
			}
			result.PendingActions = pendingActionViewsFromModels(pendingModels)
		}
		var txErr error
		output, txErr = PersistTurnResultTx(ctx, tx, PersistTurnResultParams{
			Binding:     tctx.binding,
			Conv:        tctx.conv,
			Inbound:     tctx.inbound,
			Job:         tctx.job,
			Adapter:     strings.TrimSpace(tctx.binding.Adapter),
			Result:      result,
			RunErr:      runErr,
			FinalizeJob: false,
		})
		return txErr
	})
	if err != nil {
		return TurnResultOutput{}, err
	}
	return output, nil
}

func (s *Service) failAndPersistTurn(ctx context.Context, tctx *turnContext, agentResp pmAgentTurnResponse, runErr error) error {
	if tctx == nil {
		return runErr
	}
	tctx.agentResp = agentResp
	if tctx.collector.CLIEventCount() == 0 {
		for _, ev := range copyCLIEvents(agentResp.Events) {
			tctx.collector.AppendCLIEvent(ev)
		}
	}
	tctx.collector.AppendLifecycleEnd(runErr)
	output, pErr := s.persistTurnJobResult(ctx, tctx, runErr, tctx.failureProcessResult(), nil)
	if pErr != nil {
		return tctx.failTurn(s, pErr, "")
	}
	return tctx.failTurn(s, runErr, output.ResultJSON)
}
