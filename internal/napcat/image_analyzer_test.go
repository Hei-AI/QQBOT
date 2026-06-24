package napcat

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"QqBot/internal/capabilities/vision"
)

type fakeVisionClient struct {
	called bool
}

func (c *fakeVisionClient) Describe(_ context.Context, _ string, images []vision.ImagePart) (string, error) {
	c.called = len(images) == 1 && images[0].MimeType == "image/png" && images[0].Filename == "qq.png"
	return "一张测试图片", nil
}

func TestAnalyzeImageSegmentReadsLocalFilePath(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "qq.png")
	if err := os.WriteFile(imagePath, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeVisionClient{}
	analyzer := ImageMessageAnalyzer{Vision: vision.Agent{Client: client}}
	got := analyzer.AnalyzeImageSegment(context.Background(), map[string]any{"file": imagePath})
	if !client.called || got != "[图片: 一张测试图片]" {
		t.Fatalf("unexpected image analysis: called=%v got=%q", client.called, got)
	}
}

func TestAnalyzeImageSegmentReadsFileURL(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "qq.png")
	if err := os.WriteFile(imagePath, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeVisionClient{}
	analyzer := ImageMessageAnalyzer{Vision: vision.Agent{Client: client}}
	got := analyzer.AnalyzeImageSegment(context.Background(), map[string]any{"url": cqImageTestFileURL(imagePath)})
	if !client.called || got != "[图片: 一张测试图片]" {
		t.Fatalf("unexpected file URL analysis: called=%v got=%q", client.called, got)
	}
}

func TestAnalyzeImageSegmentFallsBackFromBadURLToLocalFile(t *testing.T) {
	dir := t.TempDir()
	imagePath := filepath.Join(dir, "qq.png")
	if err := os.WriteFile(imagePath, []byte{1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	client := &fakeVisionClient{}
	analyzer := ImageMessageAnalyzer{Vision: vision.Agent{Client: client}}
	got, err := analyzer.AnalyzeImageSegmentWithError(context.Background(), map[string]any{
		"url":       "https://127.0.0.1/not-reachable.png",
		"localFile": imagePath,
	})
	if err != nil || !client.called || got != "[图片: 一张测试图片]" {
		t.Fatalf("unexpected fallback analysis: called=%v got=%q err=%v", client.called, got, err)
	}
}

func TestAnalyzeImageSegmentReadsBase64Payload(t *testing.T) {
	client := &fakeVisionClient{}
	analyzer := ImageMessageAnalyzer{Vision: vision.Agent{Client: client}}
	got, err := analyzer.AnalyzeImageSegmentWithError(context.Background(), map[string]any{
		"base64":   base64.StdEncoding.EncodeToString([]byte{1, 2, 3}),
		"mimeType": "image/png",
		"file":     "qq.png",
	})
	if err != nil || !client.called || got != "[图片: 一张测试图片]" {
		t.Fatalf("unexpected base64 analysis: called=%v got=%q err=%v", client.called, got, err)
	}
}

func cqImageTestFileURL(path string) string {
	slash := filepath.ToSlash(path)
	if filepath.VolumeName(path) != "" && slash[0] != '/' {
		slash = "/" + slash
	}
	return "file://" + slash
}
