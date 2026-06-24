package magnetsearch

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

const defaultTokyoLibBaseURL = "https://tokyolib.com"

type baseProvider struct {
	client   *http.Client
	baseURL  string
	name     string
	priority int
}

func (p baseProvider) Name() string  { return p.name }
func (p baseProvider) Priority() int { return p.priority }

type TokyoLibProvider struct {
	baseProvider
}

func NewTokyoLibProvider(client *http.Client, baseURL string) *TokyoLibProvider {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = defaultTokyoLibBaseURL
	}
	return &TokyoLibProvider{
		baseProvider{
			client: client, baseURL: strings.TrimRight(baseURL, "/"),
			name: "tokyolib", priority: 0,
		},
	}
}

type tokyoLibWork struct {
	URL  string
	Name string
	Date *time.Time
}

var (
	tokyoLibCodePattern    = regexp.MustCompile(`(?i)\b[A-Z]{2,12}[-_ ]?\d{2,6}\b`)
	tokyoLibWorkPattern    = regexp.MustCompile(`(?is)<a\b[^>]*class=["'][^"']*\bwork\b[^"']*["'][^>]*href=["']([^"']*/v/\d+)["'][^>]*>(.*?)</a>`)
	tokyoLibIDPattern      = regexp.MustCompile(`(?is)<h4\b[^>]*class=["'][^"']*\bwork-id\b[^"']*["'][^>]*>(.*?)</h4>`)
	tokyoLibTitlePattern   = regexp.MustCompile(`(?is)<h4\b[^>]*class=["'][^"']*\bwork-title\b[^"']*["'][^>]*>(.*?)</h4>`)
	tokyoLibCastPattern    = regexp.MustCompile(`(?is)<span\b[^>]*class=["'][^"']*\bwork-actress\b[^"']*["'][^>]*>(.*?)</span>`)
	tokyoLibDatePattern    = regexp.MustCompile(`(?i)\b(20\d{2}-\d{2}-\d{2})\b`)
	tokyoLibYearPattern    = regexp.MustCompile(`\b(?:19|20)\d{2}\b`)
	tokyoLibActressPattern = regexp.MustCompile(`(?is)<a\b[^>]*class=["'][^"']*\bactress\b[^"']*["'][^>]*href=["']([^"']*/actress/\d+)["']`)
	tokyoLibMagnetPattern  = regexp.MustCompile(`(?is)<span\b[^>]*class=["'][^"']*\bmagnet-size\b[^"']*["'][^>]*>(.*?)</span>\s*<a\b[^>]*href=["'](magnet:\?[^"']+)["'][^>]*>(.*?)</a>`)
)

func (p *TokyoLibProvider) Search(ctx context.Context, query string, _ SearchOptions) ([]Item, error) {
	searchType, query := normalizeTokyoLibQuery(query)
	if query == "" {
		return nil, nil
	}
	params := url.Values{"type": {searchType}, "q": {query}}
	resp, err := request(ctx, p.client, p.baseURL+"/search?"+params.Encode())
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	works := parseTokyoLibWorks(string(body), p.baseURL)
	if len(works) == 0 && searchType == "actress" {
		actressLinks := parseTokyoLibActressLinks(string(body), p.baseURL)
		for _, actressURL := range actressLinks {
			pageWorks, fetchErr := p.fetchWorks(ctx, actressURL)
			if fetchErr != nil {
				continue
			}
			works = append(works, pageWorks...)
			if len(works) >= 10 {
				break
			}
		}
	}
	if len(works) == 0 {
		return nil, nil
	}
	if len(works) > 10 {
		works = works[:10]
	}
	results := make(chan []Item, len(works))
	var wg sync.WaitGroup
	for _, work := range works {
		wg.Add(1)
		go func(work tokyoLibWork) {
			defer wg.Done()
			items, _ := p.fetchMagnets(ctx, work)
			results <- items
		}(work)
	}
	wg.Wait()
	close(results)
	items := []Item{}
	for result := range results {
		items = append(items, result...)
	}
	return items, nil
}

