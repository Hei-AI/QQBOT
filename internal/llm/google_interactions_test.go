package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"QqBot/internal/config"
)

func TestCallGoogleUsesInteractionsAPIForImage(t *testing.T) {
	var requestPayload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/interactions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Fatalf("missing API key header")
		}
		if r.Header.Get("Api-Revision") != googleInteractionsAPIRevision {
			t.Fatalf("unexpected API revision: %s", r.Header.Get("Api-Revision"))
		}
		if err := json.NewDecoder(r.Body).Decode(&requestPayload); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":     "int_test",
			"status": "completed",
			"model":  "gemini-3.5-flash",
			"steps": []any{map[string]any{
				"type": "model_output",
				"content": []any{map[string]any{
					"type": "text",
					"text": "图片中有一只猫",
				}},
			}},
			"usage": map[string]any{
				"total_input_tokens":  10,
				"total_output_tokens": 6,
				"total_tokens":        16,
			},
		})
	}))
	defer server.Close()

	client := &LLMClient{http: server.Client()}
	encoded := base64.StdEncoding.EncodeToString([]byte{1, 2, 3})
	_, response, _, err := client.callGoogle(context.Background(), LLMChatRequest{
		Model:  "gemini-3.5-flash",
		System: "识别图片",
		Messages: []LLMMessage{{Role: "user", Content: []any{
			map[string]any{"type": "text", "text": "描述图片"},
			map[string]any{"type": "image", "mimeType": "image/png", "dataUrl": "data:image/png;base64," + encoded},
		}}},
	}, config.LLMProviderConfig{APIKey: "test-key", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	message, _ := response["message"].(map[string]any)
	if message["content"] != "图片中有一只猫" {
		t.Fatalf("unexpected response: %#v", response)
	}
	if requestPayload["store"] != false || requestPayload["model"] != "gemini-3.5-flash" {
		t.Fatalf("unexpected interaction request: %#v", requestPayload)
	}
	input, _ := requestPayload["input"].([]any)
	if len(input) != 2 {
		t.Fatalf("unexpected multimodal input: %#v", input)
	}
	image, _ := input[1].(map[string]any)
	if image["type"] != "image" || image["mime_type"] != "image/png" || image["data"] != encoded {
		t.Fatalf("unexpected image content: %#v", image)
	}
}

func TestGoogleInteractionResponseMapsUsage(t *testing.T) {
	response, err := googleInteractionResponse(map[string]any{
		"status": "completed",
		"steps": []any{map[string]any{
			"type":    "model_output",
			"content": []any{map[string]any{"type": "text", "text": "ok"}},
		}},
		"usage": map[string]any{
			"total_input_tokens":  float64(8),
			"total_output_tokens": float64(3),
			"total_cached_tokens": float64(5),
			"total_tokens":        float64(11),
		},
	}, "gemini-3.5-flash")
	if err != nil {
		t.Fatal(err)
	}
	usage, _ := response["usage"].(map[string]any)
	if usage["promptTokens"] != 8 || usage["completionTokens"] != 3 || usage["cacheHitTokens"] != 5 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
}
