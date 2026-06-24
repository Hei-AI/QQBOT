package llm

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"QqBot/internal/common"
	"QqBot/internal/config"
)

const googleInteractionsAPIRevision = "2026-05-20"

func (c *LLMClient) callGoogleInteraction(ctx context.Context, req LLMChatRequest, conf config.LLMProviderConfig) (map[string]any, map[string]any, map[string]any, error) {
	payload, summary, err := googleInteractionPayload(req)
	if err != nil {
		return nil, nil, nil, err
	}
	nativeResp, err := c.doGoogleInteraction(ctx, conf, payload)
	if err != nil {
		return summary, nil, nativeResp, err
	}
	response, err := googleInteractionResponse(nativeResp, req.Model)
	return summary, response, nativeResp, err
}

func googleInteractionPayload(req LLMChatRequest) (map[string]any, map[string]any, error) {
	input, summary, err := googleInteractionInput(req.Messages)
	if err != nil {
		return nil, nil, err
	}
	if len(input) == 0 {
		return nil, nil, fmt.Errorf("google interaction has no input")
	}
	payload := map[string]any{
		"model": req.Model,
		"input": input,
		"store": false,
	}
	if system := strings.TrimSpace(req.System); system != "" {
		payload["system_instruction"] = system
	}
	if req.MaxTokens > 0 {
		payload["generation_config"] = map[string]any{"max_output_tokens": req.MaxTokens}
	}
	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, tool := range req.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.Parameters,
			})
		}
		payload["tools"] = tools
	}
	return payload, map[string]any{"api": "interactions", "messages": summary}, nil
}

func googleInteractionInput(messages []LLMMessage) ([]any, []any, error) {
	steps := make([]any, 0, len(messages))
	summary := make([]any, 0, len(messages))
	for _, message := range messages {
		content, contentSummary, err := googleInteractionContent(message.Content)
		if err != nil {
			return nil, nil, err
		}
		if len(content) == 0 {
			continue
		}
		stepType := "user_input"
		if message.Role == "assistant" {
			stepType = "model_output"
		}
		steps = append(steps, map[string]any{"type": stepType, "content": content})
		summary = append(summary, map[string]any{"type": stepType, "content": contentSummary})
	}
	if len(steps) == 1 {
		step, _ := steps[0].(map[string]any)
		if step["type"] == "user_input" {
			content, _ := step["content"].([]any)
			return content, summary, nil
		}
	}
	return steps, summary, nil
}

func googleInteractionContent(value any) ([]any, []any, error) {
	content := []any{}
	summary := []any{}
	parts, ok := value.([]any)
	if !ok {
		if text := strings.TrimSpace(common.AsString(value)); text != "" {
			item := map[string]any{"type": "text", "text": text}
			return []any{item}, []any{item}, nil
		}
		return content, summary, nil
	}
	for _, rawPart := range parts {
		part, ok := rawPart.(map[string]any)
		if !ok {
			continue
		}
		switch common.AsString(part["type"]) {
		case "image":
			mimeType := strings.TrimSpace(common.AsString(part["mimeType"]))
			encoded := dataURLPayload(common.AsString(part["dataUrl"]))
			decoded, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, nil, fmt.Errorf("decode google interaction image: %w", err)
			}
			if len(decoded) == 0 || mimeType == "" {
				continue
			}
			content = append(content, map[string]any{"type": "image", "data": encoded, "mime_type": mimeType})
			summary = append(summary, map[string]any{"type": "image", "mimeType": mimeType, "bytes": len(decoded)})
		default:
			if text := common.AsString(part["text"]); strings.TrimSpace(text) != "" {
				item := map[string]any{"type": "text", "text": text}
				content = append(content, item)
				summary = append(summary, item)
			}
		}
	}
	return content, summary, nil
}

func (c *LLMClient) callGoogleInteractionMedia(ctx context.Context, conf config.LLMProviderConfig, model, prompt, mediaType, uri, mimeType string) (string, map[string]any, map[string]any, error) {
	payload := map[string]any{
		"model": model,
		"input": []any{
			map[string]any{"type": "text", "text": prompt},
			map[string]any{"type": mediaType, "uri": uri, "mime_type": mimeType},
		},
		"store": false,
	}
	nativeResp, err := c.doGoogleInteraction(ctx, conf, payload)
	if err != nil {
		return "", payload, nativeResp, err
	}
	response, err := googleInteractionResponse(nativeResp, model)
	if err != nil {
		return "", payload, nativeResp, err
	}
	message, _ := response["message"].(map[string]any)
	text := strings.TrimSpace(common.AsString(message["content"]))
	if text == "" {
		return "", payload, nativeResp, fmt.Errorf("google interaction returned empty %s description", mediaType)
	}
	return text, payload, nativeResp, nil
}

