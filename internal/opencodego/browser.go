package opencodego

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ReferralReward mirrors the read-only reward rows rendered by the Go Console.
type ReferralReward struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Status      string `json:"status"`
	Email       string `json:"email,omitempty"`
	Amount      int    `json:"amount"`
	TimeCreated string `json:"time_created,omitempty"`
	TimeApplied string `json:"time_applied,omitempty"`
}

type ReferralSummary struct {
	ReferralCode string           `json:"referral_code,omitempty"`
	HasReferral  bool             `json:"has_referral"`
	RewardAmount int              `json:"reward_amount,omitempty"`
	Rewards      []ReferralReward `json:"rewards"`
}

// FetchReferralSummary reads the server-rendered /go page with a browser
// session cookie. The cookie is used only for this request and is never stored.
func FetchReferralSummary(ctx context.Context, client *http.Client, server, workspaceID, cookie string) (ReferralSummary, error) {
	page, err := fetchBrowserPage(ctx, client, server, workspaceID, cookie, "go")
	if err != nil {
		return ReferralSummary{}, err
	}
	summary := parseReferralSummary(page)
	if summary.ReferralCode == "" && len(summary.Rewards) == 0 {
		return ReferralSummary{}, fmt.Errorf("opencode console: referral summary was not rendered")
	}
	return summary, nil
}

// FetchDefaultAPIKeyFromBrowserSession reads the existing key shown by the
// workspace keys page. It never creates or mutates a key.
func FetchDefaultAPIKeyFromBrowserSession(ctx context.Context, client *http.Client, server, workspaceID, cookie string) (string, error) {
	page, err := fetchBrowserPage(ctx, client, server, workspaceID, cookie, "keys")
	if err != nil {
		return "", err
	}
	if match := serializedDefaultAPIKeyPattern.FindStringSubmatch(page); len(match) == 2 && len(match[1]) >= 20 {
		return match[1], nil
	}
	matches := regexp.MustCompile(`sk-[A-Za-z0-9]+`).FindAllString(page, -1)
	best := ""
	for _, candidate := range matches {
		if len(candidate) > len(best) {
			best = candidate
		}
	}
	if len(best) < 20 {
		return "", fmt.Errorf("opencode console: workspace keys page exposes no concrete default API key")
	}
	return best, nil
}

// FetchBrowserSessionEmail reads the account email rendered by the workspace Console.
func FetchBrowserSessionEmail(ctx context.Context, client *http.Client, server, workspaceID, cookie string) (string, error) {
	page, err := fetchBrowserPage(ctx, client, server, workspaceID, cookie, "keys")
	if err != nil {
		return "", err
	}
	if email := parseBrowserSessionEmail(page); email != "" {
		return email, nil
	}
	return "", fmt.Errorf("opencode console: workspace keys page exposes no account email")
}

func fetchBrowserPage(ctx context.Context, client *http.Client, server, workspaceID, cookie, pagePath string) (string, error) {
	workspaceID = strings.TrimSpace(workspaceID)
	cookie = strings.TrimSpace(cookie)
	if workspaceID == "" {
		return "", fmt.Errorf("opencode console: workspace_id is required")
	}
	if cookie == "" {
		return "", fmt.Errorf("opencode console: browser cookie is required")
	}
	endpoint := strings.TrimRight(strings.TrimSpace(server), "/")
	if endpoint == "" {
		endpoint = "https://opencode.ai"
	}
	endpoint += "/workspace/" + url.PathEscape(workspaceID) + "/" + strings.Trim(pagePath, "/")
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "text/html")
	req.Header.Set("Cookie", cookie)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("opencode console: workspace page returned %d", resp.StatusCode)
	}
	page := string(body)
	if strings.Contains(page, "<title>OpenAuth") || strings.Contains(page, "/authorize?") {
		return "", fmt.Errorf("opencode console: browser session is not authenticated")
	}
	return page, nil
}

var referralCodePattern = regexp.MustCompile(`(?:[?&]ref=)([A-Za-z0-9]+)`)
var referralEmailPattern = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
var serializedReferralCodePattern = regexp.MustCompile(`referralCode:"([^"]+)"`)
var serializedReferralAmountPattern = regexp.MustCompile(`rewardAmount:(\d+)`)
var serializedReferralRewardPattern = regexp.MustCompile(`id:"([^"]+)",source:"([^"]*)",status:"([^"]*)",email:"([^"]*)",amount:(-?\d+)(?:,timeCreated:\$R\[\d+\]=new Date\("([^"]*)"\))?(?:,timeApplied:\$R\[\d+\]=new Date\("([^"]*)"\))?`)
var serializedDefaultAPIKeyPattern = regexp.MustCompile(`name:"Default API Key",key:"(sk-[A-Za-z0-9]+)"`)

