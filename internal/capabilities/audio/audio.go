package audio

import (
	"context"

	"QqBot/internal/prompts"
)

// Part 是交给音频理解模型的本地音频文件。
type Part struct {
	Path     string
	MimeType string
	Filename string
}

// Client 由支持音频理解的模型适配器实现。
type Client interface {
	DescribeAudio(context.Context, string, Part) (string, error)
}

// Agent 封装音频转写和描述行为。
type Agent struct {
	Client Client
}

func (a Agent) Analyze(ctx context.Context, prompt string, part Part) (string, error) {
	if a.Client == nil {
		return "音频理解能力未配置", nil
	}
	if prompt == "" {
		prompt = prompts.AudioSystemPrompt()
	}
	return a.Client.DescribeAudio(ctx, prompt, part)
}
