package management

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/commandcode"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

type commandCodeImportItem struct {
	Email  string `json:"email"`
	APIKey string `json:"api_key"`
}
type commandCodeImportResult struct {
	Row    int    `json:"row"`
	Email  string `json:"email"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

func (h *Handler) commandCodeDiscovery(ctx context.Context, auth *coreauth.Auth) (commandcode.Snapshot, error) {
	client := commandcode.NewClient(&http.Client{Transport: h.apiCallTransport(auth, "")}, "", commandcode.APIKey(auth))
	account, err := client.Account(ctx)
	if err != nil {
		return commandcode.Snapshot{}, err
	}
	models, err := client.Models(ctx)
	if err != nil {
		return commandcode.Snapshot{}, err
	}
	return commandcode.Snapshot{Account: account, Models: models, FetchedAt: time.Now().UTC()}, nil
}
func (h *Handler) commandCodeAuth(id string) *coreauth.Auth {
	if h.authManager == nil {
		return nil
	}
	auth, ok := h.authManager.GetByID(id)
	if !ok || auth.Provider != commandcode.Provider {
		return nil
	}
	return auth.Clone()
}
func (h *Handler) persistCommandCode(ctx context.Context, auth *coreauth.Auth) error {
	path, err := h.saveTokenRecord(ctx, auth)
	if err != nil {
		return fmt.Errorf("command-code: could not save account")
	}
	if err = h.registerAuthFromFile(ctx, path, nil); err != nil {
		return fmt.Errorf("command-code: could not register account")
	}
	return nil
}

// ImportCommandCodeAccounts shares one path for single and batch imports.
// Only credential acquisition has a deadline. Partial failures never echo keys.
func (h *Handler) ImportCommandCodeAccounts(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 512*1024)
	var request struct {
		Items []commandCodeImportItem `json:"items"`
	}
	if c.ShouldBindJSON(&request) != nil || len(request.Items) == 0 || len(request.Items) > 100 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "provide between 1 and 100 email/API key pairs"})
		return
	}
	h.commandCodeMu.Lock()
	defer h.commandCodeMu.Unlock()
	results := make([]commandCodeImportResult, 0, len(request.Items))
	for index, item := range request.Items {
		item.Email = strings.TrimSpace(item.Email)
		item.APIKey = strings.TrimSpace(item.APIKey)
		result := commandCodeImportResult{Row: index + 1, Email: item.Email, Status: "failed"}
		address, err := mail.ParseAddress(item.Email)
		if err != nil || address.Address != item.Email || item.APIKey == "" || strings.ContainsAny(item.APIKey, "\r\n\x00") {
			result.Email = ""
			result.Error = "invalid email or API key format"
			results = append(results, result)
			continue
		}
		duplicate := false
		if h.authManager != nil {
			for _, a := range h.authManager.List() {
				if a.Provider == commandcode.Provider && commandcode.APIKey(a) == item.APIKey {
					duplicate = true
					break
				}
			}
		}
		if duplicate {
			result.Status = "skipped"
			result.Error = "account already exists"
			results = append(results, result)
			continue
		}
		record := &coreauth.Auth{Provider: commandcode.Provider, Label: item.Email, Status: coreauth.StatusActive, Metadata: map[string]any{"type": commandcode.Provider, "auth_kind": "apikey", "email": item.Email, "api_key": item.APIKey}}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		snapshot, err := h.commandCodeDiscovery(ctx, record)
		cancel()
		if err != nil {
			result.Error = err.Error()
			results = append(results, result)
			continue
		}
		if h.authManager != nil {
			for _, a := range h.authManager.List() {
				if a.Provider == commandcode.Provider {
					if stored, ok := commandcode.ReadSnapshot(a); ok && stored.Account.ID == snapshot.Account.ID {
						duplicate = true
						break
					}
				}
			}
		}
		if duplicate {
			result.Status = "skipped"
			result.Error = "account already exists"
			results = append(results, result)
			continue
		}
		digest := sha256.Sum256([]byte(snapshot.Account.ID))
		record.ID = fmt.Sprintf("command-code-%x.json", digest[:16])
		record.FileName = record.ID
		record.CreatedAt = time.Now().UTC()
		record.UpdatedAt = record.CreatedAt
		commandcode.SetSnapshot(record, snapshot)
		if err = h.persistCommandCode(c.Request.Context(), record); err != nil {
			result.Error = err.Error()
		} else {
			result.Status = "imported"
		}
		results = append(results, result)
	}
	c.JSON(http.StatusOK, gin.H{"items": results})
}

func commandCodeAccountView(auth *coreauth.Auth) gin.H {
	snapshot, _ := commandcode.ReadSnapshot(auth)
	overrides := commandcode.Overrides(auth)
	models := make([]gin.H, 0, len(snapshot.Models))
	for _, model := range snapshot.Models {
		models = append(models, gin.H{"id": model.ID, "name": model.Name, "owned_by": model.OwnedBy, "context_length": model.ContextLength, "entitled": commandcode.Entitled(snapshot.Account, model), "enabled": commandcode.Enabled(snapshot.Account, model, overrides), "default_enabled": commandcode.DefaultEnabled(model)})
	}
	return gin.H{"id": auth.ID, "email": auth.Label, "disabled": auth.Disabled, "subscription": snapshot.Account.Subscription, "usage": snapshot.Account.Usage, "fetched_at": snapshot.FetchedAt, "last_error": snapshot.LastError, "models": models, "model_overrides": overrides}
}
func (h *Handler) GetCommandCodeAccounts(c *gin.Context) {
	items := make([]gin.H, 0)
	if h.authManager != nil {
		for _, auth := range h.authManager.List() {
			if auth.Provider == commandcode.Provider {
				items = append(items, commandCodeAccountView(auth))
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
func (h *Handler) RefreshCommandCodeAccount(c *gin.Context) {
	h.commandCodeMu.Lock()
	defer h.commandCodeMu.Unlock()
	unlock := commandcode.LockAccount(c.Query("id"))
	defer unlock()
	auth := h.commandCodeAuth(c.Query("id"))
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	snapshot, err := h.commandCodeDiscovery(c.Request.Context(), auth)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}
	old, _ := commandcode.ReadSnapshot(auth)
	if snapshot.Account.ID != old.Account.ID {
		c.JSON(http.StatusConflict, gin.H{"error": "account identity changed"})
		return
	}
	commandcode.SetSnapshot(auth, snapshot)
	if err = h.persistCommandCode(c.Request.Context(), auth); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, commandCodeAccountView(auth))
}
func (h *Handler) UpdateCommandCodeAccount(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256*1024)
	var request struct {
		APIKey *string         `json:"api_key"`
		Email  *string         `json:"email"`
		Models map[string]bool `json:"model_overrides"`
	}
	if c.ShouldBindJSON(&request) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid account update"})
		return
	}
	h.commandCodeMu.Lock()
	defer h.commandCodeMu.Unlock()
	unlock := commandcode.LockAccount(c.Query("id"))
	defer unlock()
	auth := h.commandCodeAuth(c.Query("id"))
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "account not found"})
		return
	}
	if request.Email != nil {
		email := strings.TrimSpace(*request.Email)
		parsed, err := mail.ParseAddress(email)
		if err != nil || parsed.Address != email {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid email"})
			return
		}
		auth.Label = email
		auth.Metadata["email"] = email
	}
	if request.APIKey != nil {
		key := strings.TrimSpace(*request.APIKey)
		if key == "" || strings.ContainsAny(key, "\r\n\x00") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid API key"})
			return
		}
		old, _ := commandcode.ReadSnapshot(auth)
		auth.Metadata["api_key"] = key
		ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
		snapshot, err := h.commandCodeDiscovery(ctx, auth)
		cancel()
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		if old.Account.ID != snapshot.Account.ID {
			c.JSON(http.StatusConflict, gin.H{"error": "replacement key belongs to a different account"})
			return
		}
		commandcode.SetSnapshot(auth, snapshot)
	}
	if request.Models != nil {
		snapshot, _ := commandcode.ReadSnapshot(auth)
		known := make(map[string]bool, len(snapshot.Models))
		for _, m := range snapshot.Models {
			known[m.ID] = true
		}
		overrides := commandcode.Overrides(auth)
		for id, enabled := range request.Models {
			if !known[id] {
				c.JSON(http.StatusBadRequest, gin.H{"error": "unknown model"})
				return
			}
			overrides[id] = enabled
		}
		commandcode.SetOverrides(auth, overrides)
	}
	if err := h.persistCommandCode(c.Request.Context(), auth); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, commandCodeAccountView(auth))
}
