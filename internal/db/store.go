package db

import (
	"QqBot/internal/agentruntime"
	"QqBot/internal/common"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

// Store 是使用的本地 SQLite 持久化层。
type Store struct {
	mu   sync.Mutex
	path string
	db   *sql.DB
	Data StoreData
}

// StoreData 是管理台和现有上层代码使用的兼容快照结构。
type StoreData struct {
	AppLogs         []AppLogItem               `json:"appLogs"`
	LlmCalls        []LlmCallItem              `json:"llmCalls"`
	NapcatEvents    []NapcatEventItem          `json:"napcatEvents"`
	NapcatMessages  []NapcatMessageItem        `json:"napcatMessages"`
	StoryLedger     []StoryLedgerItem          `json:"storyLedger"`
	Ledger          []LinearLedgerItem         `json:"linearMessageLedger"`
	Stories         []StoryItem                `json:"stories"`
	StoryDocuments  []StoryMemoryDocument      `json:"storyMemoryDocuments"`
	EmbeddingCache  []EmbeddingCacheItem       `json:"embeddingCache"`
	Metrics         []MetricItem               `json:"metrics"`
	MetricCharts    []MetricChart              `json:"metricCharts"`
	NewsArticles    []NewsArticle              `json:"newsArticles"`
	NewsFeedCursors []NewsFeedCursor           `json:"newsFeedCursors"`
	NewsFeedCursor  []NewsFeedCursor           `json:"newsFeedCursor"`
	AgentSnapshots  map[string]json.RawMessage `json:"agentSnapshots"`
	OAuthSessions   []OAuthSession             `json:"oauthSessions"`
	AuthUsage       []AuthUsageSnapshot        `json:"authUsageSnapshots"`
	AgentSnapshot   AgentRuntimeSnapshot       `json:"agentRuntimeSnapshot"`
	TerminalState   TerminalState              `json:"terminalState"`
	TerminalOutput  []TerminalOutputItem       `json:"terminalOutput"`
}

const (
	maxStoredAppLogs        = 2000
	maxStoredLlmCalls       = 1000
	maxStoredNapcatEvents   = 1000
	maxStoredNapcatMessages = 10000
	maxStoredMetrics        = 10000
	maxStoredLlmPayload     = 12000
	maxStoredLlmPreview     = 4000
)

// AppLogItem 对应 TS 的 app_log 读取模型。
type AppLogItem struct {
	ID        int            `json:"id"`
	TraceID   string         `json:"traceId"`
	Level     string         `json:"level"`
	Message   string         `json:"message"`
	Metadata  map[string]any `json:"metadata"`
	CreatedAt time.Time      `json:"createdAt"`
}

// LlmCallItem 对应 TS 的 llm_chat_call 读取模型。
type LlmCallItem struct {
	ID                    int            `json:"id"`
	RequestID             string         `json:"requestId"`
	Seq                   int            `json:"seq"`
	Provider              string         `json:"provider"`
	Model                 string         `json:"model"`
	Extension             map[string]any `json:"extension"`
	Status                string         `json:"status"`
	RequestPayload        map[string]any `json:"requestPayload"`
	ResponsePayload       map[string]any `json:"responsePayload"`
	NativeRequestPayload  map[string]any `json:"nativeRequestPayload"`
	NativeResponsePayload map[string]any `json:"nativeResponsePayload"`
	Error                 map[string]any `json:"error"`
	NativeError           map[string]any `json:"nativeError"`
	LatencyMs             *int           `json:"latencyMs"`
	CreatedAt             time.Time      `json:"createdAt"`
}

// NapcatEventItem 对应持久化的 NapCat 原始 post-type 事件。
type NapcatEventItem struct {
	ID          int            `json:"id"`
	PostType    string         `json:"postType"`
	MessageType *string        `json:"messageType"`
	SubType     *string        `json:"subType"`
	UserID      *string        `json:"userId"`
	GroupID     *string        `json:"groupId"`
	EventTime   *time.Time     `json:"eventTime"`
	Payload     map[string]any `json:"payload"`
	CreatedAt   time.Time      `json:"createdAt"`
}

// NapcatMessageItem 对应标准化后的 QQ 群聊/私聊消息。
type NapcatMessageItem struct {
	ID              int              `json:"id"`
	MessageType     string           `json:"messageType"`
	SubType         string           `json:"subType"`
	GroupID         *string          `json:"groupId"`
	UserID          *string          `json:"userId"`
	Nickname        *string          `json:"nickname"`
	MessageID       *int             `json:"messageId"`
	Message         any              `json:"message"`
	RawMessage      string           `json:"rawMessage"`
	MessageSegments []MessageSegment `json:"messageSegments"`
	EventTime       *time.Time       `json:"eventTime"`
	Payload         map[string]any   `json:"payload"`
	CreatedAt       time.Time        `json:"createdAt"`
}

type MessageSegment struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data"`
	Text string         `json:"text,omitempty"`
}

// StoryLedgerItem 保存 Root 运行时写给 Story Agent 的线性消息账本。
type StoryLedgerItem struct {
	Seq        int       `json:"seq"`
	RuntimeKey string    `json:"runtimeKey"`
	Role       string    `json:"role"`
	Content    string    `json:"content"`
	CreatedAt  time.Time `json:"createdAt"`
}

type LinearLedgerItem struct {
	ID         int                  `json:"id"`
	RuntimeKey string               `json:"runtimeKey"`
	Message    agentruntime.Message `json:"message"`
	CreatedAt  time.Time            `json:"createdAt"`
}

