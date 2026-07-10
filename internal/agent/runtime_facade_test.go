package agent

import (
	"context"
	"encoding/json"
	"reflect"
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
		{Seq: 10, Role: "user", Content: "<qq_message>绗竴鏉?/qq_message>"},
		{Seq: 11, Role: "user", Content: "<qq_message>绗簩鏉?/qq_message>"},
	})

	for _, expected := range []string{
		"<ledger_batch>",
		"[10] user\n<qq_message>绗竴鏉?/qq_message>",
		"[11] user\n<qq_message>绗簩鏉?/qq_message>",
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
		Result: agentruntime.ToolResult{Kind: "control", Content: "wait completed"},
	}}
	if shouldPersistAssistant(message, executions) {
		t.Fatal("wait assistant call must not enter persistent model context")
	}
	if shouldPersistToolResult(executions[0]) {
		t.Fatal("wait tool output must not enter persistent model context")
	}
}

func TestUnavailableToolExecutionIsNotPersistedInModelContext(t *testing.T) {
	execution := agentruntime.ToolExecution{
		Call:   agentruntime.ToolCall{ID: "call-1", Name: "act", Arguments: map[string]any{"action": "novel_app"}},
		Result: agentruntime.ToolResult{Kind: "business", Content: `{"ok":false,"error":"INVOKE_TOOL_NOT_AVAILABLE"}`},
	}
	if shouldPersistToolResult(execution) {
		t.Fatal("unavailable tool errors should not teach the model a stale capability map")
	}
}

func TestSkippedSideEffectErrorsStopContinuationAndPersistence(t *testing.T) {
	for _, errorCode := range []string{"STALE_ROUND_TOOL_SKIPPED", "DUPLICATE_SEND_MESSAGE_SKIPPED"} {
		execution := agentruntime.ToolExecution{
			Call:   agentruntime.ToolCall{ID: "call-1", Name: "send_message"},
			Result: agentruntime.ToolResult{Kind: "business", Content: `{"ok":false,"error":"` + errorCode + `"}`},
		}
		if shouldContinueAfterTool([]agentruntime.ToolExecution{execution}) {
			t.Fatalf("%s should stop the current root round", errorCode)
		}
		if shouldPersistToolResult(execution) {
			t.Fatalf("%s should not enter model context", errorCode)
		}
	}
}

func TestSuccessfulActEnterExecutionIsPersistedInModelContext(t *testing.T) {
	execution := agentruntime.ToolExecution{
		Call:   agentruntime.ToolCall{ID: "call-1", Name: "act", Arguments: map[string]any{"action": "enter", "query": "novel"}},
		Result: agentruntime.ToolResult{Kind: "control", Content: `{"ok":true,"enteredApp":"novel","screenResult":"{\"ok\":true}"}`},
	}
	if !shouldPersistToolResult(execution) {
		t.Fatal("successful enter should be visible to the next model round")
	}
}

