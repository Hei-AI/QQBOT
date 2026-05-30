package agentruntime

import (
	"context"
	"encoding/json"
	"errors"
)

// Message 是 Go Agent 循环使用的运行时中立聊天消息格式。
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"toolCalls,omitempty"`
	ToolCallID string     `json:"toolCallId,omitempty"`
}

// Completion 是 LLM 适配器返回的标准化模型响应。
type Completion struct {
	Provider string    `json:"provider"`
	Model    string    `json:"model"`
	OS       string    `json:"os,omitempty"`
	Message  Message   `json:"message"`
	Usage    *TokenUse `json:"usage,omitempty"`
}

// TokenUse 对应供应商的 token 统计，用于指标和上下文压缩决策。
type TokenUse struct {
	PromptTokens     int `json:"promptTokens,omitempty"`
	CompletionTokens int `json:"completionTokens,omitempty"`
	TotalTokens      int `json:"totalTokens,omitempty"`
}

// Model 是 ReActKernel 所需的最小 LLM 接口。
type Model interface {
	Chat(context.Context, string, []Message, []ToolDefinition, any) (Completion, error)
}

// RoundInput 包含一次 ReAct 模型/工具轮次所需的全部状态。
type RoundInput struct {
	SystemPrompt string
	Messages     []Message
	Tools        *ToolCatalog
	ToolChoice   any
}

type ModelErrorDecision struct {
	Handled bool
	Retry   bool
}

type ToolErrorDecision struct {
	Handled bool
	Result  *ToolResult
}

type ToolExecutionAugmentation struct {
	AppendedMessages []Message
}

type ReActKernelExtension interface {
	OnBeforeModel(context.Context, RoundInput) error
	OnAfterModel(context.Context, RoundInput, Completion) error
	OnModelError(context.Context, RoundInput, error) (ModelErrorDecision, error)
	OnBeforeToolExecution(context.Context, RoundInput, Completion, ToolCall) error
	OnToolError(context.Context, RoundInput, Completion, ToolCall, error) (ToolErrorDecision, error)
	OnAfterToolExecution(context.Context, RoundInput, Completion, ToolCall, ToolResult) (ToolExecutionAugmentation, error)
}

type ReActKernelExtensionBase struct{}

func (ReActKernelExtensionBase) OnBeforeModel(context.Context, RoundInput) error { return nil }
func (ReActKernelExtensionBase) OnAfterModel(context.Context, RoundInput, Completion) error {
	return nil
}
func (ReActKernelExtensionBase) OnModelError(context.Context, RoundInput, error) (ModelErrorDecision, error) {
	return ModelErrorDecision{}, nil
}
func (ReActKernelExtensionBase) OnBeforeToolExecution(context.Context, RoundInput, Completion, ToolCall) error {
	return nil
}
func (ReActKernelExtensionBase) OnToolError(context.Context, RoundInput, Completion, ToolCall, error) (ToolErrorDecision, error) {
	return ToolErrorDecision{}, nil
}
func (ReActKernelExtensionBase) OnAfterToolExecution(context.Context, RoundInput, Completion, ToolCall, ToolResult) (ToolExecutionAugmentation, error) {
	return ToolExecutionAugmentation{}, nil
}

// ToolExecution 记录一次工具调用及其返回内容。
type ToolExecution struct {
	Call   ToolCall
	Result ToolResult
}

// RoundResult 保存助手消息以及所有已执行工具的信息。
type RoundResult struct {
	Completion       Completion
	Assistant        Message
	ToolExecutions   []ToolExecution
	AppendedMessages []Message
}

// ReActKernel 执行一轮模型调用，并分发返回的工具调用。
type ReActKernel struct {
	Model      Model
	Extensions []ReActKernelExtension
}

// RunRound 调用一次模型，并执行响应中的全部工具调用。
func (k ReActKernel) RunRound(ctx context.Context, input RoundInput) (RoundResult, error) {
	if k.Model == nil {
		return RoundResult{}, errors.New("react kernel requires model")
	}
	tools := []ToolDefinition{}
	if input.Tools != nil {
		tools = input.Tools.Definitions()
	}
	var completion Completion
	for {
		for _, extension := range k.Extensions {
			if err := extension.OnBeforeModel(ctx, input); err != nil {
				return RoundResult{}, err
			}
		}
		var err error
		completion, err = k.Model.Chat(ctx, input.SystemPrompt, input.Messages, tools, input.ToolChoice)
		if err == nil {
			break
		}
		handled := false
		retry := false
		for _, extension := range k.Extensions {
			decision, hookErr := extension.OnModelError(ctx, input, err)
			if hookErr != nil {
				return RoundResult{}, hookErr
			}
			if decision.Handled {
				handled = true
				retry = decision.Retry
				break
			}
		}
		if !handled {
			return RoundResult{}, err
		}
		if !retry {
			empty := Message{Role: "assistant"}
			return RoundResult{
				Completion: Completion{Message: empty},
				Assistant:  empty,
			}, nil
		}
	}
	for _, extension := range k.Extensions {
		if err := extension.OnAfterModel(ctx, input, completion); err != nil {
			return RoundResult{}, err
		}
	}
	result := RoundResult{Completion: completion, Assistant: completion.Message}
	if input.Tools == nil {
		return result, nil
	}
	for _, call := range completion.Message.ToolCalls {
		for _, extension := range k.Extensions {
			if err := extension.OnBeforeToolExecution(ctx, input, completion, call); err != nil {
				return RoundResult{}, err
			}
		}
		toolResult, err := input.Tools.Execute(ctx, call)
		if err != nil {
			resolved := false
			for _, extension := range k.Extensions {
				decision, hookErr := extension.OnToolError(ctx, input, completion, call, err)
				if hookErr != nil {
					return RoundResult{}, hookErr
				}
				if decision.Handled {
					resolved = true
					if decision.Result != nil {
						toolResult = *decision.Result
					}
					break
				}
			}
			if !resolved {
				payload, _ := json.Marshal(map[string]any{
					"ok":        false,
					"error":     "TEMPORARY_TOOL_FAILURE",
					"retryable": true,
					"toolName":  call.Name,
					"message":   "工具 " + call.Name + " 暂时调用失败了，请稍后重试，或换一种方式继续。",
					"details":   err.Error(),
				})
				toolResult = ToolResult{Kind: "business", Content: string(payload)}
			}
		}
		for _, extension := range k.Extensions {
			augmentation, err := extension.OnAfterToolExecution(ctx, input, completion, call, toolResult)
			if err != nil {
				return RoundResult{}, err
			}
			result.AppendedMessages = append(result.AppendedMessages, augmentation.AppendedMessages...)
		}
		result.ToolExecutions = append(result.ToolExecutions, ToolExecution{Call: call, Result: toolResult})
	}
	return result, nil
}
