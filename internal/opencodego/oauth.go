package opencodego

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	ConsoleServer = "https://opencode.ai/console"
	ClientID      = "opencode-cli"
	Provider      = "opencode-go"
)

type DeviceCode struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
}

type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Session struct {
	AccessToken  string
	RefreshToken string
	// BrowserCookie is the authenticated OpenCode web session copied from a browser.
	// It is used for browser-only Console pages such as workspace keys and referrals.
	BrowserCookie string
	ExpiresAt     time.Time
	Server        string
	AccountID     string
	Email         string
	WorkspaceID   string
	WorkspaceName string
	DefaultAPIKey string
}

// FetchInvites reads the Console invite records available to the OAuth session.
// It intentionally returns raw records because the endpoint is not the referral
// reward summary action and its schema is not part of the public Go API contract.
func FetchInvites(ctx context.Context, client *http.Client, session Session) ([]map[string]any, error) {
	if strings.TrimSpace(session.AccessToken) == "" {
		return nil, fmt.Errorf("opencode console: access token is empty")
	}
	headers := make(http.Header)
	if strings.TrimSpace(session.WorkspaceID) != "" {
		headers.Set("x-org-id", strings.TrimSpace(session.WorkspaceID))
	}
	var records []map[string]any
	_, err := requestJSON(ctx, client, http.MethodGet, normalizeConsoleServer(session.Server)+"/api/invites", nil, session.AccessToken, headers, &records)
	return records, err
}

type LoginPrompt func(string) (string, error)

func requestJSON(ctx context.Context, client *http.Client, method, endpoint string, body any, bearer string, headers http.Header, out any) (int, error) {
	if client == nil {
		client = http.DefaultClient
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if strings.TrimSpace(bearer) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(bearer))
	}
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return resp.StatusCode, fmt.Errorf("opencode console: invalid JSON: %w", err)
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("opencode console: %s returned %d", endpoint, resp.StatusCode)
	}
	return resp.StatusCode, nil
}

func StartDeviceLogin(ctx context.Context, client *http.Client, server string) (DeviceCode, error) {
	server = normalizeConsoleServer(server)
	var code DeviceCode
	_, err := requestJSON(ctx, client, http.MethodPost, server+"/auth/device/code", map[string]string{"client_id": ClientID}, "", nil, &code)
	if err != nil {
		return DeviceCode{}, err
	}
	if strings.TrimSpace(code.DeviceCode) == "" || strings.TrimSpace(code.VerificationURIComplete) == "" {
		return DeviceCode{}, fmt.Errorf("opencode console: device response is incomplete")
	}
	if code.Interval <= 0 {
		code.Interval = 5
	}
	code.VerificationURIComplete = resolveConsoleURL(server, code.VerificationURIComplete)
	return code, nil
}

func PollDeviceLogin(ctx context.Context, client *http.Client, server string, code DeviceCode) (Session, error) {
	server = normalizeConsoleServer(server)
	deadline := time.Now().Add(time.Duration(code.ExpiresIn) * time.Second)
	if code.ExpiresIn <= 0 {
		deadline = time.Now().Add(10 * time.Minute)
	}
	interval := time.Duration(code.Interval) * time.Second
	for {
		if err := ctx.Err(); err != nil {
			return Session{}, err
		}
		if time.Now().After(deadline) {
			return Session{}, fmt.Errorf("opencode console: device authorization expired")
		}
		select {
		case <-ctx.Done():
			return Session{}, ctx.Err()
		case <-time.After(interval):
		}
		var token tokenResponse
		status, err := requestJSON(ctx, client, http.MethodPost, server+"/auth/device/token", map[string]string{
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
			"device_code": code.DeviceCode,
			"client_id":   ClientID,
		}, "", nil, &token)
		if err != nil && status != http.StatusBadRequest {
			return Session{}, err
		}
		if token.Error == "authorization_pending" {
			continue
		}
		if token.Error == "slow_down" {
			interval += 5 * time.Second
			continue
		}
		if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" {
			if err != nil {
				return Session{}, err
			}
			return Session{}, fmt.Errorf("opencode console: device token response is incomplete")
		}
		return completeSession(ctx, client, server, token)
	}
}

