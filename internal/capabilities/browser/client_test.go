package browser

import (
	"QqBot/internal/agentruntime"
	"QqBot/internal/capabilities/vision"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestClientDoSendsActionAndTrimsText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/action" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var request ActionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.SessionID != "chat-1" || request.Action != "read" {
			t.Fatalf("unexpected request: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(ActionResponse{OK: true, Text: "123456789"})
	}))
	defer server.Close()

	client, err := NewClient(Config{BaseURL: server.URL, Timeout: time.Second, MaxResultChars: 6})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Do(context.Background(), ActionRequest{SessionID: "chat-1", Action: "read"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Text != "12345…" {
		t.Fatalf("unexpected trimmed text %q", result.Text)
	}
}

type recordingDescriber struct {
	called bool
}

func (d *recordingDescriber) Describe(_ context.Context, _ string, images []vision.ImagePart) (string, error) {
	d.called = len(images) == 1 && len(images[0].Data) > 0
	return "直播画面里正在展示比赛记分牌。", nil
}

func TestScreenshotToolAddsVisualDescriptionAndDropsBase64(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(ActionResponse{
			OK:               true,
			ScreenshotMIME:   "image/png",
			ScreenshotBase64: "AQID",
		})
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	describer := &recordingDescriber{}
	catalog := (SessionTools{Client: client, SessionID: "live", ScreenshotDescriber: describer}).Catalog()
	result, err := catalog.Execute(context.Background(), agentruntime.ToolCall{
		ID: "1", Name: "browser_screenshot", Arguments: map[string]any{"analyze": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if !describer.called {
		t.Fatal("expected screenshot describer to be called")
	}
	metadata, _ := payload["metadata"].(map[string]any)
	if metadata["visualDescription"] == "" {
		t.Fatalf("missing visual description: %s", result.Content)
	}
	if _, exists := payload["screenshotBase64"]; exists {
		t.Fatalf("base64 should not be exposed to model: %s", result.Content)
	}
}

func TestScreenshotToolCanSaveQQAttachmentWithoutVision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(ActionResponse{
			OK:               true,
			ScreenshotMIME:   "image/png",
			ScreenshotBase64: "AQID",
		})
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	catalog := (SessionTools{Client: client, SessionID: "send", ScreenshotDir: dir}).Catalog()
	result, err := catalog.Execute(context.Background(), agentruntime.ToolCall{
		ID: "2", Name: "browser_screenshot", Arguments: map[string]any{"mode": "send"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	metadata, _ := payload["metadata"].(map[string]any)
	imagePath, _ := metadata["imagePath"].(string)
	if filepath.Dir(imagePath) != dir {
		t.Fatalf("screenshot escaped output directory: %q", imagePath)
	}
	data, err := os.ReadFile(imagePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string([]byte{1, 2, 3}) {
		t.Fatalf("unexpected screenshot bytes: %v", data)
	}
	if _, exists := payload["screenshotBase64"]; exists {
		t.Fatalf("base64 should not be exposed to model: %s", result.Content)
	}
}

func TestScreenshotBothAnalyzesAndSaves(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(ActionResponse{
			OK:               true,
			ScreenshotMIME:   "image/png",
			ScreenshotBase64: "AQID",
		})
	}))
	defer server.Close()
	client, err := NewClient(Config{BaseURL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	describer := &recordingDescriber{}
	catalog := (SessionTools{
		Client: client, SessionID: "both", ScreenshotDir: t.TempDir(), ScreenshotDescriber: describer,
	}).Catalog()
	result, err := catalog.Execute(context.Background(), agentruntime.ToolCall{
		ID: "3", Name: "browser_screenshot", Arguments: map[string]any{"mode": "both"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	metadata, _ := payload["metadata"].(map[string]any)
	if !describer.called || metadata["visualDescription"] == "" || metadata["imagePath"] == "" {
		t.Fatalf("both mode should analyze and save: %s", result.Content)
	}
}

func TestRemoteSidecarRequiresToken(t *testing.T) {
	if _, err := NewClient(Config{BaseURL: "http://192.0.2.10:20009"}); err == nil {
		t.Fatal("expected remote sidecar without token to be rejected")
	}
}
