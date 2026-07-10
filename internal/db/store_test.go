package db

import (
	"QqBot/internal/agentruntime"
	"path/filepath"
	"testing"
	"time"
)

func TestStoryLedgerQueriesIgnoreLegacyInternalMessages(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "store.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	seq := store.AddStoryLedger("root", "user", "<qq_message>真实消息</qq_message>")
	store.AppendLedger("root", agentruntime.Message{Role: "assistant", Content: "内部等待"})
	if got := store.CountStoryLedgerAfter("root", 0); got != 1 {
		t.Fatalf("legacy internal messages must not count as story input: %d", got)
	}
	latest, ok := store.LatestStoryLedger("root")
	if !ok || latest.Seq != seq || latest.Content != "<qq_message>真实消息</qq_message>" {
		t.Fatalf("unexpected latest story ledger item: %#v ok=%v", latest, ok)
	}
	items := store.ListStoryLedgerAfter("root", 0, 10)
	if len(items) != 1 || items[0].Seq != seq {
		t.Fatalf("unexpected story ledger items: %#v", items)
	}
}

func TestStringPtrPreservesIntegerTrailingZero(t *testing.T) {
	got := StringPtr(float64(562223500))
	if got == nil {
		t.Fatal("expected string pointer")
	}
	if *got != "562223500" {
		t.Fatalf("expected trailing zero to be preserved, got %q", *got)
	}
}

func TestSearchNapcatMessagesRestrictsTimeAndScope(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "store.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	groupID, userID, nickname := "10001", "20002", "小镜"
	store.AddNapcatMessage(NapcatMessageItem{
		MessageType: "group", GroupID: &groupID, UserID: &userID, Nickname: &nickname,
		RawMessage: "山手线还在挖", CreatedAt: time.Now().Add(-time.Hour),
	})
	store.AddNapcatMessage(NapcatMessageItem{
		MessageType: "group", GroupID: &groupID, UserID: &userID, Nickname: &nickname,
		RawMessage: "过期消息", CreatedAt: time.Now().AddDate(0, 0, -8),
	})

	items := store.SearchNapcatMessages(ChatHistoryQuery{Query: "山手线", Days: 7, Limit: 10})
	if len(items) != 1 || items[0].RawMessage != "山手线还在挖" {
		t.Fatalf("unexpected keyword search: %#v", items)
	}
	items = store.SearchNapcatMessages(ChatHistoryQuery{MessageType: "group", TargetID: groupID, Days: 7, Limit: 10})
	if len(items) != 1 || items[0].RawMessage != "山手线还在挖" {
		t.Fatalf("unexpected scoped search: %#v", items)
	}
	if items = store.SearchNapcatMessages(ChatHistoryQuery{Days: 7}); items != nil {
		t.Fatalf("unscoped empty-query search should be rejected: %#v", items)
	}
}