// StoryItem 是 Story 记忆在根包中的 JSON 表示。
type StoryItem struct {
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

// StoryMemoryDocument 是 Story 面向 RAG/召回的向量化投影。
type StoryMemoryDocument struct {
	ID             int       `json:"id"`
	StoryID        string    `json:"storyId"`
	Kind           string    `json:"kind"`
	Content        string    `json:"content"`
	EmbeddingModel string    `json:"embeddingModel"`
	EmbeddingDim   int       `json:"embeddingDim"`
	Embedding      []float64 `json:"embedding"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// EmbeddingCacheItem 缓存 embedding 请求结果，避免重复调用供应商。
type EmbeddingCacheItem struct {
	ID                   int       `json:"id"`
	Provider             string    `json:"provider"`
	Model                string    `json:"model"`
	TaskType             string    `json:"taskType"`
	OutputDimensionality int       `json:"outputDimensionality"`
	TextHash             string    `json:"textHash"`
	Text                 string    `json:"text"`
	Embedding            []float64 `json:"embedding"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// MetricItem 保存一条带字符串标签的数值观测。
type MetricItem struct {
	ID         int               `json:"id"`
	MetricName string            `json:"metricName"`
	Value      float64           `json:"value"`
	Tags       map[string]string `json:"tags"`
	OccurredAt time.Time         `json:"occurredAt"`
	CreatedAt  time.Time         `json:"createdAt"`
}

// MetricChart 定义指标观测应如何聚合成图表。
type MetricChart struct {
	ChartName  string            `json:"chartName"`
	MetricName string            `json:"metricName"`
	Aggregator string            `json:"aggregator"`
	TagFilters map[string]string `json:"tagFilters"`
	GroupByTag string            `json:"groupByTag"`
	CreatedAt  time.Time         `json:"createdAt"`
	UpdatedAt  time.Time         `json:"updatedAt"`
}

// NewsArticle 保存一篇已入库的 IThome 文章。
type NewsArticle struct {
	ID          int       `json:"id"`
	SourceKey   string    `json:"sourceKey"`
	UpstreamID  string    `json:"upstreamId"`
	Title       string    `json:"title"`
	URL         string    `json:"url"`
	PublishedAt time.Time `json:"publishedAt"`
	RSSSummary  string    `json:"rssSummary"`
	Content     string    `json:"articleContent"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// NewsFeedCursor 记录某个新闻源进入后推进到的最新游标。
type NewsFeedCursor struct {
	SourceKey           string    `json:"sourceKey"`
	LastSeenArticleID   int       `json:"lastSeenArticleId"`
	LastSeenPublishedAt time.Time `json:"lastSeenPublishedAt"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

type OAuthSession struct {
	Provider      string     `json:"provider"`
	AccountID     *string    `json:"accountId"`
	Email         *string    `json:"email"`
	AccessToken   string     `json:"accessToken,omitempty"`
	RefreshToken  string     `json:"refreshToken,omitempty"`
	IDToken       string     `json:"idToken,omitempty"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	LastRefreshAt *time.Time `json:"lastRefreshAt"`
	Status        string     `json:"status"`
	LastError     *string    `json:"lastError"`
}

type AuthUsageSnapshot struct {
	ID               int        `json:"id"`
	Provider         string     `json:"provider"`
	AccountID        string     `json:"accountId"`
	WindowKey        string     `json:"windowKey"`
	RemainingPercent float64    `json:"remainingPercent"`
	ResetAt          *time.Time `json:"resetAt"`
	CapturedAt       time.Time  `json:"capturedAt"`
}

type AgentRuntimeSnapshot struct {
	RootMessages  []agentruntime.Message `json:"rootMessages"`
	StoryMessages []agentruntime.Message `json:"storyMessages"`
	Session       map[string]any         `json:"session"`
	StoryLastSeq  int                    `json:"storyLastSeq"`
	Fingerprint   string                 `json:"fingerprint"`
	UpdatedAt     time.Time              `json:"updatedAt"`
}

type TerminalState struct {
	CWD       string    `json:"cwd"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type TerminalOutputItem struct {
	ID        int       `json:"id"`
	OutputID  string    `json:"outputId"`
	Stdout    string    `json:"stdout"`
	Stderr    string    `json:"stderr"`
	ExitCode  int       `json:"exitCode"`
	CreatedAt time.Time `json:"createdAt"`
}

type AgentStackItem struct {
	ID         int            `json:"id"`
	RuntimeKey string         `json:"runtimeKey"`
	Kind       string         `json:"kind"`
	Role       string         `json:"role,omitempty"`
	ToolCallID string         `json:"toolCallId,omitempty"`
	ToolName   string         `json:"toolName,omitempty"`
	Content    any            `json:"content"`
	Metadata   map[string]any `json:"metadata,omitempty"`
	CreatedAt  time.Time      `json:"createdAt"`
}

type ToolExecutionItem struct {
	ID             int            `json:"id"`
	ExecutionKey   string         `json:"executionKey"`
	RuntimeKey     string         `json:"runtimeKey"`
	ToolCallID     string         `json:"toolCallId"`
	ToolName       string         `json:"toolName"`
	Arguments      map[string]any `json:"arguments"`
	Result         string         `json:"result,omitempty"`
	Status         string         `json:"status"`
	SideEffect     bool           `json:"sideEffect"`
	Attempt        int            `json:"attempt"`
	LeaseOwner     string         `json:"leaseOwner,omitempty"`
	LeaseExpiresAt *time.Time     `json:"leaseExpiresAt,omitempty"`
	ErrorMessage   string         `json:"errorMessage,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	CompletedAt    *time.Time     `json:"completedAt,omitempty"`
}

type AgentTaskItem struct {
	ID             int            `json:"id"`
	TaskKey        string         `json:"taskKey"`
	TaskType       string         `json:"taskType"`
	Payload        map[string]any `json:"payload"`
	Status         string         `json:"status"`
	SideEffect     bool           `json:"sideEffect"`
	Attempt        int            `json:"attempt"`
	MaxAttempts    int            `json:"maxAttempts"`
	AvailableAt    time.Time      `json:"availableAt"`
	LeaseOwner     string         `json:"leaseOwner,omitempty"`
	LeaseExpiresAt *time.Time     `json:"leaseExpiresAt,omitempty"`
	Result         map[string]any `json:"result,omitempty"`
	ErrorMessage   string         `json:"errorMessage,omitempty"`
	CreatedAt      time.Time      `json:"createdAt"`
	UpdatedAt      time.Time      `json:"updatedAt"`
	CompletedAt    *time.Time     `json:"completedAt,omitempty"`
}

// OpenStore 加载或创建 SQLite 持久化文件。
func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	store := &Store{path: path, db: db}
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := store.maintainStorage(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) initSchema() error {
	stmts := []string{
		`PRAGMA journal_mode = WAL`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS app_logs (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS llm_calls (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS napcat_events (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS napcat_messages (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_napcat_messages_type_group ON napcat_messages(json_extract(item, '$.messageType'), json_extract(item, '$.groupId'), id)`,
		`CREATE INDEX IF NOT EXISTS idx_napcat_messages_type_user ON napcat_messages(json_extract(item, '$.messageType'), json_extract(item, '$.userId'), id)`,
		`CREATE TABLE IF NOT EXISTS story_ledger (seq INTEGER PRIMARY KEY AUTOINCREMENT, runtime_key TEXT NOT NULL, created_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_story_ledger_runtime_seq ON story_ledger(runtime_key, seq)`,
		`CREATE TABLE IF NOT EXISTS stories (id TEXT PRIMARY KEY, updated_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS story_documents (id INTEGER PRIMARY KEY AUTOINCREMENT, story_id TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_story_documents_story_id ON story_documents(story_id)`,
		`CREATE TABLE IF NOT EXISTS embedding_cache (id INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL, model TEXT NOT NULL, task_type TEXT NOT NULL, output_dimensionality INTEGER NOT NULL, text_hash TEXT NOT NULL, item TEXT NOT NULL, UNIQUE(provider, model, task_type, output_dimensionality, text_hash))`,
		`CREATE TABLE IF NOT EXISTS metrics (id INTEGER PRIMARY KEY AUTOINCREMENT, created_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS metric_charts (chart_name TEXT PRIMARY KEY, item TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS news_articles (id INTEGER PRIMARY KEY AUTOINCREMENT, source_key TEXT NOT NULL, upstream_id TEXT NOT NULL, item TEXT NOT NULL, UNIQUE(source_key, upstream_id))`,
		`CREATE INDEX IF NOT EXISTS idx_news_articles_source_id ON news_articles(source_key, id)`,
		`CREATE TABLE IF NOT EXISTS news_feed_cursors (source_key TEXT PRIMARY KEY, item TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS agent_snapshots (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS oauth_sessions (provider TEXT PRIMARY KEY, item TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS auth_usage_snapshots (id INTEGER PRIMARY KEY AUTOINCREMENT, provider TEXT NOT NULL, account_id TEXT NOT NULL, captured_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_auth_usage_provider_account ON auth_usage_snapshots(provider, account_id, captured_at)`,
		`CREATE TABLE IF NOT EXISTS terminal_state (key TEXT PRIMARY KEY, item TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS terminal_outputs (id INTEGER PRIMARY KEY AUTOINCREMENT, output_id TEXT NOT NULL, created_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_terminal_outputs_output_id ON terminal_outputs(output_id, id)`,
		`CREATE TABLE IF NOT EXISTS agent_stack_items (id INTEGER PRIMARY KEY AUTOINCREMENT, runtime_key TEXT NOT NULL, kind TEXT NOT NULL, tool_call_id TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_stack_runtime_id ON agent_stack_items(runtime_key, id)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_stack_tool_call ON agent_stack_items(tool_call_id, id)`,
		`CREATE TABLE IF NOT EXISTS tool_executions (id INTEGER PRIMARY KEY AUTOINCREMENT, execution_key TEXT NOT NULL UNIQUE, status TEXT NOT NULL, side_effect INTEGER NOT NULL DEFAULT 0, lease_expires_at TEXT, updated_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_executions_status_lease ON tool_executions(status, lease_expires_at)`,
		`CREATE TABLE IF NOT EXISTS agent_tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, task_key TEXT NOT NULL UNIQUE, task_type TEXT NOT NULL, status TEXT NOT NULL, available_at TEXT NOT NULL, lease_expires_at TEXT, updated_at TEXT NOT NULL, item TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_agent_tasks_claim ON agent_tasks(status, available_at, id)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) maintainStorage() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.repairStoryTimesLocked(); err != nil {
		return err
	}
	compacted, err := s.compactOversizedLlmCallsLocked()
	if err != nil {
		return err
	}
	s.pruneTable("llm_calls", maxStoredLlmCalls)
	s.pruneTable("app_logs", maxStoredAppLogs)
	s.pruneTable("napcat_events", maxStoredNapcatEvents)
	s.pruneTable("napcat_messages", maxStoredNapcatMessages)
	s.pruneTable("metrics", maxStoredMetrics)
	if compacted > 0 {
		if _, err := s.db.Exec(`VACUUM`); err != nil {
			return err
		}
	}
	_, _ = s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return nil
}

func (s *Store) repairStoryTimesLocked() error {
	rows, err := s.db.Query(`SELECT id, item FROM stories`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type update struct {
		id   string
		item StoryItem
	}
	updates := []update{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return err
		}
		var item StoryItem
		if json.Unmarshal([]byte(raw), &item) != nil || (!item.CreatedAt.IsZero() && !item.UpdatedAt.IsZero()) {
			continue
		}
		repairedAt, parseErr := time.ParseInLocation("20060102150405.000000000", id, time.Local)
		if parseErr != nil {
			repairedAt = time.Now()
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = repairedAt
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		updates = append(updates, update{id: id, item: item})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, update := range updates {
		if _, err := s.db.Exec(
			`UPDATE stories SET updated_at = ?, item = ? WHERE id = ?`,
			formatTime(update.item.UpdatedAt),
			mustJSON(update.item),
			update.id,
		); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) compactOversizedLlmCallsLocked() (int, error) {
	rows, err := s.db.Query(`SELECT id, item FROM llm_calls WHERE length(item) > ?`, maxStoredLlmPayload)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type update struct {
		id  int
		raw string
	}
	updates := []update{}
	for rows.Next() {
		var id int
		var raw string
		if rows.Scan(&id, &raw) != nil {
			continue
		}
		var item LlmCallItem
		if json.Unmarshal([]byte(raw), &item) != nil {
			continue
		}
		item = compactLlmCall(item)
		updates = append(updates, update{id: id, raw: mustJSON(item)})
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, item := range updates {
		if _, err := s.db.Exec(`UPDATE llm_calls SET item = ? WHERE id = ?`, item.raw, item.id); err != nil {
			return len(updates), err
		}
	}
	return len(updates), nil
}

// SaveAgentSnapshot 保存 root/story 运行时快照，等价 TS 的 runtime snapshot 表。
func (s *Store) SaveAgentSnapshot(key string, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	s.mu.Lock()
	_, _ = s.db.Exec(`INSERT OR REPLACE INTO agent_snapshots(key, value, updated_at) VALUES(?, ?, ?)`, key, string(data), formatTime(time.Now()))
	s.mu.Unlock()
}

// LoadAgentSnapshot 读取指定运行时快照。
func (s *Store) LoadAgentSnapshot(key string, out any) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var data string
	err := s.db.QueryRow(`SELECT value FROM agent_snapshots WHERE key = ?`, key).Scan(&data)
	return err == nil && json.Unmarshal([]byte(data), out) == nil
}

func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) Flush() error {
	return nil
}

func (s *Store) DeleteOlder(kind string, threshold time.Time) int {
	total := 0
	for {
		deleted := s.DeleteOlderLimit(kind, threshold, 0)
		total += deleted
		if deleted == 0 {
			return total
		}
	}
}

func (s *Store) DeleteOlderLimit(kind string, threshold time.Time, limit int) int {
	if deleted, ok := s.deleteOlderLimitFromData(kind, threshold, limit); ok {
		return deleted
	}
	table := ""
	column := "created_at"
	switch kind {
	case "app_log":
		table = "app_logs"
	case "llm_chat_call":
		table = "llm_calls"
	case "metric":
		table = "metrics"
	case "napcat_event":
		table = "napcat_events"
	case "napcat_qq_message":
		table = "napcat_messages"
	case "embedding_cache":
		table = "embedding_cache"
		column = "json_extract(item, '$.createdAt')"
	case "auth_usage_snapshot":
		table = "auth_usage_snapshots"
		column = "captured_at"
	case "terminal_output":
		table = "terminal_outputs"
	case "oauth_state":
		return 0
	default:
		return 0
	}
	query := `DELETE FROM ` + table + ` WHERE id IN (SELECT id FROM ` + table + ` WHERE ` + column + ` < ? ORDER BY id ASC`
	args := []any{formatTime(threshold)}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	query += `)`
	s.mu.Lock()
	result, err := s.db.Exec(query, args...)
	s.mu.Unlock()
	if err != nil {
		return 0
	}
	rows, _ := result.RowsAffected()
	return int(rows)
}

func (s *Store) deleteOlderLimitFromData(kind string, threshold time.Time, limit int) (int, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	deleted := 0
	shouldDelete := func(createdAt time.Time) bool {
		if !createdAt.Before(threshold) {
			return false
		}
		return limit <= 0 || deleted < limit
	}
	switch kind {
	case "app_log":
		if len(s.Data.AppLogs) == 0 {
			return 0, false
		}
		out := s.Data.AppLogs[:0]
		for _, item := range s.Data.AppLogs {
			if shouldDelete(item.CreatedAt) {
				deleted++
				continue
			}
			out = append(out, item)
		}
		s.Data.AppLogs = out
	default:
		return 0, false
	}
	return deleted, true
}

func (s *Store) Log(level, message string, metadata map[string]any) {
	if metadata == nil {
		metadata = map[string]any{}
	}
	item := AppLogItem{TraceID: common.NewID(), Level: level, Message: message, Metadata: metadata, CreatedAt: time.Now()}
	s.mu.Lock()
	id, _ := s.insertJSONAutoID("app_logs", item.CreatedAt, &item)
	s.pruneTable("app_logs", maxStoredAppLogs)
	s.mu.Unlock()
	item.ID = id
	common.LogLine(level, message, metadata)
}

func (s *Store) AddLlmCall(item LlmCallItem) {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	item = compactLlmCall(item)
	s.mu.Lock()
	_, _ = s.insertJSONAutoID("llm_calls", item.CreatedAt, &item)
	s.pruneTable("llm_calls", maxStoredLlmCalls)
	s.mu.Unlock()
}

func (s *Store) AddNapcatEvent(item NapcatEventItem) {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	s.mu.Lock()
	_, _ = s.insertJSONAutoID("napcat_events", item.CreatedAt, &item)
	s.pruneTable("napcat_events", maxStoredNapcatEvents)
	s.mu.Unlock()
}

func (s *Store) AddNapcatMessage(item NapcatMessageItem) int {
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	s.mu.Lock()
	id, _ := s.insertJSONAutoID("napcat_messages", item.CreatedAt, &item)
	s.pruneTable("napcat_messages", maxStoredNapcatMessages)
	s.mu.Unlock()
	return id
}

func (s *Store) insertJSONAutoID(table string, createdAt time.Time, item any) (int, error) {
	raw := mustJSON(item)
	result, err := s.db.Exec(`INSERT INTO `+table+`(created_at, item) VALUES(?, ?)`, formatTime(createdAt), raw)
	if err != nil {
		return 0, err
	}
	id64, err := result.LastInsertId()
	if err != nil {
		return 0, err
	}
	setItemID(item, int(id64))
	_, err = s.db.Exec(`UPDATE `+table+` SET item = ? WHERE id = ?`, mustJSON(item), id64)
	return int(id64), err
}

func (s *Store) pruneTable(table string, max int) {
	if max <= 0 {
		return
	}
	_, _ = s.db.Exec(`DELETE FROM `+table+` WHERE id NOT IN (SELECT id FROM `+table+` ORDER BY id DESC LIMIT ?)`, max)
}

// AddStoryLedger 追加一条 Story Agent 可按 seq 消费的线性消息。
func (s *Store) AddStoryLedger(runtimeKey, role, content string) int {
	item := StoryLedgerItem{RuntimeKey: runtimeKey, Role: role, Content: content, CreatedAt: time.Now()}
	s.mu.Lock()
	result, err := s.db.Exec(`INSERT INTO story_ledger(runtime_key, created_at, item) VALUES(?, ?, ?)`, runtimeKey, formatTime(item.CreatedAt), mustJSON(item))
	if err == nil {
		if seq, idErr := result.LastInsertId(); idErr == nil {
			item.Seq = int(seq)
			_, _ = s.db.Exec(`UPDATE story_ledger SET item = ? WHERE seq = ?`, mustJSON(item), item.Seq)
		}
	}
	s.mu.Unlock()
	return item.Seq
}

func (s *Store) CountStoryLedgerAfter(runtimeKey string, afterSeq int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM story_ledger
		WHERE runtime_key = ? AND seq > ? AND json_extract(item, '$.seq') IS NOT NULL`, runtimeKey, afterSeq).Scan(&count)
	return count
}

func (s *Store) LatestStoryLedger(runtimeKey string) (StoryLedgerItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT item FROM story_ledger
		WHERE runtime_key = ? AND json_extract(item, '$.seq') IS NOT NULL
		ORDER BY seq DESC LIMIT 1`, runtimeKey).Scan(&raw)
	var item StoryLedgerItem
	return item, err == nil && json.Unmarshal([]byte(raw), &item) == nil
}

func (s *Store) ListStoryLedgerAfter(runtimeKey string, afterSeq, limit int) []StoryLedgerItem {
	query := `SELECT item FROM story_ledger
		WHERE runtime_key = ? AND seq > ? AND json_extract(item, '$.seq') IS NOT NULL
		ORDER BY seq ASC`
	args := []any{runtimeKey, afterSeq}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryJSONRows[StoryLedgerItem](s.db, query, args...)
}

// PruneStoryLedgerThrough keeps only a small processed tail while preserving
// every unprocessed item after throughSeq.
func (s *Store) PruneStoryLedgerThrough(runtimeKey string, throughSeq, keepProcessed int) {
	if throughSeq <= 0 || keepProcessed < 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(`DELETE FROM story_ledger
		WHERE runtime_key = ? AND seq <= ? AND seq NOT IN (
			SELECT seq FROM story_ledger WHERE runtime_key = ? AND seq <= ? ORDER BY seq DESC LIMIT ?
		)`, runtimeKey, throughSeq, runtimeKey, throughSeq, keepProcessed)
}

func (s *Store) ListStories() []StoryItem {
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryJSONRows[StoryItem](s.db, `SELECT item FROM stories ORDER BY updated_at ASC`)
}

func (s *Store) RecentNapcatMessages(messageType, id string, limit int) []NapcatMessageItem {
	if limit <= 0 {
		return nil
	}
	query := `SELECT item FROM napcat_messages WHERE json_extract(item, '$.messageType') = ?`
	args := []any{messageType}
	if messageType == "group" {
		query += ` AND json_extract(item, '$.groupId') = ?`
		args = append(args, id)
	} else {
		query += ` AND json_extract(item, '$.userId') = ?`
		args = append(args, id)
	}
	query += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	s.mu.Lock()
	defer s.mu.Unlock()
	items := queryJSONRows[NapcatMessageItem](s.db, query, args...)
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items
}

func (s *Store) ListNewsArticlesBySource(sourceKey string) []NewsArticle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryJSONRows[NewsArticle](s.db, `SELECT item FROM news_articles WHERE source_key = ? ORDER BY id DESC`, sourceKey)
}

func (s *Store) FindNewsArticleByID(id int) (NewsArticle, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT item FROM news_articles WHERE id = ?`, id).Scan(&raw)
	var article NewsArticle
	return article, err == nil && json.Unmarshal([]byte(raw), &article) == nil
}

func (s *Store) AddMetric(name string, value float64, tags map[string]string) {
	now := time.Now()
	item := MetricItem{MetricName: name, Value: value, Tags: tags, OccurredAt: now, CreatedAt: now}
	s.mu.Lock()
	_, _ = s.insertJSONAutoID("metrics", item.CreatedAt, &item)
	s.pruneTable("metrics", maxStoredMetrics)
	s.mu.Unlock()
}

func (s *Store) AddAuthUsageSnapshots(items []AuthUsageSnapshot) {
	if len(items) == 0 {
		return
	}
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, item := range items {
		if item.CapturedAt.IsZero() {
			item.CapturedAt = now
		}
		result, err := s.db.Exec(`INSERT INTO auth_usage_snapshots(provider, account_id, captured_at, item) VALUES(?, ?, ?, ?)`, item.Provider, item.AccountID, formatTime(item.CapturedAt), mustJSON(item))
		if err == nil {
			if id, idErr := result.LastInsertId(); idErr == nil {
				item.ID = int(id)
				_, _ = s.db.Exec(`UPDATE auth_usage_snapshots SET item = ? WHERE id = ?`, mustJSON(item), id)
			}
		}
	}
}

func (s *Store) AppendLedger(runtimeKey string, message agentruntime.Message) int {
	item := LinearLedgerItem{RuntimeKey: runtimeKey, Message: message, CreatedAt: time.Now()}
	s.mu.Lock()
	result, err := s.db.Exec(`INSERT INTO story_ledger(runtime_key, created_at, item) VALUES(?, ?, ?)`, runtimeKey, formatTime(item.CreatedAt), mustJSON(item))
	if err == nil {
		if id, idErr := result.LastInsertId(); idErr == nil {
			item.ID = int(id)
			_, _ = s.db.Exec(`UPDATE story_ledger SET item = ? WHERE seq = ?`, mustJSON(item), id)
		}
	}
	s.mu.Unlock()
	return item.ID
}

func (s *Store) LedgerAfter(runtimeKey string, seq int, limit int) []LinearLedgerItem {
	query := `SELECT item FROM story_ledger WHERE runtime_key = ? AND seq > ? ORDER BY seq ASC`
	args := []any{runtimeKey, seq}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryJSONRows[LinearLedgerItem](s.db, query, args...)
}

func (s *Store) SaveAgentRuntimeSnapshot(snapshot AgentRuntimeSnapshot) {
	if snapshot.Fingerprint != "" {
		if current, ok := s.AgentRuntimeSnapshot(); ok && current.Fingerprint == snapshot.Fingerprint {
			return
		}
	}
	snapshot.UpdatedAt = time.Now()
	s.SaveAgentSnapshot("agentRuntimeSnapshot", snapshot)
}

func (s *Store) AgentRuntimeSnapshot() (AgentRuntimeSnapshot, bool) {
	var snapshot AgentRuntimeSnapshot
	ok := s.LoadAgentSnapshot("agentRuntimeSnapshot", &snapshot)
	if !ok || snapshot.UpdatedAt.IsZero() {
		return AgentRuntimeSnapshot{}, false
	}
	return snapshot, true
}

func (s *Store) ResetAgentRuntimeState() {
	s.mu.Lock()
	_, _ = s.db.Exec(`DELETE FROM agent_snapshots WHERE key = ?`, "agentRuntimeSnapshot")
	_, _ = s.db.Exec(`DELETE FROM story_ledger`)
	_, _ = s.db.Exec(`DELETE FROM agent_stack_items`)
	_, _ = s.db.Exec(`DELETE FROM tool_executions`)
	_, _ = s.db.Exec(`DELETE FROM agent_tasks`)
	s.mu.Unlock()
}

func (s *Store) LoadTerminalCWD() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	if err := s.db.QueryRow(`SELECT item FROM terminal_state WHERE key = ?`, "default").Scan(&raw); err != nil {
		return ""
	}
	var state TerminalState
	_ = json.Unmarshal([]byte(raw), &state)
	return state.CWD
}

func (s *Store) SaveTerminalCWD(cwd string) {
	state := TerminalState{CWD: cwd, UpdatedAt: time.Now()}
	s.mu.Lock()
	_, _ = s.db.Exec(`INSERT OR REPLACE INTO terminal_state(key, item) VALUES(?, ?)`, "default", mustJSON(state))
	s.mu.Unlock()
}

func (s *Store) SaveTerminalOutput(outputID, stdout, stderr string, exitCode int) {
	item := TerminalOutputItem{OutputID: outputID, Stdout: stdout, Stderr: stderr, ExitCode: exitCode, CreatedAt: time.Now()}
	s.mu.Lock()
	result, err := s.db.Exec(`INSERT INTO terminal_outputs(output_id, created_at, item) VALUES(?, ?, ?)`, outputID, formatTime(item.CreatedAt), mustJSON(item))
	if err == nil {
		if id, idErr := result.LastInsertId(); idErr == nil {
			item.ID = int(id)
			_, _ = s.db.Exec(`UPDATE terminal_outputs SET item = ? WHERE id = ?`, mustJSON(item), id)
		}
	}
	s.mu.Unlock()
}

func (s *Store) ReadTerminalOutput(outputID string) (TerminalOutputItem, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT item FROM terminal_outputs WHERE output_id = ? ORDER BY id DESC LIMIT 1`, outputID).Scan(&raw)
	var item TerminalOutputItem
	return item, err == nil && json.Unmarshal([]byte(raw), &item) == nil
}

func (s *Store) ReadTerminalOutputFields(outputID string) (stdout, stderr string, exitCode int, ok bool) {
	item, found := s.ReadTerminalOutput(outputID)
	if !found {
		return "", "", 0, false
	}
	return item.Stdout, item.Stderr, item.ExitCode, true
}

func (s *Store) LatestAuthUsage(provider, accountID string) []AuthUsageSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := queryJSONRows[AuthUsageSnapshot](s.db, `SELECT item FROM auth_usage_snapshots WHERE provider = ? AND account_id = ? ORDER BY captured_at ASC`, provider, accountID)
	latest := map[string]AuthUsageSnapshot{}
	for _, item := range items {
		current, ok := latest[item.WindowKey]
		if !ok || item.CapturedAt.After(current.CapturedAt) {
			latest[item.WindowKey] = item
		}
	}
	out := make([]AuthUsageSnapshot, 0, len(latest))
	for _, item := range latest {
		out = append(out, item)
	}
	return out
}

func (s *Store) AuthUsageInRange(provider, accountID string, since time.Time) []AuthUsageSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return queryJSONRows[AuthUsageSnapshot](s.db, `SELECT item FROM auth_usage_snapshots WHERE provider = ? AND account_id = ? AND captured_at >= ? ORDER BY captured_at ASC`, provider, accountID, formatTime(since))
}

// AddStory 追加或替换一条 Story 记忆。
func (s *Store) AddStory(item StoryItem) {
	s.mu.Lock()
	now := time.Now()
	if item.CreatedAt.IsZero() {
		var raw string
		var existing StoryItem
		if err := s.db.QueryRow(`SELECT item FROM stories WHERE id = ?`, item.ID).Scan(&raw); err == nil {
			_ = json.Unmarshal([]byte(raw), &existing)
		}
		if !existing.CreatedAt.IsZero() {
			item.CreatedAt = existing.CreatedAt
		} else if parsed, err := time.ParseInLocation("20060102150405.000000000", item.ID, time.Local); err == nil {
			item.CreatedAt = parsed
		} else {
			item.CreatedAt = now
		}
	}
	if item.UpdatedAt.IsZero() {
		item.UpdatedAt = now
	}
	_, _ = s.db.Exec(`INSERT OR REPLACE INTO stories(id, updated_at, item) VALUES(?, ?, ?)`, item.ID, formatTime(item.UpdatedAt), mustJSON(item))
	s.mu.Unlock()
}

// DeleteStory 删除指定 Story 记忆。
func (s *Store) DeleteStory(id string) {
	s.mu.Lock()
	_, _ = s.db.Exec(`DELETE FROM stories WHERE id = ?`, id)
	_, _ = s.db.Exec(`DELETE FROM story_documents WHERE story_id = ?`, id)
	s.mu.Unlock()
}

func (s *Store) ReplaceStoryMemoryDocuments(storyID string, docs []StoryMemoryDocument) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return
	}
	defer tx.Rollback()
	_, _ = tx.Exec(`DELETE FROM story_documents WHERE story_id = ?`, storyID)
	now := time.Now()
	for i := range docs {
		docs[i].ID = 0
		docs[i].StoryID = storyID
		docs[i].CreatedAt = now
		docs[i].UpdatedAt = now
		result, err := tx.Exec(`INSERT INTO story_documents(story_id, item) VALUES(?, ?)`, storyID, mustJSON(docs[i]))
		if err != nil {
			return
		}
		if id, err := result.LastInsertId(); err == nil {
			docs[i].ID = int(id)
			_, _ = tx.Exec(`UPDATE story_documents SET item = ? WHERE id = ?`, mustJSON(docs[i]), id)
		}
	}
	_ = tx.Commit()
}

func (s *Store) FindEmbedding(key EmbeddingCacheKey) ([]float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT item FROM embedding_cache WHERE provider = ? AND model = ? AND task_type = ? AND output_dimensionality = ? AND text_hash = ?`, key.Provider, key.Model, key.TaskType, key.OutputDimensionality, key.TextHash).Scan(&raw)
	if err != nil {
		return nil, false
	}
	var item EmbeddingCacheItem
	if json.Unmarshal([]byte(raw), &item) != nil {
		return nil, false
	}
	return append([]float64(nil), item.Embedding...), true
}

