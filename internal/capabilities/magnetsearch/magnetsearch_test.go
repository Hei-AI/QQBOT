package magnetsearch

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
)

type fakeProvider struct {
	name     string
	priority int
	items    []Item
	err      error
	mu       sync.Mutex
	queries  []string
}

func (p *fakeProvider) Name() string  { return p.name }
func (p *fakeProvider) Priority() int { return p.priority }
func (p *fakeProvider) Search(_ context.Context, query string, _ SearchOptions) ([]Item, error) {
	p.mu.Lock()
	p.queries = append(p.queries, query)
	p.mu.Unlock()
	return append([]Item(nil), p.items...), p.err
}

func TestServiceSearchPrioritizesAndDeduplicates(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	custom := &fakeProvider{name: "custom", priority: 0, items: []Item{{
		ID: "custom-id", Name: "测试优先结果", Magnet: "magnet:?xt=urn:btih:" + hash,
	}}}
	fallback := &fakeProvider{name: "fallback", priority: 1, items: []Item{
		{ID: "duplicate", Name: "测试重复结果", Magnet: "magnet:?xt=urn:btih:" + strings.ToUpper(hash)},
		{ID: "unique", Name: "test 后备结果"},
	}}
	failing := &fakeProvider{name: "broken", priority: 1, err: errors.New("offline")}
	service := &Service{Providers: []Provider{fallback, custom, failing}}

	result := service.Search(context.Background(), []string{"测试", "测试", " test "}, "", 10)
	if len(result.Items) != 2 {
		t.Fatalf("expected 2 deduplicated items, got %#v", result.Items)
	}
	if result.Items[0].Provider != "custom" || result.Items[0].Name != "测试优先结果" {
		t.Fatalf("custom provider should be prioritized: %#v", result.Items)
	}
	if len(result.Errors) != 2 {
		t.Fatalf("one error per unique query expected, got %#v", result.Errors)
	}
	if len(custom.queries) != 2 || len(fallback.queries) != 2 {
		t.Fatalf("providers should receive unique queries: custom=%v fallback=%v", custom.queries, fallback.queries)
	}
}

func TestExtractInfoHashSupportsHexAndBase32(t *testing.T) {
	hexHash, ok := ExtractInfoHash("magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567")
	if !ok || hexHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("unexpected hex hash: %q %v", hexHash, ok)
	}
	base32Hash, ok := ExtractInfoHash("magnet:?xt=urn:btih:AERUKZ4JVPG66AJDIVTYTK6N54ASGRLH")
	if !ok || len(base32Hash) != 40 {
		t.Fatalf("unexpected base32 hash: %q %v", base32Hash, ok)
	}
}

func TestFilterRelevantItemsIgnoresGenericYearAndNewWords(t *testing.T) {
	items := []Item{
		{Name: "Project Hail Mary (2026)"},
		{Name: "河北彩花 新作 ABC-123"},
	}
	filtered := filterRelevantItems(items, "河北彩花 新片 2026")
	if len(filtered) != 1 || filtered[0].Name != "河北彩花 新作 ABC-123" {
		t.Fatalf("irrelevant year-only matches should be removed: %#v", filtered)
	}
}

func TestParseTokyoLibWorksAndMagnets(t *testing.T) {
	body := `<a class="work" href="/v/546332">
<h4 class="work-id">SNOS-283</h4><h4 class="work-title">测试作品</h4>
<div class="work-meta"><span>📅 2026-06-04</span></div>
<div class="work-actresses"><span class="work-actress">测试演员</span></div></a>`
	works := parseTokyoLibWorks(body, "https://tokyolib.com")
	if len(works) != 1 || works[0].URL != "https://tokyolib.com/v/546332" || !strings.Contains(works[0].Name, "测试演员") {
		t.Fatalf("unexpected works: %#v", works)
	}
	detail := `<div class="magnet"><div class="magnet-title">
<span class="magnet-size tag is-light">3.44GB</span>
<a href="magnet:?xt=urn:btih:0123456789ABCDEF0123456789ABCDEF01234567&amp;amp;dn=SNOS-283">SNOS-283</a>
</div></div>`
	items := parseTokyoLibMagnets(detail, works[0].Name, works[0].URL, works[0].Date)
	if len(items) != 1 || items[0].Size == 0 || strings.Contains(items[0].Magnet, "&amp;") {
		t.Fatalf("unexpected items: %#v", items)
	}
}

func TestParseTokyoLibActressLinks(t *testing.T) {
	body := `<a class="actress" href="/actress/18566"><p>河北彩花</p></a>`
	links := parseTokyoLibActressLinks(body, "https://tokyolib.com")
	if len(links) != 1 || links[0] != "https://tokyolib.com/actress/18566" {
		t.Fatalf("unexpected actress links: %#v", links)
	}
}

func TestNormalizeTokyoLibQuerySeparatesActressAndIDSearch(t *testing.T) {
	searchType, query := normalizeTokyoLibQuery("河北彩花 最新 2026 资源")
	if searchType != "actress" || query != "河北彩花" {
		t.Fatalf("unexpected actress query: type=%q query=%q", searchType, query)
	}

	searchType, query = normalizeTokyoLibQuery("再找一下 snos_283 最新")
	if searchType != "id" || query != "SNOS-283" {
		t.Fatalf("unexpected ID query: type=%q query=%q", searchType, query)
	}
}
