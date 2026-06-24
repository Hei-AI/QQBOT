package napcat

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	audiocap "QqBot/internal/capabilities/audio"
)

const (
	fallbackAudioText       = "[语音]"
	maxAudioDescriptionRune = 300
)

type AudioMessageAnalyzer struct {
	Audio audiocap.Agent
	HTTP  *http.Client
	Log   func(level, message string, metadata map[string]any)
}

func NewAudioMessageAnalyzer(agent audiocap.Agent) AudioMessageAnalyzer {
	return AudioMessageAnalyzer{Audio: agent, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

func (a AudioMessageAnalyzer) AnalyzeAudioSegment(ctx context.Context, data map[string]any) string {
	audioURL := strings.TrimSpace(stringAny(data["url"]))
	filename := strings.TrimSpace(stringAny(data["file"]))
	if audioURL == "" {
		a.logFailure("missing_url", filename, "", nil)
		return fallbackAudioText
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, audioURL, nil)
	if err != nil {
		a.logFailure("build_request", filename, "", err)
		return fallbackAudioText
	}
	resp, err := client.Do(req)
	if err != nil {
		a.logFailure("download", filename, "", err)
		return fallbackAudioText
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		a.logFailure("http_status", filename, resp.Header.Get("Content-Type"), fmt.Errorf("%s", resp.Status))
		return fallbackAudioText
	}
	mimeType := inferAudioMimeType(audioURL, resp.Header.Get("Content-Type"), filename)
	if mimeType == "" {
		a.logFailure("mime", filename, resp.Header.Get("Content-Type"), nil)
		return fallbackAudioText
	}
	extension := audioExtension(mimeType)
	temp, err := os.CreateTemp("", "qqbot-audio-*"+extension)
	if err != nil {
		a.logFailure("temp_file", filename, mimeType, err)
		return fallbackAudioText
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if _, err := io.Copy(temp, io.LimitReader(resp.Body, 64<<20)); err != nil {
		_ = temp.Close()
		a.logFailure("save_file", filename, mimeType, err)
		return fallbackAudioText
	}
	if err := temp.Close(); err != nil {
		a.logFailure("close_file", filename, mimeType, err)
		return fallbackAudioText
	}
	description, err := a.Audio.Analyze(ctx, "", audiocap.Part{
		Path:     tempPath,
		MimeType: mimeType,
		Filename: firstNonEmpty(filename, inferFilenameFromURL(audioURL)),
	})
	if err != nil {
		a.logFailure("gemini", filename, mimeType, err)
		return fallbackAudioText
	}
	text := sanitizeMediaDescription(description, maxAudioDescriptionRune)
	if text == "" {
		a.logFailure("empty_result", filename, mimeType, nil)
		return fallbackAudioText
	}
	return "[语音: " + text + "]"
}

func (a AudioMessageAnalyzer) logFailure(stage, filename, mimeType string, err error) {
	if a.Log == nil {
		return
	}
	metadata := map[string]any{
		"event":    "napcat.media.audio_failed",
		"stage":    stage,
		"filename": filename,
		"mimeType": mimeType,
	}
	if err != nil {
		metadata["error"] = err.Error()
	}
	a.Log("warn", "NapCat audio understanding failed", metadata)
}

func inferAudioMimeType(rawURL, contentType, filename string) string {
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = contentType[:idx]
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(contentType, "audio/") {
		return contentType
	}
	if filename == "" {
		filename = inferFilenameFromURL(rawURL)
	}
	switch strings.ToLower(path.Ext(filename)) {
	case ".mp3":
		return "audio/mp3"
	case ".wav":
		return "audio/wav"
	case ".aac":
		return "audio/aac"
	case ".ogg", ".oga":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	case ".aif", ".aiff":
		return "audio/aiff"
	default:
		return ""
	}
}

func audioExtension(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/wav", "audio/x-wav":
		return ".wav"
	case "audio/aac":
		return ".aac"
	case "audio/ogg":
		return ".ogg"
	case "audio/flac":
		return ".flac"
	case "audio/aiff", "audio/x-aiff":
		return ".aiff"
	default:
		return ".audio"
	}
}