func TestRootToolsExposeConcreteRootTools(t *testing.T) {
	tools := rootTools(&config.Config{}, agentruntime.NewToolCatalog(sendMessageTool{}), roottools.NewSession(nil), NewEventQueue())
	definitions := tools.Definitions()
	names := definitionNamesForTest(definitions)
	want := []string{"enter", "back_to_portal", "help", "wait", "send_message"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("root tools should expose concrete tools, got %#v", names)
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

func TestPreemptiveCountIgnoresStoryRecall(t *testing.T) {
	queue := NewEventQueue()
	queue.Enqueue(AgentEvent{Type: "story_recall_completed"})
	if got := queue.PreemptiveCount(); got != 0 {
		t.Fatalf("story recall should not stale-block side effects, got %d", got)
	}
	queue.Enqueue(AgentEvent{Type: "napcat_private_message"})
	if got := queue.PreemptiveCount(); got != 1 {
		t.Fatalf("QQ messages should stale-block side effects, got %d", got)
	}
}

func TestStoryRecallDoesNotTriggerRootRoundByItself(t *testing.T) {
	if eventTriggersRootRound("story_recall_completed") {
		t.Fatal("story recall should enrich context without starting another root round")
	}
	if !eventTriggersRootRound("napcat_private_message") {
		t.Fatal("QQ messages should trigger root rounds")
	}
}

func TestEventCoalesceWindowOnlyAppliesToQQMessages(t *testing.T) {
	cfg := config.EventCoalesceConfig{GroupWindowMs: 1200, PrivateWindowMs: 400}
	if got := eventCoalesceWindow(cfg, []AgentEvent{{Type: "news_article_ingested"}}); got != 0 {
		t.Fatalf("news should not be delayed by QQ coalesce window, got %s", got)
	}
	if got := eventCoalesceWindow(cfg, []AgentEvent{{Type: "napcat_group_message"}}); got != 1200*time.Millisecond {
		t.Fatalf("group window mismatch: %s", got)
	}
	if got := eventCoalesceWindow(cfg, []AgentEvent{{Type: "napcat_group_message"}, {Type: "napcat_private_message"}}); got != 400*time.Millisecond {
		t.Fatalf("private messages should use shorter window when mixed, got %s", got)
	}
}

func TestRenderGroupBanNoticeMarksSelfMute(t *testing.T) {
	rendered := renderGroupBanNotice(AgentEvent{
		Type: "napcat_group_ban_notice",
		Data: map[string]any{
			"groupId":    "1001",
			"userId":     "42",
			"operatorId": "24",
			"subType":    "ban",
			"duration":   600,
			"isSelf":     true,
		},
		At: time.Date(2026, 7, 10, 12, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*60*60)),
	})
	for _, expected := range []string{`<qq_group_notice type="group_ban"`, "你被禁言了", "10 分钟", "操作者 QQ：24", "群号：1001"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("rendered notice missing %q:\n%s", expected, rendered)
		}
	}
}

func TestWaitForEventCoalesceCollectsBurst(t *testing.T) {
	events := NewEventQueue()
	runtime := &AgentRuntime{
		cfg: &config.Config{Server: config.ServerConfig{Agent: config.AgentConfig{
			EventCoalesce: config.EventCoalesceConfig{Enabled: true, GroupWindowMs: 5, PrivateWindowMs: 2, MaxWindowMs: 40, MaxEvents: 8},
		}}},
		events: events,
	}
	events.Enqueue(AgentEvent{Type: "napcat_group_message", Data: map[string]any{"rawMessage": "1"}})
	go func() {
		time.Sleep(2 * time.Millisecond)
		events.Enqueue(AgentEvent{Type: "napcat_group_message", Data: map[string]any{"rawMessage": "2"}})
	}()
	runtime.waitForEventCoalesce()
	drained := events.DequeueAll()
	if len(drained) != 2 {
		t.Fatalf("expected burst messages to coalesce, got %d: %#v", len(drained), drained)
	}
}

