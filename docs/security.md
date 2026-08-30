# Security model

## Credential ownership

- OpenAI access, refresh, and ID tokens exist only in encrypted router storage and transient router memory.
- OpenRouter API keys exist only in encrypted router storage and transient router memory.
- The helper receives OAuth authorization codes but never final OpenAI tokens.
- The Mac stores only its device credential and local helper secret in Keychain.
- Codex receives only a short-lived loopback credential.

AES-256-GCM envelopes use per-record random nonces and authenticated associated data. Stable ChatGPT account IDs are represented outside the envelope only by SHA-256 duplicate-detection hashes. Device and enrollment credentials are also stored as hashes; the one-time device issue is separately encrypted until acknowledgement.

## Network boundary

- The helper binds IPv4 loopback only.
- OAuth callbacks bind registered localhost ports only.
- Non-loopback helper/router HTTP is rejected unless insecure development mode is explicitly enabled.
- Production Compose publishes only Caddy on 80/443; the router remains on a private Docker network.
- WebSockets are not advertised or implemented.

## Request privacy

The helper reverse proxy does not parse inference request bodies. The router reads only the JSON model route and supported-control fields needed for routing/validation. It does not log request or response bodies. A bounded in-memory response tail is used only to extract aggregate token counts and is then discarded.

Native OpenAI request bodies remain byte-for-byte unchanged. Third-party catalog entries use an intentionally empty instruction template required by Codex's catalog schema; the router does not author or inject a provider persona, system prompt, or model-name instruction.

Daily telemetry contains provider, routed model, opaque internal account key, request count, and token totals. The dashboard telemetry endpoint combines account rows and never returns the internal account key. It contains no prompts or responses.

The dashboard and paired-device reset operations delete only those aggregate
rows and their reconciliation metadata. They do not access Codex rollout files
and do not delete accounts, devices, providers, catalogs, or routing state.

Dashboard cost figures are estimates. The router refreshes the unauthenticated public OpenRouter model catalog, applies exact published input/output token prices to matching routed model IDs, and leaves unmatched models visibly unpriced. It does not present subscription usage as a bill or invent a cost for local Ollama execution.

## Header policy

The router removes hop-by-hop headers, cookies, forwarded credentials, device/local authorization, `ChatGPT-Account-ID`, FedRAMP selection, API keys, and OpenAI organization/project selection. It installs only the selected upstream authentication.

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
