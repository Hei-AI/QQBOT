package db

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStoreRoundTrip(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "store.sqlite"))
	if err != nil {
		t.Fatalf("OpenStore() error = %v", err)
	}
	defer store.Close()

	store.Log("info", "hello", map[string]any{"x": "y"})
	store.AddLlmCall(LlmCallItem{RequestID: "r1", Seq: 1, Provider: "p", Model: "m", Status: "success"})
	msgID := store.AddNapcatMessage(NapcatMessageItem{MessageType: "group", GroupID: StringPtr("123"), UserID: StringPtr("456"), Message: "hi"})
	if msgID == 0 {
		t.Fatalf("AddNapcatMessage() returned zero id")
	}
	seq := store.AddStoryLedger("root", "user", "event")
	if seq == 0 {
		t.Fatalf("AddStoryLedger() returned zero seq")
	}

	store.AddStory(StoryItem{ID: "s1", Markdown: "# story", UpdatedAt: time.Now()})
	store.SaveAgentSnapshot("root", map[string]any{"ok": true})

	article, created := store.UpsertNewsArticle(NewsArticle{SourceKey: "ithome", UpstreamID: "1", Title: "old"})
	if !created || article.ID == 0 {
		t.Fatalf("first UpsertNewsArticle() = (%+v, %v), want created with id", article, created)
	}
	article, created = store.UpsertNewsArticle(NewsArticle{SourceKey: "ithome", UpstreamID: "1", Title: "new"})
	if created || article.Title != "new" {
		t.Fatalf("second UpsertNewsArticle() = (%+v, %v), want update", article, created)
	}

	var snapshot map[string]any
	if !store.LoadAgentSnapshot("root", &snapshot) || snapshot["ok"] != true {
		t.Fatalf("LoadAgentSnapshot() = %#v", snapshot)
	}

	data := store.Snapshot()
	if len(data.AppLogs) != 1 || len(data.LlmCalls) != 1 || len(data.NapcatMessages) != 1 || len(data.StoryLedger) != 1 || len(data.Stories) != 1 || len(data.NewsArticles) != 1 {
		t.Fatalf("unexpected snapshot counts: %+v", data)
	}
	if count := store.CountStoryLedgerAfter("root", 0); count != 1 {
		t.Fatalf("CountStoryLedgerAfter() = %d, want 1", count)
	}
	if latest, ok := store.LatestStoryLedger("root"); !ok || latest.Seq != seq {
		t.Fatalf("LatestStoryLedger() = (%+v, %v), want seq %d", latest, ok, seq)
	}
}

func TestStorePrunesProcessedStoryLedgerTail(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "store.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < 8; i++ {
		store.AddStoryLedger("root", "user", fmt.Sprintf("message-%d", i))
	}
	store.PruneStoryLedgerThrough("root", 6, 2)
	items := store.ListStoryLedgerAfter("root", 0, 0)
	if len(items) != 4 {
		t.Fatalf("got %d ledger items, want processed tail 2 plus pending 2", len(items))
	}
	if items[0].Seq != 5 || items[3].Seq != 8 {
		t.Fatalf("unexpected ledger tail: %#v", items)
	}
}

func TestStoreRecentNapcatMessagesFiltersConversation(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "store.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.AddNapcatMessage(NapcatMessageItem{MessageType: "group", GroupID: StringPtr("1"), UserID: StringPtr("u1"), Message: "old"})
	store.AddNapcatMessage(NapcatMessageItem{MessageType: "group", GroupID: StringPtr("2"), UserID: StringPtr("u2"), Message: "other"})
	store.AddNapcatMessage(NapcatMessageItem{MessageType: "group", GroupID: StringPtr("1"), UserID: StringPtr("u1"), Message: "new"})
	items := store.RecentNapcatMessages("group", "1", 2)
	if len(items) != 2 || items[0].Message != "old" || items[1].Message != "new" {
		t.Fatalf("unexpected recent messages: %#v", items)
	}
}
