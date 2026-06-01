package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"qqbot-ai/internal/agentruntime"
	"qqbot-ai/internal/capabilities/websearch"
	"qqbot-ai/internal/common"
	"qqbot-ai/internal/prompts"
	"sync"
	"time"
)

type WebSearchTaskAgentTool struct {
	service   websearch.Service
	model     agentruntime.Model
	timeout   time.Duration
	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

func NewWebSearchTaskAgentTool(service websearch.Service) *WebSearchTaskAgentTool {
	return &WebSearchTaskAgentTool{service: service}
}

func (t *WebSearchTaskAgentTool) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

func (t *WebSearchTaskAgentTool) SetModel(model agentruntime.Model) {
	t.model = model
}

func (t *WebSearchTaskAgentTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: "search_web", Description: "读取网页 URL 或搜索互联网，并返回一段可靠中文摘要", Parameters: agentruntime.ObjectSchema(map[string]any{"query": map[string]any{"type": "string"}})}
}

func (t *WebSearchTaskAgentTool) Kind() string { return "business" }

func (t *WebSearchTaskAgentTool) Execute(ctx context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	if !t.allowed() {
		return agentruntime.ToolResult{}, fmt.Errorf("web search agent is temporarily unavailable after repeated failures")
	}
	if t.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.timeout)
		defer cancel()
	}
	query := common.AsString(call.Arguments["query"])
	if query == "" {
		query = common.AsString(call.Arguments["question"])
	}
	if t.model == nil {
		raw, err := websearch.SearchWebRawTool{Service: t.service}.Execute(ctx, agentruntime.ToolCall{ID: call.ID + ":raw", Name: "search_web_raw", Arguments: map[string]any{"query": query, "maxResults": 5}})
		if err != nil {
			t.report(err)
			return raw, err
		}
		t.report(nil)
		return raw, nil
	}

	tools := agentruntime.NewToolCatalog(
		websearch.SearchWebRawTool{Service: t.service},
		websearch.FinalizeWebSearchTool{},
	)
	kernel := agentruntime.ReActKernel{Model: t.model}
	messages := []agentruntime.Message{{Role: "user", Content: prompts.WebSearchInstruction(query)}}
	var last agentruntime.RoundResult
	for i := 0; i < 4; i++ {
		result, err := kernel.RunRound(ctx, agentruntime.RoundInput{
			SystemPrompt: prompts.WebSearchSystemPrompt(),
			Messages:     messages,
			Tools:        tools,
			ToolChoice:   "auto",
		})
		if err != nil {
			t.report(err)
			return agentruntime.ToolResult{}, err
		}
		last = result
		messages = append(messages, result.Assistant)
		for _, execution := range result.ToolExecutions {
			if execution.Call.Name == "finalize_web_search" {
				t.report(nil)
				return agentruntime.ToolResult{Kind: "business", Content: execution.Result.Content}, nil
			}
			messages = append(messages, agentruntime.Message{Role: "tool", ToolCallID: execution.Call.ID, Content: execution.Result.Content})
		}
	}
	content := last.Assistant.Content
	if content == "" {
		data, _ := json.Marshal(map[string]any{"answer": "网页搜索未能形成最终摘要", "sources": []any{}})
		content = string(data)
	}
	return agentruntime.ToolResult{Kind: "business", Content: content}, nil
}

func (t *WebSearchTaskAgentTool) allowed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.openUntil.IsZero() || time.Now().After(t.openUntil)
}

func (t *WebSearchTaskAgentTool) report(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil {
		t.failures = 0
		t.openUntil = time.Time{}
		return
	}
	t.failures++
	if t.failures >= 3 {
		t.failures = 0
		t.openUntil = time.Now().Add(2 * time.Minute)
	}
}
