package llm

import (
	"encoding/base64"
	"testing"
)

func TestVisionAttemptSupportsImages(t *testing.T) {
	if visionAttemptSupportsImages("deepseek", "deepseek-v4-pro") {
		t.Fatal("deepseek reasoner should not be treated as image-capable")
	}
	if !visionAttemptSupportsImages("openai", "gpt-4o-mini") {
		t.Fatal("gpt-4o-mini should be treated as image-capable")
	}
	if !visionAttemptSupportsImages("claude-code", "claude-sonnet-4.6") {
		t.Fatal("claude-code should be treated as image-capable")
	}
	if !visionAttemptSupportsImages("google", "gemini-3.5-flash") {
		t.Fatal("gemini should be treated as image-capable")
	}
}

func TestOpenAIChatPayloadIncludesMaxTokens(t *testing.T) {
	payload := toOpenAIChatPayload(LLMChatRequest{Provider: "deepseek", Model: "deepseek-v4-flash", MaxTokens: 2048})
	if payload["max_tokens"] != 2048 {
		t.Fatalf("max_tokens missing from payload: %#v", payload)
	}
}

func TestGoogleInteractionInputConvertsInlineImage(t *testing.T) {
	data := []byte("image")
	input, summary, err := googleInteractionInput([]LLMMessage{{
		Role: "user",
		Content: []any{
			map[string]any{"type": "text", "text": "describe"},
			map[string]any{"type": "image", "mimeType": "image/png", "dataUrl": "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)},
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 2 {
		t.Fatalf("unexpected google interaction input: %#v", input)
	}
	image, _ := input[1].(map[string]any)
	decoded, err := base64.StdEncoding.DecodeString(image["data"].(string))
	if err != nil || string(decoded) != string(data) {
		t.Fatalf("unexpected image bytes: %q err=%v", decoded, err)
	}
	if len(summary) != 1 {
		t.Fatalf("unexpected request summary: %#v", summary)
	}
}
