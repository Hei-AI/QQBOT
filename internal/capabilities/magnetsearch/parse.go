package magnetsearch

import (
	"html"
	"regexp"
	"strconv"
	"strings"
)

var (
	sizePattern  = regexp.MustCompile(`(?i)([\d.]+)\s*([KMGT]?I?B)`)
	tagPattern   = regexp.MustCompile(`(?s)<[^>]*>`)
	spacePattern = regexp.MustCompile(`\s+`)
)

func parseSize(value string) int64 {
	value = strings.TrimSpace(strings.ReplaceAll(value, ",", ""))
	match := sizePattern.FindStringSubmatch(value)
	if len(match) != 3 {
		parsed, _ := strconv.ParseFloat(value, 64)
		return int64(parsed)
	}
	amount, _ := strconv.ParseFloat(match[1], 64)
	multipliers := map[string]float64{
		"B": 1, "KB": 1 << 10, "KIB": 1 << 10,
		"MB": 1 << 20, "MIB": 1 << 20,
		"GB": 1 << 30, "GIB": 1 << 30,
		"TB": 1 << 40, "TIB": 1 << 40,
	}
	return int64(amount * multipliers[strings.ToUpper(match[2])])
}

func textContent(value string) string {
	value = tagPattern.ReplaceAllString(value, " ")
	value = html.UnescapeString(value)
	return strings.TrimSpace(spacePattern.ReplaceAllString(value, " "))
}