func (s *Store) SaveEmbedding(key EmbeddingCacheKey, text string, values []float64) {
	now := time.Now()
	item := EmbeddingCacheItem{
		Provider:             key.Provider,
		Model:                key.Model,
		TaskType:             key.TaskType,
		OutputDimensionality: key.OutputDimensionality,
		TextHash:             key.TextHash,
		Text:                 text,
		Embedding:            append([]float64(nil), values...),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	s.mu.Lock()
	var id int
	var raw string
	if err := s.db.QueryRow(`SELECT id, item FROM embedding_cache WHERE provider = ? AND model = ? AND task_type = ? AND output_dimensionality = ? AND text_hash = ?`, key.Provider, key.Model, key.TaskType, key.OutputDimensionality, key.TextHash).Scan(&id, &raw); err == nil {
		var existing EmbeddingCacheItem
		_ = json.Unmarshal([]byte(raw), &existing)
		item.ID = id
		item.CreatedAt = existing.CreatedAt
		_, _ = s.db.Exec(`UPDATE embedding_cache SET item = ? WHERE id = ?`, mustJSON(item), id)
	} else {
		result, err := s.db.Exec(`INSERT INTO embedding_cache(provider, model, task_type, output_dimensionality, text_hash, item) VALUES(?, ?, ?, ?, ?, ?)`, item.Provider, item.Model, item.TaskType, item.OutputDimensionality, item.TextHash, mustJSON(item))
		if err == nil {
			if id64, idErr := result.LastInsertId(); idErr == nil {
				item.ID = int(id64)
				_, _ = s.db.Exec(`UPDATE embedding_cache SET item = ? WHERE id = ?`, mustJSON(item), id64)
			}
		}
	}
	s.mu.Unlock()
}

type EmbeddingCacheKey struct {
	Provider             string
	Model                string
	TaskType             string
	OutputDimensionality int
	TextHash             string
}

// UpsertMetricChart 按图表名称创建或替换指标图表。
func (s *Store) UpsertMetricChart(chart MetricChart) MetricChart {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	if err := s.db.QueryRow(`SELECT item FROM metric_charts WHERE chart_name = ?`, chart.ChartName).Scan(&raw); err == nil {
		var existing MetricChart
		if json.Unmarshal([]byte(raw), &existing) == nil {
			chart.CreatedAt = existing.CreatedAt
		}
	}
	if chart.CreatedAt.IsZero() {
		chart.CreatedAt = time.Now()
	}
	chart.UpdatedAt = time.Now()
	_, _ = s.db.Exec(`INSERT OR REPLACE INTO metric_charts(chart_name, item) VALUES(?, ?)`, chart.ChartName, mustJSON(chart))
	return chart
}

// DeleteMetricChart 按名称删除图表。
func (s *Store) DeleteMetricChart(name string) {
	s.mu.Lock()
	_, _ = s.db.Exec(`DELETE FROM metric_charts WHERE chart_name = ?`, name)
	s.mu.Unlock()
}

// AddNewsArticle 追加一篇已入库新闻文章。
func (s *Store) AddNewsArticle(article NewsArticle) {
	if article.CreatedAt.IsZero() {
		article.CreatedAt = time.Now()
	}
	if article.UpdatedAt.IsZero() {
		article.UpdatedAt = article.CreatedAt
	}
	s.mu.Lock()
	var result sql.Result
	var err error
	upstreamID := article.UpstreamID
	if upstreamID == "" {
		if article.ID > 0 {
			upstreamID = "local-id:" + common.JSONNumber(float64(article.ID))
		} else {
			upstreamID = "local:" + common.NewID()
		}
	}
	if article.ID > 0 {
		result, err = s.db.Exec(`INSERT OR REPLACE INTO news_articles(id, source_key, upstream_id, item) VALUES(?, ?, ?, ?)`, article.ID, article.SourceKey, upstreamID, mustJSON(article))
	} else {
		result, err = s.db.Exec(`INSERT INTO news_articles(source_key, upstream_id, item) VALUES(?, ?, ?)`, article.SourceKey, upstreamID, mustJSON(article))
	}
	if err == nil {
		if id, idErr := result.LastInsertId(); idErr == nil {
			if article.ID == 0 {
				article.ID = int(id)
			}
			_, _ = s.db.Exec(`UPDATE news_articles SET item = ? WHERE id = ?`, mustJSON(article), article.ID)
		}
	}
	s.mu.Unlock()
}

// UpsertNewsArticle 按 sourceKey/upstreamId 创建或更新新闻文章。
func (s *Store) UpsertNewsArticle(article NewsArticle) (NewsArticle, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	var id int
	var raw string
	if err := s.db.QueryRow(`SELECT id, item FROM news_articles WHERE source_key = ? AND upstream_id = ?`, article.SourceKey, article.UpstreamID).Scan(&id, &raw); err == nil {
		var existing NewsArticle
		_ = json.Unmarshal([]byte(raw), &existing)
		article.ID = id
		article.CreatedAt = existing.CreatedAt
		article.UpdatedAt = now
		_, _ = s.db.Exec(`UPDATE news_articles SET item = ? WHERE id = ?`, mustJSON(article), id)
		return article, false
	}
	if article.CreatedAt.IsZero() {
		article.CreatedAt = now
	}
	article.UpdatedAt = now
	result, err := s.db.Exec(`INSERT INTO news_articles(source_key, upstream_id, item) VALUES(?, ?, ?)`, article.SourceKey, article.UpstreamID, mustJSON(article))
	if err == nil {
		if id64, idErr := result.LastInsertId(); idErr == nil {
			article.ID = int(id64)
			_, _ = s.db.Exec(`UPDATE news_articles SET item = ? WHERE id = ?`, mustJSON(article), id64)
		}
	}
	return article, true
}

func (s *Store) FindNewsArticleBySource(sourceKey, upstreamID string) (NewsArticle, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT item FROM news_articles WHERE source_key = ? AND upstream_id = ?`, sourceKey, upstreamID).Scan(&raw)
	var article NewsArticle
	return article, err == nil && json.Unmarshal([]byte(raw), &article) == nil
}

func (s *Store) NewsFeedCursor(sourceKey string) (NewsFeedCursor, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT item FROM news_feed_cursors WHERE source_key = ?`, sourceKey).Scan(&raw)
	var cursor NewsFeedCursor
	return cursor, err == nil && json.Unmarshal([]byte(raw), &cursor) == nil
}

