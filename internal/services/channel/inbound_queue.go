package channel

import (
	"errors"
	"strings"
	"sync"
)

var ErrInboundQueueFull = errors.New("inbound queue full")
var ErrInboundQueueClosed = errors.New("inbound queue closed")

// InboundQueue 按 project 维度维护独立队列：同 project 串行，不同 project 并行。
type InboundQueue struct {
	mu     sync.Mutex
	depth  int
	closed bool
	queues map[string]chan InboundItem
}

func NewInboundQueue(depth int) *InboundQueue {
	if depth <= 0 {
		depth = 32
	}
	return &InboundQueue{
		depth:  depth,
		queues: make(map[string]chan InboundItem),
	}
}

func (q *InboundQueue) Depth() int {
	if q == nil || q.depth <= 0 {
		return 32
	}
	return q.depth
}

func (q *InboundQueue) GetOrCreate(projectName string) (chan InboundItem, bool, error) {
	if q == nil {
		return nil, false, errors.New("inbound queue 为空")
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return nil, false, errors.New("project name 不能为空")
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil, false, ErrInboundQueueClosed
	}
	if ch, ok := q.queues[projectName]; ok {
		return ch, false, nil
	}
	ch := make(chan InboundItem, q.Depth())
	q.queues[projectName] = ch
	return ch, true, nil
}

func (q *InboundQueue) Enqueue(projectName string, item InboundItem) error {
	ch, _, err := q.GetOrCreate(projectName)
	if err != nil {
		return err
	}
	select {
	case ch <- item:
		return nil
	default:
		return ErrInboundQueueFull
	}
}

func (q *InboundQueue) Len(projectName string) int {
	if q == nil {
		return 0
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return 0
	}
	q.mu.Lock()
	ch := q.queues[projectName]
	q.mu.Unlock()
	if ch == nil {
		return 0
	}
	return len(ch)
}

func (q *InboundQueue) Close() {
	if q == nil {
		return
	}
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	oldQueues := q.queues
	q.queues = make(map[string]chan InboundItem)
	q.mu.Unlock()

	closed := make(map[chan InboundItem]struct{}, len(oldQueues))
	for _, ch := range oldQueues {
		if ch == nil {
			continue
		}
		if _, ok := closed[ch]; ok {
			continue
		}
		close(ch)
		closed[ch] = struct{}{}
	}
}
