package agentruntime

import (
	"context"
	"sync"
)

// LoopAgent 是 root 和任务 Agent 共用的生命周期约定。
type LoopAgent interface {
	Initialize(context.Context) error
	Run(context.Context) error
	Stop()
}

// BaseLoopAgent 提供可取消的无 tick RunOnce 循环。
//
// 如果需要等待，RunOnce 内部的工具或队列负责阻塞。
type BaseLoopAgent struct {
	RunOnce         func(context.Context) error
	OnStopRequested func()

	mu      sync.Mutex
	running bool
	stop    context.CancelFunc
}

// Initialize 准备循环宿主；基础实现不执行任何操作。
func (a *BaseLoopAgent) Initialize(context.Context) error { return nil }

// Run 启动循环，直到调用 Stop 或上下文被取消。
func (a *BaseLoopAgent) Run(ctx context.Context) error {
	a.mu.Lock()
	if a.running {
		a.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	a.stop = cancel
	a.running = true
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		a.running = false
		a.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if a.RunOnce != nil {
			if err := a.RunOnce(ctx); err != nil {
				return err
			}
		}
	}
}

// Stop 取消当前正在运行的 Run 调用。
func (a *BaseLoopAgent) Stop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stop != nil {
		a.stop()
	}
	if a.OnStopRequested != nil {
		a.OnStopRequested()
	}
}
