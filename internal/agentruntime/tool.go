package agentruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
	tools map[string]Tool
	order []string
}

// NewToolCatalog 根据零个或多个工具构建目录。
func NewToolCatalog(tools ...Tool) *ToolCatalog {
	c := &ToolCatalog{tools: map[string]Tool{}}
	for _, tool := range tools {
		c.Add(tool)
	}
	return c
}

// Add 按工具定义名称注册工具。
//
// 重复注册通常意味着工具集合接线错误。这里直接 panic，让问题在启动或测试阶段暴露，
// 避免后注册工具静默覆盖先注册工具。
func (c *ToolCatalog) Add(tool Tool) {
	name := tool.Definition().Name
	if _, exists := c.tools[name]; exists {
		panic(fmt.Sprintf("tool name is duplicated: %s", name))
	}
	c.order = append(c.order, name)
	c.tools[name] = tool
}

// Get 按名称返回工具。
func (c *ToolCatalog) Get(name string) (Tool, bool) {
	tool, ok := c.tools[name]
	return tool, ok
}

// Definitions 按稳定顺序返回面向 LLM 的 schema。
func (c *ToolCatalog) Definitions() []ToolDefinition {
	out := make([]ToolDefinition, 0, len(c.order))
	for _, name := range c.order {
		out = append(out, c.tools[name].Definition())
	}
	return out
}

// Pick 创建只包含指定工具名称的较小目录。
//
// 缺失工具会 panic，因为静默跳过会让模型看到的工具集合和运行时期望不一致。
func (c *ToolCatalog) Pick(names ...string) *ToolCatalog {
	next := NewToolCatalog()
	for _, name := range names {
		if tool, ok := c.tools[name]; ok {
			next.Add(tool)
		} else {
			panic(fmt.Sprintf("tool is not registered: %s", name))
		}
	}
	return next
}

// TryPick 是 Pick 的温和版本，适合配置驱动或降级路径。
func (c *ToolCatalog) TryPick(names ...string) *ToolCatalog {
	next := NewToolCatalog()
	for _, name := range names {
		if tool, ok := c.tools[name]; ok {
			next.Add(tool)
		} else {
			log.Printf("[AGENT] tool pick skipped missing tool=%s", name)
		}
	}
	return next
}

// Execute 分发模型工具调用。
//
// 工具自身返回的错误会原样交给 ReActKernel，这样 kernel extension
// 能统一决定 fallback、retry 或记录指标。未知工具仍作为工具结果返回，
// 因为这更像模型请求了不存在的能力，而不是宿主执行故障。
func (c *ToolCatalog) Execute(ctx context.Context, call ToolCall) (ToolResult, error) {
	tool, ok := c.Get(call.Name)
	if !ok {
		return ToolResult{Kind: "control", Content: mustJSON(map[string]any{"ok": false, "error": "UNKNOWN_TOOL", "toolName": call.Name})}, nil
	}
	result, err := tool.Execute(ctx, call)
	if err != nil {
		return ToolResult{}, err
	}
	return result, nil
}

// ObjectSchema 创建工具定义使用的 JSON Schema 外壳。
func ObjectSchema(properties map[string]any) map[string]any {
	return map[string]any{"type": "object", "properties": properties}
}

func mustJSON(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf(`{"ok":false,"error":"JSON_ENCODE_FAILED","message":%q}`, err.Error())
	}
	return string(data)
}
