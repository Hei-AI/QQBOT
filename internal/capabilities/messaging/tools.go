package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"QqBot/internal/agentruntime"
)

// Sender 是 SendMessageTool 所需的 NapCat 消息发送能力子集。
type Sender interface {
	SendGroupMessage(groupID, message string) (int, error)
	SendPrivateMessage(userID, message string) (int, error)
}

// SendMessageTool 向群聊或私聊发送文本。
type SendMessageTool struct{ Sender Sender }

func (t SendMessageTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: "send_message", Description: "向指定群聊或私聊发送消息", Parameters: agentruntime.ObjectSchema(map[string]any{
		"targetType": map[string]any{"type": "string", "enum": []string{"group", "private"}},
		"targetId":   map[string]any{"type": "string"},
		"message":    map[string]any{"type": "string"},
	})}
}
func (t SendMessageTool) Kind() string { return "business" }
func (t SendMessageTool) Execute(_ context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	if t.Sender == nil {
		return agentruntime.ToolResult{}, fmt.Errorf("消息发送器不可用")
	}
	targetType, targetID := sendMessageTarget(call.Arguments)
	message, _ := call.Arguments["message"].(string)
	var id int
	var err error
	if targetType == "private" {
		id, err = t.Sender.SendPrivateMessage(targetID, message)
	} else {
		id, err = t.Sender.SendGroupMessage(targetID, message)
	}
	data, _ := json.Marshal(map[string]any{"messageId": id})
	return agentruntime.ToolResult{Kind: "business", Content: string(data)}, err
}

func sendMessageTarget(args map[string]any) (string, string) {
	targetType := strings.TrimSpace(sendMessageStringArg(args, "targetType", "target_type", "messageType", "groupType"))
	targetID := strings.TrimSpace(sendMessageStringArg(args, "targetId", "target_id"))
	if targetType == "" {
		if groupID := strings.TrimSpace(sendMessageStringArg(args, "groupId", "group_id")); groupID != "" {
			targetType = "group"
			targetID = groupID
		} else if userID := strings.TrimSpace(sendMessageStringArg(args, "userId", "user_id", "privateUserId", "private_user_id")); userID != "" {
			targetType = "private"
			targetID = userID
		}
	}
	switch targetType {
	case "qq_group":
		targetType = "group"
	case "qq_private":
		targetType = "private"
	}
	return targetType, targetID
}

func sendMessageStringArg(args map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
