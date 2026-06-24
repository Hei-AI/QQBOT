package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	roottools "QqBot/internal/agent/root"
	"QqBot/internal/agentruntime"
	"QqBot/internal/config"
	"QqBot/internal/db"
)

func TestStoryBatchScheduleDecision(t *testing.T) {
	now := time.Date(2026, 6, 6, 12, 0, 0, 0, time.Local)
	idleFlush := 2 * time.Minute

	shouldRun, retryAfter := storyBatchScheduleDecision(1, 24, now.Add(-30*time.Second), true, now, idleFlush)
	if shouldRun || retryAfter != 90*time.Second {
		t.Fatalf("single recent message should wait for idle flush: run=%v retry=%v", shouldRun, retryAfter)
	}

	shouldRun, retryAfter = storyBatchScheduleDecision(24, 24, now, true, now, idleFlush)
	if !shouldRun || retryAfter != 0 {
		t.Fatalf("full batch should run immediately: run=%v retry=%v", shouldRun, retryAfter)
	}

	shouldRun, retryAfter = storyBatchScheduleDecision(3, 24, now.Add(-idleFlush), true, now, idleFlush)
	if !shouldRun || retryAfter != 0 {
		t.Fatalf("idle partial batch should run: run=%v retry=%v", shouldRun, retryAfter)
	}

	shouldRun, retryAfter = storyBatchScheduleDecision(0, 24, time.Time{}, false, now, idleFlush)
	if shouldRun || retryAfter != 0 {
		t.Fatalf("empty ledger should remain idle: run=%v retry=%v", shouldRun, retryAfter)
	}
}

func TestRenderStoryLedgerBatchIncludesCompleteLinearContext(t *testing.T) {
	rendered := renderStoryLedgerBatch([]db.StoryLedgerItem{
		{Seq: 10, Role: "user", Content: "<qq_message>第一条</qq_message>"},
		{Seq: 11, Role: "user", Content: "<qq_message>第二条</qq_message>"},
	})

	for _, expected := range []string{
		"<ledger_batch>",
		"[10] user\n<qq_message>第一条</qq_message>",
		"[11] user\n<qq_message>第二条</qq_message>",
		"</ledger_batch>",
	} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("rendered batch missing %q:\n%s", expected, rendered)
		}
	}
}

func TestWaitTimeoutWakeTriggersRootRound(t *testing.T) {
	if wakeTriggersRootRound("wait_timeout") {
		t.Fatal("wait timeout must not trigger an LLM round unless cache keepalive is explicitly enabled")
	}
	if !wakeTriggersRootRound("continue_after_tool") {
		t.Fatal("tool continuation wake should trigger an LLM round")
	}
	if !wakeTriggersRootRound("self_continuation") {
		t.Fatal("self continuation wake should trigger an LLM round")
	}
	if wakeTriggersRootRound("") {
		t.Fatal("unclassified wake should remain silent")
	}
}

func TestWaitExecutionIsNotPersistedInModelContext(t *testing.T) {
	message := agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{ID: "wait-1", Name: "wait"}}}
	executions := []agentruntime.ToolExecution{{
		Call:   message.ToolCalls[0],
		Result: agentruntime.ToolResult{Kind: "control", Content: "休息结束了"},
	}}
	if shouldPersistAssistant(message, executions) {
		t.Fatal("wait assistant call must not enter persistent model context")
	}
	if shouldPersistToolResult(executions[0]) {
		t.Fatal("wait tool output must not enter persistent model context")
	}
}

func TestAutonomousRoundDecisionLimitsBurstAndRestartsAfterCooldown(t *testing.T) {
	rounds := 0
	for expected := 1; expected <= 4; expected++ {
		allowed, next := autonomousRoundDecision(rounds, 4, false)
		if !allowed || next != expected {
			t.Fatalf("round %d should be allowed, got allowed=%v next=%d", expected, allowed, next)
		}
		rounds = next
	}
	if allowed, next := autonomousRoundDecision(rounds, 4, false); allowed || next != 4 {
		t.Fatalf("fifth consecutive round should enter cooldown: allowed=%v next=%d", allowed, next)
	}
	if allowed, next := autonomousRoundDecision(rounds, 4, true); !allowed || next != 1 {
		t.Fatalf("round after cooldown should restart burst: allowed=%v next=%d", allowed, next)
	}
}

func TestExternalEventSuppressesSameBatchSelfContinuation(t *testing.T) {
	events := []AgentEvent{
		{Type: "napcat_group_message"},
		{Type: "wake", Data: map[string]any{"reason": "self_continuation"}},
	}
	if !hasExternalAgentEvent(events) {
		t.Fatal("real QQ event must take priority over self continuation")
	}
	if hasExternalAgentEvent([]AgentEvent{{Type: "wake", Data: map[string]any{"reason": "self_continuation"}}}) {
		t.Fatal("wake-only batch should remain autonomous")
	}
}

