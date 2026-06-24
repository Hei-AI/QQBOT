package napcat

import (
	"context"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	videocap "QqBot/internal/capabilities/video"
)

const (
	fallbackVideoText       = "[视频]"
	maxVideoDescriptionRune = 400
)

type VideoMessageAnalyzer struct {
	Video videocap.Agent
	HTTP  *http.Client
	Log   func(level, message string, metadata map[string]any)
}

func NewVideoMessageAnalyzer(agent videocap.Agent) VideoMessageAnalyzer {
	return VideoMessageAnalyzer{Video: agent, HTTP: &http.Client{Timeout: 3 * time.Minute}}
}

func (a VideoMessageAnalyzer) AnalyzeVideoSegment(ctx context.Context, data map[string]any) string {
	videoURL := firstHTTPURL(stringAny(data["url"]), stringAny(data["file"]))
	filename := strings.TrimSpace(stringAny(data["file"]))
	if videoURL == "" {
		return fallbackVideoText
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, videoURL, nil)
	if err != nil {
		return fallbackVideoText
	}
	resp, err := client.Do(req)
	if err != nil {
		return fallbackVideoText
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fallbackVideoText
	}
	mimeType := inferVideoMimeType(videoURL, resp.Header.Get("Content-Type"), filename)
	if mimeType == "" {
		return fallbackVideoText
	}
	temp, err := os.CreateTemp("", "qqbot-video-*"+videoExtension(mimeType))
	if err != nil {
		return fallbackVideoText
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, io.LimitReader(resp.Body, 512<<20)); err != nil {
		_ = temp.Close()
		return fallbackVideoText
	}
	if err := temp.Close(); err != nil {
		return fallbackVideoText
	}
	description, err := a.Video.Analyze(ctx, "", videocap.Part{
		Path:     tempPath,
		MimeType: mimeType,
		Filename: firstNonEmpty(filename, inferFilenameFromURL(videoURL)),
	})
	if err != nil {
		return fallbackVideoText
	}
	text := sanitizeMediaDescription(description, maxVideoDescriptionRune)
	if text == "" {
		return fallbackVideoText
	}
	return "[视频: " + text + "]"
}

func firstHTTPURL(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
			return value
		}
	}
	return ""
}

func inferVideoMimeType(rawURL, contentType, filename string) string {
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = contentType[:idx]
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(contentType, "video/") {
		return contentType
	}
	if filename == "" {
		filename = inferFilenameFromURL(rawURL)
	}
	switch strings.ToLower(path.Ext(filename)) {
	case ".mp4":
		return "video/mp4"
	case ".mpeg", ".mpe":
		return "video/mpeg"
	case ".mov":
		return "video/mov"
	case ".avi":
		return "video/avi"
	case ".flv":
		return "video/x-flv"
	case ".mpg":
		return "video/mpg"
	case ".webm":
		return "video/webm"
	case ".wmv":
		return "video/wmv"
	case ".3gp", ".3gpp":
		return "video/3gpp"
	default:
		return ""
	}
}

func videoExtension(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "video/mp4":
		return ".mp4"
	case "video/mpeg":
		return ".mpeg"
	case "video/mov", "video/quicktime":
		return ".mov"
	case "video/avi", "video/x-msvideo":
		return ".avi"
	case "video/x-flv":
		return ".flv"
	case "video/mpg":
		return ".mpg"
	case "video/webm":
		return ".webm"
	case "video/wmv", "video/x-ms-wmv":
		return ".wmv"
	case "video/3gpp":
		return ".3gp"
	default:
		return ".video"
	}
}
