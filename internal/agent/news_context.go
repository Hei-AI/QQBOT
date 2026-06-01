package agent

import (
	"qqbot-ai/internal/db"
	"qqbot-ai/internal/prompts"
)

func (a *AgentRuntime) renderNewsArticles(ids any) string {
	articleIDs := intSlice(ids)
	if len(articleIDs) == 0 {
		return ""
	}
	summaries := []prompts.ArticleSummary{}
	for _, id := range articleIDs {
		if article, ok := a.store.FindNewsArticleByID(id); ok {
			summaries = append(summaries, prompts.ArticleSummary{
				ID:              article.ID,
				Title:           article.Title,
				PublishedAtText: formatTime(article.PublishedAt),
				URL:             article.URL,
				RSSSummary:      article.RSSSummary,
			})
		}
	}
	if len(summaries) == 0 {
		return ""
	}
	if len(summaries) > 5 {
		summaries = summaries[:5]
	}
	return prompts.ITHomeArticleListInstruction("IT 之家", true, 0, summaries)
}

func (a *AgentRuntime) findNewsArticle(id any) (db.NewsArticle, bool) {
	articleID := intValue(id)
	if articleID == 0 {
		return db.NewsArticle{}, false
	}
	return a.store.FindNewsArticleByID(articleID)
}
