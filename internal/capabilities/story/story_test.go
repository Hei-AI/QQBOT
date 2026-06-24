package story

import (
	"context"
	"testing"
)

func TestCreateReturnsPersistedTimestamps(t *testing.T) {
	service := Service{Repo: NewMemoryRepository()}
	created, err := service.Create(context.Background(), Story{Markdown: "# story"})
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("expected timestamps in create result, got %#v", created)
	}
}
