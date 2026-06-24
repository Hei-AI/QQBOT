package auth

import (
	"QqBot/internal/common"
	"QqBot/internal/config"
	"QqBot/internal/db"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	codexAuthURL       = "https://auth.openai.com/oauth/authorize"
	codexTokenURL      = "https://auth.openai.com/oauth/token"
	codexClientID      = "app_EMoamEEZ73f0CkXaXp7hrann"
	claudeAuthURL      = "https://claude.ai/oauth/authorize"
	claudeTokenURL     = "https://platform.claude.com/v1/oauth/token"
	claudeCodeClientID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e"
)

//http://localhost:1455/success?id_token=
//eyJhbGciOiJSUzI1NiIsImtpZCI6ImIxZGQzZjhmLTlhYWQtNDdmZS1iMGU3LWVkYjAwOTc3N2Q2YiIsInR5cCI6IkpXVCJ9.eyJhdF9oYXNoIjoidzY5QzlMZnZVazM5UWhjNjF6QTJ5dyIsImF1ZCI6WyJhcHBfRU1vYW1FRVo3M2YwQ2tYYVhwN2hyYW5uIl0sImF1dGhfcHJvdmlkZXIiOiJnb29nbGUiLCJhdXRoX3RpbWUiOjE3NzgwNDMzMDMsImVtYWlsIjoiejQ2MTEwNTAzOUBnbWFpbC5jb20iLCJlbWFpbF92ZXJpZmllZCI6dHJ1ZSwiZXhwIjoxNzc4MDQ2OTA1LCJodHRwczovL2FwaS5vcGVuYWkuY29tL2F1dGgiOnsiY2hhdGdwdF9hY2NvdW50X2lkIjoiYTA3NjViZmUtMjE5Ny00ZDAyLWE0MWItNDUyOGVhNzI1OWRlIiwiY2hhdGdwdF9wbGFuX3R5cGUiOiJ0ZWFtIiwiY2hhdGdwdF9zdWJzY3JpcHRpb25fYWN0aXZlX3N0YXJ0IjoiMjAyNi0wNC0yMVQwNjowMzoxNyswMDowMCIsImNoYXRncHRfc3Vic2NyaXB0aW9uX2FjdGl2ZV91bnRpbCI6IjIwMjYtMDUtMjFUMDY6MDM6MTcrMDA6MDAiLCJjaGF0Z3B0X3N1YnNjcmlwdGlvbl9sYXN0X2NoZWNrZWQiOiIyMDI2LTA1LTA2VDA0OjU1OjAzLjQxOTExOSswMDowMCIsImNoYXRncHRfdXNlcl9pZCI6InVzZXItUzZ6RjdZSFNwWENaZVhsYnZZdnRhbzhlIiwiZ3JvdXBzIjpbXSwibG9jYWxob3N0Ijp0cnVlLCJvcmdhbml6YXRpb25zIjpbeyJpZCI6Im9yZy1HMnRNa2RWTktvQ2dnN1dqamZWdkhHRlYiLCJpc19kZWZhdWx0Ijp0cnVlLCJyb2xlIjoib3duZXIiLCJ0aXRsZSI6IlBlcnNvbmFsIn1dLCJ1c2VyX2lkIjoidXNlci1TNnpGN1lIU3BYQ1plWGxidll2dGFvOGUifSwiaWF0IjoxNzc4MDQzMzA1LCJpc3MiOiJodHRwczovL2F1dGgub3BlbmFpLmNvbSIsImp0aSI6ImMwNDM2ZmEyLTU4NWMtNDJkYS1hNjFhLWU1OGRmMmUyZjJhZCIsIm5hbWUiOiLmoqYiLCJyYXQiOjE3NzgwNDMyNzMsInNpZCI6ImJlYTYyZTBmLTY0ZWQtNGU4Ni04OWM5LTM0NzJjZWI0OTA4ZSIsInN1YiI6Imdvb2dsZS1vYXV0aDJ8MTAzNzA2MjU4MDE5OTAzNjUzMjM4In0.ugGZ3qiNISOJivyugBeWlbVNBEfW5j7kJxQ8yYQjYwfqpHeK5n21xukm9d9OlZlV_xDAvn9TaJ9JSjHQjngppPtHmoQI0dTAvyz_HZ71KyDnqbrBijERM0fvKApgt7_GF9DQAcrfBiv3wNzHgXCitNYWupNtewTGWZu5fZ-vDIwQMfr8KNNYkO8dptiJSyVZ_44xUK3iaKxJGIbScEyxtqfZnaQl_Ahz0iWCNhneL_9tUZBydAEcx4eqZzcFcHRN0MY4rXFKcKpNInzpwbkvB-C3cxwl-M3VvTsLB4KdZJ-fZ1cBgGC8f_AvAu9V5DAJy4cQb6fhEjlPASsGGm9qXHBNID7pt4FdbXGsI6-yCuZFGes8aqWU-5ukiEiF3TKDUlxy9S0tya2MiJX0nSXAKpRdNm6SQwiN2RmhNMCgO3iMNKGKUqmX3MoLrH8D8YefkpYROQkJ9L8J6zjoSnu2Afy-mKhKcx9jTa8dU8A11m5Q4mB4aM_sNWayFUs1AhhgR8-07nJ1rmKUklOTCPleE3zIX2i8S63qKjQF2Gp5bYqo4cmnBufoG94LAssZbYm-ZBSDmcbeSNWPfk_hnAxSdZcSMaZa7Atp4MSHlQLme28nHFVVV2BaW6SSRTIsBJDpNeNOF6WpUmHGiVY-oir_P04XR_oEQ-7PIxN92bKwlg4
//&needs_setup=false&org_id=&project_id=&plan_type=team&platform_url=https%3A%2F%2Fplatform.openai.com

