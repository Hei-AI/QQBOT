package napcat

import (
	rootagent "QqBot/internal/agent"
	"QqBot/internal/config"
	"QqBot/internal/db"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreferredNapcatImageRefUsesExistingLocalFileBeforeURL(t *testing.T) {
	imagePath := filepath.Join(t.TempDir(), "qq.jpg")
	if err := os.WriteFile(imagePath, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{
		"url":  "https://multimedia.nt.qq.com.cn/expired.jpg",
		"file": imagePath,
	}
	if got := preferredNapcatImageRef(payload); got != imagePath {
		t.Fatalf("expected local file %q, got %q", imagePath, got)
	}
}

func TestMediaKindFromFilename(t *testing.T) {
	cases := map[string]string{
		"漂洋过海来看你.mp3":  "audio",
		"voice.FLAC":   "audio",
		"clip.mp4":     "video",
		"movie.WEBM":   "video",
		"document.pdf": "",
	}
	for filename, want := range cases {
		if got := mediaKindFromFilename(filename); got != want {
			t.Errorf("%s: got %q want %q", filename, got, want)
		}
	}
}

func TestRenderForwardNodes(t *testing.T) {
	nodes := []any{
		map[string]any{
			"sender":  map[string]any{"nickname": "小伊"},
			"time":    float64(1780714940),
			"message": []any{map[string]any{"type": "text", "data": map[string]any{"text": "第一句"}}},
		},
		map[string]any{
			"type": "node",
			"data": map[string]any{
				"nickname": "小沐",
				"content": []any{
					map[string]any{"type": "text", "data": map[string]any{"text": "看图"}},
					map[string]any{"type": "image", "data": map[string]any{}},
				},
			},
		},
	}
	got := renderForwardNodes(nodes, 0)
	if !strings.Contains(got, "小伊") || !strings.Contains(got, "第一句") || !strings.Contains(got, "小沐: 看图[图片]") {
		t.Fatalf("unexpected forward preview: %q", got)
	}
}

func TestRenderCompactSegments(t *testing.T) {
	got := renderCompactSegments([]any{
		map[string]any{"type": "reply", "data": map[string]any{"id": "1"}},
		map[string]any{"type": "text", "data": map[string]any{"text": "收到"}},
		map[string]any{"type": "record", "data": map[string]any{}},
	}, 0)
	if got != "[引用消息]收到[语音]" {
		t.Fatalf("unexpected compact rendering: %q", got)
	}
}

func TestNormalizeFileSegmentDownloadsTextPreview(t *testing.T) {
	t.Chdir(t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("第一行\n第二行"))
	}))
	defer server.Close()
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	gateway := NewNapcatGateway(&config.Config{}, store, rootagent.NewEventQueue(), nil)

	segments, raw := gateway.normalizeMessageSegments(map[string]any{
		"message": []any{map[string]any{
			"type": "file",
			"data": map[string]any{"file": "note.txt", "url": server.URL},
		}},
	}, "1001")

	if len(segments) != 1 || segments[0].Type != "file" {
		t.Fatalf("unexpected segments: %#v", segments)
	}
	if !strings.Contains(raw, "[文件: note.txt]") || !strings.Contains(raw, "第一行") {
		t.Fatalf("file preview not rendered: %q", raw)
	}
	if segments[0].Data["localFile"] == "" || segments[0].Data["preview"] == "" {
		t.Fatalf("file data missing local path/preview: %#v", segments[0].Data)
	}
}

func TestParseOutgoingMessageKeepsBrowserScreenshotAsImageSegment(t *testing.T) {
	segments := parseOutgoingMessage("页面截图[CQ:image,file=file:///D:/qq-bot/data/browser-screenshots/browser.png]")
	if len(segments) != 2 {
		t.Fatalf("unexpected segments: %#v", segments)
	}
	if segments[0]["type"] != "text" || segments[1]["type"] != "image" {
		t.Fatalf("unexpected segment types: %#v", segments)
	}
	data, _ := segments[1]["data"].(map[string]any)
	if data["file"] != "file:///D:/qq-bot/data/browser-screenshots/browser.png" {
		t.Fatalf("unexpected image file: %#v", data)
	}
}

