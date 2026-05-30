package schema

type Pagination struct {
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
	Total    int `json:"total"`
}

type Paginated[T any] struct {
	Pagination Pagination `json:"pagination"`
	Items      []T        `json:"items"`
}

type HealthResponse struct {
	Status    string `json:"status"`
	Timestamp string `json:"timestamp"`
}

type LLMProviderOption struct {
	ID     string   `json:"id"`
	Models []string `json:"models"`
}

type LLMProviderListResponse struct {
	Providers []LLMProviderOption `json:"providers"`
}

type AgentDashboardSnapshot struct {
	GeneratedAt string `json:"generatedAt"`
	Agents      []any  `json:"agents"`
}

type AuthStatusResponse struct {
	Provider   string `json:"provider"`
	Status     string `json:"status"`
	IsLoggedIn bool   `json:"isLoggedIn"`
	Session    any    `json:"session"`
}

type NapcatSendMessageResponse struct {
	MessageID int `json:"messageId"`
}

type MetricChartDefinition struct {
	ChartName  string            `json:"chartName"`
	MetricName string            `json:"metricName"`
	Aggregator string            `json:"aggregator"`
	TagFilters map[string]string `json:"tagFilters"`
	GroupByTag string            `json:"groupByTag"`
	CreatedAt  string            `json:"createdAt"`
	UpdatedAt  string            `json:"updatedAt"`
}