// Runtime 保存 Codex 和 Claude Code 的 OAuth 状态与会话。
type Runtime struct {
	Config *config.Config
	Store  *db.Store
	Client *http.Client
	States map[string]State
	mu     sync.Mutex
	usage  map[string]map[string]any
}

// NewRuntime 创建由应用 Store 支撑的 OAuth 运行时。
func NewRuntime(cfg *config.Config, store *db.Store) *Runtime {
	return &Runtime{Config: cfg, Store: store, Client: &http.Client{Timeout: time.Duration(cfg.Server.LLM.TimeoutMs) * time.Millisecond}, States: map[string]State{}, usage: map[string]map[string]any{}}
}

// Status 返回当前供应商认证状态。
func (r *Runtime) Status(provider string) map[string]any {
	session, ok := r.Store.OAuthSession(provider)
	if !ok || session.AccessToken == "" {
		return map[string]any{"provider": provider, "status": "unavailable", "isLoggedIn": false, "session": nil}
	}
	status := session.Status
	if status == "" {
		status = "active"
	}
	return map[string]any{"provider": provider, "status": status, "isLoggedIn": status == "active", "session": publicSession(session)}
}

// LoginURL 创建供应商专属的 PKCE 授权 URL。
func (r *Runtime) LoginURL(provider string) (map[string]any, error) {
	state := randomURLSafe(24)
	verifier := randomURLSafe(48)
	challenge := oauthPKCEChallenge(verifier)
	redirectURI := r.redirectURI(provider)
	expiresAt := time.Now().Add(10 * time.Minute)
	r.States[state] = State{State: state, CodeVerifier: verifier, RedirectURI: redirectURI, ExpiresAt: expiresAt}
	authURL := ""
	switch provider {
	case "codex":
		authURL = buildCodexAuthorizeURL(redirectURI, state, challenge)
	case "claude-code":
		authURL = buildClaudeAuthorizeURL(redirectURI, state, challenge)
	default:
		return nil, fmt.Errorf("unsupported provider")
	}
	return map[string]any{"provider": provider, "loginUrl": authURL, "expiresAt": expiresAt.Format(time.RFC3339Nano)}, nil
}

