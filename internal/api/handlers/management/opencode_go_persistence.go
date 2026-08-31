package management

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/opencodego"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

// prepareOpenCodeGoRecord canonicalizes OAuth/browser records and returns
// duplicate files that can be removed after the canonical record is saved.
func (h *Handler) prepareOpenCodeGoRecord(record *coreauth.Auth) []string {
	if record == nil || (!opencodego.IsProvider(record.Provider) && !strings.HasPrefix(strings.ToLower(strings.ReplaceAll(record.FileName, "\\", "/")), "opencode-go/")) {
		return nil
	}
	if record.Metadata == nil {
		record.Metadata = make(map[string]any)
	}
	record.Provider = opencodego.ProviderName
	if record.Attributes == nil {
		record.Attributes = make(map[string]string)
	}
	// OpenCode Go is a first-class provider, not an OpenAI compatibility channel.
	delete(record.Attributes, "provider_key")
	delete(record.Attributes, "compat_name")
	if strings.TrimSpace(record.Attributes["base_url"]) == "" {
		record.Attributes["base_url"] = opencodego.DefaultBaseURL
	}
	record.Metadata["type"] = opencodego.ProviderName

	email := strings.TrimSpace(stringMetadata(record.Metadata, "email"))
	workspaceID := strings.TrimSpace(stringMetadata(record.Metadata, "workspace_id"))
	accountID := strings.TrimSpace(stringMetadata(record.Metadata, "account_id"))
	authDir := ""
	if h != nil && h.cfg != nil {
		authDir = strings.TrimSpace(h.cfg.AuthDir)
	}
	opencodeDir := filepath.Join(authDir, "opencode-go")
	entries, err := os.ReadDir(opencodeDir)
	if err != nil {
		if email != "" {
			record.FileName = filepath.Join("opencode-go", openCodeGoFileStem(email)+".json")
		}
		return nil
	}

	var duplicatePaths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(opencodeDir, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var existing map[string]any
		if json.Unmarshal(data, &existing) != nil {
			continue
		}
		existingEmail := strings.TrimSpace(stringMetadata(existing, "email"))
		existingWorkspace := strings.TrimSpace(stringMetadata(existing, "workspace_id"))
		existingAccount := strings.TrimSpace(stringMetadata(existing, "account_id"))
		matched := (email != "" && strings.EqualFold(email, existingEmail)) ||
			(workspaceID != "" && workspaceID == existingWorkspace) ||
			(accountID != "" && accountID == existingAccount)
		if !matched {
			continue
		}
		if email == "" && existingEmail != "" {
			email = existingEmail
		}
		if workspaceID == "" && existingWorkspace != "" {
			workspaceID = existingWorkspace
		}
		if accountID == "" && existingAccount != "" {
			accountID = existingAccount
		}
		// Preserve browser session, default key, workspace and referral fields
		// when a device OAuth refresh is saved over the browser record.
		coreauth.MergeExistingAuthMetadata(record, existing)
		mergeOpenCodeGoEmptyFields(record.Metadata, existing)
		canonicalPath := filepath.Join(opencodeDir, openCodeGoFileStem(email)+".json")
		if email == "" {
			canonicalPath = path
		}
		if !sameAuthFilePath(path, canonicalPath) {
			duplicatePaths = append(duplicatePaths, path)
		}
	}

	if email != "" {
		record.Metadata["email"] = email
		record.Label = email
		record.FileName = filepath.Join("opencode-go", openCodeGoFileStem(email)+".json")
	} else if workspaceID != "" {
		record.Metadata["workspace_id"] = workspaceID
		record.FileName = filepath.Join("opencode-go", workspaceID+".json")
	}
	if accountID != "" {
		record.Metadata["account_id"] = accountID
	}
	return duplicatePaths
}

func mergeOpenCodeGoEmptyFields(target, existing map[string]any) {
	if len(existing) == 0 {
		return
	}
	for _, key := range []string{"api_key", "auth_cookie", "browser_cookie", "email", "workspace_id", "workspace_name", "account_id", "server", "expires_at", "referral_summary"} {
		if current, ok := target[key]; ok {
			if key == "referral_summary" {
				if current != nil {
					continue
				}
			} else if value, ok := current.(string); ok && strings.TrimSpace(value) != "" {
				continue
			}
		}
		if value, ok := existing[key]; ok {
			target[key] = value
		}
	}
}

func stringMetadata(values map[string]any, key string) string {
	if value, ok := values[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func openCodeGoFileStem(email string) string {
	stem := strings.NewReplacer("@", "-", "/", "-", "\\", "-", "\x00", "-").Replace(strings.TrimSpace(email))
	stem = strings.TrimSpace(stem)
	if stem == "" {
		return "default"
	}
	return stem
}
