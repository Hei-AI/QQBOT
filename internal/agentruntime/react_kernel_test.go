package agentruntime

import (
	"context"
	"errors"
	"testing"
)

type retryModel struct {
	calls int
}

func (m *retryModel) Chat(context.Context, string, []Message, []ToolDefinition, any) (Completion, error) {
	m.calls++
	if m.calls == 1 {
		return Completion{}, errors.New("temporary")
	}
	return Completion{Message: Message{Role: "assistant", Content: "ok"}}, nil
}

type retryExtension struct {
	ReActKernelExtensionBase
	retries int
}

type failingTool struct{}

func (failingTool) Definition() ToolDefinition {
	return ToolDefinition{Name: "fail", Parameters: ObjectSchema(nil)}
}

func (failingTool) Kind() string { return "business" }

func (failingTool) Execute(context.Context, ToolCall) (ToolResult, error) {
	return ToolResult{}, errors.New("boom")
}

type toolModel struct{}

func (toolModel) Chat(context.Context, string, []Message, []ToolDefinition, any) (Completion, error) {
	return Completion{Message: Message{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "fail"}}}}, nil
}

type toolFallbackExtension struct {
	ReActKernelExtensionBase
	called bool
}

func (e *toolFallbackExtension) OnToolError(context.Context, RoundInput, Completion, ToolCall, error) (ToolErrorDecision, error) {
	e.called = true
	result := ToolResult{Kind: "business", Content: `{"ok":false,"fallback":true}`}
	return ToolErrorDecision{Handled: true, Result: &result}, nil
}

func (e *retryExtension) OnModelError(context.Context, RoundInput, error) (ModelErrorDecision, error) {
	e.retries++
	return ModelErrorDecision{Handled: true, Retry: true}, nil
}

func TestReActKernelExtensionCanRetryModelError(t *testing.T) {
	model := &retryModel{}
	extension := &retryExtension{}
	kernel := ReActKernel{Model: model, Extensions: []ReActKernelExtension{extension}}

	result, err := kernel.RunRound(context.Background(), RoundInput{})
	if err != nil {
		t.Fatalf("RunRound returned error: %v", err)
	}
	if model.calls != 2 || extension.retries != 1 {
		t.Fatalf("retry did not run through extension: calls=%d retries=%d", model.calls, extension.retries)
	}
	if result.Assistant.Content != "ok" {
		t.Fatalf("unexpected assistant: %#v", result.Assistant)
	}
}

func TestReActKernelExtensionCanHandleToolError(t *testing.T) {
	extension := &toolFallbackExtension{}
	kernel := ReActKernel{Model: toolModel{}, Extensions: []ReActKernelExtension{extension}}

	result, err := kernel.RunRound(context.Background(), RoundInput{Tools: NewToolCatalog(failingTool{})})
	if err != nil {
		t.Fatalf("RunRound returned error: %v", err)
	}
	if !extension.called {
		t.Fatal("tool error extension was not called")
	}
	if len(result.ToolExecutions) != 1 || result.ToolExecutions[0].Result.Content == "" {
		t.Fatalf("fallback tool result missing: %#v", result.ToolExecutions)
	}
}
