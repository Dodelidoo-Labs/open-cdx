# Helper and Codex setup

## Pair a Mac

The menu app is the normal interface. The equivalent CLI flow is:

```sh
router-helper enroll --router https://router.example.com --name "Work Mac" --no-wait
# Approve the pending device in the dashboard.
router-helper pair
router-helper daemon
```

On macOS, the enrollment secret, one-time issued device token, and local token secret are stored through macOS Keychain. The JSON helper config contains the router URL, opaque device ID, loopback port, and catalog path only. It never contains OpenAI or OpenRouter credentials.

Each helper has an independently revocable device credential. Approval delivers it exactly once; the helper acknowledges receipt, after which the router removes the encrypted issuance copy.

## Add OpenAI accounts

Use the menu app or:

```sh
router-helper login-openai
```

The helper binds `localhost:1455`, falling back to `1457`, before asking the router to start OAuth. It opens the browser, validates returned state locally, and forwards only code, state, and transaction ID to the router. The router owns the PKCE verifier and token exchange.

For an already-registered stable ChatGPT account, the first login is rejected as a duplicate. Explicitly choose replacement and repeat the browser login:

```sh
router-helper login-openai --replace
```

No existing Codex, ChatGPT app, browser-cookie, keyring, or `~/.codex/auth.json` state is read.

## Generate Codex configuration

```sh
router-helper sync-catalog
router-helper config
```

The generated snippet has this shape:

```toml
model_provider = "opencdx"
model_catalog_json = "/Users/USER/Library/Application Support/OpenCDX Router/catalog.json"

[model_providers.opencdx]
name = "OpenCDX Router"
base_url = "http://127.0.0.1:17464/v1"
wire_api = "responses"
supports_websockets = false

[model_providers.opencdx.auth]
command = "/path/to/OpenCDX Router.app/Contents/Resources/router-helper"
args = ["token"]
timeout_ms = 5000
refresh_interval_ms = 300000
```

The provider ID is deliberately `opencdx`, rather than the generic `router`.
Codex records this ID in rollout metadata, which lets reconciliation identify
OpenCDX-routed work without confusing it with another custom proxy.

Copy it manually into `~/.codex/config.toml`. Do not add `requires_openai_auth = true`. The command returns a five-minute HMAC-authenticated local token. The loopback daemon removes that token and authenticates remotely with the separate device credential.

Codex must restart after a catalog file changes. The menu app and catalog endpoint report this explicitly.

## Reconcile usage history

The menu app previews and imports only the default Codex home at `~/.codex`.
Before replacement it shows the resolved directory plus scanned-file and
routed/native request counts. It never discovers or combines other Codex homes.

Use the helper directly when Codex runs with a custom `CODEX_HOME`:

```sh
router-helper reconcile-usage --codex-home /absolute/path/to/codex-home --dry-run
# Review the routed/native counts, then replace telemetry:
router-helper reconcile-usage --codex-home /absolute/path/to/codex-home
```

The chosen directory is the complete reconciliation source. A successful run
replaces existing telemetry rather than merging it, so importing a different
home later also replaces the previous snapshot. Prompts, responses, paths,
credentials, and account identifiers are never sent to the router.

## Useful commands

| Command | Effect |
|---|---|
| `router-helper token` | Print only a short-lived local credential for Codex |
| `router-helper status` | Read helper/menu status as JSON |
| `router-helper refresh-catalog` | Refresh providers, atomically download catalog |
| `router-helper refresh-quotas` | Refresh account quotas |
| `router-helper reconnect` | Recheck remote connectivity |
| `router-helper reconcile-usage [--codex-home PATH] [--dry-run]` | Preview or replace telemetry from one Codex history root |
| `router-helper open-dashboard` | Open the configured dashboard |
| `router-helper quit` | Stop the user helper daemon |
| `router-helper config` | Print, but never install, the Codex TOML snippet |

## Uninstall

`scripts/uninstall-macos-app.sh` stops the helper, removes its three Keychain entries, and moves the app and its application-support folder to Trash. It deliberately does not read or change `~/.codex`, the Codex executable, or native Codex authentication. Remove the manually pasted provider snippet yourself if desired.