func (s *Store) UpsertNewsFeedCursor(sourceKey string, articleID int, publishedAt time.Time) {
	if sourceKey == "" || articleID == 0 || publishedAt.IsZero() {
		return
	}
	cursor := NewsFeedCursor{
		SourceKey:           sourceKey,
		LastSeenArticleID:   articleID,
		LastSeenPublishedAt: publishedAt,
		UpdatedAt:           time.Now(),
	}
	if existing, ok := s.NewsFeedCursor(sourceKey); ok {
		cursor.CreatedAt = existing.CreatedAt
	}
	if cursor.CreatedAt.IsZero() {
		cursor.CreatedAt = cursor.UpdatedAt
	}
	cursor.UpdatedAt = time.Now()
	s.mu.Lock()
	_, _ = s.db.Exec(`INSERT OR REPLACE INTO news_feed_cursors(source_key, item) VALUES(?, ?)`, cursor.SourceKey, mustJSON(cursor))
	s.mu.Unlock()
}

func (s *Store) ListNewsArticlesLatest(sourceKey string, limit int) []NewsArticle {
	items := s.ListNewsArticlesBySource(sourceKey)
	sortNewsArticlesNewestFirst(items)
	return limitNewsArticles(items, limit)
}

func (s *Store) ListNewsArticlesNewerThanCursor(sourceKey string, cursor NewsFeedCursor, limit int) []NewsArticle {
	items := []NewsArticle{}
	for _, article := range s.ListNewsArticlesBySource(sourceKey) {
		if newsArticleAfterCursor(article, cursor) {
			items = append(items, article)
		}
	}
	sortNewsArticlesNewestFirst(items)
	return limitNewsArticles(items, limit)
}

