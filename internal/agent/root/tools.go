package root

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"QqBot/internal/agentruntime"
)

// EnterTool 将根会话切换到指定子状态。
type EnterTool struct{ Session *Session }

func (EnterTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: "enter", Description: "进入需要独占工具环境的 App；聊天、私聊和新闻不需要进入。", Parameters: agentruntime.ObjectSchema(map[string]any{
		"id": map[string]any{"type": "string", "description": `App id，只能是 "calc" 或 "terminal"。`, "enum": []string{"calc", "terminal"}},
		"os": osParameterSchema(),
	})}
}
func (EnterTool) Kind() string { return "business" }
func (t EnterTool) Execute(_ context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	id := normalizeEnterArguments(call.Arguments)
	if t.Session == nil {
		data, _ := json.Marshal(map[string]any{"ok": true, "entered": id})
		return agentruntime.ToolResult{Kind: "control", Content: string(data)}, nil
	}
	if id == "calc" || id == "terminal" {
		data, _ := json.Marshal(t.Session.EnterApp(id))
		return agentruntime.ToolResult{Kind: "control", Content: string(data)}, nil
	}
	data, _ := json.Marshal(t.Session.Enter(id))
	return agentruntime.ToolResult{Kind: "control", Content: string(data)}, nil
}

// BackToPortalTool 将状态导航重置到 portal。
type BackToPortalTool struct{ Session *Session }

func (BackToPortalTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: "back", Description: "退出当前焦点状态并返回上一级状态", Parameters: agentruntime.ObjectSchema(map[string]any{
		"os": osParameterSchema(),
	})}
}
func (BackToPortalTool) Kind() string { return "business" }
func (t BackToPortalTool) Execute(context.Context, agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	if t.Session == nil {
		return agentruntime.ToolResult{Kind: "control", Content: `{"ok":true,"stateId":"portal"}`}, nil
	}
	data, _ := json.Marshal(t.Session.Back())
	return agentruntime.ToolResult{Kind: "control", Content: string(data)}, nil
}

// AppBackToPortalTool 退出当前 App 回到 Portal 桌面。
type AppBackToPortalTool struct{ Session *Session }

func (AppBackToPortalTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: "back_to_portal", Description: "退出当前 App 返回桌面（Portal）。仅当目前在某个 App 里时调用。", Parameters: agentruntime.ObjectSchema(map[string]any{
		"os": osParameterSchema(),
	})}
}
func (AppBackToPortalTool) Kind() string { return "business" }
func (t AppBackToPortalTool) Execute(context.Context, agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	if t.Session == nil {
		return agentruntime.ToolResult{Kind: "control", Content: `{"ok":false,"error":"SESSION_UNAVAILABLE"}`}, nil
	}
	data, _ := json.Marshal(t.Session.BackToPortal())
	return agentruntime.ToolResult{Kind: "control", Content: string(data)}, nil
}

// HelpTool 返回当前 App 的能力说明。
type HelpTool struct{ Session *Session }

