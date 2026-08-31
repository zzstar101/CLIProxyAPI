package opencodego

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchModelsAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Fatalf("authorization header = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/usage":
			_, _ = w.Write([]byte(`{"usage":{"rolling":{"status":"ok","percent":12,"resetsAt":"2026-08-23T10:00:00Z"},"weekly":{"status":"ok","percent":20,"resetsAt":"2026-08-24T00:00:00Z"},"monthly":{"status":"ok","percent":30,"resetsAt":"2026-08-25T00:00:00Z"}}}`))
		case "/models":
			_, _ = w.Write([]byte(`{"data":[{"id":"opencode/gpt-5","object":"model","owned_by":"opencode"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	usage, err := FetchUsage(context.Background(), server.Client(), server.URL, "sk-test")
	if err != nil || usage.Rolling.Percent != 12 || usage.Monthly.Percent != 30 {
		t.Fatalf("FetchUsage() = %#v, %v", usage, err)
	}
	models, err := FetchModels(context.Background(), server.Client(), server.URL, "sk-test")
	if err != nil || len(models) != 1 || models[0].ID != "opencode/gpt-5" {
		t.Fatalf("FetchModels() = %#v, %v", models, err)
	}
}

func TestProtocolForModel(t *testing.T) {
	if got := ProtocolForModel("gpt-5.6-luna"); got != "responses" {
		t.Fatalf("ProtocolForModel(gpt-5.6-luna) = %q, want responses", got)
	}
	if got := ProtocolForModel("opencode/gpt-5.6-luna"); got != "responses" {
		t.Fatalf("ProtocolForModel(opencode/gpt-5.6-luna) = %q, want responses", got)
	}
	if got := ProtocolForModel("deepseek-v4-pro"); got != "chat" {
		t.Fatalf("ProtocolForModel(deepseek-v4-pro) = %q, want chat", got)
	}
}