func (s *Store) CountNewsArticlesNewerThanCursor(sourceKey string, cursor NewsFeedCursor) int {
	count := 0
	for _, article := range s.ListNewsArticlesBySource(sourceKey) {
		if newsArticleAfterCursor(article, cursor) {
			count++
		}
	}
	return count
}

func sortNewsArticlesNewestFirst(items []NewsArticle) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].PublishedAt.Equal(items[j].PublishedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].PublishedAt.After(items[j].PublishedAt)
	})
}

func limitNewsArticles(items []NewsArticle, limit int) []NewsArticle {
	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return append([]NewsArticle(nil), items...)
}

func newsArticleAfterCursor(article NewsArticle, cursor NewsFeedCursor) bool {
	if cursor.LastSeenArticleID == 0 || cursor.LastSeenPublishedAt.IsZero() {
		return true
	}
	if article.PublishedAt.After(cursor.LastSeenPublishedAt) {
		return true
	}
	return article.PublishedAt.Equal(cursor.LastSeenPublishedAt) && article.ID > cursor.LastSeenArticleID
}

func (s *Store) UpsertOAuthSession(session OAuthSession) {
	s.mu.Lock()
	_, _ = s.db.Exec(`INSERT OR REPLACE INTO oauth_sessions(provider, item) VALUES(?, ?)`, session.Provider, mustJSON(session))
	s.mu.Unlock()
}