func TestSelfContinuationReminderIsEphemeral(t *testing.T) {
	runtime := &AgentRuntime{
		rootMessages:      []agentruntime.Message{{Role: "user", Content: "real context"}},
		autonomousPending: true,
	}
	messages, autonomous := runtime.rootRoundMessages()
	if !autonomous || len(messages) != 2 || !strings.Contains(messages[1].Content, "rhythm_signal") {
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

func TestAutonomousIdleWakeRequiresRealIdleRuntime(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.Local)
	lastActivity := now.Add(-11 * time.Minute)
	runtime := &AgentRuntime{
		cfg: &config.Config{Server: config.ServerConfig{Agent: config.AgentConfig{
			Autonomous: config.AutonomousConfig{Enabled: true, IdleDelayMs: int((10 * time.Minute).Milliseconds())},
		}}},
		events:       NewEventQueue(),
		initialized:  true,
		loopState:    "idle",
		lastActivity: &lastActivity,
	}
	if !runtime.shouldQueueAutonomousIdleWake(now, 10*time.Minute) {
		t.Fatal("idle initialized runtime should queue autonomous wake")
	}

	runtime.events.Enqueue(AgentEvent{Type: "napcat_private_message"})
	if runtime.shouldQueueAutonomousIdleWake(now, 10*time.Minute) {
		t.Fatal("pending external events must suppress autonomous wake")
	}
	runtime.events.DequeueAll()

	runtime.autonomousPending = true
	if runtime.shouldQueueAutonomousIdleWake(now, 10*time.Minute) {
		t.Fatal("existing autonomous pending wake must not be duplicated")
	}
	runtime.autonomousPending = false

	runtime.loopState = "calling_root_llm"
	if runtime.shouldQueueAutonomousIdleWake(now, 10*time.Minute) {
		t.Fatal("busy runtime must not queue autonomous wake")
	}
}

func TestAutonomousIdleWatchIntervalIsBounded(t *testing.T) {
	if got := autonomousIdleWatchInterval(time.Second); got != 5*time.Second {
		t.Fatalf("short idle delay should use minimum interval, got %s", got)
	}
	if got := autonomousIdleWatchInterval(time.Hour); got != time.Minute {
		t.Fatalf("long idle delay should use maximum interval, got %s", got)
	}
	if got := autonomousIdleWatchInterval(40 * time.Second); got != 10*time.Second {
		t.Fatalf("normal idle delay should use quarter interval, got %s", got)
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

type runtimeFacadeTestTool struct {
	name string
}

func (t runtimeFacadeTestTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: t.name, Description: "test tool", Parameters: agentruntime.ObjectSchema(map[string]any{})}
}

func (runtimeFacadeTestTool) Kind() string { return "business" }

func (runtimeFacadeTestTool) Execute(context.Context, agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	return agentruntime.ToolResult{Kind: "business", Content: `{"ok":true}`}, nil
}

func TestDetectAIToneIsAlwaysAvailable(t *testing.T) {
	cfg := &config.Config{}
	business := agentruntime.NewToolCatalog(runtimeFacadeTestTool{name: "detect_ai_tone"})
	session := roottools.NewSession([]string{"1001"})
	events := NewEventQueue()

	result, err := rootTools(cfg, business, session, events).Execute(context.Background(), agentruntime.ToolCall{
		ID:        "detect-1",
		Name:      "detect_ai_tone",
		Arguments: map[string]any{"text": "娴嬭瘯"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Content, "INVOKE_TOOL_NOT_AVAILABLE") {
		t.Fatalf("detect_ai_tone should be available in main state: %s", result.Content)
	}
	if !strings.Contains(result.Content, `"ok":true`) {
		t.Fatalf("unexpected result: %s", result.Content)
	}
}

func TestToolSideEffectClassification(t *testing.T) {
	if !toolCallHasSideEffect(agentruntime.ToolCall{Name: "invoke", Arguments: map[string]any{"tool": "send_message"}}) {
		t.Fatal("send_message must be protected as side-effecting")
	}
	if !toolCallHasSideEffect(agentruntime.ToolCall{Name: "bash"}) {
		t.Fatal("bash must be protected as side-effecting")
	}
	if !toolCallHasSideEffect(agentruntime.ToolCall{Name: "act", Arguments: map[string]any{"action": "novel_app", "action_text": "create_project"}}) {
		t.Fatal("novel project creation must be protected as side-effecting")
	}
	if toolCallHasSideEffect(agentruntime.ToolCall{Name: "act", Arguments: map[string]any{"action": "novel_app", "action_text": "screen"}}) {
		t.Fatal("novel screen should remain read-only")
	}
	if toolCallHasSideEffect(agentruntime.ToolCall{Name: "search_web"}) {
		t.Fatal("read-only web search should be replayable")
	}
}

func TestRecentSendDedupKeyUsesTargetAndMessage(t *testing.T) {
	call := agentruntime.ToolCall{Name: "send_message", Arguments: map[string]any{
		"targetType": "private",
		"targetId":   "461105039",
		"message":    "六周年 红包还是转账 挺甜啊",
	}}
	runtime := &AgentRuntime{}
	key := sendMessageDedupKey(call)
	if key == "" {
		t.Fatal("send_message dedup key should not be empty")
	}
	if runtime.recentSendExists(key, 2*time.Minute) {
		t.Fatal("empty recent send cache should not report duplicate")
	}
	runtime.recordRecentSend(call)
	if !runtime.recentSendExists(key, 2*time.Minute) {
		t.Fatal("same target and message should be treated as duplicate")
	}
	otherTarget := agentruntime.ToolCall{Name: "send_message", Arguments: map[string]any{
		"targetType": "group",
		"targetId":   "253631878",
		"message":    "六周年 红包还是转账 挺甜啊",
	}}
	if runtime.recentSendExists(sendMessageDedupKey(otherTarget), 2*time.Minute) {
		t.Fatal("same message to a different target should not be deduplicated")
	}
}

func TestRecentSendDedupNormalizesTonePrefix(t *testing.T) {
	first := agentruntime.ToolCall{Name: "send_message", Arguments: map[string]any{
		"targetType": "private",
		"targetId":   "461105039",
		"message":    "没有啊 小腻发了个百叶窗页面 我去截图看看里面是啥",
	}}
	second := agentruntime.ToolCall{Name: "send_message", Arguments: map[string]any{
		"targetType": "private",
		"targetId":   "461105039",
		"message":    "小腻发了个百叶窗页面 我去截图看看里面是啥",
	}}
	if sendMessageDedupKey(first) != sendMessageDedupKey(second) {
		t.Fatalf("tone prefix should not bypass send dedup:\n%q\n%q", sendMessageDedupKey(first), sendMessageDedupKey(second))
	}
}

func TestStaleSideEffectSkipDoesNotApplyToSendMessage(t *testing.T) {
	if shouldSkipStaleSideEffect("send_message", true, 1) {
		t.Fatal("send_message already chosen by the model should be sent even if a new event is queued")
	}
	if !shouldSkipStaleSideEffect("browser", true, 1) {
		t.Fatal("external side effects should still be skipped on stale rounds")
	}
	if !shouldSkipStaleSideEffect("bash", true, 1) {
		t.Fatal("terminal side effects should still be skipped on stale rounds")
	}
	if shouldSkipStaleSideEffect("novel_app", true, 1) {
		t.Fatal("personal app writes should be persisted even if a new event is queued")
	}
	if shouldSkipStaleSideEffect("search_web", false, 1) {
		t.Fatal("read-only tools should not be skipped as side effects")
	}
}

func TestToolExecutionKeyIncludesArguments(t *testing.T) {
	first := toolExecutionKey(agentruntime.ToolCall{
		ID:        "call-reused",
		Name:      "novel_app",
		Arguments: map[string]any{"action": "append_draft", "projectId": "novel-1", "text": "first"},
	})
	second := toolExecutionKey(agentruntime.ToolCall{
		ID:        "call-reused",
		Name:      "novel_app",
		Arguments: map[string]any{"action": "append_draft", "projectId": "novel-1", "text": "second"},
	})
	if first == second {
		t.Fatalf("tool execution key must not reuse prior result when arguments change: %s", first)
	}
	if !strings.HasPrefix(first, "call-reused:") || !strings.HasPrefix(second, "call-reused:") {
		t.Fatalf("execution key should retain provider call id for traceability: %q %q", first, second)
	}
}

func TestShouldStopAfterSuccessfulSendMessage(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call: agentruntime.ToolCall{
			Name: "invoke",
			Arguments: map[string]any{
				"tool":      "send_message",
				"arguments": map[string]any{"message": "next"},
			},
		},
		Result: agentruntime.ToolResult{Kind: "business", Content: `{"messageId":1}`},
	}}
	if shouldContinueAfterTool(executions) {
		t.Fatal("successful send_message must end the round until a new external event arrives")
	}
}

func TestShouldStopAfterPersonalAppWrite(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call: agentruntime.ToolCall{
			Name:      "novel_app",
			Arguments: map[string]any{"action_text": "create_project", "title": "闅忕瑪"},
		},
		Result: agentruntime.ToolResult{Kind: "business", Content: `{"ok":true,"project":{"id":"novel-1"}}`},
	}}
	if shouldContinueAfterTool(executions) {
		t.Fatal("successful personal app write must end the round until a new external event arrives")
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

func TestSuccessfulSendMessageToolPairStaysOutOfModelContext(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call:   agentruntime.ToolCall{ID: "call-send", Name: "send_message"},
		Result: agentruntime.ToolResult{Kind: "business", Content: `{"messageId":1}`},
	}}
	if shouldPersistToolResult(executions[0]) {
		t.Fatal("successful send_message tool result should stay out of model context; self qq_message records what was sent")
	}
	assistant := assistantForPersistence(agentruntime.Message{
		Role:      "assistant",
		ToolCalls: []agentruntime.ToolCall{{ID: executions[0].Call.ID, Name: "send_message"}},
	}, executions)
	if len(assistant.ToolCalls) != 0 {
		t.Fatalf("successful send_message tool call should be dropped from model context: %#v", assistant)
	}
	if shouldPersistAssistant(agentruntime.Message{
		Role:      "assistant",
		ToolCalls: []agentruntime.ToolCall{{ID: executions[0].Call.ID, Name: "send_message"}},
	}, executions) {
		t.Fatal("assistant send_message tool call should not be persisted when the send succeeded")
	}
}

