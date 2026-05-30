package story

import "strings"

// Score 计算范围为 [0,1] 的轻量关键词匹配分数。
func Score(item Story, query string) float64 {
	if query == "" {
		return 0
	}
	text := strings.ToLower(item.Title + "\n" + item.Markdown + "\n" + item.Scene + "\n" + strings.Join(item.People, " "))
	terms := strings.Fields(query)
	if len(terms) == 0 {
		return 0
	}
	hit := 0
	for _, term := range terms {
		if strings.Contains(text, term) {
			hit++
		}
	}
	return float64(hit) / float64(len(terms))
}