func TestSelfContinuationReminderIsEphemeral(t *testing.T) {
	runtime := &AgentRuntime{
		rootMessages:      []agentruntime.Message{{Role: "user", Content: "真实上下文"}},
		autonomousPending: true,
	}
	messages, autonomous := runtime.rootRoundMessages()
	if !autonomous || len(messages) != 2 || !strings.Contains(messages[1].Content, "self_continuation") {
		t.Fatalf("expected one ephemeral reminder: %#v", messages)
	}
	if len(runtime.rootMessages) != 1 {
		t.Fatalf("self continuation must not enter persisted root history: %#v", runtime.rootMessages)
	}
	next, autonomous := runtime.rootRoundMessages()
	if autonomous || len(next) != 1 {
		t.Fatalf("ephemeral reminder should be consumed once: %#v", next)
	}
}

func TestRootToolSchemaStaysStableAcrossAppTransitions(t *testing.T) {
	cfg := &config.Config{}
	business := agentruntime.NewToolCatalog(
		sendMessageTool{},
		calculateTool{},
	)
	session := roottools.NewSession([]string{"1001"})
	events := NewEventQueue()

	before, err := json.Marshal(rootTools(cfg, business, session, events).Definitions())
	if err != nil {
		t.Fatal(err)
	}
	if result := session.EnterApp("calc"); result["ok"] != true {
		t.Fatalf("failed to enter calc: %#v", result)
	}
	after, err := json.Marshal(rootTools(cfg, business, session, events).Definitions())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("root tool schema changed across state transition:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestToolSideEffectClassification(t *testing.T) {
	if !toolCallHasSideEffect(agentruntime.ToolCall{Name: "invoke", Arguments: map[string]any{"tool": "send_message"}}) {
		t.Fatal("send_message must be protected as side-effecting")
	}
	if !toolCallHasSideEffect(agentruntime.ToolCall{Name: "bash"}) {
		t.Fatal("bash must be protected as side-effecting")
	}
	if toolCallHasSideEffect(agentruntime.ToolCall{Name: "search_web"}) {
		t.Fatal("read-only web search should be replayable")
	}
}

func TestShouldStopAfterSuccessfulSendMessage(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call: agentruntime.ToolCall{
			Name: "invoke",
			Arguments: map[string]any{
				"tool":      "send_message",
				"arguments": map[string]any{"message": "继续看下一章"},
			},
		},
		Result: agentruntime.ToolResult{Kind: "business", Content: `{"messageId":1}`},
	}}
	if shouldContinueAfterTool(executions) {
		t.Fatal("successful send_message must end the round until a new external event arrives")
	}
}

func TestShouldContinueAfterFailedSendMessage(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call:   agentruntime.ToolCall{Name: "send_message"},
		Result: agentruntime.ToolResult{Kind: "business", Content: `{"ok":false,"error":"NapCat disconnected"}`},
	}}
	if !shouldContinueAfterTool(executions) {
		t.Fatal("failed send_message should continue so the model can recover")
	}
}

func TestShouldStopAfterWait(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call:   agentruntime.ToolCall{Name: "wait"},
		Result: agentruntime.ToolResult{Kind: "control", Content: "休息结束了"},
	}}
	if shouldContinueAfterTool(executions) {
		t.Fatal("wait must suspend the autonomous loop")
	}
}

func TestShouldContinueAfterToolFailure(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call:   agentruntime.ToolCall{Name: "search_web"},
		Result: agentruntime.ToolResult{Kind: "business", Content: `{"ok":false,"error":"temporary failure"}`},
	}}
	if !shouldContinueAfterTool(executions) {
		t.Fatal("a non-wait tool failure must continue so the model can inspect the error and recover")
	}
}

func TestShouldStopAfterUnknownToolFailure(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call:   agentruntime.ToolCall{Name: "send_message"},
		Result: agentruntime.ToolResult{Kind: "control", Content: `{"ok":false,"error":"UNKNOWN_TOOL"}`},
	}}
	if shouldContinueAfterTool(executions) {
		t.Fatal("permanent unknown-tool failures must not create an autonomous retry loop")
	}
}

func TestMigrateParallelRootMessagesRemovesLegacyNavigationOnly(t *testing.T) {
	messages := []agentruntime.Message{
		{Role: "user", Content: "<system_reminder>\n你进入了 QQ 群 1001 节点\n</system_reminder>"},
		{Role: "user", Content: "<system_reminder>\n[跨状态通知]\nQQ 群 1002 有消息\n</system_reminder>"},
		{Role: "user", Content: `<qq_message target_type="group" target_id="1001">alice: hello</qq_message>`},
		{Role: "user", Content: "<conversation_summary>保留摘要</conversation_summary>"},
	}
	migrated := migrateParallelRootMessages(messages)
	if len(migrated) != 2 {
		t.Fatalf("unexpected migrated messages: %#v", migrated)
	}
	if !strings.Contains(migrated[0].Content, "target_id") || !strings.Contains(migrated[1].Content, "conversation_summary") {
		t.Fatalf("real context should be preserved: %#v", migrated)
	}
}

func TestLatestStoryRecallQueryUsesNewestQQMessage(t *testing.T) {
	messages := []agentruntime.Message{
		{Role: "user", Content: `<qq_message target_type="group" target_id="1001">alice (1): old topic</qq_message>`},
		{Role: "user", Content: "<system_reminder>当前时间</system_reminder>"},
		{Role: "user", Content: `<qq_message target_type="private" target_id="2">
bob (2):
new topic
</qq_message>`},
	}
	if query := latestStoryRecallQuery(messages); query != "bob (2): new topic" {
		t.Fatalf("unexpected recall query: %q", query)
	}
}
