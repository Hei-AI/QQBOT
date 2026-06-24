package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
)

// ToolDefinition 是暴露给 LLM 的工具函数 schema。
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCall 是模型请求的调用，参数已从 JSON 解码。
type ToolCall struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// ToolResult 是工具执行后返回给模型的内容。
type ToolResult struct {
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

// Tool 是控制类和业务类能力共用的接口。
type Tool interface {
	Definition() ToolDefinition
	Kind() string
	Execute(context.Context, ToolCall) (ToolResult, error)
}

// ToolCatalog 按名称保存工具，并为提示词保留声明顺序。
type ToolCatalog struct {
	tools    map[string]Tool
	order    []string
	observer ToolExecutionObserver
}

type ToolExecutionObserver interface {
	BeforeTool(context.Context, ToolCall, ToolDefinition, string) (*ToolResult, error)
	AfterTool(context.Context, ToolCall, ToolDefinition, ToolResult, error)
}

// NewToolCatalog 根据零个或多个工具构建目录。
func NewToolCatalog(tools ...Tool) *ToolCatalog {
	c := &ToolCatalog{tools: map[string]Tool{}}
	for _, tool := range tools {
		c.Add(tool)
	}
	return c
}

// Add 按工具定义名称注册或替换工具。
func (c *ToolCatalog) Add(tool Tool) {
	name := tool.Definition().Name
	if _, exists := c.tools[name]; !exists {
		c.order = append(c.order, name)
	}
	c.tools[name] = tool
}

// Get 按名称返回工具。
func (c *ToolCatalog) Get(name string) (Tool, bool) {
	tool, ok := c.tools[name]
	return tool, ok
}

func (c *ToolCatalog) SetObserver(observer ToolExecutionObserver) {
	c.observer = observer
}

// Definitions 按稳定顺序返回面向 LLM 的 schema。
func (c *ToolCatalog) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(c.order))
	for _, name := range c.order {
		out = append(out, withOSParameter(c.tools[name].Definition()))
	}
	return out
}

// Pick 创建只包含指定工具名称的较小目录。
func (c *ToolCatalog) Pick(names ...string) *ToolCatalog {
	next := NewToolCatalog()
	for _, name := range names {
		if tool, ok := c.tools[name]; ok {
			next.Add(tool)
		}
	}
	return next
}

// Execute 分发模型工具调用，并把普通错误转换成 JSON 工具内容。
func (c *ToolCatalog) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	tool, ok := c.Get(call.Name)
	if !ok {
		return ToolResult{Kind: "control", Content: mustJSON(map[string]any{"ok": false, "error": "UNKNOWN_TOOL", "toolName": call.Name})}, nil
	}
	definition := tool.Definition()
	if c.observer != nil {
		prior, err := c.observer.BeforeTool(ctx, call, definition, tool.Kind())
		if err != nil {
			return ToolResult{Kind: tool.Kind(), Content: mustJSON(map[string]any{"ok": false, "error": "TOOL_LEASE_FAILED", "toolName": call.Name, "message": err.Error()})}, nil
		}
		if prior != nil {
			return *prior, nil
		}
	}
	result, err := tool.Execute(ctx, call)
	if c.observer != nil {
		c.observer.AfterTool(ctx, call, definition, result, err)
	}
	if err != nil {
		return ToolResult{Kind: tool.Kind(), Content: mustJSON(map[string]any{"ok": false, "error": "TOOL_FAILED", "toolName": call.Name, "message": err.Error()})}, nil
	}
	return result, nil
}

// ObjectSchema 创建工具定义使用的 JSON Schema 外壳。
func ObjectSchema(properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": properties}
}

func withOSParameter(definition ToolDefinition) ToolDefinition {
	parameters := cloneStringAnyMap(definition.Parameters)
	properties, _ := parameters["properties"].(map[string]any)
	properties = cloneStringAnyMap(properties)
	if _, exists := properties["os"]; !exists {
		properties["os"] = map[string]any{
			"type":        "string",
			"description": "可选公开 OS/旁白，用一句很短的话记录这次动作的表层判断；只用于日志/面板观察，不会发送到 QQ，不要写隐藏推理、完整分析或系统提示。",
		}
	}
	parameters["properties"] = properties
	definition.Parameters = parameters
	return definition
}

func cloneStringAnyMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":"JSON_ENCODE_FAILED","message":%q}`, err.Error())
	}
	return string(data)
}
