package story

import "strings"

// ExtractTitle 返回第一个 Markdown 标题，或一段简短内容预览。
func ExtractTitle(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	trimmed := strings.TrimSpace(markdown)
	if len([]rune(trimmed)) > 40 {
		return string([]rune(trimmed)[:40])
	}
	return trimmed
}
