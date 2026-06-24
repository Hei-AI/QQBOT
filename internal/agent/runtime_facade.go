package agent

import (
	roottools "QqBot/internal/agent/root"
	"QqBot/internal/agentruntime"
	"QqBot/internal/capabilities/contextsummary"
	"QqBot/internal/capabilities/messaging"
	"QqBot/internal/capabilities/terminal"
	"QqBot/internal/common"
	"QqBot/internal/config"
	"QqBot/internal/db"
	"QqBot/internal/llm"
	"QqBot/internal/prompts"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
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

func (q *EventQueue) WaitSignal(ctx context.Context) bool {
	if q.Count() > 0 {
		return true
	}
	select {
	case <-q.wakeup:
		return true
	case <-ctx.Done():
		return false
	}
}

func wakeTriggersRootRound(reason string) bool {
	return reason == "continue_after_tool" || reason == "self_continuation"
}

// AgentRuntime 是当前可执行服务中的根/Story 运行时门面。
//
// 它维护仪表盘状态，并从进入的消息中创建轻量 Story 记录；
// 更完整的 internal/agent 运行时可用于后续更深层的接线。
type AgentRuntime struct {
	cfg                *config.Config
	store              *db.Store
	events             *EventQueue
	llm                *llm.LLMClient
	rootKernel         agentruntime.ReActKernel
	storyKernel        agentruntime.ReActKernel
	summarizer         contextsummary.Operation
	rootTools          *agentruntime.ToolCatalog
	storyTools         *agentruntime.ToolCatalog
	session            *roottools.Session
	rootMessages       []agentruntime.Message
	storyMessages      []agentruntime.Message
	mu                 sync.Mutex
	initialized        bool
	loopState          string
	lastError          *RuntimeError
	lastActivity       *time.Time
	contextItems       []DashboardContextItem
	lastToolCall       *DashboardToolCall
	lastToolResult     *string
	lastLlmCall        *DashboardLlmCall
	storyLastSeq       int
	terminal           *terminal.Service
	lastCompaction     *time.Time
	lastWakeReminderAt *time.Time
	lastStoryRecallAt  int
	lastStoryRecallKey string
	injectedStoryIDs   map[string]bool
	storyBatchRunning  bool
	storyIdleTimer     *time.Timer
	storyRecallRunning bool
	autonomousRounds   int
	autonomousPending  bool
	autonomousTimer    *time.Timer
	autonomousUntil    *time.Time
}

type RuntimeError struct {
	Name      string `json:"name"`
	Message   string `json:"message"`
	UpdatedAt string `json:"updatedAt"`
}

type DashboardContextItem struct {
	Kind      string `json:"kind"`
	Label     string `json:"label"`
	Preview   string `json:"preview"`
	Truncated bool   `json:"truncated"`
}

type DashboardToolCall struct {
	Name             string `json:"name"`
	ArgumentsPreview string `json:"argumentsPreview"`
	UpdatedAt        string `json:"updatedAt"`
}

type DashboardLlmCall struct {
	Provider                string   `json:"provider"`
	Model                   string   `json:"model"`
	AssistantContentPreview string   `json:"assistantContentPreview"`
	ToolCallNames           []string `json:"toolCallNames"`
	TotalTokens             *int     `json:"totalTokens"`
	UpdatedAt               string   `json:"updatedAt"`
}

func NewAgentRuntime(cfg *config.Config, store *db.Store, events *EventQueue, llmClient *llm.LLMClient, sender messaging.Sender) *AgentRuntime {
	rootModel := llmModelAdapter{client: llmClient, usage: "agent"}
	browserModel := llmModelAdapter{client: llmClient, usage: "browserAgent"}
	storyModel := llmModelAdapter{client: llmClient, usage: "storyAgent"}
	summarizerModel := llmModelAdapter{client: llmClient, usage: "contextSummarizer"}
	terminalService, _ := terminal.NewService(terminal.Config{
		InitialCwd:        cfg.Server.Agent.Terminal.InitialCWD,
		CommandTimeout:    time.Duration(cfg.Server.Agent.Terminal.CommandTimeoutMs) * time.Millisecond,
		PreviewBytes:      cfg.Server.Agent.Terminal.PreviewBytes,
		MaxOutputBytes:    cfg.Server.Agent.Terminal.MaxOutputBytes,
		MaxCommandLength:  cfg.Server.Agent.Terminal.MaxCommandLength,
		ReadOutputMaxSize: cfg.Server.Agent.Terminal.ReadOutputMaxSize,
		Shell:             cfg.Server.Agent.Terminal.Shell,
	}, store)
	session := roottools.NewSession(cfg.Server.Napcat.ListenGroupIDs)
	runtime := &AgentRuntime{
		cfg:              cfg,
		store:            store,
		events:           events,
		llm:              llmClient,
		rootKernel:       agentruntime.ReActKernel{Model: rootModel},
		storyKernel:      agentruntime.ReActKernel{Model: storyModel},
		summarizer:       contextsummary.Operation{Model: summarizerModel},
		rootTools:        buildBusinessTools(cfg, store, sender, terminalService, llmClient),
		storyTools:       buildStoryTools(cfg, store),
		session:          session,
		terminal:         terminalService,
		loopState:        "starting",
		injectedStoryIDs: map[string]bool{},
	}
	if snapshot, ok := store.AgentRuntimeSnapshot(); ok {
		runtime.rootMessages = migrateParallelRootMessages(snapshot.RootMessages)
		runtime.storyMessages = snapshot.StoryMessages
		runtime.storyLastSeq = snapshot.StoryLastSeq
		runtime.session.Restore(snapshot.Session)
	}
	if tool, ok := runtime.rootTools.Get("search_web"); ok {
		if searchTool, ok := tool.(*WebSearchTaskAgentTool); ok {
			searchTool.SetModel(rootModel)
			searchTool.SetTaskContext(
				func() string { return createSystemPrompt(cfg) },
				func() []agentruntime.Message {
					runtime.mu.Lock()
					defer runtime.mu.Unlock()
					return append([]agentruntime.Message(nil), runtime.rootMessages...)
				},
				func() *agentruntime.ToolCatalog {
					return rootTools(cfg, runtime.rootTools, runtime.session, runtime.events)
				},
			)
		}
	}
	if tool, ok := runtime.rootTools.Get("browser"); ok {
		if browserTool, ok := tool.(*BrowserTaskAgentTool); ok {
			browserTool.SetModel(browserModel)
			browserTool.SetTaskContext(
				func() string { return createSystemPrompt(cfg) },
				func() []agentruntime.Message {
					runtime.mu.Lock()
					defer runtime.mu.Unlock()
					return append([]agentruntime.Message(nil), runtime.rootMessages...)
				},
			)
		}
	}
	return runtime
}

func migrateParallelRootMessages(messages []agentruntime.Message) []agentruntime.Message {
	out := make([]agentruntime.Message, 0, len(messages))
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if message.Role == "user" && isLegacyNavigationReminder(content) {
			continue
		}
		out = append(out, message)
	}
	return out
}

func isLegacyNavigationReminder(content string) bool {
	return strings.Contains(content, "[跨状态通知]") ||
		strings.Contains(content, "你已进入 QQ 群聊节点") ||
		strings.Contains(content, "你已进入 QQ 私聊节点") ||
		(strings.HasPrefix(content, "<system_reminder>") &&
			strings.Contains(content, "你进入了 ") &&
			strings.Contains(content, " 节点"))
}

func (a *AgentRuntime) Start(ctx context.Context) {
	failedTools, uncertainTools := a.store.RecoverExpiredToolExecutions(time.Now())
	recoveredTasks := a.store.RecoverExpiredAgentTasks(time.Now())
	if failedTools > 0 || uncertainTools > 0 || recoveredTasks > 0 {
		a.store.Log("warn", "Recovered stale agent runtime work", map[string]any{
			"event":                "agent.runtime.recovered",
			"failedToolExecutions": failedTools,
			"uncertainSideEffects": uncertainTools,
			"recoveredAgentTasks":  recoveredTasks,
		})
	}
	a.mu.Lock()
	now := time.Now()
	a.initialized = true
	a.loopState = "idle"
	a.lastActivity = &now
	if len(a.rootMessages) == 0 {
		a.contextItems = append(a.contextItems, contextItem("llm_message", "system", createSystemPrompt(a.cfg)))
		for _, message := range a.focusMessagesForStateLocked("portal") {
			a.appendRootMessageLocked(message)
			a.contextItems = append(a.contextItems, contextItem("system_reminder", "state_focus", message.Content))
		}
	} else {
		for _, message := range a.rootMessages {
			a.contextItems = append(a.contextItems, contextItem("llm_message", message.Role, message.Content))
		}
	}
	a.mu.Unlock()

	go a.runAgentTaskWorker(ctx)
	a.scheduleStoryBatch()
	go func() {
		continueRound := false
		for {
			if ctx.Err() != nil {
				return
			}
			shouldRunRoot := a.consumePendingEvents()
			if !shouldRunRoot && !continueRound {
				a.markRootLoopIdle()
				if !a.events.WaitSignal(ctx) {
					return
				}
				continue
			}
			continueRound = a.runRootRound()
			a.markRootLoopIdle()
		}
	}()
}