func TestLLMTokenUsageSummaryUsesStructuredTable(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "store.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	store.AddLlmCall(LlmCallItem{
		RequestID: "req-1",
		Seq:       1,
		Provider:  "longcat",
		Model:     "LongCat-2.0",
		Extension: map[string]any{"usage": "agent"},
		Status:    "success",
		CreatedAt: time.Date(2026, 7, 3, 12, 0, 0, 0, time.Local),
		ResponsePayload: map[string]any{"usage": map[string]any{
			"promptTokens":     120159,
			"completionTokens": 180,
			"totalTokens":      120339,
			"cacheHitTokens":   119936,
			"cacheMissTokens":  223,
		}},
	})
	store.AddLlmCall(LlmCallItem{
		RequestID: "req-2",
		Seq:       1,
		Provider:  "longcat",
		Model:     "LongCat-2.0",
		Extension: map[string]any{"usage": "agent"},
		Status:    "success",
		CreatedAt: time.Date(2026, 7, 2, 12, 0, 0, 0, time.Local),
		ResponsePayload: map[string]any{"usage": map[string]any{
			"promptTokens":     1,
			"completionTokens": 1,
			"totalTokens":      2,
		}},
	})
	store.AddLlmCall(LlmCallItem{
		RequestID: "req-3",
		Seq:       1,
		Provider:  "longcat",
		Model:     "LongCat-2.0",
		Status:    "success",
		CreatedAt: time.Date(2026, 7, 3, 13, 0, 0, 0, time.Local),
		ResponsePayload: map[string]any{"usage": map[string]any{
			"promptTokens":     2,
			"completionTokens": 3,
			"totalTokens":      5,
		}},
	})

	summary := store.LLMTokenUsageSummary(nil, nil)
	if summary.Total.Calls != 3 {
		t.Fatalf("expected three calls, got %#v", summary.Total)
	}
	start := time.Date(2026, 7, 3, 0, 0, 0, 0, time.Local)
	end := start.AddDate(0, 0, 1)
	summary = store.LLMTokenUsageSummary(&start, &end)
	if summary.Total.Calls != 2 {
		t.Fatalf("expected two calls in one daily row, got %#v", summary.Total)
	}
	if summary.Total.Input != 120161 || summary.Total.Output != 183 || summary.Total.CacheRead != 119936 || summary.Total.CacheWrite != 223 || summary.Total.Total != 120344 {
		t.Fatalf("unexpected token summary: %#v", summary.Total)
	}
	if len(summary.Recent) != 1 || summary.Recent[0].Date != "2026-07-03" || summary.Recent[0].Calls != 2 {
		t.Fatalf("unexpected recent rows: %#v", summary.Recent)
	}
}

func TestNewsFeedCursorListsNewArticlesAndAdvances(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	base := time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC)
	store.AddNewsArticle(NewsArticle{ID: 1, SourceKey: "ithome", Title: "old", PublishedAt: base})
	store.AddNewsArticle(NewsArticle{ID: 2, SourceKey: "ithome", Title: "same-newer-id", PublishedAt: base})
	store.AddNewsArticle(NewsArticle{ID: 3, SourceKey: "ithome", Title: "new", PublishedAt: base.Add(time.Hour)})
	store.AddNewsArticle(NewsArticle{ID: 4, SourceKey: "other", Title: "ignored", PublishedAt: base.Add(2 * time.Hour)})

	store.UpsertNewsFeedCursor("ithome", 1, base)
	cursor, ok := store.NewsFeedCursor("ithome")
	if !ok {
		t.Fatal("expected cursor")
	}
	if got := store.CountNewsArticlesNewerThanCursor("ithome", cursor); got != 2 {
		t.Fatalf("unexpected new article count: %d", got)
	}
	items := store.ListNewsArticlesNewerThanCursor("ithome", cursor, 10)
	if len(items) != 2 || items[0].ID != 3 || items[1].ID != 2 {
		t.Fatalf("unexpected new articles: %#v", items)
	}
	store.UpsertNewsFeedCursor("ithome", items[0].ID, items[0].PublishedAt)
	cursor, _ = store.NewsFeedCursor("ithome")
	if got := store.CountNewsArticlesNewerThanCursor("ithome", cursor); got != 0 {
		t.Fatalf("expected cursor to mark feed read, got %d new articles", got)
	}
}

func TestStoryTimesAreFilledAndRepaired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.sqlite")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	store.AddStory(StoryItem{ID: "20260606102319.868609100", Markdown: "# story"})
	item := store.Snapshot().Stories[0]
	if item.CreatedAt.IsZero() || item.UpdatedAt.IsZero() {
		t.Fatalf("expected story timestamps, got %#v", item)
	}
	zero := StoryItem{ID: item.ID, Markdown: item.Markdown}
	if _, err := store.db.Exec(
		`UPDATE stories SET updated_at = ?, item = ? WHERE id = ?`,
		formatTime(time.Time{}),
		mustJSON(zero),
		item.ID,
	); err != nil {
		t.Fatal(err)
	}
	store.Close()

	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	item = store.Snapshot().Stories[0]
	want := time.Date(2026, 6, 6, 10, 23, 19, 868609100, time.Local)
	if !item.CreatedAt.Equal(want) {
		t.Fatalf("expected timestamp inferred from story id %v, got %v", want, item.CreatedAt)
	}
}
