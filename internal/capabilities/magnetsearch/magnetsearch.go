package magnetsearch

import (
	"context"
	"encoding/base32"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

type Category struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type Item struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Magnet    string     `json:"magnet,omitempty"`
	Link      string     `json:"link,omitempty"`
	Size      int64      `json:"size,omitempty"`
	Date      *time.Time `json:"date,omitempty"`
	Seeds     int        `json:"seeds,omitempty"`
	Peers     int        `json:"peers,omitempty"`
	Downloads int        `json:"downloads,omitempty"`
	Category  *Category  `json:"category,omitempty"`
	Provider  string     `json:"provider"`
}

type SearchOptions struct {
	Category string
}

type Provider interface {
	Name() string
	Priority() int
	Search(context.Context, string, SearchOptions) ([]Item, error)
}

type ProviderError struct {
	Provider string `json:"provider"`
	Query    string `json:"query"`
	Error    string `json:"error"`
}

type Result struct {
	Items  []Item          `json:"items"`
	Errors []ProviderError `json:"errors,omitempty"`
}

type Service struct {
	Providers []Provider
}

func NewDefaultService(client *http.Client, tokyoLibBaseURL string) *Service {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Service{Providers: []Provider{
		NewTokyoLibProvider(client, tokyoLibBaseURL),
	}}
}

func (s *Service) Search(ctx context.Context, queries []string, category string, limit int) Result {
	uniqueQueries := normalizeQueries(queries)
	if len(uniqueQueries) == 0 || len(s.Providers) == 0 {
		return Result{}
	}

	type providerResult struct {
		priority   int
		order      int
		queryOrder int
		items      []Item
		err        *ProviderError
	}
	results := make(chan providerResult, len(uniqueQueries)*len(s.Providers))
	var wg sync.WaitGroup
	for providerOrder, provider := range s.Providers {
		for queryOrder, query := range uniqueQueries {
			wg.Add(1)
			go func(provider Provider, providerOrder, queryOrder int, query string) {
				defer wg.Done()
				items, err := provider.Search(ctx, query, SearchOptions{Category: category})
				items = filterRelevantItems(items, query)
				for i := range items {
					items[i].Provider = provider.Name()
				}
				result := providerResult{priority: provider.Priority(), order: providerOrder, queryOrder: queryOrder, items: items}
				if err != nil {
					result.err = &ProviderError{Provider: provider.Name(), Query: query, Error: err.Error()}
				}
				results <- result
			}(provider, providerOrder, queryOrder, query)
		}
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	collected := make([]providerResult, 0, cap(results))
	for result := range results {
		collected = append(collected, result)
	}
	sort.SliceStable(collected, func(i, j int) bool {
		if collected[i].priority != collected[j].priority {
			return collected[i].priority < collected[j].priority
		}
		if collected[i].order != collected[j].order {
			return collected[i].order < collected[j].order
		}
		return collected[i].queryOrder < collected[j].queryOrder
	})

	allItems := make([]Item, 0)
	errorsOut := make([]ProviderError, 0)
	for _, result := range collected {
		allItems = append(allItems, result.items...)
		if result.err != nil {
			errorsOut = append(errorsOut, *result.err)
		}
	}
	items := deduplicate(allItems)
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return Result{Items: items, Errors: errorsOut}
}

func filterRelevantItems(items []Item, query string) []Item {
	terms := relevanceTerms(query)
	if len(terms) == 0 {
		return items
	}
	filtered := make([]Item, 0, len(items))
	for _, item := range items {
		name := strings.ToLower(item.Name)
		for _, term := range terms {
			if strings.Contains(name, term) {
				filtered = append(filtered, item)
				break
			}
		}
	}
	return filtered
}

func relevanceTerms(query string) []string {
	stopWords := map[string]bool{
		"new": true, "latest": true, "movie": true, "video": true,
		"新": true, "新片": true, "新作": true, "最新": true, "电影": true,
	}
	seen := map[string]bool{}
	terms := []string{}
	for _, term := range strings.Fields(strings.ToLower(strings.TrimSpace(query))) {
		term = strings.Trim(term, "-_.,，。:：/\\()[]{}")
		if term == "" || stopWords[term] || isYearTerm(term) || seen[term] {
			continue
		}
		seen[term] = true
		terms = append(terms, term)
	}
	return terms
}

func isYearTerm(value string) bool {
	if len(value) != 4 {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizeQueries(queries []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(queries))
	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" || seen[query] {
			continue
		}
		seen[query] = true
		result = append(result, query)
	}
	return result
}

func deduplicate(items []Item) []Item {
	seen := map[string]bool{}
	result := make([]Item, 0, len(items))
	for _, item := range items {
		key := strings.ToLower(strings.TrimSpace(item.ID))
		if hash, ok := ExtractInfoHash(item.Magnet); ok {
			key = hash
		}
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(item.Provider + "\x00" + item.Name + "\x00" + item.Link))
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, item)
	}
	return result
}

func ExtractInfoHash(magnet string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(magnet))
	if err != nil || !strings.EqualFold(parsed.Scheme, "magnet") {
		return "", false
	}
	for _, xt := range parsed.Query()["xt"] {
		const prefix = "urn:btih:"
		if len(xt) <= len(prefix) || !strings.EqualFold(xt[:len(prefix)], prefix) {
			continue
		}
		hash := xt[len(prefix):]
		if len(hash) == 40 {
			if _, err := hex.DecodeString(hash); err == nil {
				return strings.ToLower(hash), true
			}
		}
		if len(hash) == 32 {
			decoded, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.ToUpper(hash))
			if err == nil && len(decoded) == 20 {
				return hex.EncodeToString(decoded), true
			}
		}
	}
	return "", false
}

func request(ctx context.Context, client *http.Client, rawURL string) (*http.Response, error) {
	return requestWithHeaders(ctx, client, rawURL, nil)
}

func requestWithHeaders(ctx context.Context, client *http.Client, rawURL string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,application/json;q=0.8,*/*;q=0.7")
	for name, value := range headers {
		if strings.TrimSpace(value) != "" {
			req.Header.Set(name, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, errors.New(resp.Status)
	}
	return resp, nil
}