func (a *AgentRuntime) runRootLoopOnce() {
	a.consumePendingEvents()
	a.runRootRound()
	a.markRootLoopIdle()
}

func (a *AgentRuntime) markRootLoopIdle() {
	a.mu.Lock()
	now := time.Now()
	a.loopState = "idle"
	a.lastActivity = &now
	a.mu.Unlock()
	a.persistSnapshot()
}

func (a *AgentRuntime) consumePendingEvents() bool {
	a.mu.Lock()
	a.loopState = "consuming_events"
	a.mu.Unlock()
	events := a.events.DequeueAll()
	a.store.Log("info", "Agent consuming events", map[string]any{"event": "agent.root.consume_events", "count": len(events)})
	shouldRunRoot := false
	hasExternalEvent := hasExternalAgentEvent(events)
	if hasExternalEvent {
		a.resetAutonomousRounds()
	}
	for _, event := range events {
		if event.Type == "wake" {
			reason := common.AsString(event.Data["reason"])
			switch reason {
			case "continue_after_tool":
				a.store.Log("info", "Agent internal continuation wake", map[string]any{"event": "agent.root.continue_after_tool"})
				shouldRunRoot = wakeTriggersRootRound(reason)
				continue
			case "wait_timeout":
				a.store.Log("info", "Agent cache keepalive wake", map[string]any{
					"event":      "agent.root.cache_keepalive",
					"intervalMs": a.cfg.Server.Agent.WaitToolMaxWaitMs,
				})
				shouldRunRoot = a.cfg.Server.Agent.CacheKeepaliveEnabled
				continue
			case "self_continuation":
				if hasExternalEvent {
					continue
				}
				afterCooldown, _ := event.Data["afterCooldown"].(bool)
				if a.prepareAutonomousRound(afterCooldown) {
					a.store.Log("info", "Agent autonomous continuation wake", map[string]any{
						"event":            "agent.root.self_continuation",
						"consecutiveRound": a.autonomousRoundCount(),
						"maxConsecutive":   a.cfg.Server.Agent.Autonomous.MaxConsecutiveRounds,
						"afterCooldown":    afterCooldown,
					})
					shouldRunRoot = true
				}
				continue
			}
		}
		rendered := a.renderEventContext(event)
		a.store.AppendAgentStackItem(db.AgentStackItem{
			RuntimeKey: "root",
			Kind:       "runtime_input",
			Role:       eventMessageRole(event.Type),
			Content:    rendered,
			Metadata: map[string]any{
				"eventType": event.Type,
				"eventAt":   common.ISO(event.At),
			},
		})
		focused := true
		switch event.Type {
		case "napcat_group_message":
			focused = a.session.OnGroupMessage(
				common.AsString(event.Data["groupId"]),
				common.AsString(event.Data["userId"]),
				common.AsString(event.Data["nickname"]),
				common.AsString(event.Data["rawMessage"]),
				intValue(event.Data["messageSeq"]),
				intValue(event.Data["messageId"]),
				event.At,
			)
		case "napcat_private_message":
			focused = a.session.OnPrivateMessage(
				common.AsString(event.Data["userId"]),
				common.AsString(event.Data["nickname"]),
				common.AsString(event.Data["rawMessage"]),
				intValue(event.Data["messageSeq"]),
				intValue(event.Data["messageId"]),
				event.At,
			)
		case "news_article_ingested":
			focused = a.session.OnNewsArticle()
		case "story_recall_completed":
			focused = true
		}
		currentState := a.session.State()
		a.store.Log("info", "Agent event normalized", map[string]any{"event": "agent.root.event", "type": event.Type, "focused": focused, "state": currentState, "preview": trimPreview(rendered, 240)})
		a.appendContext(contextItem(eventContextKind(event.Type), event.Type, rendered))
		if event.Type == "wake" {
			continue
		}
		if focused {
			a.appendRootMessage(agentruntime.Message{Role: eventMessageRole(event.Type), Content: rendered})
			if event.Type == "napcat_group_message" || event.Type == "napcat_private_message" {
				a.store.AddStoryLedger("root", eventMessageRole(event.Type), rendered)
			}
			shouldRunRoot = true
		}
	}
	a.scheduleStoryBatch()
	return shouldRunRoot
}

func hasExternalAgentEvent(events []AgentEvent) bool {
	for _, event := range events {
		if event.Type != "wake" {
			return true
		}
	}
	return false
}

func (a *AgentRuntime) runRootRound() bool {
	if a.llm == nil || len(a.rootMessages) == 0 {
		a.store.Log("warn", "Root LLM round skipped", map[string]any{"event": "agent.root.llm.skipped", "hasLlm": a.llm != nil, "messageCount": len(a.rootMessages)})
		return false
	}
	a.mu.Lock()
	a.loopState = "calling_root_llm"
	a.mu.Unlock()
	a.appendWakeReminderIfNeeded()
	a.scheduleStoryRecall()
	messages, autonomous := a.rootRoundMessages()
	tools := rootTools(a.cfg, a.rootTools, a.session, a.events)
	tools.SetObserver(a)
	a.store.Log("info", "Root LLM round start", map[string]any{"event": "agent.root.llm.start", "round": 1, "messageCount": len(messages), "state": a.session.State(), "availableTools": a.session.AvailableTools(), "autonomous": autonomous})
	result, err := a.rootKernel.RunRound(context.Background(), agentruntime.RoundInput{
		SystemPrompt: createSystemPrompt(a.cfg),
		Messages:     messages,
		Tools:        tools,
		ToolChoice:   "required",
	})
	if err != nil {
		a.store.Log("error", "Root LLM round failed", map[string]any{"event": "agent.root.llm.failed", "round": 1, "error": err.Error()})
		a.setRuntimeError(err)
		return false
	}
	staleRound := a.events.Count() > 0
	if staleRound {
		a.store.Log("info", "Root LLM round marked stale", map[string]any{"event": "agent.root.llm.stale", "pendingEventCount": a.events.Count()})
	}
	a.store.Log("info", "Root LLM response", map[string]any{"event": "agent.root.llm.response", "round": 1, "provider": result.Completion.Provider, "model": result.Completion.Model, "assistant": trimPreview(result.Assistant.Content, 500), "toolCalls": toolCallNames(result.Assistant.ToolCalls), "reasoningCaptured": strings.TrimSpace(result.Assistant.ReasoningContent) != ""})
	a.appendRootRoundStack(result)
	if shouldPersistAssistant(result.Assistant, result.ToolExecutions) {
		a.appendRootMessage(assistantForPersistence(result.Assistant, result.ToolExecutions))
		a.appendContext(contextItem("llm_message", "assistant", result.Assistant.Content))
	}
	for _, execution := range result.ToolExecutions {
		a.store.Log("info", "Root tool executed", map[string]any{"event": "agent.root.tool.executed", "tool": execution.Call.Name, "arguments": execution.Call.Arguments, "result": trimPreview(execution.Result.Content, 500)})
		if shouldPersistToolResult(execution) {
			a.appendRootMessage(agentruntime.Message{Role: "tool", ToolCallID: execution.Call.ID, Content: execution.Result.Content})
		}
		a.recordToolExecution(execution)
		for _, message := range a.postToolFocusMessages(execution) {
			a.appendRootMessage(message)
			a.appendContext(contextItem("system_instruction", "state_focus", message.Content))
		}
		if reminder := sentMessageReminder(execution); reminder != "" {
			a.appendRootMessage(agentruntime.Message{Role: "user", Content: reminder})
			a.appendContext(contextItem("system_reminder", "message_sent", reminder))
		}
	}
	a.recordLLMCall(result.Completion)
	a.maybeCompactRoot(result.Completion)
	if shouldContinueAfterTool(result.ToolExecutions) {
		return true
	}
	if len(result.ToolExecutions) == 0 && strings.TrimSpace(result.Assistant.Content) != "" {
		reason := "disabled"
		if staleRound {
			reason = "disabled_stale_round_pending_events"
		}
		a.store.Log("info", "Assistant content fallback skipped", map[string]any{"event": "agent.root.fallback_send.skipped", "reason": reason, "message": trimPreview(result.Assistant.Content, 300)})
		if !staleRound {
			a.appendRootMessage(agentruntime.Message{Role: "user", Content: prompts.AssistantActionRequiredReminder(result.Assistant.Content)})
			a.appendContext(contextItem("system_reminder", "assistant_action_required", prompts.AssistantActionRequiredReminder(result.Assistant.Content)))
			return true
		}
	}
	return false
}

