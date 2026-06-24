package agent

import (
	"QqBot/internal/agentruntime"
	browsercap "QqBot/internal/capabilities/browser"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type scriptedBrowserModel struct {
	round int
	tools [][]agentruntime.ToolDefinition
}

func (m *scriptedBrowserModel) Chat(_ context.Context, _ string, _ []agentruntime.Message, tools []agentruntime.ToolDefinition, _ any) (agentruntime.Completion, error) {
	m.tools = append(m.tools, tools)
	m.round++
	if m.round == 1 {
		return agentruntime.Completion{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
			ID: "read-1", Name: "browser_read", Arguments: map[string]any{},
		}}}}, nil
	}
	return agentruntime.Completion{Message: agentruntime.Message{Role: "assistant", ToolCalls: []agentruntime.ToolCall{{
		ID: "done-1", Name: "finalize_browser", Arguments: map[string]any{
			"summary": "页面显示测试内容。", "url": "https://example.com", "title": "Example",
		},
	}}}}, nil
}

func TestBrowserTaskAgentProgressivelyExposesBrowserActions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(browsercap.ActionResponse{
			OK: true, URL: "https://example.com", Title: "Example", Text: "测试内容",
		})
	}))
	defer server.Close()
	client, err := browsercap.NewClient(browsercap.Config{BaseURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedBrowserModel{}
	tool := NewBrowserTaskAgentTool(client, "default", 4, nil, 0, t.TempDir())
	tool.SetModel(model)
	result, err := tool.Execute(context.Background(), agentruntime.ToolCall{
		ID: "browser-1", Name: "browser", Arguments: map[string]any{"task": "读取测试页面", "sessionId": "qq-group-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["sessionId"] != "qq-group-1" || payload["summary"] != "页面显示测试内容。" {
		t.Fatalf("unexpected result: %s", result.Content)
	}
	if len(model.tools) != 2 {
		t.Fatalf("expected two browser rounds, got %d", len(model.tools))
	}
	names := map[string]bool{}
	for _, definition := range model.tools[0] {
		names[definition.Name] = true
	}
	if !names["browser_read"] || !names["browser_click"] || !names["finalize_browser"] {
		t.Fatalf("browser actions were not exposed: %#v", names)
	}
	if names["send_message"] || names["search_web"] || names["browser"] {
		t.Fatalf("top-level tools leaked into browser agent: %#v", names)
	}
}

func TestMergeBrowserTaskResultCarriesSavedScreenshot(t *testing.T) {
	content := mergeBrowserTaskResult(
		`{"ok":true,"summary":"已截图","imagePath":""}`,
		"default",
		`D:\qq-bot\data\browser-screenshots\browser.png`,
	)
	var payload map[string]any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["imagePath"] != `D:\qq-bot\data\browser-screenshots\browser.png` {
		t.Fatalf("saved screenshot was not carried to root: %s", content)
	}
}
