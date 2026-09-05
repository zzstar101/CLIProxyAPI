package management

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/commandcode"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	sdkAuth "github.com/router-for-me/CLIProxyAPI/v7/sdk/auth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCommandCodeImportDuplicateAndModelUpdatePersistence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := sdkAuth.NewFileTokenStore()
	manager := coreauth.NewManager(store, nil, nil)
	h := NewHandler(&config.Config{AuthDir: t.TempDir()}, "", manager)
	h.tokenStore = store
	auth := &coreauth.Auth{ID: "command-code-test.json", FileName: "command-code-test.json", Provider: commandcode.Provider, Label: "test@example.com", Status: coreauth.StatusActive, Metadata: map[string]any{"type": commandcode.Provider, "auth_kind": "apikey", "email": "test@example.com", "api_key": "test-secret"}}
	commandcode.SetSnapshot(auth, commandcode.Snapshot{Account: commandcode.Account{ID: "a", Subscription: commandcode.Subscription{PlanID: "individual-pro-v1", Status: "active"}}, Models: []commandcode.Model{{ID: "claude-sonnet-5"}}})
	if err := h.persistCommandCode(t.Context(), auth); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.POST("/import", h.ImportCommandCodeAccounts)
	router.PATCH("/account", h.UpdateCommandCodeAccount)
	router.GET("/accounts", h.GetCommandCodeAccounts)
	invoke := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	result := invoke(http.MethodPost, "/import", `{"items":[{"email":"other@example.com","api_key":"test-secret"},{"email":"sk-misplaced-secret","api_key":""}]}`)
	var batch struct {
		Items []commandCodeImportResult `json:"items"`
	}
	if json.Unmarshal(result.Body.Bytes(), &batch) != nil || len(batch.Items) != 2 {
		t.Fatalf("invalid batch response: %s", result.Body.String())
	}
	if batch.Items[0].Status != "skipped" || batch.Items[1].Status != "failed" || strings.Contains(result.Body.String(), "secret") {
		t.Fatalf("incorrect or unsafe import result: %s", result.Body.String())
	}
	updated := invoke(http.MethodPatch, "/account?id=command-code-test.json", `{"model_overrides":{"claude-sonnet-5":true}}`)
	if updated.Code != 200 {
		t.Fatalf("update failed: %s", updated.Body.String())
	}
	persisted, err := store.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) != 1 || !commandcode.Overrides(persisted[0])["claude-sonnet-5"] {
		t.Fatal("model override did not survive persistence")
	}
	listed := invoke(http.MethodGet, "/accounts", "")
	if listed.Code != 200 || strings.Contains(listed.Body.String(), "test-secret") {
		t.Fatalf("unsafe account listing: %s", listed.Body.String())
	}
	if commandcode.APIKey(h.commandCodeAuth(auth.ID)) != "test-secret" {
		t.Fatal("model edit replaced credential")
	}
}