func (a *AgentRuntime) BeforeTool(_ context.Context, call agentruntime.ToolCall, definition agentruntime.ToolDefinition, kind string) (*agentruntime.ToolResult, error) {
	sideEffect := toolCallHasSideEffect(call)
	execution, acquired, err := a.store.BeginToolExecution(db.ToolExecutionItem{
		ExecutionKey: call.ID,
		RuntimeKey:   "root",
		ToolCallID:   call.ID,
		ToolName:     resolvedToolCallName(call),
		Arguments:    call.Arguments,
		Status:       "processing",
		SideEffect:   sideEffect,
		LeaseOwner:   "root",
	}, 2*time.Minute)
	if err != nil {
		return nil, err
	}
	if acquired {
		return nil, nil
	}
	if execution.Status == "completed" {
		return &agentruntime.ToolResult{Kind: kind, Content: execution.Result}, nil
	}
	payload := map[string]any{
		"ok":         false,
		"error":      "TOOL_EXECUTION_BLOCKED",
		"tool":       definition.Name,
		"status":     execution.Status,
		"message":    execution.ErrorMessage,
		"sideEffect": execution.SideEffect,
	}
	data, _ := json.Marshal(payload)
	return &agentruntime.ToolResult{Kind: kind, Content: string(data)}, nil
}

func (a *AgentRuntime) AfterTool(_ context.Context, call agentruntime.ToolCall, _ agentruntime.ToolDefinition, result agentruntime.ToolResult, err error) {
	a.store.CompleteToolExecution(call.ID, result.Content, err)
}

func toolCallHasSideEffect(call agentruntime.ToolCall) bool {
	name := resolvedToolCallName(call)
	switch name {
	case "send_message", "bash", "browser", "searchMagnetFromWeb":
		return true
	default:
		return false
	}
}

func resolvedToolCallName(call agentruntime.ToolCall) string {
	if call.Name != "invoke" {
		return call.Name
	}
	if name := invocationToolName(call.Arguments); name != "" {
		return name
	}
	return call.Name
}

func (a *AgentRuntime) appendRootRoundStack(result agentruntime.RoundResult) {
	if strings.TrimSpace(result.Assistant.Content) != "" || strings.TrimSpace(result.Assistant.ReasoningContent) != "" {
		a.store.AppendAgentStackItem(db.AgentStackItem{
			RuntimeKey: "root",
			Kind:       "assistant_output",
			Role:       "assistant",
			Content: map[string]any{
				"content":          result.Assistant.Content,
				"reasoningContent": result.Assistant.ReasoningContent,
			},
			Metadata: map[string]any{"provider": result.Completion.Provider, "model": result.Completion.Model},
		})
	}
	for _, call := range result.Assistant.ToolCalls {
		a.store.AppendAgentStackItem(db.AgentStackItem{
			RuntimeKey: "root",
			Kind:       "function_call",
			Role:       "assistant",
			ToolCallID: call.ID,
			ToolName:   resolvedToolCallName(call),
			Content:    call.Arguments,
		})
	}
	for _, execution := range result.ToolExecutions {
		a.store.AppendAgentStackItem(db.AgentStackItem{
			RuntimeKey: "root",
			Kind:       "function_call_output",
			Role:       "tool",
			ToolCallID: execution.Call.ID,
			ToolName:   resolvedToolCallName(execution.Call),
			Content:    execution.Result.Content,
		})
	}
}

func (a *AgentRuntime) rootRoundMessages() ([]agentruntime.Message, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	messages := append([]agentruntime.Message(nil), a.rootMessages...)
	if !a.autonomousPending {
		return messages, false
	}
	messages = append(messages, agentruntime.Message{Role: "user", Content: prompts.SelfContinuationReminder()})
	a.autonomousPending = false
	return messages, true
}

func (a *AgentRuntime) prepareAutonomousRound(afterCooldown bool) bool {
	if !a.cfg.Server.Agent.Autonomous.Enabled {
		return false
	}
	maxRounds := a.cfg.Server.Agent.Autonomous.MaxConsecutiveRounds
	if maxRounds <= 0 {
		maxRounds = 4
	}
	a.mu.Lock()
	if afterCooldown {
		a.autonomousTimer = nil
		a.autonomousUntil = nil
	}
	allowed, nextRounds := autonomousRoundDecision(a.autonomousRounds, maxRounds, afterCooldown)
	if !allowed {
		a.mu.Unlock()
		a.scheduleAutonomousCooldown()
		return false
	}
	a.autonomousRounds = nextRounds
	a.autonomousPending = true
	a.mu.Unlock()
	a.appendContext(contextItem("system_reminder", "self_continuation", prompts.SelfContinuationReminder()))
	return true
}

func autonomousRoundDecision(current, max int, afterCooldown bool) (bool, int) {
	if max <= 0 {
		max = 1
	}
	if afterCooldown {
		current = 0
	}
	if current >= max {
		return false, current
	}
	return true, current + 1
}

func (a *AgentRuntime) autonomousRoundCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.autonomousRounds
}

func (a *AgentRuntime) scheduleAutonomousCooldown() {
	cooldown := time.Duration(a.cfg.Server.Agent.Autonomous.CooldownMs) * time.Millisecond
	if cooldown <= 0 {
		cooldown = 5 * time.Minute
	}
	a.mu.Lock()
	if a.autonomousTimer != nil {
		a.mu.Unlock()
		return
	}
	until := time.Now().Add(cooldown)
	a.autonomousUntil = &until
	a.autonomousTimer = time.AfterFunc(cooldown, func() {
		a.events.Enqueue(AgentEvent{
			Type: "wake",
			Data: map[string]any{"reason": "self_continuation", "afterCooldown": true},
		})
	})
	a.mu.Unlock()
	a.store.Log("info", "Agent autonomous continuation cooling down", map[string]any{
		"event":      "agent.root.self_continuation.cooldown",
		"cooldownMs": int(cooldown.Milliseconds()),
		"until":      common.ISO(until),
	})
}

func (a *AgentRuntime) resetAutonomousRounds() {
	a.mu.Lock()
	if a.autonomousTimer != nil {
		a.autonomousTimer.Stop()
		a.autonomousTimer = nil
	}
	a.autonomousRounds = 0
	a.autonomousPending = false
	a.autonomousUntil = nil
	a.mu.Unlock()
}

func hasToolExecution(executions []agentruntime.ToolExecution, name string) bool {
	for _, execution := range executions {
		if execution.Call.Name == name {
			return true
		}
	}
	return false
}

func shouldContinueAfterTool(executions []agentruntime.ToolExecution) bool {
	if len(executions) == 0 || hasToolExecution(executions, "wait") {
		return false
	}
	allSuccessfulSends := true
	for _, execution := range executions {
		if resolvedToolCallName(execution.Call) != "send_message" || toolResultHasError(execution.Result.Content) {
			allSuccessfulSends = false
		}
		var payload map[string]any
		if json.Unmarshal([]byte(execution.Result.Content), &payload) != nil {
			continue
		}
		switch common.AsString(payload["error"]) {
		case "UNKNOWN_TOOL", "INVOKE_TOOL_NOT_FOUND":
			return false
		}
	}
	if allSuccessfulSends {
		return false
	}
	return true
}

func toolResultHasError(content string) bool {
	var payload map[string]any
	return json.Unmarshal([]byte(content), &payload) == nil && strings.TrimSpace(common.AsString(payload["error"])) != ""
}

func shouldPersistAssistant(message agentruntime.Message, executions []agentruntime.ToolExecution) bool {
	persisted := assistantForPersistence(message, executions)
	return strings.TrimSpace(persisted.Content) != "" || len(persisted.ToolCalls) > 0
}

func assistantForPersistence(message agentruntime.Message, executions []agentruntime.ToolExecution) agentruntime.Message {
	control := map[string]bool{}
	for _, execution := range executions {
		if execution.Result.Kind == "control" {
			control[execution.Call.ID] = true
		}
	}
	if len(control) == 0 {
		return message
	}
	out := message
	out.ToolCalls = nil
	for _, call := range message.ToolCalls {
		if !control[call.ID] {
			out.ToolCalls = append(out.ToolCalls, call)
		}
	}
	return out
}

func shouldPersistToolResult(execution agentruntime.ToolExecution) bool {
	return execution.Result.Kind != "control"
}

func (a *AgentRuntime) appendWakeReminderIfNeeded() {
	now := time.Now()
	a.mu.Lock()
	if a.lastWakeReminderAt != nil && sameMinute(*a.lastWakeReminderAt, now) {
		a.mu.Unlock()
		return
	}
	a.lastWakeReminderAt = &now
	a.mu.Unlock()
	a.appendRootMessage(agentruntime.Message{Role: "user", Content: prompts.WakeReminder(now)})
	a.appendContext(contextItem("system_reminder", "wake", prompts.WakeReminder(now)))
}

func sameMinute(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day() && a.Hour() == b.Hour() && a.Minute() == b.Minute()
}

