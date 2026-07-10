package chathistory

import (
	"context"
	"encoding/json"
	"strings"

	"QqBot/internal/agentruntime"
	"QqBot/internal/db"
)

// SearchTool 提供受限的原始 QQ 聊天记录检索，不替代 Story 长期记忆。
type SearchTool struct{ Store *db.Store }

func (t SearchTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{
		Name:        "search_chat_history",
		Description: "检索最近最多 7 天的原始 QQ 聊天记录，用于核对原话、说话人、时间或先前约定；不是长期故事记忆。默认返回 5 条，最多 10 条。",
		Parameters: agentruntime.ObjectSchema(map[string]any{
			"query":      map[string]any{"type": "string", "description": "可选关键词；匹配原文或昵称。"},
			"targetType": map[string]any{"type": "string", "enum": []string{"group", "private"}, "description": "可选会话类型。未填 query 时必须和 targetId 一起填写。"},
			"targetId":   map[string]any{"type": "string", "description": "可选群号或私聊 QQ。"},
			"userId":     map[string]any{"type": "string", "description": "可选发送者 QQ，用于进一步缩小范围。"},
			"days":       map[string]any{"type": "integer", "description": "回看天数，1 到 7，默认 7。"},
			"limit":      map[string]any{"type": "integer", "description": "返回条数，1 到 10，默认 5。"},
		}),
	}
}

func (SearchTool) Kind() string { return "business" }

func (t SearchTool) Execute(_ context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	messageType := strings.TrimSpace(stringArg(call.Arguments, "targetType", "messageType"))
	if messageType != "" && messageType != "group" && messageType != "private" {
		return result(map[string]any{"ok": false, "error": "INVALID_TARGET_TYPE", "message": "targetType 只能是 group 或 private。"}), nil
	}
	targetID := strings.TrimSpace(stringArg(call.Arguments, "targetId", "groupId", "privateUserId"))
	keyword := strings.TrimSpace(stringArg(call.Arguments, "query"))
	userID := strings.TrimSpace(stringArg(call.Arguments, "userId"))
	if keyword == "" && (messageType == "" || targetID == "") && userID == "" {
		return result(map[string]any{"ok": false, "error": "CHAT_HISTORY_SCOPE_REQUIRED", "message": "请提供关键词，或同时提供 targetType 和 targetId，或提供 userId。"}), nil
	}
	if t.Store == nil {
		return result(map[string]any{"ok": false, "error": "CHAT_HISTORY_UNAVAILABLE", "message": "聊天记录存储不可用。"}), nil
	}
	items := t.Store.SearchNapcatMessages(db.ChatHistoryQuery{
		Query:       keyword,
		MessageType: messageType,
		TargetID:    targetID,
		UserID:      userID,
		Days:        intArg(call.Arguments["days"]),
		Limit:       intArg(call.Arguments["limit"]),
	})
	entries := make([]map[string]any, 0, len(items))
	for _, item := range items {
		entry := map[string]any{
			"messageType": item.MessageType,
			"rawMessage":  item.RawMessage,
			"createdAt":   item.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		if item.EventTime != nil {
			entry["eventTime"] = item.EventTime.Format("2006-01-02T15:04:05Z07:00")
		}
		if item.GroupID != nil {
			entry["groupId"] = *item.GroupID
		}
		if item.UserID != nil {
			entry["userId"] = *item.UserID
		}
		if item.Nickname != nil {
			entry["nickname"] = *item.Nickname
		}
		if item.MessageID != nil {
			entry["messageId"] = *item.MessageID
		}
		entries = append(entries, entry)
	}
	return result(map[string]any{
		"ok":       true,
		"days":     normalizedDays(call.Arguments["days"]),
		"count":    len(entries),
		"messages": entries,
		"message":  "结果仅来自最近最多 7 天已接收并保存的 QQ 消息。",
	}), nil
}

func result(value any) agentruntime.ToolResult {
	data, _ := json.Marshal(value)
	return agentruntime.ToolResult{Kind: "business", Content: string(data)}
}

func stringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok {
			return value
		}
	}
	return ""
}

func intArg(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case float64:
		return int(v)
	case json.Number:
		out, _ := v.Int64()
		return int(out)
	default:
		return 0
	}
}

func normalizedDays(value any) int {
	days := intArg(value)
	if days <= 0 || days > 7 {
		return 7
	}
	return days
}
