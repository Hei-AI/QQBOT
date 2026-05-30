package agentruntime

import (
	"context"
	"sync"
)

// EventQueue 是带一次性等待者的内存 FIFO 队列。
// 供 root/story Agent 使用，
// 例如在 wait 工具中阻塞等待 NapCat、新闻或定时器事件到达。
type EventQueue[T any] struct {
	mu      sync.Mutex
	items   []T
	waiters []chan T
}

// NewEventQueue 为 T 类型事件创建一个空队列。
func NewEventQueue[T any]() *EventQueue[T] {
	return &EventQueue[T]{}
}

// Enqueue 追加事件，或立即唤醒最早的等待者。
func (q *EventQueue[T]) Enqueue(item T) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.waiters) > 0 {
		waiter := q.waiters[0]
		q.waiters = q.waiters[1:]
		waiter <- item
		close(waiter)
		return len(q.items)
	}
	q.items = append(q.items, item)
	return len(q.items)
}

// Dequeue 非阻塞地取出最早入队的事件。
func (q *EventQueue[T]) Dequeue() (T, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	var zero T
	if len(q.items) == 0 {
		return zero, false
	}
	item := q.items[0]
	q.items = q.items[1:]
	return item, true
}

// Drain 返回当前所有排队事件并清空队列。
func (q *EventQueue[T]) Drain() []T {
	q.mu.Lock()
	defer q.mu.Unlock()
	items := q.items
	q.items = nil
	return items
}

// Wait 阻塞直到事件到达或上下文被取消。
func (q *EventQueue[T]) Wait(ctx context.Context) (T, bool) {
	if item, ok := q.Dequeue(); ok {
		return item, true
	}
	ch := make(chan T, 1)
	q.mu.Lock()
	q.waiters = append(q.waiters, ch)
	q.mu.Unlock()
	select {
	case item := <-ch:
		return item, true
	case <-ctx.Done():
		q.removeWaiter(ch)
		var zero T
		return zero, false
	}
}

// Clear 清除所有排队事件，但不影响正在等待的调用。
func (q *EventQueue[T]) Clear() {
	q.mu.Lock()
	q.items = nil
	q.mu.Unlock()
}

// Len 返回当前排队事件数量。
func (q *EventQueue[T]) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

func (q *EventQueue[T]) removeWaiter(ch chan T) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, waiter := range q.waiters {
		if waiter == ch {
			q.waiters = append(q.waiters[:i], q.waiters[i+1:]...)
			return
		}
	}
}