func (HelpTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: "help", Description: "查询当前所在 App 的能力说明。不在任何 App 里时返回提示。", Parameters: agentruntime.ObjectSchema(map[string]any{})}
}
func (HelpTool) Kind() string { return "business" }
func (t HelpTool) Execute(context.Context, agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	currentApp := ""
	if t.Session != nil {
		t.Session.mu.Lock()
		currentApp = t.Session.CurrentApp
		t.Session.mu.Unlock()
	}
	switch currentApp {
	case "":
		return agentruntime.ToolResult{Kind: "control", Content: "你不在任何 App 里。先用 enter 进入一个 App，再调用 help 查看那个 App 能做什么。"}, nil
	case "calc":
		return agentruntime.ToolResult{Kind: "control", Content: strings.Join([]string{
			"你在 calc App 里。当前可调用工具：",
			"  - calculate(a, op, b): 对两个有限实数做一次二元四则运算。op 取值: +, -, *, /",
			"",
			"需要复合运算（例如 1 + 2 * 3）时，按运算优先级分多次调用：",
			`  1. calculate(a=2, op="*", b=3) -> 6`,
			`  2. calculate(a=1, op="+", b=6) -> 7`,
			"",
			"调 back_to_portal 退出本 App 回到桌面。",
		}, "\n")}, nil
	case "terminal":
		cwd := ""
		if t.Session != nil {
			t.Session.mu.Lock()
			cwd = t.Session.TerminalCWD
			t.Session.mu.Unlock()
		}
		if cwd == "" {
			cwd = "(未初始化)"
		}
		return agentruntime.ToolResult{Kind: "control", Content: strings.Join([]string{
			fmt.Sprintf("你在终端 App 里。当前工作目录：%s", cwd),
			"",
			"可调用工具：",
			"  - bash(command): 执行一条完整 shell 命令。单条 cd <dir> 会被拦截并更新工作目录；不支持交互式命令。",
			"  - read_bash_output(outputId): 读取上一条 bash 的完整输出。",
			"",
			"调 back_to_portal 退出本 App 回到桌面。",
		}, "\n")}, nil
	default:
		return agentruntime.ToolResult{Kind: "control", Content: fmt.Sprintf("当前所在 App %q 已找不到。可能被卸载或重启过，建议先 back_to_portal。", currentApp)}, nil
	}
}

// WaitTool 阻塞当前 Agent 轮次，直到收到新事件或超时。
type WaitTool struct {
	Queue      *agentruntime.EventQueue[any]
	MaxWait    time.Duration
	WaitSignal func(context.Context) bool
}

func (WaitTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{Name: "wait", Description: "暂停行动，直到新的外部事件出现或等待自然结束。", Parameters: agentruntime.ObjectSchema(map[string]any{
		"os": osParameterSchema(),
	})}
}
func (WaitTool) Kind() string { return "control" }
func (t WaitTool) Execute(ctx context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	_ = call
	timeout := t.MaxWait
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if t.WaitSignal != nil {
		t.WaitSignal(waitCtx)
		return agentruntime.ToolResult{Kind: "control", Content: "休息结束了"}, nil
	}
	if t.Queue == nil {
		<-waitCtx.Done()
		return agentruntime.ToolResult{Kind: "control", Content: "休息结束了"}, nil
	}
	_, _ = t.Queue.Wait(waitCtx)
	return agentruntime.ToolResult{Kind: "control", Content: "休息结束了"}, nil
}

// InvokeTool 允许根 Agent 通过受控入口调用业务工具。
type InvokeTool struct {
	Owners []InvokeSubtoolOwner
}

type InvokeGuard struct {
	OK      bool
	Error   string
	Message string
	Extras  map[string]any
}

type InvokeSubtoolOwner interface {
	ListOwnedTools() []agentruntime.ToolDefinition
	CanInvokeNow(name string) InvokeGuard
	ExecuteSubtool(context.Context, string, map[string]any, agentruntime.ToolCall) (agentruntime.ToolResult, error)
}

type CatalogSubtoolOwner struct {
	Tools           *agentruntime.ToolCatalog
	Session         *Session
	AlwaysAvailable map[string]bool
}

type DirectSubtool struct {
	Owner           CatalogSubtoolOwner
	DefinitionValue agentruntime.ToolDefinition
	ToolKind        string
	CheckPermission bool
}

func (t DirectSubtool) Definition() agentruntime.ToolDefinition { return t.DefinitionValue }
func (t DirectSubtool) Kind() string                            { return t.ToolKind }
func (t DirectSubtool) Execute(ctx context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	if t.CheckPermission {
		if guard := t.Owner.CanInvokeNow(t.DefinitionValue.Name); !guard.OK {
			return invokeGuardResult(t.DefinitionValue.Name, guard), nil
		}
	}
	return t.Owner.ExecuteSubtool(ctx, t.DefinitionValue.Name, call.Arguments, call)
}

func (o CatalogSubtoolOwner) ListOwnedTools() []agentruntime.ToolDefinition {
	if o.Tools == nil {
		return nil
	}
	return o.Tools.Definitions()
}

