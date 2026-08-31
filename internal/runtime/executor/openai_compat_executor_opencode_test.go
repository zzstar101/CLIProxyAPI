package executor

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/opencodego"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestOpenCodeGoExecutorResolvesPersistedMetadataCredentials(t *testing.T) {
	executor := NewOpenCodeGoExecutor(nil)
	auth := &cliproxyauth.Auth{
		Provider: opencodego.ProviderName,
		Metadata: map[string]any{"api_key": "metadata-key"},
	}

	baseURL, apiKey := executor.resolveCredentials(auth)
	if baseURL != opencodego.DefaultBaseURL {
		t.Fatalf("baseURL = %q, want %q", baseURL, opencodego.DefaultBaseURL)
	}
	if apiKey != "metadata-key" {
		t.Fatalf("apiKey = %q, want metadata-key", apiKey)
	}
}

func TestOpenCodeGoExecutorSelectsNativeModelProtocol(t *testing.T) {
	executor := NewOpenCodeGoExecutor(nil)
	auth := &cliproxyauth.Auth{Provider: opencodego.ProviderName}

	if got := executor.openCodeGoProtocol(auth, "gpt-5.6-luna"); got != "responses" {
		t.Fatalf("gpt protocol = %q, want responses", got)
	}
	if got := executor.openCodeGoProtocol(auth, "deepseek-v4-pro"); got != "chat" {
		t.Fatalf("deepseek protocol = %q, want chat", got)
	}
}
