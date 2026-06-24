package agent

import (
	"QqBot/internal/config"
	"QqBot/internal/db"
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestReindexStoriesSkipsUpToDateStories(t *testing.T) {
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	story := db.StoryItem{
		ID:        "s1",
		Markdown:  "# 一件事\n- 时间：2026-05-21\n- 场景：测试\n- 人物：小明\n- 影响：有影响\n\n起因：开始\n经过：\n1. 发生\n结果：结束",
		Title:     "一件事",
		Time:      "2026-05-21",
		Scene:     "测试",
		People:    []string{"小明"},
		Impact:    "有影响",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	store.AddStory(story)
	store.ReplaceStoryMemoryDocuments("s1", []db.StoryMemoryDocument{
		{Kind: "overview", EmbeddingModel: "m", EmbeddingDim: 3},
		{Kind: "people_scene", EmbeddingModel: "m", EmbeddingDim: 3},
		{Kind: "process", EmbeddingModel: "m", EmbeddingDim: 3},
	})
	cfg := &config.Config{}
	cfg.Server.Agent.Story.Memory.Embedding.Model = "m"
	cfg.Server.Agent.Story.Memory.Embedding.OutputDimensionality = 3

	resp := ReindexStories(context.Background(), cfg, store, "outdated")
	if resp.TargetedStories != 0 || resp.SkippedStories != 1 {
		t.Fatalf("expected story to be skipped, got %#v", resp)
	}
}

func TestReindexStoriesReportsMissingEmbeddingConfig(t *testing.T) {
	store, err := db.OpenStore(filepath.Join(t.TempDir(), "store.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.AddStory(db.StoryItem{ID: "s1", Markdown: "# 一件事", Title: "一件事", CreatedAt: time.Now(), UpdatedAt: time.Now()})

	resp := ReindexStories(context.Background(), &config.Config{}, store, "all")
	if resp.TargetedStories != 1 || resp.FailedStories != 1 || len(resp.Failures) != 1 {
		t.Fatalf("expected missing embedding config failure, got %#v", resp)
	}
}