func TestShouldContinueAfterAIToneBlockedSendMessage(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call:   agentruntime.ToolCall{Name: "send_message"},
		Result: agentruntime.ToolResult{Kind: "business", Content: `{"ok":false,"error":"AI_TONE_TOO_HIGH","prob":0.8}`},
	}}
	if !shouldContinueAfterTool(executions) {
		t.Fatal("AI tone blocked send_message should continue so the model can rewrite or wait")
	}
	if shouldPersistToolResult(executions[0]) {
		t.Fatal("AI tone blocked result should stay out of model context")
	}
	assistant := assistantForPersistence(agentruntime.Message{
		Role:      "assistant",
		ToolCalls: []agentruntime.ToolCall{{ID: executions[0].Call.ID, Name: "send_message"}},
	}, executions)
	if len(assistant.ToolCalls) != 0 {
		t.Fatalf("AI tone blocked assistant tool call should be dropped from model context: %#v", assistant)
	}
	reminder := aiToneBlockedReminderMessage()
	for _, forbidden := range []string{"prob", "0.8", "messageId", "AI_TONE_TOO_HIGH"} {
		if strings.Contains(reminder.Content, forbidden) {
			t.Fatalf("AI tone reminder should not include volatile failure details %q: %s", forbidden, reminder.Content)
		}
	}
}

