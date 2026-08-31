package opencodego

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeviceLoginRefreshAndDefaultKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth/device/code":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"device_code":"device","user_code":"ABCD","verification_uri_complete":"https://example.test/verify","expires_in":30,"interval":0}`))
		case "/auth/device/token":
			if r.Method != http.MethodPost {
				t.Fatal("token endpoint must use POST")
			}
			body := mustReadBody(t, r)
			if strings.Contains(string(body), "device_code") {
				_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))
				return
			}
			_, _ = w.Write([]byte(`{"access_token":"access-2","refresh_token":"refresh-2","expires_in":3600}`))
		case "/api/user":
			_, _ = w.Write([]byte(`{"id":"user-1","email":"user@example.com"}`))
		case "/api/orgs":
			_, _ = w.Write([]byte(`[{"id":"org-1","name":"Personal"}]`))
		case "/api/config":
			_, _ = w.Write([]byte(`{"providers":{"opencode":{"options":{"apiKey":"sk-default"}}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	device, err := StartDeviceLogin(ctx, server.Client(), server.URL)
	if err != nil || device.DeviceCode != "device" {
		t.Fatalf("StartDeviceLogin() = %#v, %v", device, err)
	}
	device.Interval = 0
	session, err := PollDeviceLogin(ctx, server.Client(), server.URL, device)
	if err != nil {
		t.Fatalf("PollDeviceLogin() error = %v", err)
	}
	if session.WorkspaceID != "org-1" {
		t.Fatalf("workspace = %q", session.WorkspaceID)
	}
	key, err := FetchDefaultAPIKey(ctx, server.Client(), session)
	if err != nil || key != "sk-default" {
		t.Fatalf("FetchDefaultAPIKey() = %q, %v", key, err)
	}
	refreshed, err := RefreshSession(ctx, server.Client(), session)
	if err != nil || refreshed.AccessToken != "access-2" {
		t.Fatalf("RefreshSession() = %#v, %v", refreshed, err)
	}
}

func mustReadBody(t *testing.T, r *http.Request) []byte {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestStartDeviceLoginResolvesRelativeVerificationURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"device_code":"device","user_code":"ABCD","verification_uri_complete":"/console/device?user_code=ABCD","expires_in":900,"interval":5}`))
	}))
	defer server.Close()

	code, err := StartDeviceLogin(context.Background(), server.Client(), server.URL+"/console")
	if err != nil {
		t.Fatal(err)
	}
	want := server.URL + "/console/device?user_code=ABCD"
	if code.VerificationURIComplete != want {
		t.Fatalf("verification URL = %q, want %q", code.VerificationURIComplete, want)
	}
}

func TestSessionMetadataRoundTrip(t *testing.T) {
	want := Session{AccessToken: "a", RefreshToken: "r", ExpiresAt: time.Now().UTC().Add(time.Hour), Server: ConsoleServer, WorkspaceID: "w", DefaultAPIKey: "sk-key"}
	got, err := ParseSessionMetadata(SessionMetadata(want))
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != want.AccessToken || got.RefreshToken != want.RefreshToken || got.DefaultAPIKey != want.DefaultAPIKey {
		t.Fatalf("round trip = %#v, want %#v", got, want)
	}
}
