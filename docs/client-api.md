# Local client API

The OpenCDX helper exposes an authenticated local API at
`http://127.0.0.1:17464/v1` by default. A client connects to this endpoint with a
short-lived local bearer token; provider credentials remain with the router.

## Command credentials

On macOS, obtain renewable command credentials with:

```sh
"/Applications/OpenCDX Router.app/Contents/Resources/router-helper" token --json
```

The JSON result contains `access_token` and `expires_in` (seconds). Clients must
renew the token before expiry and send it as `Authorization: Bearer <token>`.
Without `--json`, the command returns only the token for clients such as Codex
that consume plain command output. The token is a credential; do not log it.

## Live models

Authenticated `GET /v1/models` returns an OpenAI-style list with `object: "list"`
and `data` entries containing model IDs and advertised capabilities. It reads
the same atomically synchronized catalog as Codex on every request. Visible
model additions, removals, context windows, reasoning efforts and defaults,
modalities, and service tiers come from that catalog. Private instructions and
hidden models are excluded.

The helper synchronizes with the router every minute; provider refresh
intervals still apply. Responses include an ETag covering IDs and metadata, plus
`Cache-Control: private, no-cache`. Clients should revalidate instead of retaining
a permanent model snapshot. A matching `If-None-Match` receives `304`. A valid
empty catalog returns an empty `data` array; unavailable or invalid catalogs
return `503`. Discovery does not count as inference usage.

## Responses and cache routing

Use `POST /v1/responses` for inference and `POST /v1/responses/compact` for native
OpenAI compaction. Third-party support depends on the selected provider. The
router resolves the model ID and supplies the selected provider's authentication.

For native OpenAI Responses, official Codex supplies a stable cache scope in the
body's `prompt_cache_key` and, for root agents, the same value in the `session-id`
header. `thread-id` and `x-client-request-id` carry the actual thread identity.
OpenCDX preserves these fields and `x-codex-turn-state` without rewriting them.
Account selection uses `thread-id`, falling back to `session-id`; this is
separate from OpenAI's upstream cache routing.

The router retains the upstream `__oailb` routing cookie per account and HTTPS
origin. Client-supplied cookies remain blocked, and upstream cookies are not
returned to clients. See [Security](security.md#header-policy) for the complete
scope and redirect policy. These mechanisms preserve routing hints; actual cache
reuse depends on the provider and matching prompt prefixes. Inspect reported
`usage.input_tokens_details.cached_tokens` to measure it.

Third-party client source changes, packaging, and update automation belong in
those clients' own repositories; OpenCDX distributes only its server and macOS
companion/helper.
