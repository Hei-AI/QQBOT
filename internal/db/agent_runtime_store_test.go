package db

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openRuntimeTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "runtime.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestAgentStackPreservesToolCallOutputOrder(t *testing.T) {
	store := openRuntimeTestStore(t)
	store.AppendAgentStackItem(AgentStackItem{RuntimeKey: "root", Kind: "function_call", ToolCallID: "call-1", ToolName: "search_web", Content: map[string]any{"query": "test"}})
	store.AppendAgentStackItem(AgentStackItem{RuntimeKey: "root", Kind: "function_call_output", ToolCallID: "call-1", ToolName: "search_web", Content: `{"ok":true,"answer":"exact"}`})

	items := store.ListAgentStackItems("root", 0, 10)
	if len(items) != 2 || items[0].Kind != "function_call" || items[1].Kind != "function_call_output" {
		t.Fatalf("unexpected stack: %#v", items)
	}
	if items[1].Content != `{"ok":true,"answer":"exact"}` {
		t.Fatalf("tool output was changed: %#v", items[1].Content)
	}
}

func TestCompletedToolExecutionReturnsExactPriorResult(t *testing.T) {
	store := openRuntimeTestStore(t)
	item, acquired, err := store.BeginToolExecution(ToolExecutionItem{
		ExecutionKey: "call-1", ToolCallID: "call-1", ToolName: "send_message", SideEffect: true,
	}, time.Minute)
	if err != nil || !acquired {
		t.Fatalf("first lease failed: item=%#v acquired=%v err=%v", item, acquired, err)
	}
	store.CompleteToolExecution("call-1", `{"messageId":123}`, nil)

	existing, acquired, err := store.BeginToolExecution(ToolExecutionItem{
		ExecutionKey: "call-1", ToolCallID: "call-1", ToolName: "send_message", SideEffect: true,
	}, time.Minute)
	if err != nil || acquired || existing.Status != "completed" || existing.Result != `{"messageId":123}` {
		t.Fatalf("completed result should be replayed exactly: %#v acquired=%v err=%v", existing, acquired, err)
	}
}

func TestExpiredSideEffectExecutionBecomesUncertain(t *testing.T) {
	store := openRuntimeTestStore(t)
	_, acquired, err := store.BeginToolExecution(ToolExecutionItem{
		ExecutionKey: "call-side-effect", ToolCallID: "call-side-effect", ToolName: "send_message", SideEffect: true,
	}, time.Millisecond)
	if err != nil || !acquired {
		t.Fatalf("lease failed: acquired=%v err=%v", acquired, err)
	}
	time.Sleep(5 * time.Millisecond)

	existing, acquired, err := store.BeginToolExecution(ToolExecutionItem{
		ExecutionKey: "call-side-effect", ToolCallID: "call-side-effect", ToolName: "send_message", SideEffect: true,
	}, time.Minute)
	if err != nil || acquired || existing.Status != "uncertain" {
		t.Fatalf("expired side effect must not replay: %#v acquired=%v err=%v", existing, acquired, err)
	}
}

func TestAgentTaskClaimRetryAndComplete(t *testing.T) {
	store := openRuntimeTestStore(t)
	task, created, err := store.EnqueueAgentTask(AgentTaskItem{
		TaskKey: "story:1", TaskType: "story_batch", MaxAttempts: 2, Payload: map[string]any{"seq": 1},
	})
	if err != nil || !created || task.ID == 0 {
		t.Fatalf("enqueue failed: %#v created=%v err=%v", task, created, err)
	}
	claimed, ok := store.ClaimNextAgentTask("worker-1", time.Minute)
	if !ok || claimed.Status != "processing" || claimed.Attempt != 1 {
		t.Fatalf("claim failed: %#v", claimed)
	}
	retried := store.FinishAgentTask(claimed.ID, nil, errors.New("temporary"), time.Millisecond)
	if retried.Status != "pending" {
		t.Fatalf("task should retry: %#v", retried)
	}
	time.Sleep(5 * time.Millisecond)
	claimed, ok = store.ClaimNextAgentTask("worker-1", time.Minute)
	if !ok || claimed.Attempt != 2 {
		t.Fatalf("retry claim failed: %#v", claimed)
	}
	completed := store.FinishAgentTask(claimed.ID, map[string]any{"done": true}, nil, 0)
	if completed.Status != "completed" || completed.CompletedAt == nil {
		t.Fatalf("task should complete: %#v", completed)
	}
}

func TestExpiredSideEffectTaskBecomesUncertain(t *testing.T) {
	store := openRuntimeTestStore(t)
	task, _, err := store.EnqueueAgentTask(AgentTaskItem{
		TaskKey: "send:1", TaskType: "send", SideEffect: true, MaxAttempts: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok := store.ClaimNextAgentTask("worker", time.Millisecond)
	if !ok || claimed.ID != task.ID {
		t.Fatalf("claim failed: %#v", claimed)
	}
	time.Sleep(5 * time.Millisecond)
	if recovered := store.RecoverExpiredAgentTasks(time.Now()); recovered != 1 {
		t.Fatalf("expected one recovered task, got %d", recovered)
	}
	items := store.ListAgentTasks()
	if len(items) != 1 || items[0].Status != "uncertain" {
		t.Fatalf("side-effecting task must not replay: %#v", items)
	}
}