func (a *AgentRuntime) scheduleStoryRecall() {
	a.mu.Lock()
	if a.storyRecallRunning {
		a.mu.Unlock()
		return
	}
	a.storyRecallRunning = true
	a.mu.Unlock()
	go func() {
		defer func() {
			a.mu.Lock()
			a.storyRecallRunning = false
			a.mu.Unlock()
		}()
		a.triggerStoryRecallIfNeeded()
	}()
}

func (a *AgentRuntime) triggerStoryRecallIfNeeded() {
	a.mu.Lock()
	messageCount := len(a.rootMessages)
	if messageCount == 0 || a.lastStoryRecallAt == messageCount {
		a.mu.Unlock()
		return
	}
	messages := append([]agentruntime.Message(nil), a.rootMessages...)
	a.lastStoryRecallAt = messageCount
	query := latestStoryRecallQuery(messages)
	if query == "" || query == a.lastStoryRecallKey {
		a.mu.Unlock()
		return
	}
	a.lastStoryRecallKey = query
	a.mu.Unlock()
	searchTool, ok := a.rootTools.Get("search_memory")
	if !ok {
		return
	}
	a.mu.Lock()
	stale := latestStoryRecallQuery(a.rootMessages) != query
	a.mu.Unlock()
	if stale {
		a.store.Log("info", "Story recall skipped", map[string]any{"event": "agent.story_recall.skipped", "reason": "stale_query"})
		return
	}
	topK := a.cfg.Server.Agent.Story.Recall.TopK
	if topK <= 0 {
		topK = 2
	}
	recallArgs := map[string]any{"query": query, "limit": topK}
	result, err := searchTool.Execute(context.Background(), agentruntime.ToolCall{ID: common.NewID() + ":story-recall", Name: "search_memory", Arguments: recallArgs})
	if err != nil {
		a.store.Log("warn", "Story recall search failed", map[string]any{"event": "agent.story_recall.search_failed", "error": err.Error()})
		return
	}
	content := strings.TrimSpace(result.Content)
	if content == "" || content == "[]" || content == "null" {
		return
	}
	filtered := a.filterNewStoryRecallContent(content)
	if filtered == "" {
		return
	}
	a.events.Enqueue(AgentEvent{Type: "story_recall_completed", Data: map[string]any{"content": filtered}})
	a.store.Log("info", "Story recall enqueued", map[string]any{"event": "agent.story_recall.enqueued", "query": trimPreview(query, 160), "preview": trimPreview(filtered, 300)})
}

func latestStoryRecallQuery(messages []agentruntime.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		content := common.AsString(messages[i].Content)
		start := strings.LastIndex(content, "<qq_message")
		if start < 0 {
			continue
		}
		openEnd := strings.Index(content[start:], ">")
		if openEnd < 0 {
			continue
		}
		bodyStart := start + openEnd + 1
		closeOffset := strings.Index(content[bodyStart:], "</qq_message>")
		if closeOffset < 0 {
			continue
		}
		query := strings.Join(strings.Fields(content[bodyStart:bodyStart+closeOffset]), " ")
		runes := []rune(query)
		if len(runes) > 500 {
			query = string(runes[len(runes)-500:])
		}
		return query
	}
	return ""
}

func (a *AgentRuntime) filterNewStoryRecallContent(content string) string {
	var items []map[string]any
	if err := json.Unmarshal([]byte(content), &items); err != nil {
		return content
	}
	fresh := []map[string]any{}
	threshold := a.cfg.Server.Agent.Story.Recall.ScoreThreshold
	a.mu.Lock()
	if a.injectedStoryIDs == nil {
		a.injectedStoryIDs = map[string]bool{}
	}
	for _, item := range items {
		if threshold > 0 {
			score, ok := floatValue(item["score"])
			if !ok || score < threshold {
				continue
			}
		}
		id := common.AsString(item["id"])
		if id != "" && a.injectedStoryIDs[id] {
			continue
		}
		if id != "" {
			a.injectedStoryIDs[id] = true
		}
		fresh = append(fresh, item)
	}
	a.mu.Unlock()
	if len(fresh) == 0 {
		return ""
	}
	data, _ := json.Marshal(fresh)
	return string(data)
}

func (a *AgentRuntime) postToolFocusMessages(execution agentruntime.ToolExecution) []agentruntime.Message {
	switch execution.Call.Name {
	case "enter":
		if !toolResultOK(execution.Result.Content) {
			return nil
		}
		return a.focusMessagesForState(enteredStateID(execution.Call.Arguments))
	case "back":
		if !toolResultOK(execution.Result.Content) {
			return nil
		}
		return a.focusMessagesForState(a.session.State())
	case "invoke":
		toolName := invocationToolName(execution.Call.Arguments)
		if toolName == "open_ithome_article" && toolResultOK(execution.Result.Content) {
			if message, ok := a.ithomeArticleDetailMessage(invocationArguments(execution.Call.Arguments)); ok {
				return []agentruntime.Message{message}
			}
		}
	case "open_ithome_article":
		if toolResultOK(execution.Result.Content) {
			if message, ok := a.ithomeArticleDetailMessage(execution.Call.Arguments); ok {
				return []agentruntime.Message{message}
			}
		}
	}
	return nil
}

func (a *AgentRuntime) ithomeFocusMessages() []agentruntime.Message {
	limit := a.cfg.Server.News.Ithome.RecentArticleLimit
	if limit <= 0 {
		limit = 10
	}
	cursor, hasCursor := a.store.NewsFeedCursor("ithome")
	isNewMode := false
	hiddenNewCount := 0
	var articles []db.NewsArticle
	if hasCursor {
		totalNew := a.store.CountNewsArticlesNewerThanCursor("ithome", cursor)
		if totalNew > 0 {
			isNewMode = true
			articles = a.store.ListNewsArticlesNewerThanCursor("ithome", cursor, limit)
			hiddenNewCount = totalNew - len(articles)
			if hiddenNewCount < 0 {
				hiddenNewCount = 0
			}
		}
	}
	if len(articles) == 0 {
		articles = a.store.ListNewsArticlesLatest("ithome", limit)
	}
	if len(articles) > 0 {
		a.store.UpsertNewsFeedCursor("ithome", articles[0].ID, articles[0].PublishedAt)
	}
	summaries := make([]prompts.ArticleSummary, 0, len(articles))
	for _, article := range articles {
		summaries = append(summaries, prompts.ArticleSummary{
			ID:              article.ID,
			Title:           article.Title,
			PublishedAtText: formatTime(article.PublishedAt),
			URL:             article.URL,
			RSSSummary:      article.RSSSummary,
		})
	}
	content := prompts.ITHomeArticleListInstruction("IT 之家", isNewMode, hiddenNewCount, summaries)
	return []agentruntime.Message{{Role: "user", Content: content}}
}

func (a *AgentRuntime) focusMessagesForState(stateID string) []agentruntime.Message {
	messages := []agentruntime.Message{a.stateReminderMessage(stateID)}
	messages = append(messages, a.stateOnFocusMessages(stateID)...)
	return messages
}

func (a *AgentRuntime) focusMessagesForStateLocked(stateID string) []agentruntime.Message {
	if stateID == "main" {
		return nil
	}
	return []agentruntime.Message{a.stateReminderMessage(stateID)}
}

func (a *AgentRuntime) stateOnFocusMessages(stateID string) []agentruntime.Message {
	switch {
	case stateID == "portal":
		return nil
	case stateID == "ithome":
		return a.ithomeFocusMessages()
	case strings.HasPrefix(stateID, "qq_group:"), strings.HasPrefix(stateID, "qq_private:"):
		return a.chatFocusMessages(stateID)
	default:
		return nil
	}
}

func (a *AgentRuntime) stateReminderMessage(stateID string) agentruntime.Message {
	snapshot := a.session.Snapshot()
	displayName := common.AsString(snapshot["focusedStateDisplayName"])
	if stateID != a.session.State() {
		displayName = stateDisplayNameFromSessionSnapshot(snapshot, stateID)
	}
	if displayName == "" {
		displayName = stateID
	}
	children := []prompts.StateReminderChild{}
	if stateID == a.session.State() {
		if items, ok := snapshot["children"].([]roottools.ChildState); ok {
			for _, child := range items {
				children = append(children, prompts.StateReminderChild{
					ID:          child.ID,
					DisplayName: child.DisplayName,
					Description: child.Description,
				})
			}
		}
	}
	apps := []prompts.StateReminderApp(nil)
	if stateID == "portal" {
		apps = []prompts.StateReminderApp{
			{ID: "calc", DisplayName: "计算器"},
			{ID: "terminal", DisplayName: "终端"},
		}
	}
	return agentruntime.Message{Role: "user", Content: prompts.StateSystemReminder(displayName, children, apps)}
}