func TestShouldStopAfterWait(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call:   agentruntime.ToolCall{Name: "wait"},
		Result: agentruntime.ToolResult{Kind: "control", Content: "wait completed"},
	}}
	if shouldContinueAfterTool(executions) {
		t.Fatal("wait must suspend the autonomous loop")
	}
}

func TestShouldStopAfterActWait(t *testing.T) {
	executions := []agentruntime.ToolExecution{{
		Call:   agentruntime.ToolCall{Name: "act", Arguments: map[string]any{"action": "wait"}},
		Result: agentruntime.ToolResult{Kind: "control", Content: "wait completed"},
	}}
	if shouldContinueAfterTool(executions) {
		t.Fatal("act(action=wait) must suspend the autonomous loop")
	}
}

func TestPlainWaitAssistantIsNotPersisted(t *testing.T) {
	if !isPlainWaitContent("wait") || !isPlainWaitContent(" wait.") {
		t.Fatal("plain wait text should be treated as an idle action")
	}
	if shouldPersistAssistant(agentruntime.Message{Role: "assistant", Content: "wait"}, nil) {
		t.Fatal("plain wait assistant content should not pollute chat history")
	}
	if shouldPersistAssistant(agentruntime.Message{Role: "assistant", Content: "I will wait a bit"}, nil) {
		t.Fatal("plain assistant content without a tool call was not sent and must not pollute chat history")
	}
}