// Callback 使用 OAuth code 换取 token，并保存生成的会话。
func (r *Runtime) Callback(ctx context.Context, provider, state, code string) (map[string]any, error) {
	saved, ok := r.States[state]
	if !ok || time.Now().After(saved.ExpiresAt) {
		return nil, fmt.Errorf("oauth state expired or not found")
	}
	delete(r.States, state)
	var session db.OAuthSession
	var err error
	switch provider {
	case "codex", "openai-codex":
		session, err = r.exchangeCodex(ctx, code, saved.CodeVerifier, saved.RedirectURI)
	case "claude-code":
		session, err = r.exchangeClaude(ctx, code, state, saved.CodeVerifier, saved.RedirectURI)
	default:
		err = fmt.Errorf("unsupported provider")
	}
	if err != nil {
		return nil, err
	}
	r.Store.UpsertOAuthSession(session)
	return map[string]any{"provider": provider, "status": session.Status, "session": publicSession(session)}, nil
}

// Refresh 在存在 refresh token 时刷新已保存的 token。
func (r *Runtime) Refresh(ctx context.Context, provider string) (map[string]any, error) {
	session, ok := r.Store.OAuthSession(provider)
	if !ok || session.RefreshToken == "" {
		return map[string]any{"provider": provider, "success": true, "status": "unavailable", "session": nil}, nil
	}
	var refreshed db.OAuthSession
	var err error
	switch provider {
	case "codex", "openai-codex":
		refreshed, err = r.refreshCodex(ctx, session.RefreshToken)
	case "claude-code":
		refreshed, err = r.refreshClaude(ctx, session.RefreshToken)
	default:
		err = fmt.Errorf("unsupported provider")
	}
	if err != nil {
		msg := err.Error()
		session.Status = "refresh_failed"
		session.LastError = &msg
		r.Store.UpsertOAuthSession(session)
		return nil, err
	}
	r.Store.UpsertOAuthSession(refreshed)
	return map[string]any{"provider": provider, "success": true, "status": refreshed.Status, "session": publicSession(refreshed)}, nil
}

// Logout 删除指定供应商的凭据。
func (r *Runtime) Logout(provider string) map[string]any {
	r.Store.DeleteOAuthSession(provider)
	return map[string]any{"provider": provider, "success": true, "status": "logged_out"}
}

func (r *Runtime) RefreshIfNeeded(ctx context.Context, provider string) (bool, error) {
	session, ok := r.Store.OAuthSession(provider)
	if !ok || session.RefreshToken == "" || session.ExpiresAt == nil {
		return false, nil
	}
	leeway := r.refreshLeeway(provider)
	if time.Until(*session.ExpiresAt) > leeway && session.Status != "expired" {
		return false, nil
	}
	_, err := r.Refresh(ctx, provider)
	return true, err
}

func (r *Runtime) UsageLimits(provider string) map[string]any {
	internal := provider
	if provider == "codex" {
		internal = "openai-codex"
	}
	r.mu.Lock()
	if cached := r.usage[internal]; cached != nil {
		out := commonMap(cached)
		r.mu.Unlock()
		return out
	}
	r.mu.Unlock()
	session, ok := r.Store.OAuthSession(internal)
	if !ok || session.AccountID == nil {
		return emptyUsage(provider)
	}
	limits := r.usageFromSnapshots(internal, *session.AccountID)
	if limits != nil {
		return map[string]any{"provider": providerAlias(internal), "limits": limits}
	}
	return emptyUsage(provider)
}

func (r *Runtime) RefreshUsage(ctx context.Context) {
	r.refreshClaudeUsage(ctx)
	r.refreshCodexUsage(ctx)
}

