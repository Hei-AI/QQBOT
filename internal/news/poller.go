package news

import (
	rootagent "QqBot/internal/agent"
	"QqBot/internal/config"
	"QqBot/internal/db"
	"context"
	"encoding/xml"
	"io"
	"net/http"
	"strings"
	"time"
)

// IthomePoller 拉取 IThome RSS 文章并发送 Agent 事件。
type IthomePoller struct {
	cfg    *config.Config
	store  *db.Store
	events *rootagent.EventQueue
	client *http.Client
}

// NewIthomePoller 创建一个尚未启动的 IThome 轮询器。
func NewIthomePoller(cfg *config.Config, store *db.Store, events *rootagent.EventQueue) *IthomePoller {
	return &IthomePoller{cfg: cfg, store: store, events: events, client: &http.Client{Timeout: 15 * time.Second}}
}

// PollInterval 返回 IThome 轮询间隔；非正数表示禁用轮询。
func (p *IthomePoller) PollInterval() time.Duration {
	interval := time.Duration(p.cfg.Server.News.Ithome.PollIntervalMs) * time.Millisecond
	if interval <= 0 {
		return 0
	}
	return interval
}

// Start 立即执行一次轮询，并持续按间隔轮询直到上下文取消。
//
// Deprecated: 新代码应通过 scheduler 调用 RunOnce。
func (p *IthomePoller) Start(ctx context.Context) {
	interval := p.PollInterval()
	if interval <= 0 {
		return
	}
	go func() {
		_, _ = p.RunOnce(ctx)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_, _ = p.RunOnce(ctx)
			}
		}
	}()
}

type rssFeed struct {
	Items []rssItem `xml:"channel>item"`
}

type rssItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
}

func (p *IthomePoller) RunOnce(ctx context.Context) (int, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.ithome.com/rss/", nil)
	res, err := p.client.Do(req)
	if err != nil {
		p.store.Log("warn", "IThome poll failed", map[string]any{"error": err.Error()})
		return 0, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	var feed rssFeed
	if err := xml.Unmarshal(raw, &feed); err != nil {
		return 0, err
	}
	limit := p.cfg.Server.News.Ithome.RecentArticleLimit
	if limit <= 0 || limit > len(feed.Items) {
		limit = len(feed.Items)
	}
	ingested := 0
	for _, item := range feed.Items[:limit] {
		id := item.GUID
		if id == "" {
			id = item.Link
		}
		if id == "" || p.hasArticle(id) {
			continue
		}
		pub, _ := time.Parse(time.RFC1123Z, item.PubDate)
		if pub.IsZero() {
			pub = time.Now()
		}
		article := db.NewsArticle{ID: p.store.NextID(), SourceKey: "ithome", UpstreamID: id, Title: strings.TrimSpace(item.Title), URL: strings.TrimSpace(item.Link), PublishedAt: pub, RSSSummary: strings.TrimSpace(item.Description), CreatedAt: time.Now(), UpdatedAt: time.Now()}
		p.store.AddNewsArticle(article)
		p.events.Enqueue(rootagent.AgentEvent{Type: "news_article_ingested", Data: map[string]any{"sourceKey": "ithome", "articleId": article.ID, "title": article.Title}})
		ingested++
	}
	return ingested, nil
}

func (p *IthomePoller) hasArticle(upstreamID string) bool {
	data := p.store.Snapshot()
	for _, article := range data.NewsArticles {
		if article.SourceKey == "ithome" && article.UpstreamID == upstreamID {
			return true
		}
	}
	return false
}