func RefreshSession(ctx context.Context, client *http.Client, session Session) (Session, error) {
	server := normalizeConsoleServer(session.Server)
	var token tokenResponse
	_, err := requestJSON(ctx, client, http.MethodPost, server+"/auth/device/token", map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": session.RefreshToken,
		"client_id":     ClientID,
	}, "", nil, &token)
	if err != nil {
		return Session{}, err
	}
	if strings.TrimSpace(token.AccessToken) == "" || strings.TrimSpace(token.RefreshToken) == "" {
		return Session{}, fmt.Errorf("opencode console: refresh response is incomplete")
	}
	refreshed, err := completeSession(ctx, client, server, token)
	if err != nil {
		return Session{}, err
	}
	if refreshed.WorkspaceID == "" {
		refreshed.WorkspaceID = session.WorkspaceID
		refreshed.WorkspaceName = session.WorkspaceName
	}
	return refreshed, nil
}

func completeSession(ctx context.Context, client *http.Client, server string, token tokenResponse) (Session, error) {
	var user User
	if _, err := requestJSON(ctx, client, http.MethodGet, server+"/api/user", nil, token.AccessToken, nil, &user); err != nil {
		return Session{}, err
	}
	var orgs []Organization
	if _, err := requestJSON(ctx, client, http.MethodGet, server+"/api/orgs", nil, token.AccessToken, nil, &orgs); err != nil {
		return Session{}, err
	}
	if len(orgs) == 0 {
		return Session{}, fmt.Errorf("opencode console: no workspace is available")
	}
	return Session{
		AccessToken: token.AccessToken, RefreshToken: token.RefreshToken,
		ExpiresAt: time.Now().Add(time.Duration(token.ExpiresIn) * time.Second).UTC(),
		Server:    server, AccountID: user.ID, Email: user.Email,
		WorkspaceID: orgs[0].ID, WorkspaceName: orgs[0].Name,
	}, nil
}

func ListOrganizations(ctx context.Context, client *http.Client, session Session) ([]Organization, error) {
	var orgs []Organization
	_, err := requestJSON(ctx, client, http.MethodGet, normalizeConsoleServer(session.Server)+"/api/orgs", nil, session.AccessToken, nil, &orgs)
	return orgs, err
}

// SelectWorkspace applies the requested workspace, or asks when OAuth returns
// multiple workspaces and no explicit selection was supplied.
func SelectWorkspace(ctx context.Context, client *http.Client, session Session, requested string, prompt LoginPrompt) (Session, error) {
	orgs, err := ListOrganizations(ctx, client, session)
	if err != nil {
		return Session{}, err
	}
	requested = strings.TrimSpace(requested)
	if requested == "" && len(orgs) > 1 {
		if prompt == nil {
			return Session{}, fmt.Errorf("opencode console: multiple workspaces found; workspace_id is required")
		}
		choices := make([]string, 0, len(orgs))
		for _, org := range orgs {
			choices = append(choices, org.ID+" ("+org.Name+")")
		}
		requested, err = prompt("选择 OpenCode workspace: " + strings.Join(choices, ", "))
		if err != nil {
			return Session{}, err
		}
		requested = strings.TrimSpace(requested)
	}
	for _, org := range orgs {
		if requested == "" || requested == org.ID {
			session.WorkspaceID = org.ID
			session.WorkspaceName = org.Name
			return session, nil
		}
	}
	return Session{}, fmt.Errorf("opencode console: workspace %s was not found", requested)
}

