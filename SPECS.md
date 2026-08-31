## Objective

Build a small, reliable provider router for Codex with:

- A central service that runs in Docker on any suitable LAN machine.
- A local companion on every Codex machine.
- A native macOS menu-bar interface for the companion.
- Explicit browser login for every OpenAI account managed by the router.
- OpenAI, OpenRouter, and eventually Ollama support.
- Accurate model capabilities and account-aware routing.
- No Codex executable replacement, prompt rewriting, or existing-login scraping.

Implement and validate the project end to end. Do not copy OpenCodex wholesale. Consult OpenCodex or codex-lb only for narrowly identified behavior such as OAuth fields, quota endpoints, or account-rotation edge cases.

### Non-negotiable invariants

1. Never inspect or import credentials from:

   - Codex’s OS-keyring entry
   - `~/.codex/auth.json`
   - ChatGPT desktop-app storage
   - Browser cookies
   - Another CLI’s credential files

2. Every OpenAI account must complete a fresh login initiated by this router.

3. Use the normal browser authorization-code flow with PKCE. Do not use OAuth device-code login.

4. A login created for the router has exactly one refresh owner: the remote router. Never copy a refresh token into multiple processes.

5. Do not replace, rename, wrap, shim, or modify the Codex executable.

6. Do not automatically rewrite `~/.codex/config.toml`. Generate a documented snippet that the user copies manually.

7. Never modify native OpenAI model definitions. Preserve every field exactly, including internal and safety-related models such as auto-review models.

8. Never inject provider prompts or rename the model inside its system instructions.

9. Never advertise reasoning levels or features that the destination model does not support.

10. Never log prompts, responses, bearer tokens, refresh tokens, API keys, OAuth codes, or account identifiers.

### Architecture

#### 1. Remote router

Implement the central service as a standalone Go service packaged in Docker.

Recommended layout:

```text
cmd/routerd/
cmd/router-helper/
internal/accounts/
internal/catalog/
internal/providers/openai/
internal/providers/openrouter/
internal/providers/ollama/
internal/routing/
internal/storage/
internal/telemetry/
internal/devices/
web/
mac/
docker/
docs/
```

The remote router owns:

- OpenAI OAuth transactions and token exchange
- All OpenAI access and refresh credentials
- OpenRouter API credentials
- Account refresh and rotation
- Account quota, plan, and entitlement state
- Provider model discovery
- Canonical merged catalogs
- Routing and conversation affinity
- Aggregate usage telemetry
- Device registration
- Web management dashboard

Use SQLite for metadata. Encrypt credentials at the application layer using an authenticated cipher. Load the master encryption key from a Docker secret file mounted separately from the database. Refuse to start with credential storage enabled when no valid master key is available.

The service must support clean container restarts without losing accounts, catalog snapshots, quotas, device registrations, or routing configuration.

#### 2. Local helper

Implement a small cross-platform helper daemon, initially targeting macOS.

It owns:

- A loopback-only Codex-compatible Responses endpoint
- Authentication to the remote router
- Atomic model-catalog downloads
- The localhost OAuth callback listener
- Read-only Codex-version detection
- Status data for the menu-bar application
- A command that supplies short-lived authentication to Codex

It must not store OpenAI or OpenRouter credentials.

Codex traffic flows through it:

```text
Codex → 127.0.0.1 helper → remote router → selected provider
```

This hides the remote address and router credential from Codex and lets the menu-bar app observe connectivity and active routing without reading prompt content.

#### 3. macOS menu-bar application

Build a native SwiftUI `MenuBarExtra` application with no Dock icon.

It should display:

- Remote router connection state
- Current request model and provider
- Active OpenAI account, masked
- Quota remaining and reset time
- Catalog synchronization state
- Connected/degraded/error state

Its menu should provide:

- Open dashboard
- Add OpenAI account
- Refresh quotas
- Refresh model catalog
- Reconnect
- Copy Codex configuration
- Launch at login
- Quit helper

Bundle the helper with the app. Use normal macOS login-item facilities; do not install a system daemon or require administrator privileges.

### OpenAI account login

Use this exact ownership flow:

1. An authenticated administrator clicks **Add OpenAI account** in the dashboard or menu app.
2. The remote router creates:

   - A short-lived OAuth transaction
   - Random state
   - PKCE verifier and challenge
   - A five-minute expiration

