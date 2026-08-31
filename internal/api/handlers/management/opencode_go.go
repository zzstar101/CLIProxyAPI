package management

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/opencodego"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type openCodeGoCredential struct {
	Index     int
	BaseURL   string
	APIKey    string
	Auth      *coreauth.Auth
	Fields    map[string]string
	Name      string
	ID        string
	AuthIndex string
	Email     string
}

func (h *Handler) openCodeGoAuthFilePath(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	path := ""
	if auth.Attributes != nil {
		path = strings.TrimSpace(auth.Attributes["path"])
	}
	if path == "" {
		path = strings.TrimSpace(auth.FileName)
		if h.cfg != nil && path != "" && !filepath.IsAbs(path) {
			path = filepath.Join(h.cfg.AuthDir, path)
		}
	}
	return path
}

func (h *Handler) openCodeGoPersistedJSON(auth *coreauth.Auth) map[string]any {
	path := h.openCodeGoAuthFilePath(auth)
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return nil
	}
	return raw
}

// openCodeGoPersistedFields reads flat auth JSON fields used by the file store.
// Older records predate the structured Auth.Metadata representation.
func (h *Handler) openCodeGoPersistedFields(auth *coreauth.Auth) map[string]string {
	fields := make(map[string]string)
	for key, value := range h.openCodeGoPersistedJSON(auth) {
		if _, ok := value.(string); !ok {
			continue
		}
		if trimmed := strings.TrimSpace(value.(string)); trimmed != "" {
			switch key {
			case "api_key", "email", "auth_cookie", "browser_cookie", "workspace_id", "workspace_name", "server", "expires_at", "account_id", "access_token", "refresh_token":
				fields[key] = trimmed
			}
		}
	}
	return fields
}

