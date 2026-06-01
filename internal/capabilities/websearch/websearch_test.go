package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestURLAwareServiceFetchesURLBeforeFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>Direct page</title><style>.hidden{}</style></head><body><h1>Hello</h1><script>ignored()</script><p>Useful text</p></body></html>`))
	}))
	defer server.Close()

	service := URLAwareService{
		Fallback:            MemoryService{Results: []Result{{Title: "fallback"}}},
		AllowPrivateNetwork: true,
	}
	results, err := service.Search(context.Background(), server.URL, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if results[0].Title != "Direct page" {
		t.Fatalf("got title %q", results[0].Title)
	}
	if !strings.Contains(results[0].Content, "Hello Useful text") {
		t.Fatalf("missing page text in %q", results[0].Content)
	}
	if strings.Contains(results[0].Content, "ignored") {
		t.Fatalf("script content should be removed: %q", results[0].Content)
	}
}

func TestURLAwareServiceRecognizesSchemeLessURL(t *testing.T) {
	got, ok := extractHTTPURL("帮我看看 novalattice.online/wonder.html 是什么")
	if !ok {
		t.Fatal("expected URL to be recognized")
	}
	if got != "https://novalattice.online/wonder.html" {
		t.Fatalf("got %q", got)
	}
}

func TestURLAwareServiceFallsBackForKeyword(t *testing.T) {
	service := URLAwareService{
		Fallback: MemoryService{Results: []Result{{Title: "keyword result"}}},
	}
	results, err := service.Search(context.Background(), "latest indie websites", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "keyword result" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestURLAwareServiceFallsBackWhenFetchFails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusBadGateway)
	}))
	defer server.Close()

	service := URLAwareService{
		Fallback:            MemoryService{Results: []Result{{Title: "fallback"}}},
		AllowPrivateNetwork: true,
	}
	results, err := service.Search(context.Background(), server.URL, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "fallback" {
		t.Fatalf("unexpected results: %#v", results)
	}
}

func TestURLAwareServiceRejectsPrivateURL(t *testing.T) {
	service := URLAwareService{
		Fallback: MemoryService{Results: []Result{{Title: "fallback"}}},
	}
	_, err := service.Search(context.Background(), "http://127.0.0.1:20003/health", 5)
	if err == nil {
		t.Fatal("expected private URL fetch to be rejected")
	}
}
