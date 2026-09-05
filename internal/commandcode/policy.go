package commandcode

import "strings"

func SupportedPlan(plan string) bool {
	switch plan {
	case "individual-goat", "individual-pro", "individual-pro-v1", "individual-max", "individual-ultra":
		return true
	}
	return false
}
func ActiveSubscription(status string) bool {
	switch status {
	case "active", "trialing", "past_due":
		return true
	}
	return false
}

// These billing facts were checked against command-code@1.49.1's model registry
// and plan gates. Marketing ownership and billing category are independent:
// e.g. GPT Sol/Luna and recent Gemini Flash models bill as standard.
// Unknown models follow the CLI's permissive category behavior. Ownership
// defaults still apply, and an upstream permission error remains authoritative.
var premiumModels = map[string]bool{
	"claude-sonnet-5": true, "claude-sonnet-4-6": true,
	"claude-fable-5-1": true, "claude-fable-5": true,
	"claude-opus-5": true, "claude-opus-4-8": true, "claude-opus-4-7": true,
	"claude-haiku-4-5-20251001": true, "claude-haiku-4-5": true,
	"gpt-5.6-terra": true, "gpt-6-astra": true, "gpt-5.5": true, "gpt-5.4": true,
	"gpt-5.3-codex": true, "gpt-5.4-mini": true,
	"google/gemini-3.5-flash": true, "google/gemini-3.1-flash-lite": true,
	"sakana/fugu-ultra": true, "meta/muse-spark-1.1": true,
}
var proBlockedModels = map[string]bool{
	"claude-fable-5-1": true, "claude-fable-5": true, "claude-opus-5": true,
	"claude-opus-4-8": true, "claude-opus-4-7": true, "claude-opus-4-6": true,
	"claude-opus-4-5-20251101": true, "gpt-6-astra": true, "sakana/fugu-ultra": true,
}

// DefaultEnabled uses model identity as well as owned_by: a serving gateway is
// not necessarily the model's originating company.
func DefaultEnabled(model Model) bool {
	owner := strings.ToLower(strings.TrimSpace(model.OwnedBy))
	id := strings.ToLower(model.ID)
	leaf := id[strings.LastIndex(id, "/")+1:]
	if owner == "anthropic" || owner == "openai" || owner == "google" || owner == "gemini" {
		return false
	}
	return !(strings.HasPrefix(id, "anthropic/") || strings.HasPrefix(id, "openai/") || strings.HasPrefix(leaf, "claude-") || strings.HasPrefix(leaf, "gpt-") || strings.HasPrefix(leaf, "gemini-") || strings.HasPrefix(leaf, "chatgpt-") || strings.HasPrefix(leaf, "o1") || strings.HasPrefix(leaf, "o3") || strings.HasPrefix(leaf, "o4"))
}

func Entitled(account Account, model Model) bool {
	if !SupportedPlan(account.Subscription.PlanID) || !ActiveSubscription(account.Subscription.Status) {
		return false
	}
	if account.Usage.Credits.Purchased > 0 || account.Usage.Credits.Free > 0 {
		return true
	}
	id := strings.ToLower(model.ID)
	switch account.Subscription.PlanID {
	case "individual-goat":
		return !premiumModels[id]
	case "individual-pro", "individual-pro-v1":
		return !proBlockedModels[id]
	default:
		return true
	}
}

// Overrides are per-account explicit choices, not a materialized exclusion list.
// This preserves user choices across refresh while new models inherit defaults.
func Enabled(account Account, model Model, overrides map[string]bool) bool {
	if !Entitled(account, model) {
		return false
	}
	if enabled, ok := overrides[model.ID]; ok {
		return enabled
	}
	return DefaultEnabled(model)
}
func Protocol(model string) string {
	leaf := strings.ToLower(model[strings.LastIndex(model, "/")+1:])
	if strings.HasPrefix(leaf, "claude-") {
		return "messages"
	}
	return "chat"
}
