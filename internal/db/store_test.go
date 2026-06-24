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
