# Command Code provider

Command Code is a native `command-code` provider backed by API-key-authenticated
subscription model service. It does not run the Command Code CLI or retain web
sessions. The management panel entry is **AI providers → Command Code**.

## Accounts

Supported plan IDs are `individual-goat`, `individual-pro`, `individual-pro-v1`,
`individual-max`, and `individual-ultra`. The last two display as Max 10× and
Max 20×. Other plans and organization credentials are rejected.

Import either a single email label and API key, or up to 100 lines of:

```text
user@example.com,sk-example
another@example.com,sk-another-example
```

The email is only a label. `/alpha/whoami` determines identity;
`/alpha/billing/subscriptions` determines subscription, and
`/alpha/billing/credits` supplies quota. Imports validate each account separately,
permit partial success, and skip duplicate keys/accounts without replacing them.
Use account editing to rotate a key; replacement keys must resolve to the same
upstream identity. Existing per-account model choices survive key rotation.

Credentials use the existing auth store. Filenames derive from a hash of the
upstream account ID, not an email or secret. Management account responses never
include keys. Access these management endpoints over authenticated HTTPS and
keep credential files and backups private.

## Models and routing

`/provider/v1/models` returns a catalog, not an entitlement list. On the verified
Pro account, authenticated and unauthenticated requests returned identical IDs.
CPA applies billing-category and blocked-model rules checked against the
published `command-code@1.49.1` CLI. Unknown model categories follow its permissive
behavior; upstream authorization remains authoritative. Rule changes require
maintenance when Command Code changes existing subscription entitlements.

Claude, OpenAI and Gemini models start disabled **per account**. They can be
explicitly enabled in that account's model settings. New models from other
vendors default to enabled, subject to entitlements. Explicit choices survive
catalog refresh. Additional upstream balance can unlock models just as in the
CLI; it never bypasses an explicit account-level disable.

Models participate in the existing same-ID mixed-provider routing, configured
round-robin/fill-first selection, retry/cooling rules and session affinity. This
provider does not introduce an independent scheduler. Existing global exclusions
and aliases still apply. The executor rechecks account model eligibility so an
alias cannot bypass a per-account disable.

OpenAI Chat Completions, Responses, and Anthropic Messages clients use the
existing translators, including streaming and tool calling. Upstream Claude
requests use `/provider/v1/messages`; other models use
`/provider/v1/chat/completions`. Responses compact and image-generation endpoints
are unsupported. The public catalog supplies context length but does not supply
a complete capabilities schema. Discovered models use the existing unvalidated
thinking path: explicit user settings are preserved for upstream validation, not
silently removed. This is not a blanket vision/reasoning guarantee. In live tests,
DeepSeek accepted `low` effort but Command Code rejected `none`; automatic tools
worked on DeepSeek, while named tools were verified on GPT mini and Claude Haiku.

## Discovery and quota

Import and manual refresh persist a complete account/catalog snapshot. Model
registration itself performs no network access. In ordinary CPA server mode, a
cancellable background worker refreshes runtime snapshots after startup and
every five minutes without repeatedly rewriting credential files. Transient
failures retain the previous snapshot and expose an error; definitive invalid
credentials, unsupported plans or inactive subscriptions remove eligibility.
Home subscriber mode does not run this discovery worker and requires separately
validated integration before deployment with this provider.

The panel shows the timestamp of the last successful sync. Window percentages
are calculated from upstream `used / cap`; monthly percentage is shown only if
upstream explicitly supplies `monthlyCreditsGranted`. Otherwise only remaining
credits are displayed. A zero reset timestamp is not rendered as a date. Credits
are not assumed to be interchangeable dollar balances across models.

CPA neither enables top-ups nor enforces subscription-only billing. Command Code
may spend additional balance after subscription/window limits. Consult upstream
billing settings before using funded accounts.

## Management API

All paths below are under the existing authenticated `/v0/management` namespace:

- `GET /command-code/accounts`: sanitized cached account/model/quota views.
- `POST /command-code/accounts/import`: `{ "items": [{ "email": "…", "api_key": "…" }] }`.
- `PATCH /command-code/accounts?id=…`: optional `email`, `api_key`, and a
  `model_overrides` map of model IDs to booleans (merged with existing choices).
- `POST /command-code/accounts/refresh?id=…`: refresh and persist discovery.

Use the existing account controls for disabling or deleting credential files.

## Verification boundary

The current Pro-account acceptance paths passed after fixes: all three client
interfaces, streaming tool calls and tool-result continuations, named tools on
GPT mini, native Claude inference, Claude image input, and DeepSeek `low` thinking
with reasoning-token usage. See [the acceptance report](command-code-acceptance.md)
for the tested matrix, repaired conversion boundaries, upstream restrictions,
quota changes and temporary-key revocation. This is not an all-model guarantee.
GOAT/Max accounts, live multi-account scheduling/affinity and Home subscriber mode
remain unverified.
