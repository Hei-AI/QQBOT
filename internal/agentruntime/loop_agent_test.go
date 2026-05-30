package agentruntime

import (
	"context"
	"testing"
	"time"
)

func TestBaseLoopAgentRunsAgainImmediatelyAfterBlockingRunOnce(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	calls := make(chan int, 2)
	count := 0
	agent := BaseLoopAgent{
		RunOnce: func(context.Context) error {
			count++
			calls <- count
			if count == 1 {
				<-release
			} else {
				cancel()
			}
			return nil
		},
	}

	done := make(chan error, 1)
	go func() {
		done <- agent.Run(ctx)
	}()

	select {
	case got := <-calls:
		if got != 1 {
			t.Fatalf("first call mismatch: %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("first RunOnce did not start")
	}
	close(release)
	select {
	case got := <-calls:
		if got != 2 {
			t.Fatalf("second call mismatch: %d", got)
		}
	case <-time.After(time.Second):
		t.Fatal("loop did not run again after blocking RunOnce released")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("loop did not stop after context cancellation")
	}
}

func TestBaseLoopAgentStopCallsOnStopRequested(t *testing.T) {
	called := make(chan struct{}, 1)
	agent := BaseLoopAgent{OnStopRequested: func() { called <- struct{}{} }}
	agent.Stop()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("OnStopRequested was not called")
	}
}