func stateDisplayNameFromSessionSnapshot(snapshot map[string]any, stateID string) string {
	if stack, ok := snapshot["stateStack"].([]map[string]string); ok {
		for _, item := range stack {
			if item["id"] == stateID {
				return item["displayName"]
			}
		}
	}
	if children, ok := snapshot["children"].([]roottools.ChildState); ok {
		for _, child := range children {
			if child.ID == stateID {
				return child.DisplayName
			}
		}
	}
	return ""
}

func (a *AgentRuntime) chatFocusMessages(stateID string) []agentruntime.Message {
	if groupID := strings.TrimPrefix(stateID, "qq_group:"); groupID != stateID {
		if messages := a.session.ConsumeGroupFocusMessages(groupID); len(messages) > 0 {
			return groupUnreadFocusMessages(messages)
		}
	}
	if userID := strings.TrimPrefix(stateID, "qq_private:"); userID != stateID {
		if messages, useRecent := a.session.ConsumePrivateFocusMessages(userID); !useRecent {
			return privateUnreadFocusMessages(messages)
		}
	}
	limit := a.cfg.Server.Napcat.StartupContextRecentMessageCount
	if limit <= 0 {
		limit = 20
	}
	data := a.store.Snapshot()
	selected := make([]db.NapcatMessageItem, 0, limit)
	for _, item := range data.NapcatMessages {
		if !messageBelongsToState(item, stateID) {
			continue
		}
		selected = append(selected, item)
		if len(selected) > limit {
			selected = selected[1:]
		}
	}
	if len(selected) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("<system_reminder>\n你已进入 QQ 对话节点。以下是最近消息上下文：\n</system_reminder>\n")
	for i, item := range selected {
		if i > 0 {
			b.WriteString("\n")
		}
		nickname := common.AsString(item.Nickname)
		if nickname == "" {
			nickname = stringPtrValue(item.Nickname)
		}
		if nickname == "" {
			nickname = "未知用户"
		}
		b.WriteString(prompts.QQMessageAt(nickname, stringPtrValue(item.UserID), item.RawMessage, timePtrValue(item.EventTime)))
	}
	return []agentruntime.Message{{Role: "user", Content: b.String()}}
}

func groupUnreadFocusMessages(messages []roottools.GroupUnreadMessage) []agentruntime.Message {
	if len(messages) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("<system_reminder>\n你已进入 QQ 群聊节点。以下是进入前积累的未读消息：\n</system_reminder>\n")
	for i, item := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		nickname := strings.TrimSpace(item.Nickname)
		if nickname == "" {
			nickname = "未知用户"
		}
		b.WriteString(prompts.QQMessageAt(nickname, item.UserID, item.RawMessage, item.EventTime))
	}
	return []agentruntime.Message{{Role: "user", Content: b.String()}}
}

func privateUnreadFocusMessages(messages []roottools.PrivateUnreadMessage) []agentruntime.Message {
	if len(messages) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("<system_reminder>\n你已进入 QQ 私聊节点。以下是进入前积累的未读消息：\n</system_reminder>\n")
	for i, item := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		nickname := strings.TrimSpace(item.Nickname)
		if nickname == "" {
			nickname = "未知用户"
		}
		b.WriteString(prompts.QQMessageAt(nickname, item.UserID, item.RawMessage, item.EventTime))
	}
	return []agentruntime.Message{{Role: "user", Content: b.String()}}
}

func (a *AgentRuntime) ithomeArticleDetailMessage(args map[string]any) (agentruntime.Message, bool) {
	articleID := intValue(args["articleId"])
	if articleID == 0 {
		return agentruntime.Message{}, false
	}
	for _, article := range a.store.Snapshot().NewsArticles {
		if article.ID != articleID {
			continue
		}
		content := strings.TrimSpace(article.Content)
		fallbackToSummary := false
		if content == "" {
			content = strings.TrimSpace(article.RSSSummary)
			fallbackToSummary = true
		}
		maxChars := a.cfg.Server.News.Ithome.ArticleMaxChars
		if maxChars <= 0 {
			maxChars = 8000
		}
		truncated := false
		if len([]rune(content)) > maxChars {
			runes := []rune(content)
			content = string(runes[:maxChars])
			truncated = true
		}
		return agentruntime.Message{Role: "user", Content: prompts.ITHomeArticleDetail(article.Title, formatTime(article.PublishedAt), article.URL, content, fallbackToSummary, truncated, maxChars)}, true
	}
	return agentruntime.Message{}, false
}

func enteredStateID(args map[string]any) string {
	kind := common.AsString(args["kind"])
	id := common.AsString(args["id"])
	if id == "" {
		id = common.AsString(args["stateId"])
	}
	switch kind {
	case "qq_group":
		if id == "" || strings.HasPrefix(id, "qq_group:") {
			return id
		}
		return "qq_group:" + id
	case "qq_private":
		if id == "" || strings.HasPrefix(id, "qq_private:") {
			return id
		}
		return "qq_private:" + id
	case "ithome":
		return kind
	default:
		return id
	}
}

func messageBelongsToState(item db.NapcatMessageItem, stateID string) bool {
	if groupID := strings.TrimPrefix(stateID, "qq_group:"); groupID != stateID {
		return item.MessageType == "group" && stringPtrValue(item.GroupID) == groupID
	}
	if userID := strings.TrimPrefix(stateID, "qq_private:"); userID != stateID {
		return item.MessageType == "private" && stringPtrValue(item.UserID) == userID
	}
	return false
}

func invocationToolName(args map[string]any) string {
	toolName := common.AsString(args["tool"])
	if toolName == "" {
		toolName = common.AsString(args["toolName"])
	}
	return toolName
}

func invocationArguments(args map[string]any) map[string]any {
	nested, _ := args["arguments"].(map[string]any)
	if nested != nil {
		return nested
	}
	if raw := common.AsString(args["arguments"]); strings.TrimSpace(raw) != "" {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil && parsed != nil {
			return parsed
		}
	}
	out := map[string]any{}
	for key, value := range args {
		if key == "tool" || key == "toolName" || key == "arguments" {
			continue
		}
		out[key] = value
	}
	return out
}

func toolResultOK(content string) bool {
	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return true
	}
	ok, exists := data["ok"]
	if !exists {
		return true
	}
	value, _ := ok.(bool)
	return value
}

func toolResultString(content, key string) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return ""
	}
	return common.AsString(data[key])
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func timePtrValue(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}

func sentMessageReminder(execution agentruntime.ToolExecution) string {
	args := execution.Call.Arguments
	switch execution.Call.Name {
	case "invoke":
		if invocationToolName(execution.Call.Arguments) != "send_message" {
			return ""
		}
		args = invocationArguments(execution.Call.Arguments)
	case "send_message":
	default:
		return ""
	}
	message := common.AsString(args["message"])
	imagePath := common.AsString(args["imagePath"])
	if strings.TrimSpace(message) == "" && strings.TrimSpace(imagePath) != "" {
		message = "[浏览器截图]"
	}
	if strings.TrimSpace(message) == "" {
		return ""
	}
	return fmt.Sprintf("<system_reminder>你刚刚已经通过 send_message 对外发送了这条消息：%s</system_reminder>", message)
}

func (a *AgentRuntime) maybeCompactRoot(completion agentruntime.Completion) {
	if completion.Usage == nil {
		return
	}
	a.mu.Lock()
	messages := append([]agentruntime.Message(nil), a.rootMessages...)
	a.mu.Unlock()
	plan, ok := createCompactionPlan(messages, completion.Usage.TotalTokens, a.cfg.Server.Agent.ContextCompactionTotalTokenThreshold)
	if !ok {
		return
	}
	summary, err := a.summarizeWithRetry(createSystemPrompt(a.cfg), plan.summarize, prompts.RootContextSummaryReminder())
	if err != nil {
		a.setRuntimeError(err)
		return
	}
	if strings.TrimSpace(summary) == "" {
		return
	}
	now := time.Now()
	a.mu.Lock()
	a.rootMessages = append([]agentruntime.Message{{Role: "user", Content: prompts.ConversationSummary(summary)}}, plan.keep...)
	a.lastCompaction = &now
	a.contextItems = append(a.contextItems, contextItem("system_reminder", "root_compaction", prompts.ConversationSummary(summary)))
	a.mu.Unlock()
	a.store.AppendAgentStackItem(db.AgentStackItem{
		RuntimeKey: "root",
		Kind:       "memory_event",
		Role:       "user",
		Content: map[string]any{
			"summary":            summary,
			"summarizedMessages": len(plan.summarize),
			"retainedMessages":   len(plan.keep),
		},
		Metadata: map[string]any{"event": "root_compaction"},
	})
}

