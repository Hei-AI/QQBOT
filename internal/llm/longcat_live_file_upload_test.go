//go:build live_longcat

package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveLongCatAnthropicFileUploadVision(t *testing.T) {
	apiKey := os.Getenv("LONGCAT_API_KEY")
	imagePath := os.Getenv("LONGCAT_TEST_IMAGE")
	if apiKey == "" || imagePath == "" {
		t.Skip("LONGCAT_API_KEY and LONGCAT_TEST_IMAGE are required")
	}
	fileID, uploadBody := liveLongCatUploadFile(t, apiKey, imagePath)
	t.Logf("uploaded file_id=%s body=%s", fileID, trimLog(uploadBody, 500))

	answer, raw := liveLongCatAskUploadedImage(t, apiKey, fileID)
	t.Logf("answer=%s", answer)
	t.Logf("raw=%s", trimLog(raw, 800))
}

func liveLongCatUploadFile(t *testing.T, apiKey, imagePath string) (string, string) {
	t.Helper()
	data, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filepath.Base(imagePath))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.longcat.chat/anthropic/v1/files", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "files-api-2025-04-14")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("upload failed: status=%s body=%s", resp.Status, string(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode upload response: %v body=%s", err, string(raw))
	}
	fileID := strings.TrimSpace(fmt.Sprint(payload["id"]))
	if fileID == "" || fileID == "<nil>" {
		t.Fatalf("upload response has no id: %s", string(raw))
	}
	return fileID, string(raw)
}

func liveLongCatAskUploadedImage(t *testing.T, apiKey, fileID string) (string, string) {
	t.Helper()
	payload := map[string]any{
		"model":      "LongCat-2.0",
		"max_tokens": 128,
		"system":     "你是图片 OCR。必须根据上传文件回答。不要寒暄。",
		"messages": []map[string]any{{
			"role": "user",
			"content": []map[string]any{
				{"type": "text", "text": "只回答两项：1. 图片标题；2. 图片中最大的数字。"},
				{"type": "image", "source": map[string]any{"type": "file", "file_id": fileID}},
			},
		}},
	}
	rawBody, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.longcat.chat/anthropic/v1/messages", bytes.NewReader(rawBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Anthropic-Version", "2023-06-01")
	req.Header.Set("Anthropic-Beta", "files-api-2025-04-14")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("message failed: status=%s body=%s", resp.Status, string(raw))
	}
	var payloadResp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &payloadResp); err != nil {
		t.Fatalf("decode message response: %v body=%s", err, string(raw))
	}
	parts := []string{}
	for _, part := range payloadResp.Content {
		if part.Type == "text" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n"), string(raw)
}
