package cliproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	internalregistry "github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
)

func TestRegisterModelsForAuthOpenCodeGoFetchesDynamicCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/models" {
			http.NotFound(w, request)
			return
		}
		if request.Header.Get("Authorization") != "Bearer test-key" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"open-model-a","object":"model","owned_by":"opencode"},{"id":"open-model-b"}]}`))
	}))
	defer server.Close()

	auth := &coreauth.Auth{
		ID:       "opencode-go-dynamic-models",
		Provider: "opencode-go",
		Status:   coreauth.StatusActive,
		Attributes: map[string]string{
			"api_key":  "test-key",
			"base_url": server.URL,
		},
	}
	registry := internalregistry.GetGlobalRegistry()
	registry.UnregisterClient(auth.ID)
	t.Cleanup(func() { registry.UnregisterClient(auth.ID) })

	service := &Service{cfg: &config.Config{}}
	service.registerModelsForAuth(context.Background(), auth)

	models := registry.GetModelsForClient(auth.ID)
	if len(models) != 2 {
		t.Fatalf("expected 2 OpenCode Go models, got %d", len(models))
	}
	if models[0].ID != "open-model-a" || models[1].ID != "open-model-b" {
		t.Fatalf("unexpected model IDs: %q, %q", models[0].ID, models[1].ID)
	}
}
