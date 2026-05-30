package story

import "time"

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