func TestRecentAssistantActionRequiredReminderDetected(t *testing.T) {
	messages := []agentruntime.Message{
		{Role: "user", Content: "old"},
		{Role: "user", Content: `<system_reminder label="assistant_action_required">必须调用工具</system_reminder>`},
	}
	if !hasRecentAssistantActionRequiredReminder(messages) {
		t.Fatal("recent assistant action reminder should suppress another repair round")
	}
	messages = append(messages,
		agentruntime.Message{Role: "user", Content: "1"},
		agentruntime.Message{Role: "user", Content: "2"},
		agentruntime.Message{Role: "user", Content: "3"},
		agentruntime.Message{Role: "user", Content: "4"},
	)
	if hasRecentAssistantActionRequiredReminder(messages) {
		t.Fatal("old assistant action reminder should not suppress future repairs forever")
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

func TestBrowserResultReminderIncludesSummaryAndImagePath(t *testing.T) {
	message, ok := browserResultReminderMessage(
		`{"ok":true,"summary":"四扇百叶窗全部打开。","title":"No Ideas Here","url":"https://example.com","imagePath":"D:\\goGroup\\workspace\\qq-bot\\data\\browser-screenshots\\browser.png"}`,
		map[string]any{"task": "打开这个页面看看", "url": "https://example.com"},
		`<qq_message target_type="group" target_id="1001">alice (1): 看这个 https://example.com</qq_message>`,
	)
	if !ok {
		t.Fatal("browser result should produce a follow-up reminder")
	}
	for _, expected := range []string{"browser_result_ready", "source_qq_message", "requested_action", "四扇百叶窗全部打开", "imagePath:", "send_message"} {
		if !strings.Contains(message.Content, expected) {
			t.Fatalf("browser reminder missing %q:\n%s", expected, message.Content)
		}
	}
}

func TestWebSearchResultReminderIncludesOrigin(t *testing.T) {
	message, ok := webSearchResultReminderMessage(
		"这是搜索摘要。",
		map[string]any{"query": "https://example.com"},
		`<qq_message target_type="private" target_id="2">bob (2): 看这个</qq_message>`,
	)
	if !ok {
		t.Fatal("web search result should produce a follow-up reminder")
	}
	for _, expected := range []string{"web_search_result_ready", "source_qq_message", "requested_action", "这是搜索摘要"} {
		if !strings.Contains(message.Content, expected) {
			t.Fatalf("web search reminder missing %q:\n%s", expected, message.Content)
		}
	}
}

func definitionNamesForTest(definitions []agentruntime.ToolDefinition) []string {
	names := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		names = append(names, definition.Name)
	}
	return names
}

func TestLatestStoryRecallQueryUsesNewestQQMessage(t *testing.T) {
	messages := []agentruntime.Message{
		{Role: "user", Content: `<qq_message target_type="group" target_id="1001">alice (1): old topic</qq_message>`},
		{Role: "user", Content: "<system_reminder>褰撳墠鏃堕棿</system_reminder>"},
		{Role: "user", Content: `<qq_message target_type="private" target_id="2">
bob (2):
new topic
</qq_message>`},
	}
	if query := latestStoryRecallQuery(messages); query != "bob (2): new topic" {
		t.Fatalf("unexpected recall query: %q", query)
	}
}

func TestLatestStoryRecallQueryIgnoresSelfQQMessage(t *testing.T) {
	messages := []agentruntime.Message{
		{Role: "user", Content: `<qq_message target_type="group" target_id="1001">
alice (1):
new topic
</qq_message>`},
		{Role: "user", Content: `<qq_message self="true" target_type="group" target_id="1001">
帕秋莉 (180920020):
my reply
</qq_message>`},
	}
	if query := latestStoryRecallQuery(messages); query != "alice (1): new topic" {
		t.Fatalf("self qq_message should not drive story recall query, got %q", query)
	}
}

func TestTurnFocusReminderPinsLatestQQMessage(t *testing.T) {
	message := turnFocusReminder(`<qq_message target_type="group" target_id="1001">
alice (1):
新消息
</qq_message>`)
	for _, expected := range []string{"turn_focus", "本轮最新 QQ 消息", "先识别是谁说的", "只允许 send_message 或 wait", "不要调用个人工作台", "不要回头补更早", "新消息"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("turn focus reminder missing %q:\n%s", expected, message)
		}
	}
}