func (r *Runtime) UsageTrend(provider, rangeValue string) map[string]any {
	internal := provider
	if provider == "codex" {
		internal = "openai-codex"
	}
	if rangeValue == "" {
		rangeValue = "24h"
	}
	session, ok := r.Store.OAuthSession(internal)
	if !ok || session.AccountID == nil {
		return emptyTrend(rangeValue)
	}
	since := time.Now().Add(-24 * time.Hour)
	if rangeValue == "7d" {
		since = time.Now().Add(-7 * 24 * time.Hour)
	}
	if rangeValue == "30d" {
		since = time.Now().Add(-30 * 24 * time.Hour)
	}
	items := r.Store.AuthUsageInRange(internal, *session.AccountID, since)
	return trendResponse(rangeValue, items)
}

func (r *Runtime) refreshClaudeUsage(ctx context.Context) {
	session, ok := r.Store.OAuthSession("claude-code")
	if !ok || session.AccessToken == "" || session.AccountID == nil || session.Status != "active" {
		r.setUsage("claude-code", emptyUsage("claude-code"))
		return
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", nil)
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	req.Header.Set("authorization", "Bearer "+session.AccessToken)
	req.Header.Set("user-agent", "claude-code/2.1.39")
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	res, err := r.Client.Do(req)
	if err != nil {
		r.Store.Log("warn", "Claude Code usage refresh failed", map[string]any{"event": "auth_usage.claude_code_refresh_failed", "error": err.Error()})
		return
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		r.Store.Log("warn", "Claude Code usage refresh failed", map[string]any{"event": "auth_usage.claude_code_refresh_failed", "status": res.Status})
		return
	}
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	limits := map[string]any{
		"five_hour":   normalizeClaudeWindow(parsed["five_hour"]),
		"seven_day":   normalizeClaudeWindow(parsed["seven_day"]),
		"extra_usage": normalizeClaudeExtraUsage(parsed["extra_usage"]),
	}
	r.Store.AddAuthUsageSnapshots(claudeSnapshots(*session.AccountID, limits, time.Now()))
	r.setUsage("claude-code", map[string]any{"provider": "claude-code", "limits": limits})
}

func (r *Runtime) refreshCodexUsage(ctx context.Context) {
	session, ok := r.Store.OAuthSession("openai-codex")
	if !ok || session.AccessToken == "" || session.RefreshToken == "" || session.AccountID == nil || session.Status != "active" {
		r.setUsage("openai-codex", emptyUsage("codex"))
		return
	}
	limits, err := r.fetchCodexUsageLimits(ctx, session)
	if err != nil {
		r.Store.Log("warn", "Codex usage refresh failed", map[string]any{"event": "auth_usage.codex_refresh_failed", "error": err.Error()})
		if cached := r.usageFromSnapshots("openai-codex", *session.AccountID); cached != nil {
			r.setUsage("openai-codex", map[string]any{"provider": "codex", "limits": cached})
		}
		return
	}
	r.Store.AddAuthUsageSnapshots(codexSnapshots(*session.AccountID, limits, time.Now()))
	r.setUsage("openai-codex", map[string]any{"provider": "codex", "limits": limits})
}

func (r *Runtime) fetchCodexUsageLimits(ctx context.Context, session db.OAuthSession) (map[string]any, error) {
	binaryPath := strings.TrimSpace(r.Config.Server.LLM.CodexAuth.BinaryPath)
	if binaryPath == "" {
		binaryPath = "codex"
	}
	timeout := 15 * time.Second
	if r.Config.Server.LLM.TimeoutMs > 0 {
		timeout = time.Duration(r.Config.Server.LLM.TimeoutMs) * time.Millisecond
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	codexHome, err := os.MkdirTemp("", "kagami-codex-home-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(codexHome)

	authFile := map[string]any{
		"auth_mode":      "chatgpt",
		"OPENAI_API_KEY": nil,
		"tokens": map[string]any{
			"id_token":      emptyToNil(session.IDToken),
			"access_token":  session.AccessToken,
			"refresh_token": session.RefreshToken,
			"account_id":    stringPtrAny(session.AccountID),
		},
		"last_refresh": session.LastRefreshAt,
	}
	rawAuth, _ := json.MarshalIndent(authFile, "", "  ")
	if err := os.WriteFile(filepath.Join(codexHome, "auth.json"), rawAuth, 0o600); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(runCtx, binaryPath, "app-server")
	cmd.Env = append(os.Environ(), "CODEX_HOME="+codexHome)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
	}()

	writeJSONLine := func(id int, method string, params map[string]any) error {
		raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		_, err := stdin.Write(append(raw, '\n'))
		return err
	}
	if err := writeJSONLine(1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "kagami", "version": "0.0.0"}}); err != nil {
		return nil, err
	}

	errCh := make(chan string, 1)
	go func() {
		raw, _ := io.ReadAll(io.LimitReader(stderr, 1<<20))
		errCh <- strings.TrimSpace(string(raw))
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var message struct {
			ID     int            `json:"id"`
			Result map[string]any `json:"result"`
			Error  map[string]any `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			continue
		}
		if message.ID == 1 {
			if message.Error != nil {
				return nil, fmt.Errorf("codex app-server initialize failed: %s", common.AsString(message.Error["message"]))
			}
			if err := writeJSONLine(2, "account/rateLimits/read", map[string]any{}); err != nil {
				return nil, err
			}
			continue
		}
		if message.ID == 2 {
			if message.Error != nil {
				return nil, fmt.Errorf("codex app-server rate limits failed: %s", common.AsString(message.Error["message"]))
			}
			rateLimits, _ := message.Result["rateLimits"].(map[string]any)
			if rateLimits == nil {
				return nil, fmt.Errorf("codex app-server returned no rate limits")
			}
			return map[string]any{"primary": optionalAny(rateLimits["primary"]), "secondary": optionalAny(rateLimits["secondary"])}, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	select {
	case stderrText := <-errCh:
		if stderrText != "" {
			return nil, fmt.Errorf("codex app-server exited before responding: %s", stderrText)
		}
	default:
	}
	if runCtx.Err() != nil {
		return nil, runCtx.Err()
	}
	return nil, fmt.Errorf("codex app-server exited before responding")
}

func (r *Runtime) setUsage(provider string, value map[string]any) {
	r.mu.Lock()
	r.usage[provider] = value
	r.mu.Unlock()
}

func (r *Runtime) usageFromSnapshots(provider, accountID string) map[string]any {
	items := r.Store.LatestAuthUsage(provider, accountID)
	if len(items) == 0 {
		return nil
	}
	if provider == "openai-codex" {
		limits := map[string]any{"primary": nil, "secondary": nil}
		for _, item := range items {
			window := map[string]any{"usedPercent": 100 - item.RemainingPercent, "resetsAt": item.ResetAt, "windowDurationMins": 0}
			if item.WindowKey == "five_hour" {
				window["windowDurationMins"] = 300
				limits["primary"] = window
			}
			if item.WindowKey == "seven_day" {
				window["windowDurationMins"] = 10080
				limits["secondary"] = window
			}
		}
		return limits
	}
	limits := map[string]any{"five_hour": nil, "seven_day": nil, "extra_usage": nil}
	for _, item := range items {
		window := map[string]any{"utilization": 100 - item.RemainingPercent, "resets_at": item.ResetAt}
		if item.WindowKey == "five_hour" || item.WindowKey == "seven_day" {
			limits[item.WindowKey] = window
		}
	}
	return limits
}

func (r *Runtime) refreshLeeway(provider string) time.Duration {
	if provider == "openai-codex" || provider == "codex" {
		return time.Duration(r.Config.Server.LLM.CodexAuth.RefreshLeewayMs) * time.Millisecond
	}
	return time.Duration(r.Config.Server.LLM.ClaudeCodeAuth.RefreshLeewayMs) * time.Millisecond
}

func (r *Runtime) redirectURI(provider string) string {
	if provider == "codex" {
		return strings.TrimRight(r.Config.Server.LLM.CodexAuth.PublicBaseURL, "/") + r.Config.Server.LLM.CodexAuth.OAuthRedirectPath
	}
	return strings.TrimRight(r.Config.Server.LLM.ClaudeCodeAuth.PublicBaseURL, "/") + r.Config.Server.LLM.ClaudeCodeAuth.OAuthRedirectPath
}

func buildCodexAuthorizeURL(redirectURI, state, challenge string) string {
	q := url.Values{
		"response_type":              {"code"},
		"client_id":                  {codexClientID},
		"redirect_uri":               {redirectURI},
		"scope":                      {"openid profile email offline_access api.connectors.read api.connectors.invoke"},
		"state":                      {state},
		"code_challenge":             {challenge},
		"code_challenge_method":      {"S256"},
		"id_token_add_organizations": {"true"},
		"codex_cli_simplified_flow":  {"true"},
		//"originator":                 {"Codex Desktop"},
	}
	return codexAuthURL + "?" + q.Encode()
}

func buildClaudeAuthorizeURL(redirectURI, state, challenge string) string {
	q := url.Values{"code": {"true"}, "client_id": {claudeCodeClientID}, "response_type": {"code"}, "redirect_uri": {redirectURI}, "scope": {"org:create_api_key user:profile user:inference"}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}}
	return claudeAuthURL + "?" + q.Encode()
}

func (r *Runtime) exchangeCodex(ctx context.Context, code, verifier, redirectURI string) (db.OAuthSession, error) {
	body := url.Values{"grant_type": {"authorization_code"}, "client_id": {codexClientID}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}
	token, err := r.requestForm(ctx, codexTokenURL, body)
	if err != nil {
		return db.OAuthSession{}, err
	}
	return codexTokenToSession(token), nil
}

func (r *Runtime) refreshCodex(ctx context.Context, refreshToken string) (db.OAuthSession, error) {
	body := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}, "client_id": {codexClientID}, "scope": {"openid profile email"}}
	token, err := r.requestForm(ctx, codexTokenURL, body)
	if err != nil {
		return db.OAuthSession{}, err
	}
	return codexTokenToSession(token), nil
}

func (r *Runtime) exchangeClaude(ctx context.Context, code, state, verifier, redirectURI string) (db.OAuthSession, error) {
	parsedCode, parsedState := parseClaudeCodeAndState(code)
	if parsedState == "" {
		parsedState = state
	}
	payload := map[string]string{"code": parsedCode, "state": parsedState, "grant_type": "authorization_code", "client_id": claudeCodeClientID, "redirect_uri": redirectURI, "code_verifier": verifier}
	token, err := r.requestJSON(ctx, claudeTokenURL, payload)
	if err != nil {
		return db.OAuthSession{}, err
	}
	return claudeTokenToSession(token), nil
}

func (r *Runtime) refreshClaude(ctx context.Context, refreshToken string) (db.OAuthSession, error) {
	token, err := r.requestJSON(ctx, claudeTokenURL, map[string]string{"client_id": claudeCodeClientID, "grant_type": "refresh_token", "refresh_token": refreshToken})
	if err != nil {
		return db.OAuthSession{}, err
	}
	return claudeTokenToSession(token), nil
}

func (r *Runtime) requestForm(ctx context.Context, endpoint string, body url.Values) (map[string]any, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(body.Encode()))
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/x-www-form-urlencoded")
	return r.doTokenRequest(req)
}

func (r *Runtime) requestJSON(ctx context.Context, endpoint string, payload any) (map[string]any, error) {
	raw, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(raw))
	req.Header.Set("accept", "application/json")
	req.Header.Set("content-type", "application/json")
	return r.doTokenRequest(req)
}

func (r *Runtime) doTokenRequest(req *http.Request) (map[string]any, error) {
	res, err := r.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
	var parsed map[string]any
	_ = json.Unmarshal(raw, &parsed)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, fmt.Errorf("oauth token request failed: %s", res.Status)
	}
	if parsed["access_token"] == nil || parsed["refresh_token"] == nil {
		return nil, fmt.Errorf("oauth token response is invalid")
	}
	return parsed, nil
}

func codexTokenToSession(token map[string]any) db.OAuthSession {
	expires := expiresAt(token)
	claims := parseJWTClaims(stringValue(token["id_token"]))
	accountID := optionalString("")
	email := optionalString("")
	if auth, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		accountID = optionalString(stringValue(auth["chatgpt_account_id"]))
	}
	email = optionalString(stringValue(claims["email"]))
	now := time.Now()
	return db.OAuthSession{Provider: "openai-codex", AccountID: accountID, Email: email, AccessToken: stringValue(token["access_token"]), RefreshToken: stringValue(token["refresh_token"]), IDToken: stringValue(token["id_token"]), ExpiresAt: expires, LastRefreshAt: &now, Status: "active"}
}

func claudeTokenToSession(token map[string]any) db.OAuthSession {
	expires := expiresAt(token)
	account, _ := token["account"].(map[string]any)
	now := time.Now()
	return db.OAuthSession{Provider: "claude-code", AccountID: optionalString(stringValue(account["uuid"])), Email: optionalString(stringValue(account["email_address"])), AccessToken: stringValue(token["access_token"]), RefreshToken: stringValue(token["refresh_token"]), ExpiresAt: expires, LastRefreshAt: &now, Status: "active"}
}

func expiresAt(token map[string]any) *time.Time {
	seconds := 0.0
	if v, ok := token["expires_in"].(float64); ok {
		seconds = v
	}
	t := time.Now().Add(time.Duration(seconds) * time.Second)
	return &t
}

func publicSession(session db.OAuthSession) map[string]any {
	return map[string]any{"provider": providerAlias(session.Provider), "accountId": session.AccountID, "email": session.Email, "expiresAt": session.ExpiresAt, "lastRefreshAt": session.LastRefreshAt, "lastError": session.LastError}
}

func providerAlias(provider string) string {
	if provider == "openai-codex" {
		return "codex"
	}
	return provider
}

func parseJWTClaims(token string) map[string]any {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return map[string]any{}
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func parseClaudeCodeAndState(code string) (string, string) {
	parts := strings.SplitN(code, "#", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return code, ""
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func optionalString(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

func oauthPKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func emptyUsage(provider string) map[string]any {
	if provider == "codex" || provider == "openai-codex" {
		return map[string]any{"provider": "codex", "limits": map[string]any{"primary": nil, "secondary": nil}}
	}
	return map[string]any{"provider": provider, "limits": map[string]any{"five_hour": nil, "seven_day": nil, "extra_usage": nil}}
}

func commonMap(input map[string]any) map[string]any {
	out := map[string]any{}
	for key, value := range input {
		out[key] = value
	}
	return out
}

func normalizeClaudeWindow(value any) any {
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return map[string]any{"utilization": numberValue(m["utilization"]), "resets_at": optionalAny(m["resets_at"])}
}

func normalizeClaudeExtraUsage(value any) any {
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return map[string]any{
		"is_enabled":    m["is_enabled"],
		"monthly_limit": optionalAny(m["monthly_limit"]),
		"used_credits":  optionalAny(m["used_credits"]),
		"utilization":   optionalAny(m["utilization"]),
	}
}

func claudeSnapshots(accountID string, limits map[string]any, capturedAt time.Time) []db.AuthUsageSnapshot {
	items := []db.AuthUsageSnapshot{}
	for _, key := range []string{"five_hour", "seven_day"} {
		window, ok := limits[key].(map[string]any)
		if !ok {
			continue
		}
		resetAt := optionalTime(window["resets_at"])
		items = append(items, db.AuthUsageSnapshot{
			Provider:         "claude-code",
			AccountID:        accountID,
			WindowKey:        key,
			RemainingPercent: clampPercent(100 - numberValue(window["utilization"])),
			ResetAt:          resetAt,
			CapturedAt:       capturedAt,
		})
	}
	return items
}

func codexSnapshots(accountID string, limits map[string]any, capturedAt time.Time) []db.AuthUsageSnapshot {
	items := []db.AuthUsageSnapshot{}
	for _, key := range []string{"primary", "secondary"} {
		window, ok := limits[key].(map[string]any)
		if !ok {
			continue
		}
		windowKey := codexWindowKey(numberValue(window["windowDurationMins"]))
		if windowKey == "" {
			continue
		}
		items = append(items, db.AuthUsageSnapshot{
			Provider:         "openai-codex",
			AccountID:        accountID,
			WindowKey:        windowKey,
			RemainingPercent: clampPercent(100 - numberValue(window["usedPercent"])),
			ResetAt:          optionalTime(window["resetsAt"]),
			CapturedAt:       capturedAt,
		})
	}
	return items
}

func codexWindowKey(windowDurationMins float64) string {
	switch int(windowDurationMins) {
	case 300:
		return "five_hour"
	case 10080:
		return "seven_day"
	default:
		return ""
	}
}

func optionalTime(value any) *time.Time {
	switch x := value.(type) {
	case string:
		t, err := time.Parse(time.RFC3339, x)
		if err == nil {
			return &t
		}
	case *time.Time:
		return x
	case time.Time:
		return &x
	case float64:
		t := unixMillisOrSeconds(x)
		return &t
	case int:
		t := unixMillisOrSeconds(float64(x))
		return &t
	case int64:
		t := unixMillisOrSeconds(float64(x))
		return &t
	}
	return nil
}

func unixMillisOrSeconds(value float64) time.Time {
	if value < 10000000000 {
		return time.Unix(int64(value), 0)
	}
	return time.UnixMilli(int64(value))
}

func optionalAny(value any) any {
	if value == nil {
		return nil
	}
	return value
}

func emptyToNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func stringPtrAny(value *string) any {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return *value
}

func numberValue(value any) float64 {
	switch x := value.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	case json.Number:
		v, _ := x.Float64()
		return v
	default:
		return 0
	}
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func emptyTrend(rangeValue string) map[string]any {
	return map[string]any{
		"range": rangeValue,
		"series": []any{
			map[string]any{"windowKey": "five_hour", "label": "5 小时", "points": []any{}},
			map[string]any{"windowKey": "seven_day", "label": "7 天", "points": []any{}},
		},
	}
}

func trendResponse(rangeValue string, items []db.AuthUsageSnapshot) map[string]any {
	byWindow := map[string][]map[string]any{"five_hour": {}, "seven_day": {}}
	for _, item := range items {
		if _, ok := byWindow[item.WindowKey]; !ok {
			continue
		}
		byWindow[item.WindowKey] = append(byWindow[item.WindowKey], map[string]any{"capturedAt": common.ISO(item.CapturedAt), "remainingPercent": item.RemainingPercent})
	}
	if rangeValue == "7d" {
		byWindow["five_hour"] = downsampleHourly(byWindow["five_hour"])
		byWindow["seven_day"] = downsampleHourly(byWindow["seven_day"])
	}
	return map[string]any{
		"range": rangeValue,
		"series": []any{
			map[string]any{"windowKey": "five_hour", "label": "5 小时", "points": byWindow["five_hour"]},
			map[string]any{"windowKey": "seven_day", "label": "7 天", "points": byWindow["seven_day"]},
		},
	}
}

func downsampleHourly(points []map[string]any) []map[string]any {
	buckets := map[int64]map[string]any{}
	keys := []int64{}
	seen := map[int64]bool{}
	for _, point := range points {
		raw, _ := point["capturedAt"].(string)
		t, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			continue
		}
		key := t.Unix() / 3600
		buckets[key] = point
		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, buckets[key])
	}
	return out
}