func (a *AgentRuntime) maybeCompactStory(completion agentruntime.Completion) {
	if completion.Usage == nil {
		return
	}
	a.mu.Lock()
	messages := append([]agentruntime.Message(nil), a.storyMessages...)
	a.mu.Unlock()
	plan, ok := createCompactionPlan(messages, completion.Usage.TotalTokens, a.cfg.Server.Agent.Story.ContextCompactionTotalTokenThreshold)
	if !ok {
		return
	}
	summary, err := a.summarizeWithRetry(prompts.StoryAgentSystemPrompt(), plan.summarize, prompts.StoryContextSummaryReminder())
	if err != nil {
		a.setRuntimeError(err)
		return
	}
	if strings.TrimSpace(summary) == "" {
		return
	}
	now := time.Now()
	a.mu.Lock()
	a.storyMessages = append([]agentruntime.Message{{Role: "user", Content: prompts.ConversationSummary(summary)}}, plan.keep...)
	a.lastCompaction = &now
	a.mu.Unlock()
}

func (a *AgentRuntime) summarizeWithRetry(systemPrompt string, messages []agentruntime.Message, reminder string) (string, error) {
	backoff := time.Duration(a.cfg.Server.Agent.LLMRetryBackoffMs) * time.Millisecond
	if backoff <= 0 {
		backoff = time.Second
	}
	for {
		summary, err := a.summarizer.Summarize(context.Background(), systemPrompt, messages, reminder)
		if err != nil {
			a.store.Log("warn", "Context summary failed; scheduling retry", map[string]any{"event": "agent.context_summary.retry_scheduled", "retryBackoffMs": int(backoff / time.Millisecond), "error": err.Error()})
			time.Sleep(backoff)
			continue
		}
		return summary, nil
	}
}

type compactPlan struct {
	summarize []agentruntime.Message
	keep      []agentruntime.Message
}

func createCompactionPlan(messages []agentruntime.Message, totalTokens, totalTokenThreshold int) (compactPlan, bool) {
	if len(messages) == 0 || totalTokens <= totalTokenThreshold {
		return compactPlan{}, false
	}
	keepCount := calculateCompactionKeepCount(len(messages))
	if keepCount == 0 {
		return compactPlan{}, false
	}
	cut := len(messages) - keepCount
	cut = extendCompactionCutIndexForAssistantToolBoundary(messages, cut)
	if cut <= 0 || cut >= len(messages) {
		return compactPlan{}, false
	}
	return compactPlan{summarize: append([]agentruntime.Message(nil), messages[:cut]...), keep: append([]agentruntime.Message(nil), messages[cut:]...)}, true
}

func calculateCompactionKeepCount(totalMessageCount int) int {
	if totalMessageCount <= 1 {
		return 0
	}
	return (totalMessageCount + 9) / 10
}

func extendCompactionCutIndexForAssistantToolBoundary(messages []agentruntime.Message, cutIndex int) int {
	if cutIndex <= 0 || cutIndex >= len(messages) {
		return cutIndex
	}
	previous := messages[cutIndex-1]
	if previous.Role != "assistant" || len(previous.ToolCalls) == 0 {
		return cutIndex
	}
	ids := map[string]bool{}
	for _, call := range previous.ToolCalls {
		ids[call.ID] = true
	}
	last := -1
	for i := cutIndex; i < len(messages); i++ {
		if messages[i].Role == "tool" && ids[messages[i].ToolCallID] {
			last = i
		}
	}
	if last >= 0 {
		return last + 1
	}
	return cutIndex
}

func (a *AgentRuntime) appendRootMessage(message agentruntime.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.appendRootMessageLocked(message)
}

func (a *AgentRuntime) appendRootMessageLocked(message agentruntime.Message) {
	a.rootMessages = append(a.rootMessages, message)
}

func (a *AgentRuntime) persistSnapshot() {
	a.mu.Lock()
	snapshot := db.AgentRuntimeSnapshot{
		RootMessages:  append([]agentruntime.Message(nil), a.rootMessages...),
		StoryMessages: append([]agentruntime.Message(nil), a.storyMessages...),
		Session:       a.session.Export(),
		StoryLastSeq:  a.storyLastSeq,
	}
	data, _ := json.Marshal(snapshot)
	snapshot.Fingerprint = fmt.Sprintf("%x", sha256.Sum256(data))
	a.mu.Unlock()
	a.store.SaveAgentRuntimeSnapshot(snapshot)
}

func (a *AgentRuntime) ResetPersistedState() {
	a.mu.Lock()
	if a.storyIdleTimer != nil {
		a.storyIdleTimer.Stop()
		a.storyIdleTimer = nil
	}
	if a.autonomousTimer != nil {
		a.autonomousTimer.Stop()
		a.autonomousTimer = nil
	}
	a.rootMessages = nil
	a.storyMessages = nil
	a.storyLastSeq = 0
	a.contextItems = nil
	a.lastCompaction = nil
	a.lastToolCall = nil
	a.lastToolResult = nil
	a.lastLlmCall = nil
	a.lastStoryRecallAt = 0
	a.injectedStoryIDs = map[string]bool{}
	a.autonomousRounds = 0
	a.autonomousPending = false
	a.autonomousUntil = nil
	a.mu.Unlock()
	a.session.Portal()
	a.store.ResetAgentRuntimeState()
}

func (a *AgentRuntime) scheduleStoryBatch() {
	a.mu.Lock()
	if a.storyBatchRunning {
		a.mu.Unlock()
		return
	}
	lastSeq := a.storyLastSeq
	a.mu.Unlock()

	pendingCount := a.store.CountStoryLedgerAfter("root", lastSeq)
	latest, hasLatest := a.store.LatestStoryLedger("root")
	batchSize := a.cfg.Server.Agent.Story.BatchSize
	if batchSize <= 0 {
		batchSize = 24
	}
	idleFlush := time.Duration(a.cfg.Server.Agent.Story.IdleFlushMs) * time.Millisecond
	if idleFlush <= 0 {
		idleFlush = 2 * time.Minute
	}
	shouldRun, retryAfter := storyBatchScheduleDecision(
		pendingCount,
		batchSize,
		latest.CreatedAt,
		hasLatest,
		time.Now(),
		idleFlush,
	)

	a.mu.Lock()
	if a.storyIdleTimer != nil {
		a.storyIdleTimer.Stop()
		a.storyIdleTimer = nil
	}
	if !shouldRun {
		if retryAfter > 0 {
			a.storyIdleTimer = time.AfterFunc(retryAfter, a.scheduleStoryBatch)
		}
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()

	taskKey := fmt.Sprintf("story_batch:%d:%d", lastSeq, latest.Seq)
	_, created, err := a.store.EnqueueAgentTask(db.AgentTaskItem{
		TaskKey:     taskKey,
		TaskType:    "story_batch",
		SideEffect:  false,
		MaxAttempts: 3,
		Payload: map[string]any{
			"storyLastSeq": lastSeq,
			"latestSeq":    latest.Seq,
			"notifyAgent":  false,
		},
	})
	if err != nil {
		a.store.Log("error", "Story batch task enqueue failed", map[string]any{"event": "agent.task.enqueue.failed", "taskType": "story_batch", "error": err.Error()})
		return
	}
	if created {
		a.store.Log("info", "Story batch task enqueued", map[string]any{"event": "agent.task.enqueued", "taskType": "story_batch", "taskKey": taskKey, "pendingCount": pendingCount})
	}
}

func (a *AgentRuntime) runAgentTaskWorker(ctx context.Context) {
	workerID := "agent-runtime"
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		task, ok := a.store.ClaimNextAgentTask(workerID, 5*time.Minute)
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				continue
			}
		}
		a.executeAgentTask(ctx, task)
	}
}

func (a *AgentRuntime) executeAgentTask(ctx context.Context, task db.AgentTaskItem) {
	a.store.Log("info", "Agent task started", map[string]any{"event": "agent.task.started", "taskId": task.ID, "taskType": task.TaskType, "attempt": task.Attempt})
	var result map[string]any
	var err error
	switch task.TaskType {
	case "story_batch":
		a.mu.Lock()
		a.storyBatchRunning = true
		a.mu.Unlock()
		err = a.runStoryBatch()
		a.mu.Lock()
		a.storyBatchRunning = false
		processedThrough := a.storyLastSeq
		a.mu.Unlock()
		result = map[string]any{"processedThroughSeq": processedThrough}
	default:
		err = fmt.Errorf("unsupported agent task type: %s", task.TaskType)
	}
	finished := a.store.FinishAgentTask(task.ID, result, err, time.Minute)
	if err != nil {
		a.store.Log("error", "Agent task failed", map[string]any{"event": "agent.task.failed", "taskId": task.ID, "taskType": task.TaskType, "status": finished.Status, "error": err.Error()})
	} else {
		a.store.Log("info", "Agent task completed", map[string]any{"event": "agent.task.completed", "taskId": task.ID, "taskType": task.TaskType, "result": result})
		a.store.AppendAgentStackItem(db.AgentStackItem{
			RuntimeKey: "root",
			Kind:       "task_completed",
			Content: map[string]any{
				"taskId":   task.ID,
				"taskType": task.TaskType,
				"result":   result,
			},
			Metadata: map[string]any{"taskKey": task.TaskKey},
		})
		if notify, _ := task.Payload["notifyAgent"].(bool); notify {
			a.events.Enqueue(AgentEvent{Type: "agent_task_completed", Data: map[string]any{"taskId": task.ID, "taskType": task.TaskType, "result": result}})
		}
	}
	a.persistSnapshot()
	a.scheduleStoryBatch()
	_ = ctx
}