func (s *Store) OAuthSession(provider string) (OAuthSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var raw string
	err := s.db.QueryRow(`SELECT item FROM oauth_sessions WHERE provider = ?`, provider).Scan(&raw)
	var session OAuthSession
	return session, err == nil && json.Unmarshal([]byte(raw), &session) == nil
}

func (s *Store) DeleteOAuthSession(provider string) {
	s.mu.Lock()
	_, _ = s.db.Exec(`DELETE FROM oauth_sessions WHERE provider = ?`, provider)
	s.mu.Unlock()
}

// NextID 返回 Store 内唯一的整数 ID。
func (s *Store) NextID() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	result, err := s.db.Exec(`INSERT INTO metrics(created_at, item) VALUES(?, ?)`, formatTime(time.Now()), `{}`)
	if err != nil {
		return 0
	}
	id, _ := result.LastInsertId()
	_, _ = s.db.Exec(`DELETE FROM metrics WHERE id = ?`, id)
	return int(id)
}

func (s *Store) Snapshot() StoreData {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := StoreData{AgentSnapshots: map[string]json.RawMessage{}}
	out.AppLogs = queryJSONRows[AppLogItem](s.db, `SELECT item FROM app_logs ORDER BY id ASC`)
	if len(s.Data.AppLogs) > 0 {
		out.AppLogs = append(out.AppLogs, s.Data.AppLogs...)
	}
	out.LlmCalls = queryJSONRows[LlmCallItem](s.db, `SELECT item FROM llm_calls ORDER BY id ASC`)
	out.NapcatEvents = queryJSONRows[NapcatEventItem](s.db, `SELECT item FROM napcat_events ORDER BY id ASC`)
	out.NapcatMessages = queryJSONRows[NapcatMessageItem](s.db, `SELECT item FROM napcat_messages ORDER BY id ASC`)
	out.StoryLedger = queryJSONRows[StoryLedgerItem](s.db, `SELECT item FROM story_ledger ORDER BY seq ASC`)
	out.Ledger = queryJSONRows[LinearLedgerItem](s.db, `SELECT item FROM story_ledger ORDER BY seq ASC`)
	out.Stories = queryJSONRows[StoryItem](s.db, `SELECT item FROM stories ORDER BY updated_at ASC`)
	out.StoryDocuments = queryJSONRows[StoryMemoryDocument](s.db, `SELECT item FROM story_documents ORDER BY id ASC`)
	out.EmbeddingCache = queryJSONRows[EmbeddingCacheItem](s.db, `SELECT item FROM embedding_cache ORDER BY id ASC`)
	out.Metrics = queryJSONRows[MetricItem](s.db, `SELECT item FROM metrics WHERE item != '{}' ORDER BY id ASC`)
	out.MetricCharts = queryJSONRows[MetricChart](s.db, `SELECT item FROM metric_charts ORDER BY chart_name ASC`)
	out.NewsArticles = queryJSONRows[NewsArticle](s.db, `SELECT item FROM news_articles ORDER BY id ASC`)
	out.NewsFeedCursors = queryJSONRows[NewsFeedCursor](s.db, `SELECT item FROM news_feed_cursors ORDER BY source_key ASC`)
	out.NewsFeedCursor = append([]NewsFeedCursor(nil), out.NewsFeedCursors...)
	out.OAuthSessions = queryJSONRows[OAuthSession](s.db, `SELECT item FROM oauth_sessions ORDER BY provider ASC`)
	out.AuthUsage = queryJSONRows[AuthUsageSnapshot](s.db, `SELECT item FROM auth_usage_snapshots ORDER BY id ASC`)
	out.TerminalOutput = queryJSONRows[TerminalOutputItem](s.db, `SELECT item FROM terminal_outputs ORDER BY id ASC`)
	var terminalRaw string
	if err := s.db.QueryRow(`SELECT item FROM terminal_state WHERE key = ?`, "default").Scan(&terminalRaw); err == nil {
		_ = json.Unmarshal([]byte(terminalRaw), &out.TerminalState)
	}
	rows, err := s.db.Query(`SELECT key, value FROM agent_snapshots`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var key, value string
			if rows.Scan(&key, &value) == nil {
				out.AgentSnapshots[key] = json.RawMessage(value)
			}
		}
	}
	if raw, ok := out.AgentSnapshots["agentRuntimeSnapshot"]; ok {
		_ = json.Unmarshal(raw, &out.AgentSnapshot)
	}
	return out
}

