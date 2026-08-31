package opencodego

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestFetchReferralSummaryFromBrowserSession(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "auth=test" {
			t.Fatal("browser cookie was not forwarded")
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<a href="/go?ref=abc123">invite</a><table><tr data-status="available" data-source="inviter"><td>$5</td><td>friend@example.com</td><td>Aug 23, 2026</td><td>View</td></tr></table>`))
	}))
	defer server.Close()
	summary, err := FetchReferralSummary(context.Background(), nil, server.URL, "wrk_test", "auth=test")
	if err != nil {
		t.Fatal(err)
	}
	if summary.ReferralCode != "ABC123" || len(summary.Rewards) != 1 || summary.Rewards[0].Amount != 500 || summary.Rewards[0].Status != "available" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestFetchReferralSummaryRejectsLoginPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<title>OpenAuth</title><a href="/authorize?x=1">login</a>`))
	}))
	defer server.Close()
	if _, err := FetchReferralSummary(context.Background(), nil, server.URL, "wrk_test", "auth=test"); err == nil {
		t.Fatal("expected unauthenticated error")
	}
}

func TestParseCurrentOpenCodeSerializedBrowserState(t *testing.T) {
	page := `<script>self.$R=self.$R||[];_$HY.r["userEmail[\"wrk_test\"]"]=$R[0]=$R[1];$R[2]($R[1],"owner@example.com");$R[2]($R[4],"owner@example.com");$R[2]($R[14],$R[39]={referralCode:"ABC123",hasReferral:!0,rewardAmount:1000,rewards:$R[40]=[$R[41]={id:"ref_1",source:"inviter",status:"applied",email:"friend@example.com",amount:500,timeCreated:$R[42]=new Date("2026-08-23T00:00:00Z"),timeApplied:$R[43]=new Date("2026-08-23T01:00:00Z")},$R[44]={id:"ref_2",source:"inviter",status:"pending",email:"other@example.com",amount:500,timeCreated:$R[45]=new Date("2026-08-24T00:00:00Z"),timeApplied:null}]});$R[2]($R[20],[$R[24]={id:"key_1",name:"Default API Key",key:"sk-defaultkey123456789012345678901234567890",email:"owner@example.com"}]);</script>`
	if got := parseBrowserSessionEmail(page); got != "owner@example.com" {
		t.Fatalf("email = %q", got)
	}
	key := serializedDefaultAPIKeyPattern.FindStringSubmatch(page)
	if len(key) != 2 || key[1] != "sk-defaultkey123456789012345678901234567890" {
		t.Fatalf("default key was not parsed: %#v", key)
	}
	summary := parseReferralSummary(page)
	if summary.ReferralCode != "ABC123" || summary.RewardAmount != 1000 || len(summary.Rewards) != 2 {
		t.Fatalf("unexpected serialized summary: %+v", summary)
	}
}

func TestLiveReferralSummaryFromBrowserSession(t *testing.T) {
	cookie := os.Getenv("OPENCODE_GO_BROWSER_COOKIE")
	workspaceID := os.Getenv("OPENCODE_GO_BROWSER_WORKSPACE")
	if cookie == "" || workspaceID == "" {
		t.Skip("set OPENCODE_GO_BROWSER_COOKIE and OPENCODE_GO_BROWSER_WORKSPACE for live testing")
	}
	summary, err := FetchReferralSummary(context.Background(), nil, "https://opencode.ai", workspaceID, cookie)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ReferralCode == "" || len(summary.Rewards) == 0 {
		t.Fatalf("live referral summary is incomplete: %+v", summary)
	}
	t.Logf("live referral summary: code_present=%t reward_count=%d reward_amount=%d", summary.ReferralCode != "", len(summary.Rewards), summary.RewardAmount)
	key, err := FetchDefaultAPIKeyFromBrowserSession(context.Background(), nil, "https://opencode.ai", workspaceID, cookie)
	if err != nil {
		t.Fatal(err)
	}
	if len(key) < 20 {
		t.Fatalf("live default API key is incomplete: length=%d", len(key))
	}
	t.Logf("live default API key: present=true length=%d", len(key))
}