func storyBatchScheduleDecision(
	pendingCount int,
	batchSize int,
	latestCreatedAt time.Time,
	hasLatest bool,
	now time.Time,
	idleFlush time.Duration,
) (bool, time.Duration) {
	if pendingCount <= 0 {
		return false, 0
	}
	if pendingCount >= batchSize {
		return true, 0
	}
	if !hasLatest || latestCreatedAt.IsZero() {
		return true, 0
	}
	idleFor := now.Sub(latestCreatedAt)
	if idleFor >= idleFlush {
		return true, 0
	}
	return false, idleFlush - idleFor
}

func (a *AgentRuntime) runStoryBatch() error {
	if a.llm == nil {
		return nil
	}
	limit := a.cfg.Server.Agent.Story.BatchSize
	if limit <= 0 {
		limit = 24
	}
	entries := a.store.ListStoryLedgerAfter("root", a.storyLastSeq, limit)
	maxSeq := a.storyLastSeq
	for _, entry := range entries {
		if entry.Seq > maxSeq {
			maxSeq = entry.Seq
		}
	}
	if len(entries) == 0 {
		return nil
	}
	a.storyMessages = append(a.storyMessages, agentruntime.Message{
		Role:    "user",
		Content: renderStoryLedgerBatch(entries),
	})
	for i := 0; i < 6; i++ {
		result, err := a.storyKernel.RunRound(context.Background(), agentruntime.RoundInput{
			SystemPrompt: prompts.StoryAgentSystemPrompt(),
			Messages:     append([]agentruntime.Message(nil), a.storyMessages...),
			Tools:        a.storyTools,
			ToolChoice:   "required",
		})
		if err != nil {
			a.setRuntimeError(err)
			return err
		}
		a.storyMessages = append(a.storyMessages, result.Assistant)
		finished := false
		for _, execution := range result.ToolExecutions {
			a.storyMessages = append(a.storyMessages, agentruntime.Message{Role: "tool", ToolCallID: execution.Call.ID, Content: execution.Result.Content})
			a.recordToolExecution(execution)
			if execution.Call.Name == "create_story" || execution.Call.Name == "rewrite_story" {
				a.storyLastSeq = maxSeq
			}
			if execution.Call.Name == "finish_story_batch" {
				finished = true
			}
		}
		a.maybeCompactStory(result.Completion)
		if finished || len(result.ToolExecutions) == 0 {
			if a.storyLastSeq < maxSeq {
				a.storyLastSeq = maxSeq
			}
			break
		}
	}
	return nil
}

func renderStoryLedgerBatch(entries []db.StoryLedgerItem) string {
	var b strings.Builder
	b.WriteString("<ledger_batch>\n")
	for i, entry := range entries {
		if i > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "[%d] %s\n%s", entry.Seq, entry.Role, entry.Content)
	}
	b.WriteString("\n</ledger_batch>")
	return b.String()
}

func (a *AgentRuntime) appendContext(item DashboardContextItem) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.contextItems = append(a.contextItems, item)
	if len(a.contextItems) > 400 {
		a.contextItems = a.contextItems[len(a.contextItems)-400:]
	}
}

func (a *AgentRuntime) maybeCreateStory(event AgentEvent) {
	raw := common.AsString(event.Data["rawMessage"])
	if strings.TrimSpace(raw) == "" {
		return
	}
	seq := 0
	if v, ok := event.Data["messageSeq"].(int); ok {
		seq = v
	}
	a.mu.Lock()
	a.storyLastSeq++
	if seq == 0 {
		seq = a.storyLastSeq
	}
	a.mu.Unlock()
	now := time.Now()
	nickname := common.AsString(event.Data["nickname"])
	scene := event.Type
	title := trimPreview(raw, 40)
	story := db.StoryItem{
		ID:                    common.NewID(),
		Markdown:              storyMarkdown(title, common.ISO(now), scene, []string{nickname}, raw),
		Title:                 title,
		Time:                  common.ISO(now),
		Scene:                 scene,
		People:                []string{nickname},
		Impact:                "由消息事件自动沉淀",
		SourceMessageSeqStart: seq,
		SourceMessageSeqEnd:   seq,
		CreatedAt:             now,
		UpdatedAt:             now,
		MatchedKinds:          []string{"overview"},
	}
	a.store.AddStory(story)
}

func (a *AgentRuntime) Snapshot(llm *llm.LLMClient) map[string]any {
	if a.terminal != nil {
		a.session.SetTerminalCWD(a.terminal.CWD())
	}
	if cursor, ok := a.store.NewsFeedCursor("ithome"); ok {
		a.session.SetIthomeOverview(a.store.CountNewsArticlesNewerThanCursor("ithome", cursor), true)
	} else {
		a.session.SetIthomeOverview(a.store.CountNewsArticlesNewerThanCursor("ithome", db.NewsFeedCursor{}), false)
	}
	taskCounts := a.store.AgentTaskStatusCounts()
	toolExecutionCounts := a.store.ToolExecutionStatusCounts()
	a.mu.Lock()
	defer a.mu.Unlock()
	now := common.ISO(time.Now())
	lastActivity := ""
	if a.lastActivity != nil {
		lastActivity = common.ISO(*a.lastActivity)
	}
	recent := append([]DashboardContextItem(nil), a.contextItems...)
	if len(recent) > 40 {
		recent = recent[len(recent)-40:]
	}
	runtime := map[string]any{
		"initialized":          a.initialized,
		"loopState":            a.loopState,
		"lastError":            a.lastError,
		"lastActivityAt":       nullableString(lastActivity),
		"lastRoundCompletedAt": nil,
		"lastCompactionAt":     nullableTime(a.lastCompaction),
		"autonomous": map[string]any{
			"enabled":              a.cfg.Server.Agent.Autonomous.Enabled,
			"consecutiveRounds":    a.autonomousRounds,
			"maxConsecutiveRounds": a.cfg.Server.Agent.Autonomous.MaxConsecutiveRounds,
			"cooldownUntil":        nullableTime(a.autonomousUntil),
		},
	}
	contextSummary := map[string]any{
		"messageCount":                  len(a.contextItems),
		"compactionTotalTokenThreshold": a.cfg.Server.Agent.ContextCompactionTotalTokenThreshold,
		"recentItems":                   recent,
		"recentItemsTruncated":          len(a.contextItems) > len(recent),
	}
	activity := map[string]any{"lastToolCall": a.lastToolCall, "lastToolResultPreview": a.lastToolResult, "lastLlmCall": a.lastLlmCall}
	return map[string]any{
		"generatedAt": now,
		"agents": []any{
			map[string]any{
				"id": "root", "kind": "root", "label": "Root Agent",
				"runtime": runtime, "context": contextSummary, "activity": activity,
				"session": a.session.Snapshot(),
				"queue": map[string]any{
					"pendingEventCount": a.events.Count(),
					"agentTasks":        taskCounts,
					"toolExecutions":    toolExecutionCounts,
				},
				"providers": llm.ListProviders("agent")["providers"],
			},
			map[string]any{
				"id": "story", "kind": "story", "label": "Story Agent",
				"runtime": runtime, "context": contextSummary, "activity": activity,
				"story": map[string]any{
					"lastProcessedMessageSeq": a.storyLastSeq,
					"pendingMessageCount":     a.store.CountStoryLedgerAfter("root", a.storyLastSeq),
					"pendingBatch":            nil,
					"batchSize":               a.cfg.Server.Agent.Story.BatchSize,
					"idleFlushMs":             a.cfg.Server.Agent.Story.IdleFlushMs,
				},
			},
		},
	}
}

func createSystemPrompt(cfg *config.Config) string {
	return prompts.MainEngineSystemPrompt(cfg, invokeToolGuide())
}

func invokeToolGuide() string {
	return "invoke 子工具清单不在 system prompt 中固定枚举；真正是否允许调用以当前状态允许的工具集合为准。如果调用错误，工具返回会附带当前可用工具说明和参数提示。"
}

func contextItem(kind, label, text string) DashboardContextItem {
	return DashboardContextItem{Kind: kind, Label: label, Preview: trimPreview(text, 2000), Truncated: len([]rune(strings.TrimSpace(text))) > 2000}
}

func eventContextKind(eventType string) string {
	switch eventType {
	case "napcat_group_message", "napcat_private_message":
		return "qq_message"
	case "wake", "news_article_ingested", "story_recall_completed":
		return "system_reminder"
	default:
		return "event"
	}
}

func eventMessageRole(eventType string) string {
	switch eventType {
	case "story_recall_completed":
		return "user"
	default:
		return "user"
	}
}

