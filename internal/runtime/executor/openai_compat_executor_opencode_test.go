package executor

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/opencodego"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/tidwall/gjson"
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

	if got := executor.upstreamProtocol(auth, "gpt-5.6-luna"); got != "responses" {
		t.Fatalf("gpt protocol = %q, want responses", got)
	}
	if got := executor.upstreamProtocol(auth, "deepseek-v4-pro"); got != "chat" {
		t.Fatalf("deepseek protocol = %q, want chat", got)
	}
}

func TestOpenCodeGoExecutorNormalizesDeveloperRoleForChatModels(t *testing.T) {
	executor := NewOpenCodeGoExecutor(nil)
	payload := []byte(`{"messages":[{"role":"developer","content":"rules"},{"role":"user","content":"hello"}]}`)

	got := executor.normalizeOpenCodeGoChatRoles(payload, "chat")
	if role := gjson.GetBytes(got, "messages.0.role").String(); role != "system" {
		t.Fatalf("messages[0].role = %q, want system", role)
	}
	if role := gjson.GetBytes(got, "messages.1.role").String(); role != "user" {
		t.Fatalf("messages[1].role = %q, want user", role)
	}

	responsesPayload := executor.normalizeOpenCodeGoChatRoles(payload, "responses")
	if role := gjson.GetBytes(responsesPayload, "messages.0.role").String(); role != "developer" {
		t.Fatalf("responses messages[0].role = %q, want developer", role)
	}
}

func TestOpenCodeGoExecutorForwardsStableSessionHeader(t *testing.T) {
	executor := NewOpenCodeGoExecutor(nil)
	incoming := http.Header{
		"Session-Id": []string{"session-from-codex"},
		"Thread-Id":  []string{"thread-fallback"},
	}
	outgoing := make(http.Header)

	executor.applyOpenCodeGoSessionHeader(outgoing, incoming, nil)
	if got := outgoing.Get("x-opencode-session"); got != "session-from-codex" {
		t.Fatalf("x-opencode-session = %q, want session-from-codex", got)
	}

	outgoing.Set("x-opencode-session", "explicit-session")
	executor.applyOpenCodeGoSessionHeader(outgoing, incoming, nil)
	if got := outgoing.Get("x-opencode-session"); got != "explicit-session" {
		t.Fatalf("explicit x-opencode-session was overwritten: %q", got)
	}

	metadata := map[string]any{cliproxyexecutor.ExecutionSessionMetadataKey: "conversation-a"}
	first, second := make(http.Header), make(http.Header)
	executor.applyOpenCodeGoSessionHeader(first, nil, nil, metadata)
	executor.applyOpenCodeGoSessionHeader(second, nil, nil, metadata)
	if first.Get("x-opencode-session") == "" || first.Get("x-opencode-session") != second.Get("x-opencode-session") {
		t.Fatal("execution session must generate a stable provider session")
	}
	first, second = make(http.Header), make(http.Header)
	executor.applyOpenCodeGoSessionHeader(first, nil, nil)
	executor.applyOpenCodeGoSessionHeader(second, nil, nil)
	if _, err := uuid.Parse(first.Get("x-opencode-session")); err != nil {
		t.Fatalf("missing valid fallback session: %v", err)
	}
	if first.Get("x-opencode-session") == second.Get("x-opencode-session") {
		t.Fatal("unrelated sessionless requests must not share a fallback session")
	}
	other := make(http.Header)
	NewOpenAICompatExecutor("other-provider", nil).applyOpenCodeGoSessionHeader(other, nil, nil)
	if other.Get("x-opencode-session") != "" {
		t.Fatal("session header leaked to another provider")
	}
}