// parseBrowserSessionEmail extracts the account email from the server state.
func parseBrowserSessionEmail(page string) string {
	start := strings.Index(page, "userEmail")
	if start < 0 {
		return ""
	}
	end := strings.Index(page[start:], "key.list[")
	if end < 0 || end > 8192 {
		end = 8192
		if remaining := len(page) - start; remaining < end {
			end = remaining
		}
	}
	return referralEmailPattern.FindString(page[start : start+end])
}

func parseReferralSummary(page string) ReferralSummary {
	summary := ReferralSummary{Rewards: make([]ReferralReward, 0)}
	if match := serializedReferralCodePattern.FindStringSubmatch(page); len(match) == 2 {
		summary.ReferralCode = strings.ToUpper(match[1])
	}
	if match := serializedReferralAmountPattern.FindStringSubmatch(page); len(match) == 2 {
		summary.RewardAmount, _ = strconv.Atoi(match[1])
	}
	for _, match := range serializedReferralRewardPattern.FindAllStringSubmatch(page, -1) {
		if len(match) != 8 {
			continue
		}
		amount, _ := strconv.Atoi(match[5])
		summary.Rewards = append(summary.Rewards, ReferralReward{
			ID: match[1], Source: match[2], Status: match[3], Email: match[4], Amount: amount,
			TimeCreated: match[6], TimeApplied: match[7],
		})
	}
	if match := referralCodePattern.FindStringSubmatch(page); len(match) == 2 {
		if summary.ReferralCode == "" {
			summary.ReferralCode = strings.ToUpper(match[1])
		}
	}
	doc, err := html.Parse(strings.NewReader(page))
	if err != nil {
		return summary
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "tr" {
			status := attr(node, "data-status")
			source := attr(node, "data-source")
			if status != "" || source != "" {
				cells := elementTexts(node, "td")
				reward := ReferralReward{Status: status, Source: source}
				if len(cells) > 0 {
					reward.Amount = parseDollarAmount(cells[0])
				}
				if len(cells) > 1 {
					reward.Email = strings.TrimSpace(cells[1])
					if match := referralEmailPattern.FindString(reward.Email); match != "" {
						reward.Email = match
					}
				}
				if len(cells) > 2 {
					reward.TimeCreated = strings.TrimSpace(cells[2])
				}
				reward.ID = fmt.Sprintf("%s:%d", reward.Source, len(summary.Rewards))
				summary.Rewards = append(summary.Rewards, reward)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(doc)
	summary.HasReferral = len(summary.Rewards) > 0 || summary.ReferralCode != ""
	if summary.RewardAmount == 0 {
		for _, reward := range summary.Rewards {
			if reward.Amount > 0 {
				summary.RewardAmount += reward.Amount
			}
		}
	}
	return summary
}

func attr(node *html.Node, name string) string {
	for _, item := range node.Attr {
		if item.Key == name {
			return strings.TrimSpace(item.Val)
		}
	}
	return ""
}

func elementTexts(node *html.Node, tag string) []string {
	var out []string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.ElementNode && current.Data == tag {
			out = append(out, strings.Join(textNodes(current), " "))
			return
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return out
}

func textNodes(node *html.Node) []string {
	var out []string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current.Type == html.TextNode {
			if value := strings.TrimSpace(current.Data); value != "" {
				out = append(out, value)
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return out
}

func parseDollarAmount(value string) int {
	value = strings.TrimSpace(strings.TrimPrefix(value, "$"))
	if value == "" {
		return 0
	}
	if dot := strings.IndexByte(value, '.'); dot >= 0 {
		whole, _ := strconv.Atoi(value[:dot])
		fraction := strings.TrimRight(value[dot+1:], "0")
		if len(fraction) == 1 {
			fraction += "0"
		}
		cents, _ := strconv.Atoi(fraction)
		return whole*100 + cents
	}
	whole, _ := strconv.Atoi(value)
	return whole * 100
}
