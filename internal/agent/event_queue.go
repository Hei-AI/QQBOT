package agent

import (
	"context"
	"sync"
	"time"
)

// EventQueue 是可执行服务使用的根包事件队列。
//
// internal/agentruntime 中的队列是可复用版本；这里保留该队列是为了
// 兼容当前根运行时组装方式和仪表盘结构。
type EventQueue struct {
	mu     sync.Mutex
	queue  []AgentEvent
	wakeup chan struct{}
}

// AgentEvent 是根 Agent 消费的标准化外部事件。
type AgentEvent struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
	At   time.Time      `json:"at"`
}

func NewEventQueue() *EventQueue {
	return &EventQueue{wakeup: make(chan struct{}, 1)}
}

func (q *EventQueue) Enqueue(event AgentEvent) {
	if event.At.IsZero() {
		event.At = time.Now()
	}
	q.mu.Lock()
	q.queue = append(q.queue, event)
	q.mu.Unlock()
	select {
	case q.wakeup <- struct{}{}:
	default:
	}
}

func (q *EventQueue) DequeueAll() []AgentEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.queue
	q.queue = nil
	for {
		select {
		case <-q.wakeup:
		default:
			return out
		}
	}
}

func (q *EventQueue) Count() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.queue)
}

func (q *EventQueue) Wait(ctx context.Context) ([]AgentEvent, bool) {
	for {
		if events := q.DequeueAll(); len(events) > 0 {
			return events, true
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-q.wakeup:
		}
	}
}

func (q *EventQueue) WaitForEvent(ctx context.Context) bool {
	for {
		q.mu.Lock()
		hasEvents := len(q.queue) > 0
		q.mu.Unlock()
		if hasEvents {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-q.wakeup:
		}
	}
}