func normalizeTokyoLibQuery(query string) (string, string) {
	query = strings.TrimSpace(query)
	if code := tokyoLibCodePattern.FindString(query); code != "" {
		code = strings.ReplaceAll(code, "_", "-")
		code = strings.ReplaceAll(code, " ", "-")
		return "id", strings.ToUpper(code)
	}

	query = tokyoLibYearPattern.ReplaceAllString(query, " ")
	stopWords := map[string]bool{
		"new": true, "latest": true, "movie": true, "video": true,
		"新": true, "新片": true, "新作": true, "最新": true,
		"作品": true, "资源": true, "磁力": true, "下载": true, "电影": true,
	}
	terms := make([]string, 0)
	for _, term := range strings.Fields(query) {
		term = strings.Trim(term, "-_.,，。:：/\\()[]{}")
		if term == "" || stopWords[strings.ToLower(term)] {
			continue
		}
		terms = append(terms, term)
	}
	return "actress", strings.Join(terms, " ")
}

func (p *TokyoLibProvider) fetchWorks(ctx context.Context, rawURL string) ([]tokyoLibWork, error) {
	resp, err := request(ctx, p.client, rawURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return parseTokyoLibWorks(string(body), p.baseURL), nil
}

func (p *TokyoLibProvider) fetchMagnets(ctx context.Context, work tokyoLibWork) ([]Item, error) {
	resp, err := request(ctx, p.client, work.URL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	items := parseTokyoLibMagnets(string(body), work.Name, work.URL, work.Date)
	if len(items) == 0 {
		return nil, fmt.Errorf("TokyoLib magnet links not found")
	}
	return items, nil
}

func parseTokyoLibWorks(body, origin string) []tokyoLibWork {
	seen := map[string]bool{}
	works := []tokyoLibWork{}
	for _, match := range tokyoLibWorkPattern.FindAllStringSubmatch(body, -1) {
		detailURL := resolveURL(origin, htmlDecode(match[1]))
		if detailURL == "" || seen[detailURL] {
			continue
		}
		seen[detailURL] = true
		code, title := "", ""
		if value := tokyoLibIDPattern.FindStringSubmatch(match[2]); len(value) == 2 {
			code = textContent(value[1])
		}
		if value := tokyoLibTitlePattern.FindStringSubmatch(match[2]); len(value) == 2 {
			title = textContent(value[1])
		}
		nameParts := []string{code, title}
		for _, cast := range tokyoLibCastPattern.FindAllStringSubmatch(match[2], -1) {
			nameParts = append(nameParts, textContent(cast[1]))
		}
		name := strings.Join(strings.Fields(strings.Join(nameParts, " ")), " ")
		var date *time.Time
		if value := tokyoLibDatePattern.FindStringSubmatch(match[2]); len(value) == 2 {
			date = parseFlexibleDate(value[1])
		}
		works = append(works, tokyoLibWork{URL: detailURL, Name: name, Date: date})
	}
	return works
}

func parseTokyoLibActressLinks(body, origin string) []string {
	seen := map[string]bool{}
	links := []string{}
	for _, match := range tokyoLibActressPattern.FindAllStringSubmatch(body, -1) {
		link := resolveURL(origin, htmlDecode(match[1]))
		if link == "" || seen[link] {
			continue
		}
		seen[link] = true
		links = append(links, link)
	}
	return links
}

func parseTokyoLibMagnets(body, workName, detailURL string, date *time.Time) []Item {
	items := []Item{}
	for index, match := range tokyoLibMagnetPattern.FindAllStringSubmatch(body, -1) {
		magnet := htmlDecode(match[2])
		hash, ok := ExtractInfoHash(magnet)
		if !ok {
			continue
		}
		resourceName := textContent(match[3])
		name := strings.TrimSpace(strings.Join([]string{workName, resourceName}, " "))
		if name == "" {
			name = fmt.Sprintf("tokyolib-%d", index+1)
		}
		items = append(items, Item{
			ID: hash, Name: name, Magnet: magnet, Link: detailURL,
			Size: parseSize(textContent(match[1])), Date: date,
		})
	}
	for i := range items {
		items[i].Name = strings.Join(strings.Fields(items[i].Name), " ")
	}
	return items
}

func htmlDecode(value string) string {
	for range 3 {
		decoded := html.UnescapeString(value)
		if decoded == value {
			break
		}
		value = decoded
	}
	return value
}

func resolveURL(origin, path string) string {
	base, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	ref, err := url.Parse(path)
	if err != nil {
		return ""
	}
	return base.ResolveReference(ref).String()
}

func parseFlexibleDate(value string) *time.Time {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\u00a0", " "))
	for _, layout := range []string{
		"2006/01/02 15:04", "2006-01-02 15:04", "2006/01/02", "2006-01-02",
		time.RFC3339, time.RFC1123Z, time.RFC1123,
	} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return &parsed
		}
	}
	return nil
}
