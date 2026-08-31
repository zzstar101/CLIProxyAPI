package auth

import (
	"path/filepath"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/opencodego"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestNewOpenCodeGoAPIKeyRecord(t *testing.T) {
	record := NewOpenCodeGoAPIKeyRecord("user@example.com", "sk-test")
	if record.Provider != opencodego.ProviderName {
		t.Fatalf("provider = %q, want %q", record.Provider, opencodego.ProviderName)
	}
	if record.FileName != filepath.Join("opencode-go", "user-example.com.json") {
		t.Fatalf("file name = %q", record.FileName)
	}
	if record.ID != record.FileName {
		t.Fatalf("record ID = %q, want persisted file ID %q", record.ID, record.FileName)
	}
	if record.AuthKind() != coreauth.AuthKindAPIKey {
		t.Fatalf("auth kind = %q, want %q", record.AuthKind(), coreauth.AuthKindAPIKey)
	}
	if record.Attributes[coreauth.AttributeAPIKey] != "sk-test" {
		t.Fatalf("API key was not stored in attributes")
	}
	if record.Metadata["email"] != "user@example.com" {
		t.Fatalf("email metadata was not stored")
	}
}
