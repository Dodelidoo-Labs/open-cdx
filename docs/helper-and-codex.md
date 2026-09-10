# Helper and Codex setup

The signed macOS companion from [GitHub Releases](https://github.com/Dodelidoo-Labs/open-cdx/releases/latest) is the normal interface. It includes the helper; users do not download or install `router-helper` separately. The commands below document the equivalent or advanced flows.

## Pair a Mac

The equivalent CLI flow is:

```sh
router-helper enroll --router https://router.example.com --name "Work Mac" --no-wait
# Approve the pending device in the dashboard.
router-helper pair
router-helper daemon
```

On macOS, the enrollment secret, one-time issued device token, and local token secret are stored through macOS Keychain under service `com.dodelidoo.opencdx.helper`. The JSON helper config contains the router URL, opaque device ID, loopback port, and catalog path only. It never contains OpenAI or OpenRouter credentials.

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
model_catalog_json = "/Users/USER/Library/Application Support/com.dodelidoo.opencdx/catalog.json"

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

Codex must restart after a catalog file changes. The menu app and catalog endpoint report this explicitly. The reminder clears automatically when the helper observes a Codex process that started after the catalog was written; older running instances do not count. It can also be acknowledged manually from the menu.

## Reconcile usage history

The menu app previews and imports only the default Codex home at `~/.codex`.
Before replacement it shows the resolved directory plus scanned-file and
routed/native request counts. It never discovers or combines other Codex homes.

The local scan extracts only UTC day, provider, model, routing classification,
request count, and input/cached/cache-write/output/reasoning token totals from
`sessions` and `archived_sessions`. Prompts, responses, credentials, paths,
and account identifiers are ignored. Routed/native classification comes from
Codex's durable `model_provider = "opencdx"` rollout metadata.

Use the helper directly when Codex runs with a custom `CODEX_HOME`:

```sh
router-helper reconcile-usage --codex-home /absolute/path/to/codex-home --dry-run
# Review the routed/native counts, then replace this machine’s telemetry:
router-helper reconcile-usage --codex-home /absolute/path/to/codex-home
```

The chosen directory is the complete reconciliation source. A successful run
replaces only the authenticated machine’s telemetry rather than merging it.
The first import adds that machine’s history; subsequent imports replace it.
Other machines’ usage and reconciliation metadata are preserved. Importing a
different home later replaces the same machine’s previous snapshot. The router keeps ingestion
source (reconciled or live proxy) separate from routing classification (routed
or native), so later reconciliations preserve the dashboard's routed/native
view.

The dashboard’s **Machine** filter applies to totals, charts, activity, and CSV
exports. New routed requests are attributed to the authenticated device, and
reconciled history is tagged with that same identity by the server. Names are
shown alongside device IDs so machines with identical names remain distinct.
Deleting an enrollment preserves its usage under its device ID; enrolling again
creates a new identity. Old usage remains under **Unknown device** because it
cannot be attributed retrospectively.

To rebuild attribution from local history, reset telemetry once, then reconcile
each machine’s own history. Do not reset between machines. Avoid importing the
same copied session history from multiple machines, which would count it once
for each importing device. Update the server before using this workflow; older
servers replace telemetry globally.

This deletes server telemetry for **every machine**, including imported usage and
allowance observations. It is available only in Settings, with confirmation;
reconciliation replaces **this machine’s** history only. To start the dashboard
counters over without changing local history, use
**Settings → Delete All Server History…** in the macOS app or run:

```sh
router-helper reset-telemetry
```

This removes only aggregate telemetry and reconciliation metadata from the
router. It leaves providers, devices, accounts, and all `~/.codex` files intact.

## Useful commands

| Command | Effect |
|---|---|
| `router-helper token` | Print only a short-lived local credential for Codex |
| `router-helper status` | Read helper/menu status as JSON |
| `router-helper refresh-catalog` | Refresh providers, atomically download catalog |
| `router-helper refresh-quotas` | Refresh account quotas |
| `router-helper reconnect` | Recheck remote connectivity |
| `router-helper reconcile-usage [--codex-home PATH] [--dry-run]` | Preview or replace this machine’s telemetry from one Codex history root |
| `router-helper reset-telemetry` | Reset router telemetry without changing local Codex history or router configuration |
| `router-helper open-dashboard` | Open the configured dashboard |
| `router-helper quit` | Stop the user helper daemon |
| `router-helper config` | Print, but never install, the Codex TOML snippet |

## Uninstall

`scripts/uninstall-macos-app.sh` stops the helper, removes its three Keychain entries, and moves the app and its application-support folder to Trash. It deliberately does not read or change `~/.codex`, the Codex executable, or native Codex authentication. Remove the manually pasted provider snippet yourself if desired.

### Request timestamps and time ranges

The current helper preserves the original timestamp of each unique usage event.
The router uses these timestamps for rolling usage windows and applies its
viewer's selected reporting timezone to calendar days. Upgrade the router before importing
with the updated helper. Reconcile the same original Codex home to add timestamps
to older daily-only imports that already have machine attribution; do not reset
telemetry first in that case. For v1.2.0 upgrades with unattributed history, use
the one-time server reset and per-machine import procedure above. Other machines retain
their history and must be reconciled separately. See
[Dashboard timezone and rolling usage](deployment.md#dashboard-timezone-and-rolling-usage).

The telemetry bar chart includes small `↻` markers for observed weekly Codex
allowance window transitions. Hover or focus a marker for a preview; activate it
(or expand **cycle details**) for observation bounds and recorded input + output
tokens and requests through the next transition. Several transitions in one
chart bucket share a marker and count. Cycle totals cover the full interval,
independent of the chart date filter, and respect the selected machine. All times
use the selected viewing timezone. The activity heatmap has no reset markers.

Updated helpers import minimal weekly allowance observations from local rollout
history at the next usage reconciliation. Older helpers remain compatible but
cannot supply this history. Historical observations identify a machine, not the
billed account: account switches can resemble resets and multiple machines may
observe the same reset. These are inferred transitions, not a unique account
reset count. Future server quota polls retain account attribution; their cycle
totals include only usage whose account is still known. Reconciled history has
no account attribution and is excluded from those account totals. Main weekly
Codex windows are tracked; five-hour and Spark limits are excluded.

A reset deadline alone is not evidence of a reset. A subsequent window change
is required. Early transitions are bounded by adjacent observations; the chart
uses the first new observation as their marker time. Cycle totals are approximate
when boundaries are inferred, and daily-only records are excluded and flagged.
Cached input is included in token totals; these counts are not allowance billing
units. Clearing telemetry also clears the allowance observations.
