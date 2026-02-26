package channel

import (
	"dalek/internal/contracts"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dalek/internal/store"

	"gorm.io/gorm"
)

// GatewayEvent 是 gateway 适配层消费的统一事件帧。
type GatewayEvent struct {
	ProjectName    string
	ConversationID string
	PeerMessageID  string

	Type      string // assistant_event | assistant_message | error
	RunID     string
	Seq       int
	Stream    string
	Text      string
	EventType string

	AgentProvider string
	AgentModel    string
	JobStatus     contracts.ChannelTurnJobStatus
	JobErrorType  string
	JobError      string
	At            time.Time
}

type busSubscriber struct {
	id             uint64
	projectName    string
	conversationID string
	ch             chan GatewayEvent
}

// EventBus 是进程内 pub/sub 总线。
type EventBus struct {
	mu     sync.RWMutex
	nextID atomic.Uint64
	subs   map[uint64]busSubscriber
	closed bool

	db          *gorm.DB
	auditErrOut io.Writer
}

func NewEventBus() *EventBus {
	return &EventBus{
		subs:        map[uint64]busSubscriber{},
		auditErrOut: os.Stderr,
	}
}

// NewEventBusWithAudit 创建带审计日志的 EventBus，事件写入 event_bus_logs 表。
func NewEventBusWithAudit(db *gorm.DB) *EventBus {
	return newEventBusWithAuditAndErrWriter(db, os.Stderr)
}

func newEventBusWithAuditAndErrWriter(db *gorm.DB, errOut io.Writer) *EventBus {
	return &EventBus{
		subs:        map[uint64]busSubscriber{},
		db:          db,
		auditErrOut: errOut,
	}
}

func (eb *EventBus) Subscribe(projectName, conversationID string, buffer int) (<-chan GatewayEvent, func()) {
	if eb == nil {
		ch := make(chan GatewayEvent)
		close(ch)
		return ch, func() {}
	}
	if buffer <= 0 {
		buffer = 64
	}
	projectName = normalizeBusFilter(projectName)
	conversationID = normalizeBusFilter(conversationID)
	ch := make(chan GatewayEvent, buffer)
	id := eb.nextID.Add(1)

	eb.mu.Lock()
	if eb.closed {
		eb.mu.Unlock()
		close(ch)
		return ch, func() {}
	}
	eb.subs[id] = busSubscriber{
		id:             id,
		projectName:    projectName,
		conversationID: conversationID,
		ch:             ch,
	}
	eb.mu.Unlock()

	unsubscribe := func() {
		eb.mu.Lock()
		sub, ok := eb.subs[id]
		if ok {
			delete(eb.subs, id)
		}
		eb.mu.Unlock()
		if ok {
			close(sub.ch)
		}
	}
	return ch, unsubscribe
}

func (eb *EventBus) Publish(ev GatewayEvent) {
	if eb == nil {
		return
	}
	ev.ProjectName = strings.TrimSpace(ev.ProjectName)
	ev.ConversationID = strings.TrimSpace(ev.ConversationID)
	ev.PeerMessageID = strings.TrimSpace(ev.PeerMessageID)
	ev.Type = strings.TrimSpace(ev.Type)
	ev.RunID = strings.TrimSpace(ev.RunID)
	ev.Stream = strings.TrimSpace(ev.Stream)
	ev.Text = strings.TrimSpace(ev.Text)
	ev.EventType = strings.TrimSpace(ev.EventType)
	ev.AgentProvider = strings.TrimSpace(ev.AgentProvider)
	ev.AgentModel = strings.TrimSpace(ev.AgentModel)
	ev.JobErrorType = strings.TrimSpace(ev.JobErrorType)
	ev.JobError = strings.TrimSpace(ev.JobError)
	if ev.At.IsZero() {
		ev.At = time.Now()
	}

	eb.writeAudit(ev)

	eb.mu.RLock()
	if eb.closed {
		eb.mu.RUnlock()
		return
	}
	defer eb.mu.RUnlock()
	for _, sub := range eb.subs {
		if !busMatch(sub.projectName, ev.ProjectName) {
			continue
		}
		if !busMatch(sub.conversationID, ev.ConversationID) {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			// 订阅者自己兜底；总线不阻塞发布路径。
		}
	}
}

func (eb *EventBus) Close() {
	if eb == nil {
		return
	}
	eb.mu.Lock()
	if eb.closed {
		eb.mu.Unlock()
		return
	}
	eb.closed = true
	subs := eb.subs
	eb.subs = map[uint64]busSubscriber{}
	eb.mu.Unlock()

	for _, sub := range subs {
		if sub.ch == nil {
			continue
		}
		close(sub.ch)
	}
}

func (eb *EventBus) writeAudit(ev GatewayEvent) {
	if eb == nil || eb.db == nil {
		return
	}
	log := store.EventBusLog{
		CreatedAt:      ev.At,
		Project:        ev.ProjectName,
		ConversationID: ev.ConversationID,
		PeerMessageID:  ev.PeerMessageID,
		Type:           ev.Type,
		RunID:          ev.RunID,
		Seq:            ev.Seq,
		Stream:         ev.Stream,
		EventType:      ev.EventType,
		Text:           ev.Text,
		AgentProvider:  ev.AgentProvider,
		AgentModel:     ev.AgentModel,
		JobStatus:      string(ev.JobStatus),
		JobError:       ev.JobError,
		JobErrorType:   ev.JobErrorType,
	}
	// 写入失败不阻塞发布路径
	if err := eb.db.Create(&log).Error; err != nil {
		eb.reportAuditWriteError(ev, err)
	}
}

func (eb *EventBus) reportAuditWriteError(ev GatewayEvent, err error) {
	if eb == nil || err == nil {
		return
	}
	out := eb.auditErrOut
	if out == nil {
		out = os.Stderr
	}
	_, _ = fmt.Fprintf(out,
		"event bus audit write failed: project=%s conversation=%s peer_message_id=%s type=%s err=%v\n",
		strings.TrimSpace(ev.ProjectName),
		strings.TrimSpace(ev.ConversationID),
		strings.TrimSpace(ev.PeerMessageID),
		strings.TrimSpace(ev.Type),
		err,
	)
}

func normalizeBusFilter(in string) string {
	in = strings.TrimSpace(in)
	if in == "" {
		return "*"
	}
	return in
}

func busMatch(filter, value string) bool {
	if strings.TrimSpace(filter) == "*" {
		return true
	}
	return strings.TrimSpace(filter) == strings.TrimSpace(value)
}
