package llm

import "testing"

func TestCodexRequestBodyDropsOrphanFunctionCallOutputs(t *testing.T) {
	payload := toCodexRequestBody(LLMChatRequest{
		Model: "gpt-test",
		Messages: []LLMMessage{
			{Role: "tool", ToolCallID: "missing", Content: "orphan"},
			{Role: "assistant", ToolCalls: []LLMToolCall{{ID: "call_ok", Name: "wait", Arguments: map[string]any{}}}},
			{Role: "tool", ToolCallID: "call_ok", Content: "paired"},
		},
	}, "")
	input, ok := payload["input"].([]map[string]any)
	if !ok {
		t.Fatalf("input has unexpected type: %#v", payload["input"])
	}
	outputs := 0
	for _, item := range input {
		if item["type"] == "function_call_output" {
			outputs++
			if item["call_id"] != "call_ok" {
				t.Fatalf("unexpected function_call_output survived: %#v", item)
			}
		}
	}
	if outputs != 1 {
		t.Fatalf("expected exactly one paired function_call_output, got %d: %#v", outputs, input)
	}
}

func TestCodexRequestBodyDropsFunctionCallsWithoutOutputs(t *testing.T) {
	payload := toCodexRequestBody(LLMChatRequest{
		Model: "gpt-test",
		Messages: []LLMMessage{
			{Role: "assistant", ToolCalls: []LLMToolCall{
				{ID: "call_missing", Name: "wait", Arguments: map[string]any{}},
				{ID: "call_ok", Name: "wait", Arguments: map[string]any{}},
			}},
			{Role: "tool", ToolCallID: "call_ok", Content: "paired"},
		},
	}, "")
	input, ok := payload["input"].([]map[string]any)
	if !ok {
		t.Fatalf("input has unexpected type: %#v", payload["input"])
	}
	calls := 0
	outputs := 0
	for _, item := range input {
		switch item["type"] {
		case "function_call":
			calls++
			if item["call_id"] != "call_ok" {
				t.Fatalf("unexpected function_call survived: %#v", item)
			}
		case "function_call_output":
			outputs++
			if item["call_id"] != "call_ok" {
				t.Fatalf("unexpected function_call_output survived: %#v", item)
			}
		}
	}
	if calls != 1 || outputs != 1 {
		t.Fatalf("expected one paired call/output, got calls=%d outputs=%d input=%#v", calls, outputs, input)
	}
}

func TestExtractSSEEventAcceptsCRLF(t *testing.T) {
	text := "event: response.created\r\n" +
		"data: {\"ignored\":true}\r\n\r\n" +
		"event: response.completed\r\n" +
		"data: {\"response\":{\"id\":\"resp_1\",\"output\":[]}}\r\n\r\n"
	event := extractSSEEvent(text, "response.completed")
	if event == nil {
		t.Fatal("expected response.completed event")
	}
	response, _ := event["response"].(map[string]any)
	if response["id"] != "resp_1" {
		t.Fatalf("unexpected event payload: %#v", event)
	}
}