func queryJSONRows[T any](db *sql.DB, query string, args ...any) []T {
	rows, err := db.Query(query, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		var raw string
		var item T
		if rows.Scan(&raw) == nil && json.Unmarshal([]byte(raw), &item) == nil {
			out = append(out, item)
		}
	}
	return out
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format(time.RFC3339Nano)
}

func keepLast[T any](items []T, max int) []T {
	if max <= 0 || len(items) <= max {
		return items
	}
	return append([]T(nil), items[len(items)-max:]...)
}

func compactLlmCall(item LlmCallItem) LlmCallItem {
	item.RequestPayload = compactMapPayload(item.RequestPayload)
	item.ResponsePayload = compactMapPayload(item.ResponsePayload)
	item.NativeRequestPayload = compactMapPayload(item.NativeRequestPayload)
	item.NativeResponsePayload = compactMapPayload(item.NativeResponsePayload)
	item.Error = compactMapPayload(item.Error)
	item.NativeError = compactMapPayload(item.NativeError)
	return item
}

func compactMapPayload(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) <= maxStoredLlmPayload {
		return value
	}
	out := map[string]any{
		"compacted":   true,
		"jsonBytes":   len(raw),
		"jsonPreview": trimRunes(string(raw), maxStoredLlmPreview),
	}
	for _, key := range []string{"provider", "model", "status", "id", "type", "messageCount", "toolCount", "toolNames", "usage"} {
		if v, ok := value[key]; ok {
			out[key] = v
		}
	}
	return out
}

