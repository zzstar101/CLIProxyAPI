// Package opencodego provides a read-only client for the OpenCode Go subscription gateway.
package opencodego

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	ProviderName   = "opencode-go"
	DefaultBaseURL = "https://opencode.ai/zen/go/v1"
)

// UsageWindow describes a rolling OpenCode Go subscription window.
type UsageWindow struct {
	Status   string    `json:"status,omitempty"`
	Percent  int       `json:"percent"`
	ResetsAt time.Time `json:"resetsAt"`
}

type Usage struct {
	Rolling UsageWindow `json:"rolling"`
	Weekly  UsageWindow `json:"weekly"`
	Monthly UsageWindow `json:"monthly"`
}

type usageResponse struct {
	Usage Usage `json:"usage"`
}

type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

type modelsResponse struct {
	Data []Model `json:"data"`
}

// NormalizeProviderName accepts legacy aliases and returns the canonical provider name.
func NormalizeProviderName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "opencode-go", "opencode_go", "opencode go":
		return ProviderName
	default:
		return strings.TrimSpace(name)
	}
}

func IsProvider(name string) bool { return NormalizeProviderName(name) == ProviderName }

// ProtocolForModel returns the OpenAI protocol used by a model on the OpenCode Go gateway.
// GPT models use Responses; the remaining catalog currently uses Chat Completions.
func ProtocolForModel(model string) string {
	model = strings.ToLower(strings.TrimSpace(model))
	model = strings.TrimPrefix(model, "opencode/")
	if strings.HasPrefix(model, "gpt-") {
		return "responses"
	}
	return "chat"
}

func BaseURL(baseURL string) string {
	if trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/"); trimmed != "" {
		return trimmed
	}
	return DefaultBaseURL
}

func endpoint(baseURL, path string) string {
	return BaseURL(baseURL) + "/" + strings.TrimLeft(path, "/")
}

func doJSON(ctx context.Context, client *http.Client, baseURL, apiKey, path string, out any) error {
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint(baseURL, path), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("opencode go %s: upstream returned %d: %s", path, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("opencode go %s: invalid response: %w", path, err)
	}
	return nil
}

// FetchUsage returns the server-calculated five-hour, weekly, and monthly usage windows.
func FetchUsage(ctx context.Context, client *http.Client, baseURL, apiKey string) (Usage, error) {
	var response usageResponse
	if strings.TrimSpace(apiKey) == "" {
		return Usage{}, fmt.Errorf("opencode go: API key is empty")
	}
	if err := doJSON(ctx, client, baseURL, apiKey, "/usage", &response); err != nil {
		return Usage{}, err
	}
	return response.Usage, nil
}

// FetchModels returns the dynamically maintained model catalog for the subscription.
func FetchModels(ctx context.Context, client *http.Client, baseURL, apiKey string) ([]Model, error) {
	var response modelsResponse
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("opencode go: API key is empty")
	}
	if err := doJSON(ctx, client, baseURL, apiKey, "/models", &response); err != nil {
		return nil, err
	}
	return response.Data, nil
}