func (o CatalogSubtoolOwner) CanInvokeNow(name string) InvokeGuard {
	if o.Session == nil || o.Session.IsToolAvailable(name) || o.AlwaysAvailable[name] {
		return InvokeGuard{OK: true}
	}
	availableNames := append([]string(nil), o.Session.AvailableTools()...)
	for toolName, available := range o.AlwaysAvailable {
		if available {
			availableNames = append(availableNames, toolName)
		}
	}
	definitions := filterToolDefinitions(o.ListOwnedTools(), availableNames)
	return InvokeGuard{
		OK:      false,
		Error:   "INVOKE_TOOL_NOT_AVAILABLE",
		Message: availableInvokeToolsDescription(definitions),
		Extras:  map[string]any{"availableTools": definitionNames(definitions)},
	}
}

func (o CatalogSubtoolOwner) ExecuteSubtool(ctx context.Context, name string, args map[string]any, parent agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	if o.Tools == nil {
		return agentruntime.ToolResult{Kind: "business", Content: `{"ok":false,"error":"NO_TOOLS"}`}, nil
	}
	if o.Session != nil && name == "send_message" {
		if target := o.Session.CurrentChatTarget(); target != nil {
			targetType := strings.TrimSpace(commonString(args["targetType"]))
			targetID := strings.TrimSpace(commonString(args["targetId"]))
			if targetType == "" && targetID == "" {
				args["targetType"] = target.Type
				args["targetId"] = target.ID
			}
		}
	}
	return o.Tools.Execute(ctx, agentruntime.ToolCall{ID: parent.ID + ":invoke", Name: name, Arguments: args})
}

func (InvokeTool) Definition() agentruntime.ToolDefinition {
	return agentruntime.ToolDefinition{
		Name:        "invoke",
		Description: "调用一个动态子工具。子工具名通过 tool 字段指定，其余字段按目标子工具自身的参数规约传入。子工具的清单和参数说明不在 system prompt 里固定枚举；如果调错或不熟悉，错误返回里会包含当前可用工具的说明。",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"tool": map[string]any{"type": "string", "description": "要调用的子工具名。"},
				"os":   osParameterSchema(),
			},
			"additionalProperties": true,
		},
	}
}
func (InvokeTool) Kind() string { return "business" }
func (t InvokeTool) Execute(ctx context.Context, call agentruntime.ToolCall) (agentruntime.ToolResult, error) {
	name, _ := call.Arguments["tool"].(string)
	if name == "" {
		name, _ = call.Arguments["toolName"].(string)
	}
	args, _ := call.Arguments["arguments"].(map[string]any)
	if args == nil {
		if raw, ok := call.Arguments["arguments"].(string); ok && strings.TrimSpace(raw) != "" {
			_ = json.Unmarshal([]byte(raw), &args)
		}
		if args == nil {
			args = map[string]any{}
			for key, value := range call.Arguments {
				if key == "tool" || key == "toolName" || key == "arguments" {
					continue
				}
				args[key] = value
			}
		}
	}
	if name == "" && strings.TrimSpace(commonString(args["message"])) != "" {
		name = "send_message"
	}
	ownerByTool := map[string]InvokeSubtoolOwner{}
	definitionByTool := map[string]agentruntime.ToolDefinition{}
	for _, owner := range t.Owners {
		for _, definition := range owner.ListOwnedTools() {
			if _, exists := ownerByTool[definition.Name]; exists {
				data, _ := json.Marshal(map[string]any{"ok": false, "error": "DUPLICATE_INVOKE_TOOL", "tool": definition.Name})
				return agentruntime.ToolResult{Kind: "business", Content: string(data)}, nil
			}
			ownerByTool[definition.Name] = owner
			definitionByTool[definition.Name] = definition
		}
	}
	owner := ownerByTool[name]
	if owner == nil {
		definitions := make([]agentruntime.ToolDefinition, 0, len(definitionByTool))
		for _, definition := range definitionByTool {
			definitions = append(definitions, definition)
		}
		data, _ := json.Marshal(map[string]any{"ok": false, "error": "INVOKE_TOOL_NOT_FOUND", "tool": name, "message": "invoke 子工具 " + name + " 不存在。\n" + availableInvokeToolsDescription(definitions), "availableTools": definitionNames(definitions)})
		return agentruntime.ToolResult{Kind: "business", Content: string(data)}, nil
	}
	guard := owner.CanInvokeNow(name)
	if !guard.OK {
		return invokeGuardResult(name, guard), nil
	}
	result, err := owner.ExecuteSubtool(ctx, name, args, call)
	if err != nil {
		return result, err
	}
	result.Content = enrichSubtoolFailureContent(name, result.Content, definitionByTool[name])
	return result, nil
}