func trimRunes(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= max {
		return value
	}
	return string(runes[:max]) + "..."
}

func setItemID(item any, id int) {
	switch v := item.(type) {
	case *AppLogItem:
		v.ID = id
	case *LlmCallItem:
		v.ID = id
	case *NapcatEventItem:
		v.ID = id
	case *NapcatMessageItem:
		v.ID = id
	case *MetricItem:
		v.ID = id
	case *AuthUsageSnapshot:
		v.ID = id
	case *TerminalOutputItem:
		v.ID = id
	}
}

// Paginate 对项目切片，并返回兼容前端的分页元数据。
func Paginate[T any](items []T, page, pageSize int) ([]T, map[string]int) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	total := len(items)
	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return items[start:end], map[string]int{"page": page, "pageSize": pageSize, "total": total}
}

// NewestFirst 使用给定降序比较器返回排序后的副本。
func NewestFirst[T any](items []T, less func(a, b T) bool) []T {
	out := append([]T(nil), items...)
	sort.Slice(out, func(i, j int) bool { return less(out[i], out[j]) })
	return out
}

// StringPtr 将 JSON 标量值规范化为可选字符串。
func StringPtr(v any) *string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return nil
		}
		return &x
	case float64:
		s := common.JSONNumber(x)
		return &s
	default:
		return nil
	}
}

// IntPtr 将 JSON 标量值规范化为可选整数。
func IntPtr(v any) *int {
	switch x := v.(type) {
	case int:
		return &x
	case float64:
		i := int(x)
		return &i
	default:
		return nil
	}
}
