package channel

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
)

var ErrInboundQueueFull = errors.New("inbound queue full")
var ErrInboundQueueClosed = errors.New("inbound queue closed")

// InboundQueue 按 project 维度维护独立队列：同 project 串行，不同 project 并行。
type InboundQueue struct {
	mu     sync.Mutex
	depth  int
	closed atomic.Bool
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
	return q.getOrCreateLocked(projectName)
}

func (q *InboundQueue) Enqueue(projectName string, item InboundItem) error {
	if q == nil {
		return errors.New("inbound queue 为空")
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return errors.New("project name 不能为空")
	}
	q.mu.Lock()
	ch, _, err := q.getOrCreateLocked(projectName)
	if err != nil {
		q.mu.Unlock()
		return err
	}
	select {
	case ch <- item:
		q.mu.Unlock()
		return nil
	default:
		q.mu.Unlock()
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
	if q.closed.Swap(true) {
		return
	}
	q.mu.Lock()
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

func (q *InboundQueue) Replace(projectName string) (chan InboundItem, chan InboundItem, error) {
	if q == nil {
		return nil, nil, errors.New("inbound queue 为空")
	}
	projectName = strings.TrimSpace(projectName)
	if projectName == "" {
		return nil, nil, errors.New("project name 不能为空")
	}

	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed.Load() {
		return nil, nil, ErrInboundQueueClosed
	}
	old := q.queues[projectName]
	newCh := make(chan InboundItem, q.Depth())
	q.queues[projectName] = newCh
	return old, newCh, nil
}

func (q *InboundQueue) getOrCreateLocked(projectName string) (chan InboundItem, bool, error) {
	if q.closed.Load() {
		return nil, false, ErrInboundQueueClosed
	}
	if ch, ok := q.queues[projectName]; ok {
		return ch, false, nil
	}
	ch := make(chan InboundItem, q.Depth())
	q.queues[projectName] = ch
	return ch, true, nil
}