func rootControlTools(
	cfg *config.Config,
	business *agentruntime.ToolCatalog,
	session *roottools.Session,
	events *EventQueue,
) *agentruntime.ToolCatalog {
	maxWait := time.Duration(cfg.Server.Agent.WaitToolMaxWaitMs) * time.Millisecond
	if maxWait <= 0 {
		maxWait = 10 * time.Minute
	}
	if cfg.Server.Agent.Autonomous.Enabled {
		idleDelay := time.Duration(cfg.Server.Agent.Autonomous.IdleDelayMs) * time.Millisecond
		if idleDelay > 0 {
			maxWait = idleDelay
		}
	}
	alwaysAvailable := map[string]bool{
		"search_web":    true,
		"search_memory": true,
		"analyze_image": true,
		"browser":       true,
	}
	owner := roottools.CatalogSubtoolOwner{
		Tools:           business,
		Session:         session,
		AlwaysAvailable: alwaysAvailable,
	}
	catalog := agentruntime.NewToolCatalog(
		roottools.EnterTool{Session: session},
		roottools.AppBackToPortalTool{Session: session},
		roottools.WaitTool{MaxWait: maxWait, WaitSignal: func(ctx context.Context) bool {
			if events.WaitSignal(ctx) {
				return true
			}
			if cfg.Server.Agent.Autonomous.Enabled {
				events.Enqueue(AgentEvent{Type: "wake", Data: map[string]any{"reason": "self_continuation"}})
			} else if cfg.Server.Agent.CacheKeepaliveEnabled {
				events.Enqueue(AgentEvent{Type: "wake", Data: map[string]any{"reason": "wait_timeout"}})
			}
			return false
		}},
		roottools.InvokeTool{Owners: []roottools.InvokeSubtoolOwner{owner}},
	).Pick("enter", "back_to_portal", "wait", "invoke")
	for _, definition := range business.Definitions() {
		tool, ok := business.Get(definition.Name)
		if !ok {
			continue
		}
		catalog.Add(roottools.DirectSubtool{
			Owner:           owner,
			DefinitionValue: tool.Definition(),
			ToolKind:        tool.Kind(),
			CheckPermission: !alwaysAvailable[definition.Name],
		})
	}
	return catalog
}

func rootTools(
	cfg *config.Config,
	business *agentruntime.ToolCatalog,
	session *roottools.Session,
	events *EventQueue,
) *agentruntime.ToolCatalog {
	catalog := rootControlTools(cfg, business, session, events)
	catalog.Add(roottools.HelpTool{Session: session})
	return catalog
}

func (a *AgentRuntime) setRuntimeError(err error) {
	now := time.Now()
	a.mu.Lock()
	a.lastError = &RuntimeError{Name: "AgentRuntimeError", Message: err.Error(), UpdatedAt: common.ISO(now)}
	a.loopState = "idle"
	a.lastActivity = &now
	a.mu.Unlock()
}

func (a *AgentRuntime) recordToolExecution(execution agentruntime.ToolExecution) {
	args, _ := json.Marshal(execution.Call.Arguments)
	result := execution.Result.Content
	a.mu.Lock()
	a.lastToolCall = &DashboardToolCall{Name: execution.Call.Name, ArgumentsPreview: trimPreview(string(args), 300), UpdatedAt: common.ISO(time.Now())}
	a.lastToolResult = &result
	a.mu.Unlock()
	a.appendContext(contextItem("tool_result", execution.Call.Name, execution.Result.Content))
}

func (a *AgentRuntime) recordLLMCall(completion agentruntime.Completion) {
	var total *int
	if completion.Usage != nil && completion.Usage.TotalTokens > 0 {
		v := completion.Usage.TotalTokens
		total = &v
	}
	names := make([]string, 0, len(completion.Message.ToolCalls))
	for _, call := range completion.Message.ToolCalls {
		names = append(names, call.Name)
	}
	a.mu.Lock()
	a.lastLlmCall = &DashboardLlmCall{
		Provider:                completion.Provider,
		Model:                   completion.Model,
		AssistantContentPreview: trimPreview(completion.Message.Content, 300),
		ToolCallNames:           names,
		TotalTokens:             total,
		UpdatedAt:               common.ISO(time.Now()),
	}
	a.mu.Unlock()
}

func (a *AgentRuntime) renderEventContext(event AgentEvent) string {
	switch event.Type {
	case "wake":
		return prompts.WakeReminder(event.At)
	case "napcat_group_message":
		nickname := common.AsString(event.Data["nickname"])
		if nickname == "" {
			nickname = "未知用户"
		}
		return prompts.QQMessageRoutedAt("group", common.AsString(event.Data["groupId"]), nickname, common.AsString(event.Data["userId"]), common.AsString(event.Data["rawMessage"]), event.At)
	case "napcat_private_message":
		nickname := common.AsString(event.Data["nickname"])
		if nickname == "" {
			nickname = "未知用户"
		}
		return prompts.QQMessageRoutedAt("private", common.AsString(event.Data["userId"]), nickname, common.AsString(event.Data["userId"]), common.AsString(event.Data["rawMessage"]), event.At)
	case "news_article_ingested":
		if article, ok := a.findNewsArticle(event.Data["articleId"]); ok {
			return prompts.ITHomeArticleIngestedNotice(prompts.ArticleSummary{
				ID:              article.ID,
				Title:           article.Title,
				PublishedAtText: formatTime(article.PublishedAt),
				URL:             article.URL,
				RSSSummary:      article.RSSSummary,
			})
		}
	case "story_recall_completed":
		return renderStoryRecallMessage(common.AsString(event.Data["content"]))
	}
	return fmt.Sprintf(`<system_reminder>
收到外部事件：%s
事件内容：%v
</system_reminder>`, event.Type, event.Data)
}

func renderStoryRecallMessage(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return "<story_recall>\n</story_recall>"
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(content), &items); err == nil && len(items) > 0 {
		var b strings.Builder
		b.WriteString("<story_recall>\n")
		for _, item := range items {
			date := formatStoryRecallDate(common.AsString(item["createdAt"]))
			if date == "" {
				date = "未知日期"
			}
			fmt.Fprintf(&b, "你想起了一件发生在 %s 的事情：\n\n%s\n", date, common.AsString(item["markdown"]))
		}
		b.WriteString("</story_recall>")
		return b.String()
	}
	return "<story_recall>\n" + content + "\n</story_recall>"
}

func formatStoryRecallDate(value string) string {
	if value == "" {
		return ""
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	if t.IsZero() {
		return "未知日期"
	}
	return t.Format("2006-01-02")
}

func (a *AgentRuntime) findNewsArticle(id any) (db.NewsArticle, bool) {
	articleID := intValue(id)
	if articleID == 0 {
		return db.NewsArticle{}, false
	}
	for _, article := range a.store.Snapshot().NewsArticles {
		if article.ID == articleID {
			return article, true
		}
	}
	return db.NewsArticle{}, false
}

func portalGroups(cfg *config.Config) []prompts.PortalTarget {
	groups := make([]prompts.PortalTarget, 0, len(cfg.Server.Napcat.ListenGroupIDs))
	for _, id := range cfg.Server.Napcat.ListenGroupIDs {
		groups = append(groups, prompts.PortalTarget{
			Label:            "QQ群 " + id,
			Kind:             "qq_group",
			HasEntered:       false,
			EnterCommandText: fmt.Sprintf(`enter(id="qq_group:%s")`, id),
		})
	}
	return groups
}

func portalFeeds() []prompts.PortalTarget {
	return []prompts.PortalTarget{{
		Label:            "IT 之家",
		Kind:             "ithome",
		HasEntered:       false,
		EnterCommandText: `enter(id="ithome")`,
	}}
}

func storyMarkdown(title, timestamp, scene string, people []string, raw string) string {
	return fmt.Sprintf(`# %s
- 时间：%s
- 场景：%s
- 人物：%s
- 影响：由消息事件自动沉淀，后续可被长期记忆召回。

起因：群聊中出现了一条新的消息。
经过：
1. %s
结果：该消息已作为一条轻量 story 记录下来。`, title, timestamp, scene, strings.Join(people, "、"), raw)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	t = t.In(time.FixedZone("Asia/Shanghai", 8*60*60))
	return fmt.Sprintf("%d/%d/%d %02d:%02d", t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute())
}

func intValue(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case json.Number:
		i, _ := x.Int64()
		return int(i)
	default:
		return 0
	}
}

func floatValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func toolCallNames(calls []agentruntime.ToolCall) []string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		if call.Name != "" {
			names = append(names, call.Name)
		}
	}
	return names
}

func trimPreview(s string, n int) string {
	s = strings.TrimSpace(s)
	if len([]rune(s)) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}

func nullableString(v string) any {
	if v == "" {
		return nil
	}
	return v
}

func nullableTime(v *time.Time) any {
	if v == nil || v.IsZero() {
		return nil
	}
	return common.ISO(*v)
}
