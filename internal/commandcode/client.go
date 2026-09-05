// Package commandcode implements the Command Code account and model service.
// Browser cookies and CLI processes are deliberately not part of its interface.
package commandcode

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const Provider = "command-code"
const APIBaseURL = "https://api.commandcode.ai"
const ProviderBaseURL = APIBaseURL + "/provider/v1"

var ErrUnsupportedPlan = errors.New("command-code: only GOAT, Pro and Max subscriptions are supported")
var ErrInactiveSubscription = errors.New("command-code: subscription is inactive")

// Client uses the caller's transport, cancellation and proxy policy. It never
// follows redirects carrying credentials or includes response bodies in errors.
type Client struct {
	http    *http.Client
	baseURL string
	key     string
}

func NewClient(client *http.Client, baseURL, key string) *Client {
	if client == nil {
		client = http.DefaultClient
	}
	copyClient := *client
	copyClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if baseURL == "" {
		baseURL = APIBaseURL
	}
	return &Client{http: &copyClient, baseURL: strings.TrimRight(baseURL, "/"), key: strings.TrimSpace(key)}
}

// APIError intentionally contains no upstream-controlled message or credential.
type APIError struct {
	Status   int
	Endpoint string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("command-code: %s returned HTTP %d", e.Endpoint, e.Status)
}
func (c *Client) get(ctx context.Context, path string, out any) error {
	if c.key == "" {
		return fmt.Errorf("command-code: API key is required")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("command-code: invalid endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+c.key)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("command-code: account service unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return &APIError{Status: resp.StatusCode, Endpoint: strings.SplitN(path, "?", 2)[0]}
	}
	// Catalogs are bounded data, not inference streams.
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024+1))
	if err != nil || len(data) > 4*1024*1024 {
		return fmt.Errorf("command-code: invalid account response")
	}
	if json.Unmarshal(data, out) != nil {
		return fmt.Errorf("command-code: invalid account response")
	}
	return nil
}

type Subscription struct {
	PlanID             string `json:"planId"`
	Status             string `json:"status"`
	CurrentPeriodStart string `json:"currentPeriodStart"`
	CurrentPeriodEnd   string `json:"currentPeriodEnd"`
}
type Window struct {
	Used     float64 `json:"used"`
	Cap      float64 `json:"cap"`
	Exceeded bool    `json:"exceeded"`
	ResetAt  int64   `json:"resetAt"`
}
type Credits struct {
	Monthly   float64  `json:"monthlyCredits"`
	Purchased float64  `json:"purchasedCredits"`
	Free      float64  `json:"freeCredits,omitempty"`
	Granted   *float64 `json:"monthlyCreditsGranted,omitempty"`
	Premium   *float64 `json:"premiumMonthlyCredits,omitempty"`
	Standard  *float64 `json:"opensourceMonthlyCredits,omitempty"`
}
type Usage struct {
	Credits Credits `json:"credits"`
	Windows struct {
		Limited  bool    `json:"limited"`
		Exceeded any     `json:"exceeded"`
		FiveHour *Window `json:"fiveHour"`
		Weekly   *Window `json:"weekly"`
	} `json:"windowLimits"`
}
type Account struct {
	ID           string       `json:"account_id"`
	Subscription Subscription `json:"subscription"`
	Usage        Usage        `json:"usage"`
}
type Model struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	OwnedBy       string `json:"owned_by"`
	ContextLength int    `json:"context_length"`
}

func (c *Client) Account(ctx context.Context) (Account, error) {
	var result Account
	var who struct {
		Success bool `json:"success"`
		User    *struct {
			ID string `json:"id"`
		} `json:"user"`
		Org *struct {
			ID string `json:"id"`
		} `json:"org"`
	}
	if err := c.get(ctx, "/alpha/whoami", &who); err != nil {
		return result, err
	}
	if !who.Success || who.User == nil || who.User.ID == "" {
		return result, fmt.Errorf("command-code: missing account identity")
	}
	// Only personal GOAT/Pro/Max accounts are supported; do not silently consume
	// an organization pool using a personal label.
	if who.Org != nil && who.Org.ID != "" {
		return result, fmt.Errorf("command-code: organization accounts are not supported")
	}
	result.ID = who.User.ID
	var sub struct {
		Success bool          `json:"success"`
		Data    *Subscription `json:"data"`
	}
	if err := c.get(ctx, "/alpha/billing/subscriptions", &sub); err != nil {
		return result, err
	}
	if !sub.Success || sub.Data == nil {
		return result, fmt.Errorf("command-code: subscription is unavailable")
	}
	result.Subscription = *sub.Data
	if !SupportedPlan(sub.Data.PlanID) {
		return result, ErrUnsupportedPlan
	}
	if !ActiveSubscription(sub.Data.Status) {
		return result, ErrInactiveSubscription
	}
	var raw json.RawMessage
	if err := c.get(ctx, "/alpha/billing/credits", &raw); err != nil {
		return result, err
	}
	var required struct {
		Credits *struct {
			Monthly   *float64 `json:"monthlyCredits"`
			Purchased *float64 `json:"purchasedCredits"`
		} `json:"credits"`
	}
	if json.Unmarshal(raw, &required) != nil || required.Credits == nil || required.Credits.Monthly == nil || required.Credits.Purchased == nil || json.Unmarshal(raw, &result.Usage) != nil {
		return result, fmt.Errorf("command-code: incomplete quota response")
	}
	return result, nil
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	var response struct {
		Data []Model `json:"data"`
	}
	if err := c.get(ctx, "/provider/v1/models", &response); err != nil {
		return nil, err
	}
	if response.Data == nil {
		return nil, fmt.Errorf("command-code: model catalog is unavailable")
	}
	seen := make(map[string]bool, len(response.Data))
	models := make([]Model, 0, len(response.Data))
	for _, model := range response.Data {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" || strings.ContainsAny(model.ID, "\r\n\x00") || seen[model.ID] {
			continue
		}
		if _, err := url.Parse(model.ID); err != nil {
			continue
		}
		seen[model.ID] = true
		models = append(models, model)
	}
	return models, nil
}
