package napcat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	videocap "QqBot/internal/capabilities/video"
)

type fakeVideoClient struct {
	gotMime string
	gotData string
}

func (f *fakeVideoClient) DescribeVideo(_ context.Context, _ string, part videocap.Part) (string, error) {
	data, err := os.ReadFile(part.Path)
	if err != nil {
		return "", err
	}
	f.gotMime = part.MimeType
	f.gotData = string(data)
	return "一名用户展示应用界面，并讲解主要操作。", nil
}

func TestAnalyzeVideoSegment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = io.WriteString(w, "video-data")
	}))
	defer server.Close()

	client := &fakeVideoClient{}
	analyzer := NewVideoMessageAnalyzer(videocap.Agent{Client: client})
	got := analyzer.AnalyzeVideoSegment(context.Background(), map[string]any{"url": server.URL + "/sample.mp4"})
	if got != "[视频: 一名用户展示应用界面，并讲解主要操作]" {
		t.Fatalf("unexpected video description: %q", got)
	}
	if client.gotMime != "video/mp4" || client.gotData != "video-data" {
		t.Fatalf("unexpected video input: mime=%q data=%q", client.gotMime, client.gotData)
	}
}

func TestInferVideoMimeType(t *testing.T) {
	cases := map[string]string{
		"https://example.com/a.mp4":  "video/mp4",
		"https://example.com/a.mov":  "video/mov",
		"https://example.com/a.avi":  "video/avi",
		"https://example.com/a.webm": "video/webm",
		"https://example.com/a.3gp":  "video/3gpp",
	}
	for rawURL, want := range cases {
		if got := inferVideoMimeType(rawURL, "", ""); got != want {
			t.Errorf("%s: got %q want %q", rawURL, got, want)
		}
	}
	if got := inferVideoMimeType("https://example.com/download", "video/mp4; charset=binary", ""); got != "video/mp4" {
		t.Fatalf("content type was not recognized: %q", got)
	}
	if got := inferVideoMimeType("https://example.com/download?id=1", "application/octet-stream", "clip.mp4"); got != "video/mp4" {
		t.Fatalf("filename fallback was not recognized: %q", got)
	}
}

func TestFirstHTTPURLFallsBackToFile(t *testing.T) {
	got := firstHTTPURL("", "https://example.com/video.mp4")
	if got != "https://example.com/video.mp4" {
		t.Fatalf("unexpected video URL: %q", got)
	}
}
