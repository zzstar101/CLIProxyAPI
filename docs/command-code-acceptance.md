# Command Code live acceptance — 2026-09-05

## Final verdict

**The tested Pro-account integration paths pass after fixes.** This is not an
all-plans or all-models certification. GOAT/Max accounts, Home subscriber mode and
live multi-account scheduling/affinity remain unverified.

Two explicitly authorized rounds were run against isolated local CPA instances.
The first stopped at its six-request limit. The user then authorized continued
small-batch testing and repairs until the tested paths passed. The second round
made 18 actual inference requests: 17 through CPA and one direct diagnostic.
Two additional local attempts encountered connection refusal before server
readiness and never reached CPA or the upstream service.

**Total actual inference requests across both rounds: 24.** Every request asked
for at most 128 output tokens. No model-requested tool was executed; tool-result
continuations used a synthetic `OK` value.

## Passed live checks

| Capability | Model / route | Evidence |
|---|---|---|
| Key import and subscription detection | Real Pro API key | `individual-pro-v1`, active; sanitized management responses |
| Catalog discovery and defaults | Real account catalog | 67 catalog models, 45 initially enabled; Claude/OpenAI/Gemini disabled |
| Duplicate and malformed-row handling | Management import | Duplicate skipped; invalid row failed independently; no key echoed |
| Model switches and persistence | DeepSeek V4 Flash | Disable removes model; refresh preserves choice; enable restores model |
| Nonstreaming text | DeepSeek V4 Flash → Chat / Responses / Messages | All returned expected `OK` |
| Streaming automatic tool selection | DeepSeek V4 Flash → Chat / Responses / Messages | Actual call ID, tool name, assembled JSON arguments and terminal events validated |
| Tool-result continuation | DeepSeek V4 Flash → Chat / Responses / Messages | Actual previous call ID plus synthetic tool result accepted; final `OK` |
| Named tool selection | GPT-5.4 mini → Responses, streaming and nonstreaming | `acceptance_probe`, `{"value":"OK"}`; client-schema request echo validated |
| Native Claude upstream streaming | Claude Haiku 4.5 → Responses | Named tool call completed; `message_stop` correctly terminated Responses stream |
| Native Claude upstream nonstreaming | Claude Haiku 4.5 → Chat and Responses | Nonempty `OK`, response ID and nonzero usage after aggregation fix |
| Vision | Claude Haiku 4.5 → Responses and Messages | Generated 64×64 red PNG correctly identified as `Red` |
| Thinking configuration and usage | DeepSeek V4 Flash → Responses | `reasoning.effort: low` accepted; expected `51`; 15 reasoning tokens retained |
| Quota reporting | Real account | Remaining credits, window usage/reset timestamps and zero purchased balance refreshed |

Claude and GPT models were enabled **only in the isolated acceptance account**.
The default policy in production code was not changed to enable these vendors.

## Findings and fixes

All targeted assertions were added to existing Command Code tests rather than
creating a separate redundant suite.

1. **Responses named-tool request shape.** The Responses-to-Chat translator copied
   `{ "type": "function", "name": "…" }` unchanged. Chat requires the name under
   `function.name`. Fixed in the shared translator; verified live using GPT mini
   in streaming and nonstreaming modes. Existing string and nested choices remain
   supported. No silent downgrade from named choice to automatic choice was added.

2. **Unknown thinking capabilities treated as unsupported.** The catalog supplies
   context length, not a thinking capability schema. Registering it as a known
   model with no thinking support caused explicit reasoning settings to be stripped.
   Registration now uses the existing unvalidated-model path (`UserDefined`) so
   CPA's canonical thinking pipeline preserves explicit intent and upstream remains
   the validator. No fabricated model capability table was introduced.

3. **Nonstreaming Responses reasoning-token usage.** Chat reports reasoning under
   `completion_tokens_details.reasoning_tokens`. The translator read only
   `output_tokens_details`. Added the proper field with the old form retained as a
   fallback; the live `low`-reasoning response retained its 15 reasoning tokens.

4. **Nonstreaming Responses request echo.** The response echoed upstream Chat tool
   definitions rather than the original Responses schema. It now follows the
   original-request preference already used in the streaming path. Verified live
   with named tool selection and flat Responses tool definitions.

5. **Responses string input to Claude.** The translator handled input arrays but
   silently dropped a string input, resulting in an empty upstream messages array
   and HTTP 400. Added the single-user-message mapping. Native Claude streaming
   with string input then passed.

6. **Empty nonstreaming Claude cross-protocol output.** The shared executor passed
   a normal Claude JSON response to translators designed to aggregate Claude SSE,
   producing HTTP 200 with empty content/identity/usage. It now follows the existing
   native Claude executor pattern: request upstream SSE for cross-protocol
   nonstreaming clients, validate completion, aggregate usage, and reuse existing
   nonstream translators. Native Messages JSON responses are unchanged. Both Chat
   and Responses paths then returned nonempty text with correct identity and usage.

