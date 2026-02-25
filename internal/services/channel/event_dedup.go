package channel

import (
	"container/list"
	"strings"
	"sync"
	"time"
)

const (
	defaultEventDedupTTL      = 5 * time.Minute
	defaultEventDedupCapacity = 10000
)

type eventDedupEntry struct {
	key       string
	expiresAt time.Time
	node      *list.Element
}

// EventDeduplicator 提供基于内存的短窗口事件去重能力。
// 返回 true 表示该事件在窗口内出现过，调用方应直接跳过后续处理。
type EventDeduplicator struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	now      func() time.Time

	order   *list.List
	entries map[string]*eventDedupEntry
}

func NewEventDeduplicator(ttl time.Duration, capacity int) *EventDeduplicator {
	if ttl <= 0 {
		ttl = defaultEventDedupTTL
	}
	if capacity <= 0 {
		capacity = defaultEventDedupCapacity
	}
	return &EventDeduplicator{
		ttl:      ttl,
		capacity: capacity,
		now:      time.Now,
		order:    list.New(),
		entries:  make(map[string]*eventDedupEntry, capacity),
	}
}

// IsDuplicate 会在内部自动记录 eventID。
// 当 eventID 为空时始终返回 false。
func (d *EventDeduplicator) IsDuplicate(eventID string) bool {
	if d == nil {
		return false
	}
	key := strings.TrimSpace(eventID)
	if key == "" {
		return false
	}

	now := d.now()

	d.mu.Lock()
	defer d.mu.Unlock()

	d.evictExpiredLocked(now)

	if hit, ok := d.entries[key]; ok {
		if now.Before(hit.expiresAt) {
			// 命中后续命中窗口需要顺延，覆盖飞书连续重试场景。
			hit.expiresAt = now.Add(d.ttl)
			d.order.MoveToBack(hit.node)
			return true
		}
		d.removeLocked(hit)
	}

	d.insertLocked(key, now.Add(d.ttl))
	return false
}

func (d *EventDeduplicator) evictExpiredLocked(now time.Time) {
	for cur := d.order.Front(); cur != nil; {
		next := cur.Next()
		key, _ := cur.Value.(string)
		entry := d.entries[key]
		if entry == nil {
			d.order.Remove(cur)
			cur = next
			continue
		}
		if now.Before(entry.expiresAt) {
			// order 按“最近访问”排序，遇到首个未过期即可停止。
			break
		}
		d.removeLocked(entry)
		cur = next
	}
}

func (d *EventDeduplicator) insertLocked(key string, expiresAt time.Time) {
	if old := d.entries[key]; old != nil {
		d.removeLocked(old)
	}

	node := d.order.PushBack(key)
	d.entries[key] = &eventDedupEntry{
		key:       key,
		expiresAt: expiresAt,
		node:      node,
	}

	for len(d.entries) > d.capacity {
		head := d.order.Front()
		if head == nil {
			return
		}
		headKey, _ := head.Value.(string)
		if headEntry := d.entries[headKey]; headEntry != nil {
			d.removeLocked(headEntry)
			continue
		}
		d.order.Remove(head)
	}
}

func (d *EventDeduplicator) removeLocked(entry *eventDedupEntry) {
	if entry == nil {
		return
	}
	delete(d.entries, entry.key)
	if entry.node != nil {
		d.order.Remove(entry.node)
	}
}