// FetchDefaultAPIKey reads the existing key from the current console provider configuration.
// It returns an empty string when the console omits it; OAuth access tokens are never API keys.
func FetchDefaultAPIKey(ctx context.Context, client *http.Client, session Session) (string, error) {
	server := normalizeConsoleServer(session.Server)
	headers := make(http.Header)
	if strings.TrimSpace(session.WorkspaceID) != "" {
		headers.Set("x-org-id", strings.TrimSpace(session.WorkspaceID))
	}
	var response map[string]any
	_, err := requestJSON(ctx, client, http.MethodGet, server+"/api/config", nil, session.AccessToken, headers, &response)
	if err != nil {
		return "", err
	}
	if key := findDefaultAPIKey(response); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("opencode console: /api/config exposes no concrete default sk- API key; workspace keys page requires a browser session")
}

func findDefaultAPIKey(value any) string {
	switch item := value.(type) {
	case map[string]any:
		for key, nested := range item {
			lower := strings.ToLower(strings.TrimSpace(key))
			if lower == "apikey" || lower == "api_key" {
				if text, ok := nested.(string); ok && strings.HasPrefix(strings.TrimSpace(text), "sk-") {
					return strings.TrimSpace(text)
				}
			}
			if found := findDefaultAPIKey(nested); found != "" {
				return found
			}
		}
	case []any:
		for _, nested := range item {
			if found := findDefaultAPIKey(nested); found != "" {
				return found
			}
		}
	}
	return ""
}

func normalizeConsoleServer(server string) string {
	server = strings.TrimRight(strings.TrimSpace(server), "/")
	if server == "" {
		return ConsoleServer
	}
	if parsed, err := url.Parse(server); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		return server
	}
	return ConsoleServer
}

func resolveConsoleURL(server, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	parsed, err := url.Parse(value)
	if err == nil && parsed.IsAbs() {
		return value
	}
	base, err := url.Parse(normalizeConsoleServer(server))
	if err != nil {
		return value
	}
	if strings.HasPrefix(value, "/") {
		base.Path = ""
		base.RawPath = ""
		base.RawQuery = ""
		base.Fragment = ""
		resolved, resolveErr := base.Parse(value)
		if resolveErr == nil {
			return resolved.String()
		}
	}
	return strings.TrimRight(normalizeConsoleServer(server), "/") + "/" + value
}

func SessionMetadata(session Session) map[string]any {
	return map[string]any{
		"type":          ProviderName,
		"access_token":  session.AccessToken,
		"refresh_token": session.RefreshToken,
		"auth_cookie":   session.BrowserCookie,
		"auth_mode": func() string {
			if strings.TrimSpace(session.BrowserCookie) != "" {
				return "browser"
			}
			return "oauth"
		}(),
		"expires_at":     session.ExpiresAt.Format(time.RFC3339Nano),
		"server":         session.Server,
		"account_id":     session.AccountID,
		"email":          session.Email,
		"workspace_id":   session.WorkspaceID,
		"workspace_name": session.WorkspaceName,
		"api_key":        session.DefaultAPIKey,
	}
}

func ParseSessionMetadata(metadata map[string]any) (Session, error) {
	get := func(key string) string {
		value, _ := metadata[key].(string)
		return strings.TrimSpace(value)
	}
	refresh := get("refresh_token")
	browserCookie := get("auth_cookie")
	if browserCookie == "" {
		browserCookie = get("browser_cookie")
	}
	if refresh == "" && browserCookie == "" {
		return Session{}, fmt.Errorf("opencode console: refresh token is missing")
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, get("expires_at"))
	if err != nil {
		if seconds, parseErr := strconv.ParseInt(get("expires_at"), 10, 64); parseErr == nil {
			expiresAt = time.Unix(seconds, 0).UTC()
		} else {
			return Session{}, fmt.Errorf("opencode console: invalid expires_at")
		}
	}
	return Session{AccessToken: get("access_token"), RefreshToken: refresh, BrowserCookie: browserCookie, ExpiresAt: expiresAt,
		Server: normalizeConsoleServer(get("server")), AccountID: get("account_id"), Email: get("email"),
		WorkspaceID: get("workspace_id"), WorkspaceName: get("workspace_name"), DefaultAPIKey: get("api_key")}, nil
}
