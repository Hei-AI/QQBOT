package contextsummary

import (
	"context"
	"strings"

	"QqBot/internal/agentruntime"
)

type SummaryTool struct{}

func (SummaryTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: "summary", Description: "写入供后续继续工作的对话摘要", Parameters: agentruntime.ObjectSchema(map[string]any{"summary": map[string]any{"type": "string"}})}
}

func (SummaryTool) Kind() string { return "business" }

func (SummaryTool) Execute(_ context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	return agentruntime.ToolResult{Kind: "business", Content: strings.TrimSpace(stringValue(call.Arguments["summary"]))}, nil
}

type Operation struct {
	Model agentruntime.Model
}

func (o Operation) Summarize(ctx context.Context, systemPrompt string, messages []agentruntime.Message, reminder string) (string, error) {
	if o.Model == nil || len(messages) == 0 {
		return "", nil
	}
	kernel := agentruntime.ReActKernel{Model: o.Model}
	inputMessages := append([]agentruntime.Message(nil), messages...)
	inputMessages = append(inputMessages, agentruntime.Message{Role: "user", Content: reminder})
	result, err := kernel.RunRound(ctx, agentruntime.RoundInput{
		SystemPrompt: systemPrompt,
		Messages:     inputMessages,
		Tools:        agentruntime.NewToolCatalog(SummaryTool{}),
		ToolChoice:   map[string]any{"tool_name": "summary"},
	})
	if err != nil {
		return "", err
	}
	for _, execution := range result.ToolExecutions {
		if execution.Call.Name == "summary" {
			return strings.TrimSpace(execution.Result.Content), nil
		}
	}
	return strings.TrimSpace(result.Assistant.Content), nil
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
