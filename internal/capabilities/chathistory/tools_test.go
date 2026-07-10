package chathistory

import (
	"QqBot/internal/agentruntime"
	"QqBot/internal/db"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSearchToolReturnsCompactRecentMessages(t *testing.T) {
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "store.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	groupID, userID, nickname := "253631878", "714457117", "小镜"
	messageID := 42
	store.AddNapcatMessage(db.NapcatMessageItem{
		MessageType: "group", GroupID: &groupID, UserID: &userID, Nickname: &nickname, MessageID: &messageID,
		RawMessage: "我在看三体啊", CreatedAt: time.Now(),
	})

	tool := SearchTool{Store: store}
	result, err := tool.Execute(t.Context(), agentruntime.ToolCall{Name: "search_chat_history", Arguments: map[string]any{
		"query": "三体", "days": 99, "limit": 99,
	}})
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		OK       bool `json:"ok"`
		Days     int  `json:"days"`
		Count    int  `json:"count"`
		Messages []struct {
			RawMessage string `json:"rawMessage"`
			Nickname   string `json:"nickname"`
		} `json:"messages"`
	}
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatal(err)
	}
	if !payload.OK || payload.Days != 7 || payload.Count != 1 || payload.Messages[0].RawMessage != "我在看三体啊" || payload.Messages[0].Nickname != "小镜" {
		t.Fatalf("unexpected result: %s", result.Content)
	}
}

func TestSearchToolRequiresScopeWithoutKeyword(t *testing.T) {
	tool := SearchTool{}
	// Scope validation runs before the store availability check.
	result, err := tool.Execute(t.Context(), agentruntime.ToolCall{Name: "search_chat_history", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content == "" || !strings.Contains(result.Content, "CHAT_HISTORY_SCOPE_REQUIRED") {
		t.Fatalf("expected scope error, got %s", result.Content)
	}
}
