package magnetsearch

import (
	"context"
	"encoding/json"
	"testing"

	"QqBot/internal/agentruntime"
)

func TestSearchToolReturnsNebulaCompatibleField(t *testing.T) {
	provider := &fakeProvider{name: "test", items: []Item{{ID: "1", Name: "result"}}}
	tool := SearchTool{Service: &Service{Providers: []Provider{provider}}, DefaultLimit: 30}
	result, err := tool.Execute(context.Background(), agentruntime.ToolCall{
		Arguments: map[string]any{"query_zhCN": "测试"},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload["ok"] != true || payload["magnetSearchResult"] == nil {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}
