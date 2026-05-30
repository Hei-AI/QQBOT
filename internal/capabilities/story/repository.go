package story

import (
	"context"
	"sort"
	"sync"
	"time"
)

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