3. The Mac helper starts a loopback callback listener on the registered localhost callback port.
4. The helper opens the browser authorization URL.
5. Force an explicit login/account choice; do not silently adopt the browser’s currently active account.
6. The browser redirects the authorization code to the local helper.
7. The helper forwards only the short-lived code, state, and transaction identifier to the remote router over authenticated TLS.
8. The remote router validates state and exchanges the code using its stored PKCE verifier.
9. The remote router stores the resulting access and refresh tokens in encrypted storage.
10. Immediately fetch:

    - Stable account identity
    - Masked email
    - Plan
    - Current quota windows
    - Reset credits, when available
    - Model entitlements

11. Display the account as ready without requiring an inference request.

The helper must never receive the final access or refresh tokens.

Adding the same account twice must be detected using the stable ChatGPT account ID. Offer to replace its credentials; never silently create duplicate pool entries.

This is a separate OAuth session from any Codex or ChatGPT login already present on the machine. Existing logins must remain untouched and irrelevant.

### Codex integration

Generate a configuration resembling:

```toml
model_provider = "opencdx"
model_catalog_json = "/Users/USER/Library/Application Support/com.dodelidoo.opencdx/catalog.json"

[model_providers.opencdx]
name = "Router"
base_url = "http://127.0.0.1:PORT/v1"
wire_api = "responses"
supports_websockets = false

[model_providers.opencdx.auth]
command = "/path/to/router-helper"
args = ["token"]
timeout_ms = 5000
refresh_interval_ms = 300000
```

Requirements:

- Do not set `requires_openai_auth = true`.
- Codex must work whether it is signed into ChatGPT or completely signed out.
- The helper’s token command returns a short-lived local credential.
- The helper validates and removes that credential before forwarding.
- The remote connection uses a separate device credential.
- Advertise WebSocket support only after it is fully implemented and tested.
- Because Codex loads `model_catalog_json` at startup, notify the user when Codex must restart to consume an updated catalog.

### Model catalog

#### OpenAI

For each router-managed OpenAI account:

1. Fetch its entitled catalog using that account’s router-owned credentials.
2. Store the raw account-specific snapshot.
3. Copy native model entries into the merged catalog without deleting, renaming, rewriting, or defaulting fields.
4. Maintain account eligibility separately from the catalog entry.
5. Never remove unknown models merely because the router does not recognize them.
6. Never hardcode a fixed OpenAI model list.

Use the union of account-entitled models only if routing can guarantee that a request is assigned to an eligible account. Otherwise expose the safe intersection.

When two accounts return conflicting definitions for the same model ID, retain one complete upstream definition—preferably the designated primary account’s—and report the conflict. Do not field-merge incompatible definitions.

#### OpenRouter

Fetch OpenRouter’s live catalog periodically.

Create Codex entries only when the metadata proves that the model satisfies the required protocol and tool capabilities. Translate metadata conservatively:

- Context size
- Input modalities
- Tool/function calling
- Structured output
- Reasoning support
- Supported reasoning controls
- Image input
- Streaming

Do not infer unsupported reasoning levels from model names. If required capabilities cannot be established, exclude the model with a visible explanation instead of fabricating metadata.

Use namespaced route identities where necessary, for example:

```text
openrouter/deepseek/deepseek-r1
```

The router removes only its routing namespace before sending the upstream request.

#### Ollama

Prepare the provider interface for Ollama.

Initially support an Ollama endpoint reachable from the remote router. Per-Mac local Ollama routing through the helper can be a later phase and must not delay the OpenAI/OpenRouter MVP.

### Request routing

For native OpenAI models:

- Leave the request body and model ID unchanged.
- Replace only authentication and account-selection headers.
- Preserve Codex metadata and feature headers unless they are hop-by-hop or security-sensitive.
- Do not inject instructions.
- Do not translate native reasoning settings.
- Select only accounts entitled to the requested model.

For OpenRouter models:

- Translate only what the destination protocol requires.
- Reject unsupported parameters clearly.
- Never translate `ultra`, Fast mode, or another OpenAI-only feature into invented provider behavior.

Maintain sticky account affinity per Codex thread/session. Do not rotate accounts on every request.

When an account is exhausted:

- Rebind the next request to another eligible account.
- Retry automatically only if no streamed output was emitted and replay is demonstrably safe.
- Never replay a partially emitted response.

Use a single-flight lock for every account’s refresh-token rotation.

### Devices and networking