func TestHandleEventPersistsButDoesNotPublishSelfGroupMessage(t *testing.T) {
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events := rootagent.NewEventQueue()
	cfg := &config.Config{}
	cfg.Server.Napcat.ListenGroupIDs = []string{"1001"}
	cfg.Server.Bot.QQ = "42"
	gateway := NewNapcatGateway(cfg, store, events, nil)

	gateway.handleEvent(map[string]any{
		"post_type":    "message",
		"message_type": "group",
		"group_id":     "1001",
		"user_id":      "42",
		"self_id":      "42",
		"message_id":   float64(7),
		"message":      "hello",
		"sender":       map[string]any{"nickname": "bot"},
	})

	if got := len(store.Snapshot().NapcatMessages); got != 1 {
		t.Fatalf("expected self message to be persisted, got %d", got)
	}
	if got := events.Count(); got != 0 {
		t.Fatalf("expected self message not to publish Agent event, got %d", got)
	}
}

func TestHandleEventPublishesOtherGroupMessage(t *testing.T) {
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events := rootagent.NewEventQueue()
	cfg := &config.Config{}
	cfg.Server.Napcat.ListenGroupIDs = []string{"1001"}
	cfg.Server.Bot.QQ = "42"
	gateway := NewNapcatGateway(cfg, store, events, nil)

	gateway.handleEvent(map[string]any{
		"post_type":    "message",
		"message_type": "group",
		"group_id":     "1001",
		"user_id":      "24",
		"self_id":      "42",
		"message_id":   float64(8),
		"message":      "hello",
		"sender":       map[string]any{"nickname": "friend"},
	})

	if got := events.Count(); got != 1 {
		t.Fatalf("expected non-self message to publish Agent event, got %d", got)
	}
}

func TestHandleEventPublishesGroupBanNotice(t *testing.T) {
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events := rootagent.NewEventQueue()
	cfg := &config.Config{}
	cfg.Server.Napcat.ListenGroupIDs = []string{"1001"}
	cfg.Server.Bot.QQ = "42"
	gateway := NewNapcatGateway(cfg, store, events, nil)

	gateway.handleEvent(map[string]any{
		"post_type":   "notice",
		"notice_type": "group_ban",
		"sub_type":    "ban",
		"group_id":    "1001",
		"user_id":     "42",
		"operator_id": "24",
		"duration":    float64(600),
		"time":        float64(1780714940),
	})

	drained := events.DequeueAll()
	if len(drained) != 1 {
		t.Fatalf("expected one group ban notice event, got %d", len(drained))
	}
	event := drained[0]
	if event.Type != "napcat_group_ban_notice" {
		t.Fatalf("unexpected event type: %s", event.Type)
	}
	if event.Data["isSelf"] != true || event.Data["duration"] != 600 || event.Data["operatorId"] != "24" {
		t.Fatalf("unexpected event data: %#v", event.Data)
	}
}

func TestHandleEventIgnoresUnlistenedGroupBanNotice(t *testing.T) {
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	events := rootagent.NewEventQueue()
	cfg := &config.Config{}
	cfg.Server.Napcat.ListenGroupIDs = []string{"1001"}
	gateway := NewNapcatGateway(cfg, store, events, nil)

	gateway.handleEvent(map[string]any{
		"post_type":   "notice",
		"notice_type": "group_ban",
		"sub_type":    "lift_ban",
		"group_id":    "2002",
		"user_id":     "42",
	})

	if got := events.Count(); got != 0 {
		t.Fatalf("expected unlistened group notice not to publish Agent event, got %d", got)
	}
}
