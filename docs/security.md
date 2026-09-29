# Security model

## Credential ownership

- OpenAI access, refresh, and ID tokens exist only in encrypted router storage and transient router memory.
- OpenRouter API keys exist only in encrypted router storage and transient router memory.
- The helper receives OAuth authorization codes but never final OpenAI tokens.
- The Mac stores only its device credential and local helper secret in Keychain.
- Codex receives only a short-lived loopback credential.
- Claude Code credentials never reach OpenCDX. Claude Code signs in and talks to Anthropic itself; OpenCDX does not route, proxy, or authenticate its requests and offers no Claude sign-in.

AES-256-GCM envelopes use per-record random nonces and authenticated associated data. Stable ChatGPT account IDs are represented outside the envelope only by SHA-256 duplicate-detection hashes. Device and enrollment credentials are also stored as hashes; the one-time device issue is separately encrypted until acknowledgement.

## Network boundary

- The helper binds IPv4 loopback only.
- OAuth callbacks bind registered localhost ports only.
- Non-loopback helper/router HTTP is rejected unless insecure development mode is explicitly enabled.
- Production Compose publishes only Caddy on 80/443; the router remains on a private Docker network.
- WebSockets are not advertised or implemented.

## Request privacy

The helper reverse proxy does not parse inference request bodies. The router reads only the JSON model route and supported-control fields needed for routing/validation. It does not log request or response bodies. A bounded in-memory response tail extracts token counts and selected response diagnostics and is then discarded. Administrator-only [request logs](request-logs.md) retain model controls, timings, opaque request/thread/session/account/device identifiers, masked account labels, token counts, outcomes, and bounded provider error messages. Credential patterns in diagnostics are redacted; provider error text can still include provider-supplied context. User prompt and generated-output fields and arbitrary headers are excluded. Separately, [instruction history](instruction-history.md) retains compressed versions of provider-supplied catalogue instructions and model-message policies; it does not inspect conversation prompts. Log exports contain the same metadata and should be kept private.

Native OpenAI request bodies remain byte-for-byte unchanged. Third-party catalog entries use an intentionally empty instruction template required by Codex's catalog schema; the router does not author or inject a provider persona, system prompt, or model-name instruction.

Daily telemetry contains provider, routed model, opaque internal account key, request count, and token totals. The dashboard combines account rows for usage series. Account-attributed allowance history and reset markers include opaque router account IDs and masked labels; they never include upstream account IDs or credentials. Telemetry contains no prompts or responses.

The dashboard and paired-device reset operations delete only those aggregate
rows, allowance observations, Claude Code request digests, and their reconciliation metadata. They do not access Codex rollout files or Claude Code transcripts
and do not delete accounts, devices, providers, catalogs, routing state, or request logs.

Dashboard cost figures are estimates. The router refreshes the unauthenticated public OpenRouter model catalog, applies exact published input/output token prices to matching routed model IDs, and leaves unmatched models visibly unpriced. It does not present subscription usage as a bill or invent a cost for local Ollama execution.

### Claude Code observation

The optional [Claude Code integration](claude-code.md) adds two local inputs to
the helper, both limited to loopback:

- `POST /claude/statusline` accepts the normal five-minute local credential.
  The `claude-statusline` command reads the status line JSON Claude Code
  provides and sends only the session ID and the five-hour and weekly
  rate-limit windows. It runs the user's original status line command with
  the same input, as that user, exactly as Claude Code would have.
- `POST /claude/otlp/v1/logs` accepts only a separate, telemetry-scoped local
  credential issued by `claude-otel-headers`. That credential lasts one hour
  and is rejected by inference routes; inference credentials are rejected
  here. Only `api_request` events are kept; every other event, including
  prompt and response events, is discarded without being stored or logged.

On the Mac, the helper replaces the Claude account UUID with a SHA-256 digest
and masks the email before anything leaves the machine. Unsent observations
stay in helper memory only, up to 20,000 requests. The router stores that
digest, the masked email, the last reporting machine, the latest windows,
allowance readings, token counters, and a digest of each Anthropic request ID
for deduplication. The router rejects reports with unknown fields or account
references, future timestamps, or out-of-range counters.

Transcript import reads Claude Code's local JSONL files on the Mac and uploads
only request IDs, timestamps, model IDs, and token counts. Setup edits
`~/.claude/settings.json` only after an explicit preview and confirmation,
keeps a backup, refuses to replace telemetry settings the user configured, and
restores the original status line on removal.

## Header policy

The router removes hop-by-hop headers, cookies, forwarded credentials, device/local authorization, `ChatGPT-Account-ID`, FedRAMP selection, API keys, and OpenAI organization/project selection. It installs only the selected upstream authentication.

For native OpenAI Responses requests, the router retains only the upstream
`__oailb` infrastructure routing cookie and replays it to the same HTTPS ChatGPT
origin. Cookie jars are held in memory separately for each selected account and
exact origin; they honor cookie domain, path, expiry, deletion, and Secure rules.
The first-party host allowlist matches Codex (`chatgpt.com` and its subdomains,
`chat.openai.com`, and `chatgpt-staging.com` and its subdomains). Exact-origin
isolation is deliberately stricter than cookie Domain scope. Other configured
upstream hosts do not use this jar. No cookie values reach request logs, the
database, local clients, OpenRouter, or Ollama. Router restarts discard the jars.
Client-supplied cookies remain blocked, and upstream `Set-Cookie` remains
stripped from client responses. Native inference redirects are returned without
following them so selected-account credentials and cookies cannot move to an
unselected destination. Cookies received on a pre-stream authentication or quota
failure stay with that account when the router retries or selects a fallback.

For native OpenAI routes, all other Codex feature metadata is preserved, including current `x-codex-*`, `originator`, `version`, `session-id`, `thread-id`, `OpenAI-Beta`, `User-Agent`, subagent/memory/lite flags, Responses API feature headers, and attestation when Codex supplies it. OpenAI-only feature and attestation headers are removed before OpenRouter or Ollama requests; provider-neutral HTTP metadata and each destination's own headers remain intact.

## Retry policy

The router may replay once after an upstream 401 following refresh, or after a recognized quota 429 on a different eligible account. Both decisions happen before headers or body bytes are sent to Codex. Once streaming begins, any disconnect is returned as-is and never replayed.

## Operations

- Remove a lost Mac in the dashboard immediately; removal deletes its device row and credential.
- Pause an account to keep it stored but ineligible.
- Remove an account/provider to delete its encrypted credentials.
- Rotate the administrator token by updating its Docker secret and recreating the container.
- Preserve the master key when restoring the database; rotate it only through a planned re-encryption migration.
- Keep `docker/secrets/`, `.env`, database files, and build artifacts out of source control and Docker build contexts.
