package video

import (
	"context"

	"QqBot/internal/prompts"
)

// Part 是交给视频理解模型的本地视频文件。
type Part struct {
	Path     string
	MimeType string
	Filename string
}

// Client 由支持视频理解的模型适配器实现。
type Client interface {
	DescribeVideo(context.Context, string, Part) (string, error)
}

// Agent 封装视频摘要行为。
type Agent struct {
	Client Client
}

func (a Agent) Analyze(ctx context.Context, prompt string, part Part) (string, error) {
	if a.Client == nil {
		return "视频理解能力未配置", nil
	}
	if prompt == "" {
		prompt = prompts.VideoSystemPrompt()
	}
	return a.Client.DescribeVideo(ctx, prompt, part)
}
