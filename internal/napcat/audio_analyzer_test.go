package napcat

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	audiocap "QqBot/internal/capabilities/audio"
)

type fakeAudioClient struct {
	gotMime string
	gotData string
}

func (f *fakeAudioClient) DescribeAudio(_ context.Context, _ string, part audiocap.Part) (string, error) {
	data, err := os.ReadFile(part.Path)
	if err != nil {
		return "", err
	}
	f.gotMime = part.MimeType
	f.gotData = string(data)
	return "有人说：测试语音。", nil
}

func TestAnalyzeAudioSegment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = io.WriteString(w, "audio-data")
	}))
	defer server.Close()

	client := &fakeAudioClient{}
	analyzer := NewAudioMessageAnalyzer(audiocap.Agent{Client: client})
	got := analyzer.AnalyzeAudioSegment(context.Background(), map[string]any{"url": server.URL + "/voice.mp3"})
	if got != "[语音: 有人说：测试语音]" {
		t.Fatalf("unexpected audio description: %q", got)
	}
	if client.gotMime != "audio/mpeg" || client.gotData != "audio-data" {
		t.Fatalf("unexpected audio input: mime=%q data=%q", client.gotMime, client.gotData)
	}
}

func TestInferAudioMimeType(t *testing.T) {
	cases := map[string]string{
		"https://example.com/a.mp3":  "audio/mp3",
		"https://example.com/a.wav":  "audio/wav",
		"https://example.com/a.ogg":  "audio/ogg",
		"https://example.com/a.flac": "audio/flac",
	}
	for rawURL, want := range cases {
		if got := inferAudioMimeType(rawURL, "", ""); got != want {
			t.Errorf("%s: got %q want %q", rawURL, got, want)
		}
	}
	if got := inferAudioMimeType("https://example.com/download", "audio/aac; charset=binary", ""); got != "audio/aac" {
		t.Fatalf("content type was not recognized: %q", got)
	}
	if got := inferAudioMimeType("https://example.com/a.txt", "text/plain", ""); strings.TrimSpace(got) != "" {
		t.Fatalf("unexpected unsupported mime type: %q", got)
	}
	if got := inferAudioMimeType("https://example.com/download?id=1", "application/octet-stream", "漂洋过海来看你.mp3"); got != "audio/mp3" {
		t.Fatalf("filename fallback was not recognized: %q", got)
	}
}