func (c *LLMClient) doGoogleInteraction(ctx context.Context, conf config.LLMProviderConfig, payload map[string]any) (map[string]any, error) {
	if strings.TrimSpace(conf.APIKey) == "" {
		return nil, fmt.Errorf("google provider apiKey is empty")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(conf.BaseURL, "/") + "/v1beta/interactions"
	started := time.Now()
	delay := 500 * time.Millisecond
	var lastResp map[string]any
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("x-goog-api-key", conf.APIKey)
		httpReq.Header.Set("Api-Revision", googleInteractionsAPIRevision)
		res, requestErr := c.http.Do(httpReq)
		if requestErr == nil {
			raw, readErr := io.ReadAll(io.LimitReader(res.Body, 16<<20))
			_ = res.Body.Close()
			if readErr != nil {
				return nil, readErr
			}
			lastResp = map[string]any{}
			_ = json.Unmarshal(raw, &lastResp)
			if res.StatusCode >= 200 && res.StatusCode < 300 {
				return lastResp, nil
			}
			lastErr = fmt.Errorf("Google Interactions API 调用失败: %s%s", res.Status, upstreamErrorSuffix(lastResp))
			if !googleInteractionRetryStatus(res.StatusCode) {
				return lastResp, lastErr
			}
		} else {
			lastErr = requestErr
		}
		if attempt == 4 || time.Since(started)+delay > 30*time.Second {
			break
		}
		select {
		case <-ctx.Done():
			return lastResp, ctx.Err()
		case <-time.After(delay):
		}
		if delay < 8*time.Second {
			delay *= 2
		}
	}
	return lastResp, lastErr
}

func googleInteractionRetryStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusConflict || status == http.StatusTooManyRequests || status >= 500
}

func googleInteractionResponse(nativeResp map[string]any, fallbackModel string) (map[string]any, error) {
	status := strings.TrimSpace(common.AsString(nativeResp["status"]))
	if status == "failed" || status == "cancelled" || status == "budget_exceeded" {
		return nil, fmt.Errorf("google interaction ended with status %s%s", status, upstreamErrorSuffix(nativeResp))
	}
	textBlocks := []string{}
	toolCalls := []any{}
	steps, _ := nativeResp["steps"].([]any)
	for _, rawStep := range steps {
		step, _ := rawStep.(map[string]any)
		switch common.AsString(step["type"]) {
		case "model_output":
			current := []string{}
			contents, _ := step["content"].([]any)
			for _, rawContent := range contents {
				content, _ := rawContent.(map[string]any)
				if common.AsString(content["type"]) == "text" {
					if text := strings.TrimSpace(common.AsString(content["text"])); text != "" {
						current = append(current, text)
					}
				}
			}
			if len(current) > 0 {
				textBlocks = current
			}
		case "function_call":
			toolCalls = append(toolCalls, map[string]any{
				"id":        firstInteractionString(common.AsString(step["id"]), common.AsString(step["call_id"])),
				"name":      common.AsString(step["name"]),
				"arguments": step["arguments"],
			})
		}
	}
	text := strings.Join(textBlocks, "\n")
	if text == "" && len(toolCalls) == 0 {
		return nil, fmt.Errorf("google interaction returned no model output")
	}
	usagePayload, _ := nativeResp["usage"].(map[string]any)
	usage := map[string]any{
		"promptTokens":     interactionInt(usagePayload["total_input_tokens"]),
		"completionTokens": interactionInt(usagePayload["total_output_tokens"]),
		"totalTokens":      interactionInt(usagePayload["total_tokens"]),
	}
	if cached := interactionInt(usagePayload["total_cached_tokens"]); cached > 0 {
		usage["cacheHitTokens"] = cached
	}
	return map[string]any{
		"provider": "google",
		"model":    valueOrString(common.AsString(nativeResp["model"]), fallbackModel),
		"message":  map[string]any{"role": "assistant", "content": text, "toolCalls": toolCalls},
		"usage":    usage,
	}, nil
}

func firstInteractionString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func interactionInt(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int64:
		return int(number)
	case float64:
		return int(number)
	case json.Number:
		parsed, _ := number.Int64()
		return int(parsed)
	default:
		return 0
	}
}
