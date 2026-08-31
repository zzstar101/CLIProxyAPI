package auth

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/browser"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/opencodego"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const openCodeGoRefreshLead = 5 * time.Minute

// OpenCodeAuthenticator implements OpenCode Console's official device OAuth flow.
type OpenCodeAuthenticator struct {
	HTTPClient *http.Client
}

func NewOpenCodeAuthenticator() *OpenCodeAuthenticator { return &OpenCodeAuthenticator{} }

func (OpenCodeAuthenticator) Provider() string { return opencodego.ProviderName }

func (OpenCodeAuthenticator) RefreshLead() *time.Duration {
	lead := openCodeGoRefreshLead
	return &lead
}

func (a *OpenCodeAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}
	client := a.HTTPClient
	device, err := opencodego.StartDeviceLogin(ctx, client, opencodego.ConsoleServer)
	if err != nil {
		return nil, err
	}
	fmt.Printf("OpenCode 登录地址: %s\n验证码: %s\n", device.VerificationURIComplete, device.UserCode)
	if !opts.NoBrowser && browser.IsAvailable() {
		_ = browser.OpenURL(device.VerificationURIComplete)
	}
	session, err := opencodego.PollDeviceLogin(ctx, client, opencodego.ConsoleServer, device)
	if err != nil {
		return nil, err
	}
	requestedWorkspace := ""
	if opts.Metadata != nil {
		requestedWorkspace = strings.TrimSpace(opts.Metadata["workspace_id"])
	}
	if session, err = opencodego.SelectWorkspace(ctx, client, session, requestedWorkspace, opts.Prompt); err != nil {
		return nil, err
	}
	if key, keyErr := opencodego.FetchDefaultAPIKey(ctx, client, session); keyErr == nil {
		session.DefaultAPIKey = key
	}
	return openCodeAuthRecord(session), nil
}

func openCodeAuthRecord(session opencodego.Session) *coreauth.Auth {
	name := session.Email
	if name == "" {
		name = session.AccountID
	}
	name = strings.NewReplacer("@", "-", "/", "-", "\\", "-").Replace(name)
	if name == "" {
		name = session.WorkspaceID
	}
	if name == "" {
		name = "default"
	}
	fileName := filepath.Join("opencode-go", name+".json")
	return &coreauth.Auth{
		ID:       fileName,
		Provider: opencodego.ProviderName,
		FileName: fileName,
		Label:    strings.TrimSpace(session.Email),
		Attributes: map[string]string{
			"base_url":                 opencodego.DefaultBaseURL,
			coreauth.AttributeAuthKind: coreauth.AuthKindOAuth,
			"api_key":                  session.DefaultAPIKey,
		},
		Metadata: opencodego.SessionMetadata(session),
	}
}

// NewOpenCodeGoAuthRecord builds a persisted auth record from a completed session.
func NewOpenCodeGoAuthRecord(session opencodego.Session) *coreauth.Auth {
	return openCodeAuthRecord(session)
}

// NewOpenCodeGoAPIKeyRecord builds a first-class OpenCode Go API key auth record.
func NewOpenCodeGoAPIKeyRecord(email, apiKey string) *coreauth.Auth {
	email = strings.TrimSpace(email)
	apiKey = strings.TrimSpace(apiKey)
	name := strings.NewReplacer("@", "-", "/", "-", "\\", "-").Replace(email)
	if name == "" {
		name = "default"
	}
	fileName := filepath.Join("opencode-go", name+".json")
	return &coreauth.Auth{
		ID:       fileName,
		Provider: opencodego.ProviderName,
		FileName: fileName,
		Label:    email,
		Attributes: map[string]string{
			"base_url":                 opencodego.DefaultBaseURL,
			coreauth.AttributeAuthKind: coreauth.AuthKindAPIKey,
			coreauth.AttributeAPIKey:   apiKey,
		},
		Metadata: map[string]any{
			"type":      opencodego.ProviderName,
			"provider":  opencodego.ProviderName,
			"auth_mode": coreauth.AuthKindAPIKey,
			"email":     email,
			"api_key":   apiKey,
			"base_url":  opencodego.DefaultBaseURL,
		},
	}
}

// RefreshOpenCodeGoAuth refreshes the Console session and keeps the previous
// default key when Console does not expose it in the refreshed config.
func RefreshOpenCodeGoAuth(ctx context.Context, auth *coreauth.Auth) (*coreauth.Auth, error) {
	if auth == nil {
		return nil, fmt.Errorf("opencode console: auth is nil")
	}
	session, err := opencodego.ParseSessionMetadata(auth.Metadata)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(session.BrowserCookie) != "" {
		key, keyErr := opencodego.FetchDefaultAPIKeyFromBrowserSession(ctx, nil, session.Server, session.WorkspaceID, session.BrowserCookie)
		if keyErr != nil {
			return nil, keyErr
		}
		session.DefaultAPIKey = key
		updated := *auth
		updated.Metadata = opencodego.SessionMetadata(session)
		if summary, ok := auth.Metadata["referral_summary"]; ok {
			updated.Metadata["referral_summary"] = summary
		}
		if updated.Attributes == nil {
			updated.Attributes = make(map[string]string)
		}
		updated.Attributes[coreauth.AttributeAPIKey] = key
		updated.LastRefreshedAt = time.Now().UTC()
		return &updated, nil
	}
	refreshed, err := opencodego.RefreshSession(ctx, nil, session)
	if err != nil {
		return nil, err
	}
	if key, keyErr := opencodego.FetchDefaultAPIKey(ctx, nil, refreshed); keyErr == nil && key != "" {
		refreshed.DefaultAPIKey = key
	} else {
		refreshed.DefaultAPIKey = strings.TrimSpace(auth.Attributes[coreauth.AttributeAPIKey])
	}
	updated := *auth
	updated.Metadata = opencodego.SessionMetadata(refreshed)
	if summary, ok := auth.Metadata["referral_summary"]; ok {
		updated.Metadata["referral_summary"] = summary
	}
	if updated.Attributes == nil {
		updated.Attributes = make(map[string]string)
	}
	if refreshed.DefaultAPIKey != "" {
		updated.Attributes[coreauth.AttributeAPIKey] = refreshed.DefaultAPIKey
	}
	updated.LastRefreshedAt = time.Now().UTC()
	return &updated, nil
}