func openCodeGoField(auth *coreauth.Auth, fields map[string]string, key string) string {
	if auth != nil && auth.Metadata != nil {
		if value, ok := auth.Metadata[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	if auth != nil && auth.Attributes != nil {
		if value := strings.TrimSpace(auth.Attributes[key]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(fields[key])
}

func isOpenCodeGoAuth(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	return opencodego.IsProvider(auth.Provider) || strings.HasPrefix(strings.ToLower(strings.ReplaceAll(auth.FileName, "\\", "/")), "opencode-go/")
}

type openCodeGoReferralStatus struct {
	Provider    string                      `json:"provider"`
	Supported   bool                        `json:"supported"`
	Reason      string                      `json:"reason"`
	Endpoint    string                      `json:"endpoint,omitempty"`
	ItemCount   int                         `json:"item_count,omitempty"`
	EndpointOK  bool                        `json:"endpoint_available"`
	ConsoleURL  string                      `json:"console_url,omitempty"`
	WorkspaceID string                      `json:"workspace_id,omitempty"`
	Summary     *opencodego.ReferralSummary `json:"summary,omitempty"`
}

type openCodeGoBrowserReferralRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Cookie      string `json:"cookie"`
}

type openCodeGoBrowserLoginRequest struct {
	WorkspaceID string `json:"workspace_id"`
	Cookie      string `json:"cookie"`
}

type openCodeGoAPIKeyLoginRequest struct {
	Email  string `json:"email"`
	APIKey string `json:"api_key"`
}

type openCodeGoSessionStatus struct {
	Index            int    `json:"index"`
	Source           string `json:"source"`
	Authenticated    bool   `json:"authenticated"`
	HasDefaultAPIKey bool   `json:"has_default_api_key"`
	Email            string `json:"email,omitempty"`
	AccountID        string `json:"account_id,omitempty"`
	WorkspaceID      string `json:"workspace_id,omitempty"`
	WorkspaceName    string `json:"workspace_name,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	LastRefreshedAt  string `json:"last_refreshed_at,omitempty"`
	Error            string `json:"error,omitempty"`
}

func (h *Handler) openCodeGoCredentials(name string) []openCodeGoCredential {
	want := opencodego.NormalizeProviderName(name)
	if want == "" {
		want = opencodego.ProviderName
	}
	h.mu.Lock()
	var out []openCodeGoCredential
	if h.cfg != nil {
		for i := range h.cfg.OpenAICompatibility {
			entry := h.cfg.OpenAICompatibility[i]
			if opencodego.NormalizeProviderName(entry.Name) != want {
				continue
			}
			baseURL := strings.TrimSpace(entry.BaseURL)
			if baseURL == "" {
				baseURL = opencodego.DefaultBaseURL
			}
			for _, key := range entry.APIKeyEntries {
				if strings.TrimSpace(key.APIKey) == "" {
					continue
				}
				out = append(out, openCodeGoCredential{Index: i, BaseURL: baseURL, APIKey: strings.TrimSpace(key.APIKey)})
			}
		}
	}
	h.mu.Unlock()
	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			if auth == nil || (!isOpenCodeGoAuth(auth) && opencodego.NormalizeProviderName(auth.Provider) != want) {
				continue
			}
			fields := h.openCodeGoPersistedFields(auth)
			key := openCodeGoField(auth, fields, "api_key")
			if key == "" && openCodeGoField(auth, fields, "auth_cookie") == "" && openCodeGoField(auth, fields, "browser_cookie") == "" {
				continue
			}
			baseURL := strings.TrimSpace(auth.Attributes["base_url"])
			if baseURL == "" {
				baseURL = opencodego.DefaultBaseURL
			}
			out = append(out, openCodeGoCredential{
				Index:     -1,
				BaseURL:   baseURL,
				APIKey:    key,
				Auth:      auth,
				Fields:    fields,
				Name:      strings.TrimSpace(auth.FileName),
				ID:        strings.TrimSpace(auth.ID),
				AuthIndex: auth.EnsureIndex(),
				Email:     openCodeGoField(auth, fields, "email"),
			})
		}
	}
	return out
}

func (h *Handler) resolveOpenCodeGoCredential(ctx context.Context, credential openCodeGoCredential) openCodeGoCredential {
	if strings.TrimSpace(credential.APIKey) != "" || credential.Auth == nil {
		return credential
	}
	workspaceID := openCodeGoField(credential.Auth, credential.Fields, "workspace_id")
	cookie := openCodeGoField(credential.Auth, credential.Fields, "auth_cookie")
	if cookie == "" {
		cookie = openCodeGoField(credential.Auth, credential.Fields, "browser_cookie")
	}
	if workspaceID == "" || cookie == "" {
		return credential
	}
	server := openCodeGoField(credential.Auth, credential.Fields, "server")
	if server == "" {
		server = "https://opencode.ai"
	}
	if key, err := opencodego.FetchDefaultAPIKeyFromBrowserSession(ctx, nil, server, workspaceID, cookie); err == nil {
		credential.APIKey = key
	}
	return credential
}

// GetOpenCodeGoUsage proxies the official Go usage API and returns only upstream windows.
func (h *Handler) GetOpenCodeGoUsage(c *gin.Context) {
	credentials := h.openCodeGoCredentials(c.Query("name"))
	if len(credentials) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "opencode-go provider or API key not configured"})
		return
	}
	type item struct {
		Index     int              `json:"index"`
		Name      string           `json:"name,omitempty"`
		ID        string           `json:"id,omitempty"`
		AuthIndex string           `json:"auth_index,omitempty"`
		Email     string           `json:"email,omitempty"`
		Usage     opencodego.Usage `json:"usage,omitempty"`
		Error     string           `json:"error,omitempty"`
	}
	items := make([]item, 0, len(credentials))
	for _, credential := range credentials {
		credential = h.resolveOpenCodeGoCredential(c.Request.Context(), credential)
		usage, err := opencodego.FetchUsage(c.Request.Context(), nil, credential.BaseURL, credential.APIKey)
		result := item{
			Index:     credential.Index,
			Name:      credential.Name,
			ID:        credential.ID,
			AuthIndex: credential.AuthIndex,
			Email:     credential.Email,
			Usage:     usage,
		}
		if err != nil {
			result.Error = err.Error()
		}
		items = append(items, result)
	}
	c.JSON(http.StatusOK, gin.H{"provider": opencodego.ProviderName, "items": items, "fetched_at": time.Now().UTC()})
}

// GetOpenCodeGoModels fetches the dynamic upstream model catalog for CPAMP.
func (h *Handler) GetOpenCodeGoModels(c *gin.Context) {
	credentials := h.openCodeGoCredentials(c.Query("name"))
	if len(credentials) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "opencode-go provider or API key not configured"})
		return
	}
	type item struct {
		Index     int                `json:"index"`
		Name      string             `json:"name,omitempty"`
		ID        string             `json:"id,omitempty"`
		AuthIndex string             `json:"auth_index,omitempty"`
		Email     string             `json:"email,omitempty"`
		Models    []opencodego.Model `json:"models,omitempty"`
		Error     string             `json:"error,omitempty"`
	}
	items := make([]item, 0, len(credentials))
	for _, credential := range credentials {
		credential = h.resolveOpenCodeGoCredential(c.Request.Context(), credential)
		models, err := opencodego.FetchModels(c.Request.Context(), nil, credential.BaseURL, credential.APIKey)
		result := item{
			Index:     credential.Index,
			Name:      credential.Name,
			ID:        credential.ID,
			AuthIndex: credential.AuthIndex,
			Email:     credential.Email,
			Models:    models,
		}
		if err != nil {
			result.Error = err.Error()
		}
		items = append(items, result)
	}
	c.JSON(http.StatusOK, gin.H{"provider": opencodego.ProviderName, "items": items, "fetched_at": time.Now().UTC()})
}

// GetOpenCodeGoStatus exposes non-secret OAuth/session state for the management UI.
// Tokens and API keys are deliberately reduced to boolean presence flags.
func (h *Handler) GetOpenCodeGoStatus(c *gin.Context) {
	items := make([]openCodeGoSessionStatus, 0)
	want := opencodego.NormalizeProviderName(c.Query("name"))
	if want == "" {
		want = opencodego.ProviderName
	}
	h.mu.Lock()
	if h.cfg != nil {
		for index, entry := range h.cfg.OpenAICompatibility {
			if opencodego.NormalizeProviderName(entry.Name) != want {
				continue
			}
			hasKey := false
			for _, key := range entry.APIKeyEntries {
				if strings.TrimSpace(key.APIKey) != "" {
					hasKey = true
					break
				}
			}
			items = append(items, openCodeGoSessionStatus{Index: index, Source: "config", HasDefaultAPIKey: hasKey})
		}
	}
	h.mu.Unlock()
	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			if auth == nil || (!isOpenCodeGoAuth(auth) && opencodego.NormalizeProviderName(auth.Provider) != want) {
				continue
			}
			fields := h.openCodeGoPersistedFields(auth)
			item := openCodeGoSessionStatus{Index: -1, Source: "oauth"}
			item.HasDefaultAPIKey = openCodeGoField(auth, fields, "api_key") != ""
			if item.HasDefaultAPIKey && openCodeGoField(auth, fields, "access_token") == "" &&
				openCodeGoField(auth, fields, "refresh_token") == "" &&
				openCodeGoField(auth, fields, "auth_cookie") == "" &&
				openCodeGoField(auth, fields, "browser_cookie") == "" {
				item.Source = "api_key"
			}
			item.Authenticated = item.HasDefaultAPIKey ||
				openCodeGoField(auth, fields, "access_token") != "" ||
				openCodeGoField(auth, fields, "refresh_token") != "" ||
				openCodeGoField(auth, fields, "auth_cookie") != "" ||
				openCodeGoField(auth, fields, "browser_cookie") != ""
			if auth.Metadata != nil {
				item.Email, _ = auth.Metadata["email"].(string)
				item.AccountID, _ = auth.Metadata["account_id"].(string)
				item.WorkspaceID, _ = auth.Metadata["workspace_id"].(string)
				item.WorkspaceName, _ = auth.Metadata["workspace_name"].(string)
				item.ExpiresAt, _ = auth.Metadata["expires_at"].(string)
			}
			if strings.TrimSpace(item.Email) == "" {
				item.Email = authEmail(auth)
			}
			if strings.TrimSpace(item.Email) == "" {
				item.Email = fields["email"]
			}
			if strings.TrimSpace(item.AccountID) == "" {
				item.AccountID = fields["account_id"]
			}
			if strings.TrimSpace(item.WorkspaceID) == "" {
				item.WorkspaceID = fields["workspace_id"]
			}
			if strings.TrimSpace(item.WorkspaceName) == "" {
				item.WorkspaceName = fields["workspace_name"]
			}
			if strings.TrimSpace(item.ExpiresAt) == "" {
				item.ExpiresAt = fields["expires_at"]
			}
			if !item.HasDefaultAPIKey && item.Authenticated {
				credential := h.resolveOpenCodeGoCredential(c.Request.Context(), openCodeGoCredential{Auth: auth, Fields: fields})
				item.HasDefaultAPIKey = strings.TrimSpace(credential.APIKey) != ""
			}
			if strings.TrimSpace(item.Email) == "" && item.Authenticated && item.WorkspaceID != "" {
				cookie := fields["auth_cookie"]
				if cookie == "" {
					cookie = fields["browser_cookie"]
				}
				if cookie != "" {
					server := fields["server"]
					if server == "" {
						server = "https://opencode.ai"
					}
					if email, emailErr := opencodego.FetchBrowserSessionEmail(c.Request.Context(), nil, server, item.WorkspaceID, cookie); emailErr == nil {
						item.Email = email
						if auth.Metadata == nil {
							auth.Metadata = make(map[string]any)
						}
						auth.Metadata["email"] = email
						auth.Label = email
					}
				}
			}
			if !auth.LastRefreshedAt.IsZero() {
				item.LastRefreshedAt = auth.LastRefreshedAt.UTC().Format(time.RFC3339)
			}
			items = append(items, item)
		}
	}
	c.JSON(http.StatusOK, gin.H{"provider": opencodego.ProviderName, "items": items, "fetched_at": time.Now().UTC()})
}

// ImportOpenCodeGoAPIKey validates and persists a direct OpenCode Go API key.
func (h *Handler) ImportOpenCodeGoAPIKey(c *gin.Context) {
	var request openCodeGoAPIKeyLoginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email and api_key are required"})
		return
	}
	email := strings.TrimSpace(request.Email)
	apiKey := strings.TrimSpace(request.APIKey)
	if email == "" || apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "email and api_key are required"})
		return
	}
	if _, err := opencodego.FetchUsage(c.Request.Context(), nil, opencodego.DefaultBaseURL, apiKey); err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	record := sdkAuth.NewOpenCodeGoAPIKeyRecord(email, apiKey)
	if _, err := h.saveTokenRecord(c.Request.Context(), record); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"provider":          opencodego.ProviderName,
		"email":             email,
		"authenticated":     true,
		"default_key_saved": true,
		"fetched_at":        time.Now().UTC(),
	})
}

// GetOpenCodeGoReferral reports the real capability boundary. OpenCode exposes
// referral summary/reward operations as Console server actions, not API-key
// endpoints, so this API never labels them as banked reset or account balance.
func (h *Handler) GetOpenCodeGoReferral(c *gin.Context) {
	status := openCodeGoReferralStatus{
		Provider:  opencodego.ProviderName,
		Supported: false,
		Reason:    "OpenCode referral reward summary is a Console server action; the public invites endpoint returned no reward records",
		Endpoint:  opencodego.ConsoleServer + "/api/invites",
	}
	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			if auth == nil || !isOpenCodeGoAuth(auth) {
				continue
			}
			workspaceID := ""
			server := ""
			fields := h.openCodeGoPersistedFields(auth)
			if auth.Metadata != nil {
				workspaceID, _ = auth.Metadata["workspace_id"].(string)
				server, _ = auth.Metadata["server"].(string)
			}
			if workspaceID == "" {
				workspaceID = fields["workspace_id"]
			}
			if server == "" {
				server = fields["server"]
			}
			status.WorkspaceID = strings.TrimSpace(workspaceID)
			server = strings.TrimRight(strings.TrimSpace(server), "/")
			if server == "" {
				server = opencodego.ConsoleServer
			}
			status.ConsoleURL = server + "/workspace/" + status.WorkspaceID + "/go"
			var rawSummary map[string]any
			if auth.Metadata != nil {
				rawSummary, _ = auth.Metadata["referral_summary"].(map[string]any)
			}
			if rawSummary == nil {
				rawSummary, _ = h.openCodeGoPersistedJSON(auth)["referral_summary"].(map[string]any)
			}
			if rawSummary != nil {
				encoded, marshalErr := json.Marshal(rawSummary)
				if marshalErr == nil {
					var summary opencodego.ReferralSummary
					if json.Unmarshal(encoded, &summary) == nil {
						status.Summary = &summary
						status.Supported = true
						status.EndpointOK = true
						status.ItemCount = len(summary.Rewards)
					}
				}
			}
			if status.Summary == nil && workspaceID != "" {
				cookie := fields["auth_cookie"]
				if cookie == "" {
					cookie = fields["browser_cookie"]
				}
				if cookie != "" {
					if summary, fetchErr := opencodego.FetchReferralSummary(c.Request.Context(), nil, server, workspaceID, cookie); fetchErr == nil {
						status.Summary = &summary
						status.Supported = true
						status.EndpointOK = true
						status.ItemCount = len(summary.Rewards)
					}
				}
			}
			if session, parseErr := opencodego.ParseSessionMetadata(auth.Metadata); parseErr == nil {
				session.Server = server
				if invites, inviteErr := opencodego.FetchInvites(c.Request.Context(), nil, session); inviteErr == nil {
					status.EndpointOK = true
					status.ItemCount = len(invites)
				}
			}
			if status.Summary != nil {
				break
			}
		}
	}
	c.JSON(http.StatusOK, status)
}

// GetOpenCodeGoBrowserReferral reads the read-only referral summary rendered by
// the browser Console session. The cookie is never persisted or logged.
func (h *Handler) GetOpenCodeGoBrowserReferral(c *gin.Context) {
	var request openCodeGoBrowserReferralRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id and cookie are required"})
		return
	}
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	cookie := strings.TrimSpace(request.Cookie)
	if workspaceID == "" || cookie == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id and cookie are required"})
		return
	}
	summary, err := opencodego.FetchReferralSummary(c.Request.Context(), nil, "https://opencode.ai", workspaceID, cookie)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	apiKey, keyErr := opencodego.FetchDefaultAPIKeyFromBrowserSession(c.Request.Context(), nil, "https://opencode.ai", workspaceID, cookie)
	c.JSON(http.StatusOK, gin.H{
		"provider":     opencodego.ProviderName,
		"workspace_id": workspaceID,
		"summary":      summary,
		"api_key":      apiKey,
		"api_key_error": func() string {
			if keyErr != nil {
				return keyErr.Error()
			}
			return ""
		}(),
		"fetched_at": time.Now().UTC(),
	})
}

// ImportOpenCodeGoBrowserSession imports a web login into an auth file.
// The browser cookie is persisted as the session credential and is never
// returned in the response or written to logs.
func (h *Handler) ImportOpenCodeGoBrowserSession(c *gin.Context) {
	var request openCodeGoBrowserLoginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id and cookie are required"})
		return
	}
	workspaceID := strings.TrimSpace(request.WorkspaceID)
	cookie := strings.TrimSpace(request.Cookie)
	if workspaceID == "" || cookie == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "workspace_id and cookie are required"})
		return
	}

	ctx := c.Request.Context()
	apiKey, err := opencodego.FetchDefaultAPIKeyFromBrowserSession(ctx, nil, "https://opencode.ai", workspaceID, cookie)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	// Referral parsing is read-only and may be unavailable for accounts with no
	// referral data; it must not prevent the usable model credential being saved.
	var referral *opencodego.ReferralSummary
	if summary, referralErr := opencodego.FetchReferralSummary(ctx, nil, "https://opencode.ai", workspaceID, cookie); referralErr == nil {
		referral = &summary
	}
	email, _ := opencodego.FetchBrowserSessionEmail(ctx, nil, "https://opencode.ai", workspaceID, cookie)

	session := opencodego.Session{
		BrowserCookie: cookie,
		Server:        "https://opencode.ai",
		WorkspaceID:   workspaceID,
		Email:         email,
		DefaultAPIKey: apiKey,
	}
	record := sdkAuth.NewOpenCodeGoAuthRecord(session)
	if referral != nil {
		record.Metadata["referral_summary"] = referral
	}
	if _, saveErr := h.saveTokenRecord(ctx, record); saveErr != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": saveErr.Error()})
		return
	}

	response := gin.H{
		"provider":           opencodego.ProviderName,
		"workspace_id":       workspaceID,
		"authenticated":      true,
		"default_key_saved":  true,
		"referral_available": referral != nil,
		"fetched_at":         time.Now().UTC(),
	}
	if referral != nil {
		response["referral"] = referral
	}
	c.JSON(http.StatusOK, response)
}
