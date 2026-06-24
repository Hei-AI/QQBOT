package magnetsearch

import (
	"context"
	"encoding/json"
	"strings"

	"QqBot/internal/agentruntime"
)

type SearchTool struct {
	Service      *Service
	DefaultLimit int
}

func (t SearchTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{
		Name:        "searchMagnetFromWeb",
		Description: "通过 TokyoLib 搜索磁力链接。支持演员名搜索和番号搜索；关键词只填写演员名或番号，不要附加年份、最新、新作、资源等修饰词。",
		Parameters: agentruntime.ObjectSchema(map[string]any{
			"query_zhCN": map[string]any{"type": "string", "description": "中文演员名或番号；不要附加年份或修饰词。"},
			"query_enUS": map[string]any{"type": "string", "description": "英文演员名或番号；不要附加年份或修饰词。"},
			"query_jaJP": map[string]any{"type": "string", "description": "日文演员名或番号；不要附加年份或修饰词。"},
			"category":   map[string]any{"type": "string", "description": "可选分类。"},
			"limit":      map[string]any{"type": "integer", "description": "可选结果数量限制。"},
		}),
	}
}

func (SearchTool) Kind() string { return "business" }

func (t SearchTool) Execute(ctx context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	if t.Service == nil {
		return encodeToolResult(map[string]any{"ok": false, "error": "MAGNET_SEARCH_UNAVAILABLE"}), nil
	}
	queries := []string{
		stringArgument(call.Arguments["query_zhCN"]),
		stringArgument(call.Arguments["query_enUS"]),
		stringArgument(call.Arguments["query_jaJP"]),
	}
	if strings.TrimSpace(strings.Join(queries, "")) == "" {
		return encodeToolResult(map[string]any{"ok": false, "error": "INVALID_ARGUMENTS", "message": "至少提供一个非空搜索关键词。"}), nil
	}
	limit := integerArgument(call.Arguments["limit"])
	if limit <= 0 {
		limit = t.DefaultLimit
	}
	if limit <= 0 {
		limit = 30
	}
	if limit > 100 {
		limit = 100
	}
	result := t.Service.Search(ctx, queries, stringArgument(call.Arguments["category"]), limit)
	status := "completed"
	if len(result.Items) == 0 && len(result.Errors) > 0 {
		status = "providers_unavailable_or_no_relevant_results"
	}
	return encodeToolResult(map[string]any{
		"ok":                 true,
		"status":             status,
		"magnetSearchResult": result.Items,
		"providerErrors":     result.Errors,
		"message":            searchResultMessage(result),
	}), nil
}

func searchResultMessage(result Result) string {
	if len(result.Items) > 0 {
		return "搜索完成；结果已按关键词相关性过滤并按 InfoHash 去重。"
	}
	if len(result.Errors) > 0 {
		return "没有找到相关结果；部分 Provider 连接失败或被站点反爬拦截，详见 providerErrors。"
	}
	return "Provider 请求成功，但没有找到与关键词相关的结果。"
}

func encodeToolResult(value map[string]any) agentruntime.ToolResult {
	data, _ := json.Marshal(value)
	return agentruntime.ToolResult{Kind: "business", Content: string(data)}
}

func stringArgument(value any) string {
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func integerArgument(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	default:
		return 0
	}
}
