package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config 是从 config.yaml 加载的顶层 YAML 配置。
//
// 在实现语言切换后继续复用同一份配置文件。
type Config struct {
	Server ServerConfig `yaml:"server"`
}

// ServerConfig 汇总所有后端运行时设置。
type ServerConfig struct {
	DatabaseURL string       `yaml:"databaseUrl"`
	Port        int          `yaml:"port"`
	Agent       AgentConfig  `yaml:"agent"`
	News        NewsConfig   `yaml:"news"`
	Napcat      NapcatConfig `yaml:"napcat"`
	LLM         LLMConfig    `yaml:"llm"`
	Tavily      TavilyConfig `yaml:"tavily"`
	Bot         BotConfig    `yaml:"bot"`
}

// AgentConfig 控制 root/story 循环节奏和能力限制。
type AgentConfig struct {
	ContextCompactionTotalTokenThreshold int            `yaml:"contextCompactionTotalTokenThreshold"`
	LLMRetryBackoffMs                    int            `yaml:"llmRetryBackoffMs"`
	WaitToolMaxWaitMs                    int            `yaml:"waitToolMaxWaitMs"`
	NotificationBatchWindowMs            int            `yaml:"notificationBatchWindowMs"`
	Story                                StoryConfig    `yaml:"story"`
	Terminal                             TerminalConfig `yaml:"terminal"`
}

// StoryConfig 控制 Story 批处理、记忆和召回行为。
type StoryConfig struct {
	BatchSize   int `yaml:"batchSize"`
	IdleFlushMs int `yaml:"idleFlushMs"`
	Memory      struct {
		Embedding EmbeddingConfig `yaml:"embedding"`
	} `yaml:"memory"`
	Recall struct {
		TopK           int     `yaml:"topK"`
		ScoreThreshold float64 `yaml:"scoreThreshold"`
	} `yaml:"recall"`
}

// EmbeddingConfig 描述 Story 记忆使用的 embedding 供应商。
type EmbeddingConfig struct {
	Provider             string `yaml:"provider"`
	APIKey               string `yaml:"apiKey"`
	BaseURL              string `yaml:"baseUrl"`
	Model                string `yaml:"model"`
	OutputDimensionality int    `yaml:"outputDimensionality"`
}

// TerminalConfig 控制 Agent 的终端能力。
type TerminalConfig struct {
	InitialCWD        string `yaml:"initialCwd"`
	CommandTimeoutMs  int    `yaml:"commandTimeoutMs"`
	PreviewBytes      int    `yaml:"previewBytes"`
	MaxOutputBytes    int    `yaml:"maxOutputBytes"`
	MaxCommandLength  int    `yaml:"maxCommandLength"`
	ReadOutputMaxSize int    `yaml:"readOutputMaxSize"`
	Shell             string `yaml:"shell"`
}

// NewsConfig 汇总外部新闻源设置。
type NewsConfig struct {
	Ithome IthomeConfig `yaml:"ithome"`
}

// IthomeConfig 控制 IThome RSS 轮询。
type IthomeConfig struct {
	PollIntervalMs     int `yaml:"pollIntervalMs"`
	RecentArticleLimit int `yaml:"recentArticleLimit"`
	ArticleMaxChars    int `yaml:"articleMaxChars"`
}

// NapcatConfig 控制 NapCat websocket 网关和启动时数据补水。
type NapcatConfig struct {
	WSURL                            string   `yaml:"wsUrl"`
	ReconnectMs                      int      `yaml:"reconnectMs"`
	RequestTimeoutMs                 int      `yaml:"requestTimeoutMs"`
	ListenGroupIDs                   []string `yaml:"listenGroupIds"`
	StartupContextRecentMessageCount int      `yaml:"startupContextRecentMessageCount"`
}

// LLMConfig 定义供应商和用途调用链。
type LLMConfig struct {
	TimeoutMs      int                 `yaml:"timeoutMs"`
	DebugReasoning bool                `yaml:"debugReasoning"`
	Providers      LLMProvidersConfig  `yaml:"providers"`
	Usages         map[string]LLMUsage `yaml:"usages"`
}

// LLMProvidersConfig 包含所有受支持的 LLM 供应商定义。
type LLMProvidersConfig struct {
	Deepseek    LLMProviderConfig `yaml:"deepseek"`
	OpenAI      LLMProviderConfig `yaml:"openai"`
	OpenAICodex LLMProviderConfig `yaml:"openaiCodex"`
	ClaudeCode  LLMProviderConfig `yaml:"claudeCode"`
}

// LLMProviderConfig 是 API key、base URL、模型列表共用的供应商结构。
type LLMProviderConfig struct {
	APIKey  string   `yaml:"apiKey"`
	BaseURL string   `yaml:"baseUrl"`
	Models  []string `yaml:"models"`
}

// LLMUsage 是单个逻辑用途的有序重试/兜底调用链。
type LLMUsage struct {
	Attempts []LLMAttempt `yaml:"attempts"`
}

// LLMAttempt 选择一个供应商/模型，并可指定重试次数。
type LLMAttempt struct {
	Provider string `yaml:"provider"`
	Model    string `yaml:"model"`
	Times    int    `yaml:"times"`
}

// TavilyConfig 保存网页搜索 API key。
type TavilyConfig struct {
	APIKey string `yaml:"apiKey"`
}

// BotConfig 标识提示词中的 Agent 和创造者信息。
type BotConfig struct {
	QQ      string `yaml:"qq"`
	Creator struct {
		Name string `yaml:"name"`
		QQ   string `yaml:"qq"`
	} `yaml:"creator"`
}

// LoadConfig 读取并校验兼容 config.yaml 的配置。
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 20003
	}
	if cfg.Server.Agent.ContextCompactionTotalTokenThreshold == 0 {
		cfg.Server.Agent.ContextCompactionTotalTokenThreshold = 150000
	}
	if cfg.Server.Agent.Story.BatchSize == 0 {
		cfg.Server.Agent.Story.BatchSize = 24
	}
	if cfg.Server.Agent.Story.IdleFlushMs == 0 {
		cfg.Server.Agent.Story.IdleFlushMs = int((2 * time.Minute).Milliseconds())
	}
	if cfg.Server.Napcat.ReconnectMs == 0 {
		cfg.Server.Napcat.ReconnectMs = 3000
	}
	if cfg.Server.Napcat.RequestTimeoutMs == 0 {
		cfg.Server.Napcat.RequestTimeoutMs = 10000
	}
	if cfg.Server.LLM.TimeoutMs == 0 {
		cfg.Server.LLM.TimeoutMs = 45000
	}
	for usage, value := range cfg.Server.LLM.Usages {
		for i := range value.Attempts {
			if value.Attempts[i].Times <= 0 {
				value.Attempts[i].Times = 1
			}
		}
		cfg.Server.LLM.Usages[usage] = value
	}
}
