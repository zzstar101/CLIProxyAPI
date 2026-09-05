package commandcode

import (
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const snapshotKey = "command_code_snapshot"
const overridesKey = "command_code_model_overrides"

// Bounded striped locks serialize discovery commits with account edits without
// retaining a lock entry for every account ever imported.
var accountLocks [64]sync.Mutex

func LockAccount(id string) func() {
	digest := sha256.Sum256([]byte(id))
	lock := &accountLocks[int(digest[0])%len(accountLocks)]
	lock.Lock()
	return lock.Unlock
}

// Snapshot persists the last complete discovery result, not cookies or tokens.
// Registration is pure and cannot block server startup on an upstream request.
type Snapshot struct {
	Account   Account   `json:"account"`
	Models    []Model   `json:"models"`
	FetchedAt time.Time `json:"fetched_at"`
	LastError string    `json:"last_error,omitempty"`
}

func APIKey(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if value, ok := auth.Metadata["api_key"].(string); ok {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(auth.Attributes["api_key"])
}
func ReadSnapshot(auth *coreauth.Auth) (Snapshot, bool) {
	var snapshot Snapshot
	if auth == nil {
		return snapshot, false
	}
	raw, err := json.Marshal(auth.Metadata[snapshotKey])
	if err != nil {
		return snapshot, false
	}
	if json.Unmarshal(raw, &snapshot) != nil {
		return snapshot, false
	}
	return snapshot, snapshot.Account.ID != "" && snapshot.Models != nil
}
func SetSnapshot(auth *coreauth.Auth, snapshot Snapshot) {
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	// Use JSON-shaped metadata so cloning and file-store reload have identical behavior.
	data, _ := json.Marshal(snapshot)
	var value map[string]any
	_ = json.Unmarshal(data, &value)
	auth.Metadata[snapshotKey] = value
	auth.Metadata["account_id"] = snapshot.Account.ID
	auth.Metadata["plan_id"] = snapshot.Account.Subscription.PlanID
}
func Overrides(auth *coreauth.Auth) map[string]bool {
	result := make(map[string]bool)
	if auth == nil {
		return result
	}
	data, err := json.Marshal(auth.Metadata[overridesKey])
	if err == nil {
		_ = json.Unmarshal(data, &result)
	}
	if result == nil {
		result = make(map[string]bool)
	}
	return result
}
func SetOverrides(auth *coreauth.Auth, overrides map[string]bool) {
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	value := make(map[string]any, len(overrides))
	for id, enabled := range overrides {
		value[id] = enabled
	}
	auth.Metadata[overridesKey] = value
}
