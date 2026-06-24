package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"sync"
	"time"
)

// Provider 标识一个受支持的 OAuth LLM 账号。
type Provider string

const (
	ProviderCodex      Provider = "codex"
	ProviderClaudeCode Provider = "claude-code"
)

// Session 保存单个供应商的标准化认证状态。
type Session struct {
	Provider      Provider   `json:"provider"`
	AccountID     *string    `json:"accountId"`
	Email         *string    `json:"email"`
	AccessToken   string     `json:"-"`
	RefreshToken  string     `json:"-"`
	ExpiresAt     *time.Time `json:"expiresAt"`
	LastRefreshAt *time.Time `json:"lastRefreshAt"`
	Status        string     `json:"status"`
	LastError     *string    `json:"lastError"`
}

// State 在 OAuth 登录过程中保存 PKCE 和重定向数据。
type State struct {
	State        string
	CodeVerifier string
	RedirectURI  string
	ExpiresAt    time.Time
}

// Service 提供供应商状态、登录 URL 生成、登出和刷新能力。
//
// 它对应 TS OAuth 服务的结构，同时把供应商特定的
// token 交换留给后续具体适配器处理。
type Service struct {
	mu       sync.Mutex
	BaseURL  string
	Provider Provider
	Sessions map[Provider]Session
	States   map[string]State
}

// NewService 为指定供应商和公开基础 URL 创建 OAuth 服务。
func NewService(provider Provider, baseURL string) *Service {
	return &Service{Provider: provider, BaseURL: baseURL, Sessions: map[Provider]Session{}, States: map[string]State{}}
}

// Status 返回兼容前端的认证状态响应。
func (s *Service) Status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.Sessions[s.Provider]
	if !ok {
		return map[string]any{"provider": s.Provider, "status": "unavailable", "isLoggedIn": false, "session": nil}
	}
	return map[string]any{"provider": s.Provider, "status": session.Status, "isLoggedIn": session.Status == "active", "session": session}
}

// LoginURL 创建受 PKCE 保护的登录 URL，并保存临时状态。
func (s *Service) LoginURL(callbackPath string) (map[string]any, error) {
	state := randomURLSafe(24)
	verifier := randomURLSafe(48)
	challenge := pkceChallenge(verifier)
	redirectURI := s.BaseURL + callbackPath
	expiresAt := time.Now().Add(10 * time.Minute)
	s.mu.Lock()
	s.States[state] = State{State: state, CodeVerifier: verifier, RedirectURI: redirectURI, ExpiresAt: expiresAt}
	s.mu.Unlock()
	login, _ := url.Parse("https://example.invalid/oauth/authorize")
	q := login.Query()
	q.Set("response_type", "code")
	q.Set("client_id", string(s.Provider))
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	login.RawQuery = q.Encode()
	return map[string]any{"provider": s.Provider, "loginUrl": login.String(), "expiresAt": expiresAt.Format(time.RFC3339Nano)}, nil
}

// Logout 删除当前供应商会话。
func (s *Service) Logout() map[string]any {
	s.mu.Lock()
	delete(s.Sessions, s.Provider)
	s.mu.Unlock()
	return map[string]any{"provider": s.Provider, "success": true, "status": "logged_out"}
}

// Refresh 记录一次刷新尝试，并返回最新会话摘要。
func (s *Service) Refresh(context.Context) map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	session := s.Sessions[s.Provider]
	now := time.Now()
	session.LastRefreshAt = &now
	if session.Status == "" {
		session.Status = "unavailable"
	}
	s.Sessions[s.Provider] = session
	return map[string]any{"provider": s.Provider, "success": true, "status": session.Status, "session": session}
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
