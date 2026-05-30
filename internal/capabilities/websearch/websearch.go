package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Result 是一条标准化网页搜索结果。
type Result struct {
	Title   string  `json:"title"`
	URL     string  `json:"url"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// Service 由 Tavily 等具体搜索供应商实现。
type Service interface {
	Search(context.Context, string, int) ([]Result, error)
}

// TavilyService 调用 Tavily 的 HTTP 搜索 API。
type TavilyService struct {
	APIKey string
	Client *http.Client
}

// Search 向 Tavily 提交查询并返回标准化结果。
func (s TavilyService) Search(ctx context.Context, query string, maxResults int) ([]Result, error) {
	if s.APIKey == "" {
		return nil, fmt.Errorf("tavily api key is empty")
	}
	if maxResults <= 0 {
		maxResults = 5
	}
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	body, _ := json.Marshal(map[string]any{"api_key": s.APIKey, "query": query, "max_results": maxResults})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("tavily search failed: %s", res.Status)
	}
	var payload struct {
		Results []Result `json:"results"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}
	return payload.Results, nil
}

// MemoryService 是用于测试和离线运行的确定性搜索供应商。
type MemoryService struct {
	Results []Result
}

// Search 返回配置好的内存搜索结果。
func (s MemoryService) Search(_ context.Context, query string, maxResults int) ([]Result, error) {
	if maxResults <= 0 || maxResults > len(s.Results) {
		maxResults = len(s.Results)
	}
	return append([]Result(nil), s.Results[:maxResults]...), nil
}
