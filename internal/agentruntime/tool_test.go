package agentruntime

import (
	"context"
	"testing"
)

type toolWithoutOS struct{}

type recordingToolObserver struct {
	before int
	after  int
	prior  *ToolResult
}

func (o *recordingToolObserver) BeforeTool(context.Context, ToolCall, ToolDefinition, string) (*ToolResult, error) {
	o.before++
	return o.prior, nil
}

func (o *recordingToolObserver) AfterTool(context.Context, ToolCall, ToolDefinition, ToolResult, error) {
	o.after++
}

func (toolWithoutOS) Definition() ToolDefinition {
	return ToolDefinition{
		Name:       "example",
		Parameters: ObjectSchema(map[string]any{"message": map[string]any{"type": "string"}}),
	}
}

func (toolWithoutOS) Kind() string { return "business" }

func (toolWithoutOS) Execute(context.Context, ToolCall) (ToolResult, error) {
	return ToolResult{}, nil
}

func TestToolCatalogDefinitionsInjectOSParameter(t *testing.T) {
	definitions := NewToolCatalog(toolWithoutOS{}).Definitions()
	if len(definitions) != 1 {
		t.Fatalf("got %d definitions, want 1", len(definitions))
	}
	properties, _ := definitions[0].Parameters["properties"].(map[string]any)
	if _, ok := properties["os"]; !ok {
		t.Fatal("tool schema sent to the LLM must contain the optional os parameter")
	}
	if _, ok := properties["message"]; !ok {
		t.Fatal("injecting os must preserve existing parameters")
	}
}

func TestToolCatalogObserverCanReplayPriorResult(t *testing.T) {
	catalog := NewToolCatalog(toolWithoutOS{})
	observer := &recordingToolObserver{prior: &ToolResult{Kind: "business", Content: `{"cached":true}`}}
	catalog.SetObserver(observer)
	result, err := catalog.Execute(context.Background(), ToolCall{ID: "call-1", Name: "example", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != `{"cached":true}` || observer.before != 1 || observer.after != 0 {
		t.Fatalf("unexpected observer replay: result=%#v observer=%#v", result, observer)
	}
}