The earlier implementation also fixed Claude stream terminal recognition:
`message_stop` is terminal without requiring the OpenAI `[DONE]` marker. Its
native upstream path was now verified live.

## Upstream restrictions, not silently bypassed

- DeepSeek rejected a named tool choice while thinking was active.
- Once CPA preserved explicit reasoning intent, Command Code rejected
  `reasoning_effort: none` with HTTP 400. Its error listed `low`, `medium`, `high`,
  `xhigh`, and `max`; **only `low` was positively verified here**.
- Automatic tool selection works on the tested DeepSeek model. Named selection
  works on tested GPT mini and Claude Haiku configurations. These observations do
  not guarantee those combinations for every model or future upstream version.
- CPA retries were disabled. An upstream error nevertheless reported internal
  gateway provider attempts; CPA cannot enforce the upstream's internal retry
  policy or guarantee that one downstream request equals one vendor attempt.

## Run accounting

### Round one

Six CPA requests: three nonstreaming text successes, three streaming named-tool
HTTP 400 failures. Successful responses reported 276 input and 39 output tokens.
The run stopped at the user-approved limit. The Responses selector bug was fixed
and tested offline before continued live authorization.

### Round two

18 actual requests: 16 HTTP 200 and two HTTP 400. One HTTP 200 was the empty Claude
translation defect described above, so HTTP status alone was not treated as a
pass. The two HTTP 400 responses were the explicit `none` reasoning rejection and
the subsequently fixed empty Claude input. Every discovered implementation defect
was followed by successful live checks of its affected behavior.

The direct diagnostic confirmed that the Claude endpoint returned a valid JSON
message with `OK`, isolating the empty-response defect to CPA's conversion path.
No automatic inference retries were used. Two pre-readiness connection refusals
were recorded separately, not counted as upstream calls or test successes.

## Quota and cleanup

| Upstream field | Before all tests | After round one | After round two |
|---|---:|---:|---:|
| Monthly credits remaining | 80 | 79.9999011886 | 79.9942138539 |
| Five-hour used | 0 | 0.0000988114 | 0.0057861461 |
| Weekly used | 0 | 0.0000988114 | 0.0057861461 |
| Purchased balance | 0 | 0 | 0 |

Total observed decrease: **0.0057861461 upstream credit units**. This is not a
claimed USD charge. The monthly granted-credit denominator was not supplied.

Both temporary acceptance keys were deleted through the official UI after their
rounds. Bearer-authenticated `/alpha/whoami` checks returned **401** after deletion;
refreshed key lists no longer contained the temporary keys and retained the earlier
verification key. That earlier key was deliberately not revoked.

The isolated server/callback processes, local temporary credentials, configurations,
raw diagnostic responses and test scripts were removed. No real credentials were
added to either repository. No deployment, commit or push was performed during
live acceptance; publication was authorized separately afterward.

## Final automated verification

- Eight relevant Go packages, including both affected translator packages: passed.
- Targeted race checks for the client, service and executor: passed.
- Required Go server build and `git diff --check`: passed.
- Frontend was unchanged in these acceptance rounds; the preceding 216-file,
  2835-test suite, type check and production build remain the recorded frontend
  verification. Browser UI checks used fixture accounts, not a claim of a live
  end-to-end management-panel import test.

## Rebase and publication verification

After separate user authorization to replay onto the latest remote version:

- CPA's two existing OpenCode commits and the Command Code feature were rebased
  onto upstream `main` / `v7.2.151` (`5208aec703b5ce7e3445f6e9d91cc13b3e78003a`).
- Manager upstream `main` already matched `v1.12.8`
  (`7c4cbeadaa801613e98ea6874b902844f09e59c6`); its OpenCode commits were retained.
- All 11 selected Go packages passed, including auth/session and OpenCode coverage;
  the targeted race checks and required server build also passed.
- Frontend type check, production single-file build and all 216 test files / 2835
  tests passed again. ESLint had no errors and one existing Fast Refresh warning
  in the unchanged `AccountHealthBadge.tsx`.
- `git diff --check` passed. Both pre-existing Dockerfile edits were restored
  byte-for-byte and excluded from feature commits.
- Live results above predate this rebase. No additional inference was performed
  for publication; the post-rebase verification was automated.

Publication branches in the user's forks:
`zzstar101/CLIProxyAPI:custom/command-code-v7.2.151` and
`zzstar101/CPA-Manager-Plus:custom/command-code-v1.12.8`.

## Remaining boundaries

- No GOAT or Max account was available for live validation.
- No live multi-account failover/session-affinity or Home subscriber validation.
- No exhaustive model/capability matrix; vision and thinking results apply to the
  explicitly tested models and request shapes only.
- DeepSeek named tools with disabled thinking remain unsupported by the tested
  upstream options; this was not converted into a false pass or masked by retries.
