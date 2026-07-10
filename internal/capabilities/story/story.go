package story

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"
)

// Story 是从消息账本批次中提取的持久化叙事记忆。
type Story struct {
	ID                    string    `json:"id"`
	Markdown              string    `json:"markdown"`
	Title                 string    `json:"title"`
	Time                  string    `json:"time"`
	Scene                 string    `json:"scene"`
	People                []string  `json:"people"`
	Impact                string    `json:"impact"`
	SourceMessageSeqStart int       `json:"sourceMessageSeqStart"`
	SourceMessageSeqEnd   int       `json:"sourceMessageSeqEnd"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
	Score                 *float64  `json:"score"`
	MatchedKinds          []string  `json:"matchedKinds"`
}

// MemoryDocument 是 Story 面向搜索/RAG 的投影。
type MemoryDocument struct {
	StoryID string
	Kind    string
	Content string
}

// Repository 抽象 Story 持久化能力。
type Repository interface {
	Save(context.Context, Story) error
	List(context.Context) ([]Story, error)
	Delete(context.Context, string) error
}

// MemoryRepository 是适合测试和本地运行的内存 Story 仓库。
type MemoryRepository struct {
	mu      sync.Mutex
	stories map[string]Story
}

// NewMemoryRepository 创建一个空的内存仓库。
func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{stories: map[string]Story{}}
}

func (r *MemoryRepository) Save(_ context.Context, s Story) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	s.UpdatedAt = now
	r.stories[s.ID] = s
	return nil
}

func (r *MemoryRepository) List(context.Context) ([]Story, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Story, 0, len(r.stories))
	for _, s := range r.stories {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

func (r *MemoryRepository) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	delete(r.stories, id)
	r.mu.Unlock()
	return nil
}

// Service 负责 Story 创建和基于关键词的召回行为。
type Service struct {
	Repo   Repository
	Recall interface {
		Search(context.Context, string, int) ([]Story, error)
	}
}

// Create 校验并持久化 Story，并尽量补齐派生字段。
func (s Service) Create(ctx context.Context, story Story) (Story, error) {
	now := time.Now()
	if story.ID == "" {
		story.ID = now.Format("20060102150405.000000000")
	}
	if story.Title == "" {
		story.Title = ExtractTitle(story.Markdown)
	}
	if story.CreatedAt.IsZero() {
		story.CreatedAt = now
	}
	story.UpdatedAt = now
	if err := s.Repo.Save(ctx, story); err != nil {
		return Story{}, err
	}
	return story, nil
}

// Search 按简单词项重叠度为 Story 排序。
//
// 这是 TS embedding/vector 召回的 Go 兜底实现；它保证公开
// 能力在向量数据库配置完成前也可使用。
func (s Service) Search(ctx context.Context, query string, limit int) ([]Story, error) {
	if s.Recall != nil {
		if items, err := s.Recall.Search(ctx, query, limit); err == nil {
			return items, nil
		}
	}
	items, err := s.Repo.List(ctx)
	if err != nil {
		return nil, err
	}
	query = strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 {
		limit = 20
	}
	type scored struct {
		Story
		score float64
	}
	scoredItems := []scored{}
	for _, item := range items {
		score := Score(item, query)
		if query == "" || score > 0 {
			cp := item
			cp.Score = &score
			scoredItems = append(scoredItems, scored{Story: cp, score: score})
		}
	}
	sort.Slice(scoredItems, func(i, j int) bool {
		if scoredItems[i].score == scoredItems[j].score {
			return scoredItems[i].UpdatedAt.After(scoredItems[j].UpdatedAt)
		}
		return scoredItems[i].score > scoredItems[j].score
	})
	out := []Story{}
	for i, item := range scoredItems {
		if i >= limit {
			break
		}
		out = append(out, item.Story)
	}
	return out, nil
}

// ExtractTitle 返回第一个 Markdown 标题，或一段简短内容预览。
func ExtractTitle(markdown string) string {
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			return strings.TrimSpace(strings.TrimLeft(line, "#"))
		}
	}
	trimmed := strings.TrimSpace(markdown)
	if len([]rune(trimmed)) > 40 {
		return string([]rune(trimmed)[:40])
	}
	return trimmed
}

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