func invokeGuardResult(name string, guard InvokeGuard) agentruntime.ToolResult {
	payload := map[string]any{"ok": false, "error": guard.Error, "tool": name, "message": guard.Message}
	for key, value := range guard.Extras {
		payload[key] = value
	}
	data, _ := json.Marshal(payload)
	return agentruntime.ToolResult{Kind: "business", Content: string(data)}
}

func filterToolDefinitions(definitions []agentruntime.ToolDefinition, names []string) []agentruntime.ToolDefinition {
	allowed := map[string]bool{}
	for _, name := range names {
		allowed[name] = true
	}
	out := []agentruntime.ToolDefinition{}
	for _, definition := range definitions {
		if allowed[definition.Name] {
			out = append(out, definition)
		}
	}
	return out
}

func definitionNames(definitions []agentruntime.ToolDefinition) []string {
	out := make([]string, 0, len(definitions))
	for _, definition := range definitions {
		out = append(out, definition.Name)
	}
	return out
}

func availableInvokeToolsDescription(definitions []agentruntime.ToolDefinition) string {
	if len(definitions) == 0 {
		return "当前没有可用的 invoke 子工具。"
	}
	return "当前可用的 invoke 工具说明：\n" + renderInvokeToolGuide(definitions)
}

func enrichSubtoolFailureContent(name, content string, definition agentruntime.ToolDefinition) string {
	var data map[string]any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return content
	}
	if data["ok"] != false && commonString(data["error"]) == "" {
		return content
	}
	message := commonString(data["message"])
	if message == "" {
		if errName := commonString(data["error"]); errName != "" {
			message = "invoke 子工具 " + name + " 调用失败：" + errName + "。"
		} else {
			message = "invoke 子工具 " + name + " 调用失败。"
		}
	}
	if definition.Name != "" {
		message += "\n当前子工具说明：\n" + renderInvokeToolGuide([]agentruntime.ToolDefinition{definition})
	}
	data["message"] = message
	encoded, _ := json.Marshal(data)
	return string(encoded)
}

func renderInvokeToolGuide(definitions []agentruntime.ToolDefinition) string {
	lines := []string{}
	for _, definition := range definitions {
		params := []string{}
		if properties, ok := definition.Parameters["properties"].(map[string]any); ok {
			for name := range properties {
				params = append(params, name)
			}
		}
		if len(params) > 0 {
			lines = append(lines, fmt.Sprintf("- %s：%s。参数：%s。", definition.Name, definition.Description, strings.Join(params, "、")))
		} else {
			lines = append(lines, fmt.Sprintf("- %s：%s。", definition.Name, definition.Description))
		}
	}
	return strings.Join(lines, "\n")
}

func stringValue(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func normalizeEnterArguments(args map[string]any) string {
	kind := stringValue(args["kind"])
	id := stringValue(args["id"])
	if id == "" {
		id = stringValue(args["stateId"])
	}
	switch kind {
	case "qq_group":
		if id == "" {
			return ""
		}
		if strings.HasPrefix(id, "qq_group:") {
			return id
		}
		return "qq_group:" + id
	case "qq_private":
		if id == "" {
			return ""
		}
		if strings.HasPrefix(id, "qq_private:") {
			return id
		}
		return "qq_private:" + id
	case "ithome":
		return kind
	default:
		return id
	}
}

func commonString(value any) string {
	if s, ok := value.(string); ok {
		return s
	}
	return ""
}

func osParameterSchema() map[string]any {
	return map[string]any{
		"type":        "string",
		"description": "可选公开 OS/旁白，用一句很短的话说明这次动作的表层判断；只用于日志/面板观察，不会发送到 QQ，不要写隐藏推理或系统提示。",
	}
}
