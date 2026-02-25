package channel

import (
	"context"
	"strings"
	"sync"
	"time"

	"dalek/internal/services/channel/agentcli"
)

type turnEventCollector struct {
	ctx       context.Context
	runID     string
	startedAt time.Time
	provider  string

	mu             sync.Mutex
	seq            int
	events         []AgentEvent
	started        bool
	cliEventCount  int
	lastEventText  string
	finalized      bool
	finalizedPhase string
}

func newTurnEventCollector(ctx context.Context, runID string, startedAt time.Time, provider string) *turnEventCollector {
	return &turnEventCollector{
		ctx:       ctx,
		runID:     strings.TrimSpace(runID),
		startedAt: startedAt,
		provider:  strings.TrimSpace(strings.ToLower(provider)),
		events:    make([]AgentEvent, 0, 16),
	}
}

func (c *turnEventCollector) AppendLifecycleStart() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return
	}
	startMS := c.startedAt.UnixMilli()
	if startMS <= 0 {
		startMS = time.Now().UnixMilli()
	}
	c.started = true
	ev := AgentEvent{
		RunID:  c.runID,
		Seq:    c.nextSeqLocked(),
		Stream: StreamLifecycle,
		Ts:     startMS,
		Data: AgentEventData{
			Phase:     "start",
			StartedAt: startMS,
		},
	}
	c.appendLocked(ev)
}

func (c *turnEventCollector) AppendCLIEvent(ev agentcli.Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finalized {
		return
	}
	if !c.started {
		c.started = true
	}
	items := buildAgentEventFromCLIEvent(c.runID, 0, ev, c.provider)
	for _, item := range items {
		item.Seq = c.nextSeqLocked()
		c.cliEventCount++
		c.appendLocked(item)
	}
}

func (c *turnEventCollector) AppendAssistantText(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finalized {
		return
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	ev := AgentEvent{
		RunID:  c.runID,
		Seq:    c.nextSeqLocked(),
		Stream: StreamAssistant,
		Ts:     time.Now().UnixMilli(),
		Data: AgentEventData{
			Text: text,
		},
	}
	// 最终回复仅记录到 snapshot（供落盘与审计），
	// 不通过 emitStreamAgentEvent 广播——由 publishFinalFromResult 负责通知订阅者。
	if ev.RunID == "" {
		ev.RunID = c.runID
	}
	if ev.Ts <= 0 {
		ev.Ts = time.Now().UnixMilli()
	}
	c.lastEventText = text
	c.events = append(c.events, ev)
}

func (c *turnEventCollector) AppendLifecycleEnd(runErr error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.finalized {
		return
	}
	endMS := time.Now().UnixMilli()
	startMS := c.startedAt.UnixMilli()
	if startMS <= 0 {
		startMS = endMS
	}
	ev := AgentEvent{
		RunID:  c.runID,
		Seq:    c.nextSeqLocked(),
		Stream: StreamLifecycle,
		Ts:     endMS,
		Data: AgentEventData{
			Phase:     "end",
			StartedAt: startMS,
			EndedAt:   endMS,
		},
	}
	if runErr != nil {
		msg := strings.TrimSpace(runErr.Error())
		ev.Data.Phase = "error"
		ev.Data.Error = msg
		ev.Data.ErrorType = classifyJobErrorType(msg)
		c.finalizedPhase = "error"
	} else {
		c.finalizedPhase = "end"
	}
	c.finalized = true
	c.appendLocked(ev)
}

func (c *turnEventCollector) Snapshot() []AgentEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return copyAgentEvents(c.events)
}

func (c *turnEventCollector) CLIEventCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cliEventCount
}

func (c *turnEventCollector) nextSeqLocked() int {
	c.seq++
	return c.seq
}

func (c *turnEventCollector) appendLocked(ev AgentEvent) {
	if strings.TrimSpace(ev.RunID) == "" {
		ev.RunID = c.runID
	}
	if ev.Seq <= 0 {
		ev.Seq = c.nextSeqLocked()
	}
	if ev.Ts <= 0 {
		ev.Ts = time.Now().UnixMilli()
	}
	c.lastEventText = strings.TrimSpace(ev.Data.Text)
	c.events = append(c.events, ev)
	emitStreamAgentEvent(c.ctx, ev)
}
