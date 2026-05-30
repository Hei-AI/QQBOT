package root

import "sync"

// ChatTarget 标识当前正在处理的群聊或私聊目标。
type ChatTarget struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// Session 保存根 Agent 的导航状态和当前聚焦的聊天目标。
type Session struct {
	mu       sync.Mutex
	Target   *ChatTarget
	StateID  string
	Stack    []string
	Children []string
}

// NewSession 创建一个聚焦在 portal 状态的根会话。
func NewSession() *Session {
	return &Session{StateID: "portal", Stack: []string{"portal"}}
}

// SetTarget 更新消息工具使用的当前聊天目的地。
func (s *Session) SetTarget(target ChatTarget) {
	s.mu.Lock()
	s.Target = &target
	s.mu.Unlock()
}

// Enter 将子状态压入导航栈。
func (s *Session) Enter(stateID string) {
	s.mu.Lock()
	s.StateID = stateID
	s.Stack = append(s.Stack, stateID)
	s.mu.Unlock()
}

// Portal 将导航重置回根 portal 状态。
func (s *Session) Portal() {
	s.mu.Lock()
	s.StateID = "portal"
	s.Stack = []string{"portal"}
	s.mu.Unlock()
}

// Snapshot 返回适合仪表盘展示的会话结构。
func (s *Session) Snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	stack := []map[string]string{}
	for _, item := range s.Stack {
		stack = append(stack, map[string]string{"id": item, "displayName": item})
	}
	return map[string]any{
		"focusedStateId":          s.StateID,
		"focusedStateDisplayName": s.StateID,
		"focusedStateDescription": "",
		"stateStack":              stack,
		"children":                []any{},
		"availableInvokeTools":    []string{},
	}
}
