package cliproxy

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/commandcode"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	"github.com/tidwall/gjson"
)

func TestCommandCodeDiscoveryCommitPreservesEditsAndRejectsStaleKey(t *testing.T) {
	manager := coreauth.NewManager(nil, nil, nil)
	auth := &coreauth.Auth{ID: "command-code-refresh-test", Provider: commandcode.Provider, Metadata: map[string]any{"api_key": "new-key"}}
	original := commandcode.Snapshot{Account: commandcode.Account{ID: "a", Subscription: commandcode.Subscription{PlanID: "individual-pro-v1", Status: "active"}}, Models: []commandcode.Model{{ID: "deepseek/model"}}, FetchedAt: time.Unix(100, 0)}
	commandcode.SetSnapshot(auth, original)
	commandcode.SetOverrides(auth, map[string]bool{"deepseek/model": false})
	if _, err := manager.Register(t.Context(), auth); err != nil {
		t.Fatal(err)
	}
	s := &Service{cfg: &config.Config{}, coreManager: manager}
	next := original
	next.FetchedAt = time.Unix(200, 0)
	next.Account.Usage.Credits.Monthly = 20
	s.commitCommandCodeDiscovery(t.Context(), auth.ID, "old-key", next)
	current, _ := manager.GetByID(auth.ID)
	snapshot, _ := commandcode.ReadSnapshot(current)
	if !snapshot.FetchedAt.Equal(original.FetchedAt) {
		t.Fatal("old key overwrote current account")
	}
	s.commitCommandCodeDiscovery(t.Context(), auth.ID, "new-key", next)
	current, _ = manager.GetByID(auth.ID)
	snapshot, _ = commandcode.ReadSnapshot(current)
	if snapshot.Account.Usage.Credits.Monthly != 20 {
		t.Fatal("quota was not refreshed")
	}
	if enabled, ok := commandcode.Overrides(current)["deepseek/model"]; !ok || enabled {
		t.Fatal("refresh lost the concurrent model edit")
	}
	s.commitCommandCodeDiscovery(t.Context(), auth.ID, "new-key", original)
	current, _ = manager.GetByID(auth.ID)
	snapshot, _ = commandcode.ReadSnapshot(current)
	if !snapshot.FetchedAt.Equal(next.FetchedAt) {
		t.Fatal("older discovery overwrote newer snapshot")
	}
}

func TestCommandCodeRegistrationUsesAccountPolicyAndNativeExecutor(t *testing.T) {
	auth := &coreauth.Auth{ID: "command-code-registration-test", Provider: commandcode.Provider, Status: coreauth.StatusActive, Metadata: map[string]any{"api_key": "key", "auth_kind": "apikey"}, Attributes: map[string]string{"compat_name": "legacy", "provider_key": "legacy"}}
	commandcode.SetSnapshot(auth, commandcode.Snapshot{Account: commandcode.Account{ID: "a", Subscription: commandcode.Subscription{PlanID: "individual-pro-v1", Status: "active"}}, Models: []commandcode.Model{{ID: "deepseek/deepseek-v4-flash", ContextLength: 1000000}, {ID: "claude-sonnet-5"}, {ID: "new-owner/new-model"}}})
	reg := registry.GetGlobalRegistry()
	t.Cleanup(func() { reg.UnregisterClient(auth.ID) })
	s := &Service{cfg: &config.Config{}, coreManager: coreauth.NewManager(nil, nil, nil)}
	s.registerExecutorForAuth(auth, false)
	native, ok := s.coreManager.Executor(commandcode.Provider)
	if !ok {
		t.Fatal("native executor missing")
	}
	if _, ok := native.(*executor.CommandCodeExecutor); !ok {
		t.Fatalf("wrong executor: %T", native)
	}
	s.registerModelsForAuth(context.Background(), auth)
	if models := reg.GetModelsForClient(auth.ID); len(models) != 2 {
		t.Fatalf("default model count=%d", len(models))
	}
	body, err := thinking.ApplyThinking([]byte(`{"reasoning_effort":"none"}`), "deepseek-v4-flash", "openai", "openai", commandcode.Provider)
	if err != nil || gjson.GetBytes(body, "reasoning_effort").String() != "none" {
		t.Fatalf("unknown thinking capability stripped explicit user intent: %s, %v", body, err)
	}
	commandcode.SetOverrides(auth, map[string]bool{"claude-sonnet-5": true, "deepseek/deepseek-v4-flash": false})
	s.registerModelsForAuth(context.Background(), auth)
	ids := map[string]bool{}
	for _, model := range reg.GetModelsForClient(auth.ID) {
		ids[model.ID] = true
	}
	if !ids["claude-sonnet-5"] || ids["deepseek-v4-flash"] || !ids["new-model"] || ids["new-owner/new-model"] {
		t.Fatalf("account overrides not registered: %v", ids)
	}
	auth.Disabled = true
	s.registerModelsForAuth(context.Background(), auth)
	if len(reg.GetModelsForClient(auth.ID)) != 0 {
		t.Fatal("disabled account retained models")
	}
}
