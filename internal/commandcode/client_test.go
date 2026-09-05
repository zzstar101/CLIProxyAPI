package commandcode

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestAccountDiscoveryAndCredentialIsolation(t *testing.T) {
	const key = "secret-test-key"
	var redirectHit bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirectHit = true }))
	defer destination.Close()
	redirect := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("missing credential")
		}
		if redirect {
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/alpha/whoami":
			_, _ = w.Write([]byte(`{"success":true,"user":{"id":"account-1"},"org":null}`))
		case "/alpha/billing/subscriptions":
			_, _ = w.Write([]byte(`{"success":true,"data":{"planId":"individual-pro-v1","status":"active"}}`))
		case "/alpha/billing/credits":
			_, _ = w.Write([]byte(`{"credits":{"monthlyCredits":80,"purchasedCredits":0},"windowLimits":{"fiveHour":{"used":4,"cap":16,"resetAt":0}}}`))
		case "/provider/v1/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"deepseek/deepseek-v4-flash","context_length":1000000},{"id":"deepseek/deepseek-v4-flash"}]}`))
		default:
			w.WriteHeader(401)
			_, _ = w.Write([]byte(key))
		}
	}))
	defer server.Close()
	client := NewClient(server.Client(), server.URL, key)
	account, err := client.Account(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != "account-1" || account.Subscription.PlanID != "individual-pro-v1" || account.Usage.Windows.FiveHour.Used != 4 {
		t.Fatalf("incorrect discovery: %+v", account)
	}
	models, err := client.Models(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("catalog: %v %v", models, err)
	}
	var out any
	err = client.get(context.Background(), "/invalid", &out)
	if err == nil || strings.Contains(err.Error(), key) {
		t.Fatalf("unsafe error: %v", err)
	}
	redirect = true
	_, err = client.Account(context.Background())
	if err == nil || redirectHit {
		t.Fatalf("credential redirect followed: %v", err)
	}
}

func TestAccountModelPolicyAndPersistence(t *testing.T) {
	account := Account{ID: "a", Subscription: Subscription{PlanID: "individual-goat", Status: "active"}}
	cases := []struct {
		id, owner string
		want      bool
	}{
		{"deepseek/deepseek-v4-flash", "deepseek", true},
		{"new-vendor/new-model", "new-vendor", true},
		{"gpt-5.6-sol", "gateway", false},
		{"claude-sonnet-5", "gateway", false},
		{"google/gemini-3.8-flash", "gateway", false},
		{"opaque-new-id", "openai", false},
		{"meta/muse-spark-1.1", "meta", false},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			m := Model{ID: tc.id, OwnedBy: tc.owner}
			if got := Enabled(account, m, nil); got != tc.want {
				t.Fatalf("enabled=%v, want %v", got, tc.want)
			}
		})
	}
	claude := Model{ID: "claude-sonnet-5"}
	override := map[string]bool{claude.ID: true}
	if Enabled(account, claude, override) {
		t.Fatal("override bypassed subscription")
	}
	account.Subscription.PlanID = "individual-pro-v1"
	if !Enabled(account, claude, override) {
		t.Fatal("explicit enable ignored")
	}
	opus := Model{ID: "claude-opus-5"}
	override[opus.ID] = true
	if Enabled(account, opus, override) {
		t.Fatal("Pro acquired Max-only model")
	}
	account.Usage.Credits.Purchased = 1
	if !Enabled(account, opus, override) {
		t.Fatal("CLI extra-credit rule not applied")
	}
	override[opus.ID] = false
	if Enabled(account, opus, override) {
		t.Fatal("extra credits bypassed explicit disable")
	}
	account.Subscription.PlanID = "individual-provider"
	if Enabled(account, Model{ID: "new-model"}, nil) {
		t.Fatal("unsupported plan enabled")
	}
	account.Subscription.PlanID = "individual-ultra"
	auth := &coreauth.Auth{Metadata: map[string]any{"api_key": "not-a-real-key"}}
	SetSnapshot(auth, Snapshot{Account: account, Models: []Model{opus}})
	SetOverrides(auth, override)
	raw, err := json.Marshal(auth.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := &coreauth.Auth{}
	if err = json.Unmarshal(raw, &reloaded.Metadata); err != nil {
		t.Fatal(err)
	}
	snapshot, ok := ReadSnapshot(reloaded)
	if !ok || snapshot.Account.ID != "a" || Overrides(reloaded)[opus.ID] || APIKey(reloaded) != "not-a-real-key" {
		t.Fatal("credential round trip lost account policy")
	}
}
