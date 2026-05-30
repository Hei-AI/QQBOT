package agentruntime

import (
	"context"
	"testing"
)

type testTool struct {
	name string
}

func (t testTool) Definition() ToolDefinition {
	return ToolDefinition{Name: t.name, Parameters: ObjectSchema(nil)}
}

func (testTool) Kind() string { return "control" }

func (testTool) Execute(context.Context, ToolCall) (ToolResult, error) {
	return ToolResult{Kind: "control", Content: "{}"}, nil
}

func TestToolCatalogRejectsDuplicateNames(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected duplicate tool registration to panic")
		}
	}()
	NewToolCatalog(testTool{name: "x"}, testTool{name: "x"})
}

func TestToolCatalogPickRejectsMissingTool(t *testing.T) {
	catalog := NewToolCatalog(testTool{name: "x"})
	defer func() {
		if recover() == nil {
			t.Fatal("expected missing tool pick to panic")
		}
	}()
	catalog.Pick("missing")
}
