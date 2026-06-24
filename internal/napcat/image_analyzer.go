package napcat

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"QqBot/internal/capabilities/vision"
)

const (
	fallbackImageText       = "[图片]"
	maxImageDescriptionRune = 180
)

type ImageMessageAnalyzer struct {
	Vision vision.Agent
	HTTP   *http.Client
}

func NewImageMessageAnalyzer(agent vision.Agent) ImageMessageAnalyzer {
	return ImageMessageAnalyzer{Vision: agent, HTTP: &http.Client{Timeout: 20 * time.Second}}
}

func (a ImageMessageAnalyzer) AnalyzeImageSegment(ctx context.Context, data map[string]any) string {
	text, _ := a.AnalyzeImageSegmentWithError(ctx, data)
	return text
}

func (a ImageMessageAnalyzer) AnalyzeImageSegmentWithError(ctx context.Context, data map[string]any) (string, error) {
	if content, mimeType, filename, err := imageBase64Content(data); err == nil && len(content) > 0 {
		description, err := a.Vision.Analyze(ctx, "", []vision.ImagePart{{MimeType: mimeType, Data: content, Filename: filename}})
		if err != nil {
			return fallbackImageText, err
		}
		return formatImageText(description), nil
	}
	refs := imageRefs(data)
	if len(refs) == 0 {
		return fallbackImageText, errors.New("image segment has no url/file/localFile")
	}
	errs := []string{}
	for _, imageRef := range refs {
		content, mimeType, filename, err := a.loadImageContent(ctx, imageRef)
		if err != nil {
			errs = append(errs, imageRef+": "+err.Error())
			continue
		}
		if len(content) == 0 {
			errs = append(errs, imageRef+": image content is empty")
			continue
		}
		description, err := a.Vision.Analyze(ctx, "", []vision.ImagePart{{MimeType: mimeType, Data: content, Filename: filename}})
		if err != nil {
			return fallbackImageText, err
		}
		return formatImageText(description), nil
	}
	if len(errs) == 0 {
		return fallbackImageText, errors.New("image content unavailable")
	}
	return fallbackImageText, errors.New(strings.Join(errs, " | "))
}

func imageBase64Content(data map[string]any) ([]byte, string, string, error) {
	encoded := firstNonEmpty(stringAny(data["base64"]), stringAny(data["imageBase64"]))
	if encoded == "" {
		return nil, "", "", errors.New("missing base64 image")
	}
	mimeType := firstNonEmpty(stringAny(data["mimeType"]), stringAny(data["contentType"]))
	filename := firstNonEmpty(stringAny(data["file"]), "image.png")
	if strings.HasPrefix(encoded, "data:") {
		prefix, payload, ok := strings.Cut(encoded, ",")
		if ok {
			encoded = payload
			if mimeType == "" {
				mimeType = strings.TrimPrefix(strings.TrimSuffix(prefix, ";base64"), "data:")
			}
		}
	}
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", "", err
	}
	if mimeType == "" {
		mimeType = inferImageMimeType(filename, "")
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return content, mimeType, filename, nil
}

func imageRefs(data map[string]any) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, key := range []string{"localFile", "url", "file"} {
		value := strings.TrimSpace(stringAny(data[key]))
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (a ImageMessageAnalyzer) loadImageContent(ctx context.Context, imageRef string) ([]byte, string, string, error) {
	parsed, err := url.Parse(imageRef)
	if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") {
		client := a.HTTP
		if client == nil {
			client = &http.Client{Timeout: 20 * time.Second}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, imageRef, nil)
		if err != nil {
			return nil, "", "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, "", "", fmt.Errorf("download image returned %d", resp.StatusCode)
		}
		mimeType := inferImageMimeType(imageRef, resp.Header.Get("Content-Type"))
		if mimeType == "" {
			return nil, "", "", fmt.Errorf("unknown image MIME type")
		}
		content, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		return content, mimeType, inferFilenameFromURL(imageRef), err
	}
	filename := imageRef
	if filepath.VolumeName(filename) != "" {
		// Windows absolute path such as C:\tmp\a.png; url.Parse sees "c" as a scheme.
	} else if err == nil && parsed.Scheme == "file" {
		filename, err = url.PathUnescape(parsed.Path)
		if err != nil {
			return nil, "", "", err
		}
		if filepath.VolumeName(filename) == "" && len(filename) >= 3 && filename[0] == '/' && filename[2] == ':' {
			filename = filename[1:]
		}
	} else if err == nil && parsed.Scheme != "" {
		return nil, "", "", fmt.Errorf("unsupported image reference scheme %q", parsed.Scheme)
	} else if !filepath.IsAbs(filename) && !strings.ContainsAny(filename, `/\`) {
		return nil, "", "", fmt.Errorf("bare image file id is not a local path")
	}
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, "", "", err
	}
	baseName := filepath.Base(filename)
	mimeType := inferImageMimeType(baseName, "")
	if mimeType == "" {
		return nil, "", "", fmt.Errorf("unknown image MIME type")
	}
	return content, mimeType, baseName, nil
}

func formatImageText(description string) string {
	text := sanitizeVisionDescription(description)
	if text == "" {
		return fallbackImageText
	}
	return "[图片: " + text + "]"
}

func sanitizeVisionDescription(description string) string {
	return sanitizeMediaDescription(description, maxImageDescriptionRune)
}

func sanitizeMediaDescription(description string, maxRunes int) string {
	lines := []string{}
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = regexp.MustCompile(`^#{1,6}\s*`).ReplaceAllString(line, "")
		line = regexp.MustCompile(`^[-*]\s+`).ReplaceAllString(line, "")
		line = regexp.MustCompile(`^\d+[.)]\s*`).ReplaceAllString(line, "")
		lines = append(lines, line)
	}
	flat := strings.Join(lines, "；")
	flat = regexp.MustCompile(`如果你愿意，我还可以.*$`).ReplaceAllString(flat, "")
	flat = regexp.MustCompile(`如果需要，我还可以.*$`).ReplaceAllString(flat, "")
	flat = regexp.MustCompile(`\s+`).ReplaceAllString(flat, " ")
	flat = regexp.MustCompile(`；{2,}`).ReplaceAllString(flat, "；")
	flat = strings.TrimRight(strings.TrimSpace(flat), "；，。、 ")
	runes := []rune(flat)
	if len(runes) <= maxRunes {
		return flat
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func inferImageMimeType(rawURL, contentType string) string {
	if idx := strings.Index(contentType, ";"); idx >= 0 {
		contentType = contentType[:idx]
	}
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if strings.HasPrefix(contentType, "image/") {
		return contentType
	}
	filename := strings.ToLower(inferFilenameFromURL(rawURL))
	switch path.Ext(filename) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return ""
	}
}

func inferFilenameFromURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(path.Base(parsed.Path))
}

func stringAny(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}