Support multiple Codex machines connected to the same remote router.

Each helper must:

- Generate its own identity.
- Request enrollment.
- Appear as pending in the dashboard.
- Require explicit administrator approval.
- Store its issued credential in macOS Keychain.
- Be independently revocable.

Generate catalogs per device when Codex versions require different catalog schemas.

Never use unencrypted HTTP over a LAN. Support deployment behind Tailscale, Caddy, or another TLS reverse proxy. Refuse non-loopback plaintext remote URLs unless the user explicitly enables an insecure development mode.

### Dashboard

The remote web interface should include:

- OpenAI accounts, plans, quotas, pauses, and reauthentication state
- Provider configuration
- Discovered and excluded models
- Exclusion reasons
- Connected devices
- Per-provider/model/account aggregate usage
- Catalog refresh status
- Routing strategy
- Credential removal
- Health and diagnostics

Use server-rendered templates and minimal JavaScript so the project does not require an npm runtime or a large frontend dependency stack.

Never display fake “connected” states. An account is connected only after its credential has been validated and its identity fetched.

### Provider architecture

Define stable interfaces for:

- Authentication
- Credential refresh
- Model discovery
- Capability translation
- Quota collection
- Responses execution
- Health reporting

Keep provider implementations out of the routing core.

Do not implement unsafe in-process dynamic libraries initially. Prepare for future external plugins through a versioned provider protocol or separately executed provider process.

### Required research gates

Before relying on an assumption, prove and document:

1. A router-owned browser PKCE grant can fetch OpenAI account identity, quota, and entitled models without inference.
2. Two separately authenticated OpenAI accounts remain independently refreshable.
3. Codex works signed out when configured with the custom router provider.
4. The external catalog preserves native model behavior and reasoning options.
5. OpenRouter exposes enough metadata to make conservative capability decisions.
6. Every Codex request header required by gated models such as Daybreak is identified and preserved.
7. Native auto-review and safety models remain present and functional.

Use current Codex open-source code and official documentation as the primary references. If a fundamental assumption fails, stop and report the evidence. Do not replace it with a hardcoded approximation.

### Verification

Automated tests must cover:

- OAuth state and PKCE validation
- Duplicate-account detection
- Encrypted credential persistence
- Refresh-token single-flight behavior
- Exact OpenAI catalog-entry preservation
- Model entitlement routing
- Quota exhaustion and account switching
- No retry after partial streaming
- Header preservation and authentication replacement
- OpenRouter capability mapping
- Unsupported reasoning suppression
- Atomic catalog updates
- Device approval and revocation
- Secret/log redaction
- Docker restart recovery

Human end-to-end verification must include:

1. Start with a fresh Docker volume.
2. Use a Mac where Codex is signed out.
3. Install and pair the helper.
4. Add two OpenAI accounts through separate browser logins.
5. Confirm plan, quota, and models appear before inference.
6. Add OpenRouter.
7. Copy the generated Codex configuration manually.
8. Confirm `/model` contains the full entitled OpenAI roster plus compatible OpenRouter models.
9. Run one native OpenAI model.
10. Run one OpenRouter model.
11. Pause the first OpenAI account and confirm routing uses the second.
12. Restart the Docker service and confirm accounts remain usable.
13. Connect a second Codex machine.
14. Sign Codex or ChatGPT into an unrelated account and confirm router behavior is unchanged.
15. Remove the helper and confirm Codex itself and its native authentication remain untouched.

### Non-goals for the first release

Do not implement:

- Claude Code
- Claude, Gemini, Grok, Kimi, or numerous speculative providers
- Prompt/persona rewriting
- Codex executable shims
- Automatic `config.toml` mutation
- Browser-cookie or keyring scraping
- OAuth device-code login
- Dynamic binary plugins
- Cloud exposure without TLS
- Performance benchmarking of individual models

### Definition of done

The project is complete when:

- The Docker router can run independently on a LAN machine.
- Multiple Macs can connect through their local helpers.
- Every OpenAI account is explicitly logged in through the router.
- Existing Codex/ChatGPT authentication has no effect.
- Account information exists immediately after login.
- OpenAI catalog entries remain unmodified.
- OpenRouter models expose only verified capabilities.
- Account switching works without refresh-token races.
- The macOS menu item accurately reflects router state.
- Fresh-install and restart tests pass.
- Uninstalling the helper leaves Codex untouched.
