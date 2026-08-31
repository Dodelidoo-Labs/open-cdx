# Verification

## Automated

Run Go tests in a Go environment or isolated container:

```sh
go test ./...
go vet ./...
```

Run the real Codex signed-out command-auth proof:

```sh
./scripts/test-codex-signed-out.sh
```

Inside the dedicated Docker VM, run the restart-recovery proof. It removes and
recreates the service container without deleting the named volume, then verifies
that pre-restart enrollment state remains readable:

```sh
sudo ./scripts/test-docker-restart.sh
```

The helper smoke test requests enrollment, approves and pairs the device,
checks loopback command authentication, revokes that device, and verifies the
same helper can no longer reach the router:

```sh
OPENCODEX_HELPER_BINARY=./dist/router-helper-linux sudo -E ./scripts/test-helper-e2e.sh
```

Build the macOS target and app bundle:

```sh
swift test --disable-sandbox -c release --package-path mac/RouterMenu
OPENCODEX_CODESIGN_IDENTITY=- ./scripts/build-macos-app.sh
codesign --verify --deep --strict "dist/OpenCDX Router.app"
test "$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' 'dist/OpenCDX Router.app/Contents/Info.plist')" = "com.dodelidoo.opencdx"
codesign -d --verbose=4 "dist/OpenCDX Router.app/Contents/Resources/router-helper" 2>&1 | grep -Fx 'Identifier=com.dodelidoo.opencdx.helper'
```

The explicit ad-hoc identity above is only for build validation. Installed local
builds must use a stable Apple-issued signing identity.

The suite covers OAuth state/PKCE, duplicate detection, encrypted persistence, refresh single-flight, native entry preservation, entitlement selection, sticky affinity, quota failover, partial-stream no-retry, headers/auth replacement, capability-driven OpenRouter catalog mapping, account-collapsed token telemetry, Codex-local patch exposure, unsupported/no-op reasoning handling, Ollama hosted-search suppression, atomic catalogs, device lifecycle, error redaction, HTTP policy, and helper local tokens.

## Docker VM record

The checked-in stack was tested in a Multipass VM named `opencdx-docker-test` with Ubuntu 24.04, Docker Engine 29.1.3, Compose 2.40.3, and Buildx 0.30.1. The test performed:

1. Compose config validation and image build.
2. `/readyz` and container health verification.
3. Enrollment metadata creation.
4. Full container removal/recreation while retaining the named volume.
5. Successful lookup of the pre-restart enrollment.
6. Helper cross-build, enrollment, dashboard login/CSRF approval, one-time pairing, daemon start, local 401 rejection, command-token acceptance, remote forwarding, and clean helper quit.

## Human account/provider acceptance

This checklist requires credentials and deliberate browser choices, so it must be run by the operator:

1. Start with a fresh router volume and back up the generated master key.
2. Use a Mac whose isolated test Codex home has no native login.
3. Install the menu app, request enrollment, and approve it in the dashboard.
   Confirm macOS identifies it as `com.dodelidoo.opencdx` and prompts for the
   new Local Network permission.
4. Add OpenAI account A through a fresh explicit browser login.
5. Confirm masked email, plan, quota/reset windows, credits, and model count appear before inference.
6. Add OpenAI account B through a separate explicit browser login and confirm a second pool entry.
7. Attempt to add A again; confirm duplicate rejection, then explicitly choose replacement and confirm only A's credential changes.
8. Configure an OpenRouter key, refresh, and inspect compatible and excluded models/reasons.
9. Copy the generated TOML manually into the isolated Codex config and restart Codex.
10. Confirm `/model` contains the complete entitled native union, native auto-review/safety entries, and only compatible namespaced OpenRouter entries.
11. Run one native model and one OpenRouter model.
12. Choose **Reconcile Usage History…**, confirm the preview names the default `~/.codex` source and shows routed/native counts, then cancel and verify telemetry is unchanged.
13. Run a dry run against the isolated Codex home with `router-helper reconcile-usage --codex-home /absolute/test/home --dry-run`; confirm it reports the routed requests, then run the same command without `--dry-run` and verify the dashboard preserves their routed classification.
14. Configure a LAN Ollama `http://` endpoint with **Allow HTTP** off and confirm it is rejected; enable the option and confirm the connection can be tested. Verify HTTPS and loopback HTTP still work with the option off.
15. Choose **Reset Telemetry…**, confirm the dashboard returns to zero, then verify accounts, providers, devices, and the isolated `~/.codex` rollout files are unchanged. Confirm a new routed request starts telemetry fresh, or reconcile again.
16. Pause A and confirm a shared entitled model routes through B while an A-only model becomes unavailable.
17. Restart/recreate the Docker router without deleting its volume and confirm both accounts still refresh and route.
18. Enroll and approve a second Mac; remove it and confirm only that helper loses access and the device row disappears.
19. Sign normal Codex/ChatGPT into an unrelated account and confirm the isolated router provider behavior is unchanged.
20. Run the uninstall script and confirm the Codex executable, native auth, and `~/.codex` content are untouched.

Record account labels only as masked values. Never paste tokens, OAuth codes, raw account IDs, prompts, or responses into a test log.

## Operator-only live dashboard verification

These checks require the installed app, a real browser, and normal router activity. They must be performed manually by the operator and are not part of the hermetic automated suite:

1. Leave Telemetry visible and make normal routed calls; confirm data updates within the polling interval.
2. Reconcile from the macOS app while Telemetry is visible; confirm routed/native results update without reloading.
3. Export CSV immediately afterward; confirm it contains the current reconciled data.
4. Request enrollment from a client while Devices is visible; confirm it appears within a few seconds.
5. Approve, reject, remove, and delete devices after dynamic list replacement; confirm every form still works.
6. Leave Accounts visible through a background quota update; confirm allowance and status change without reloading.
7. Switch to Providers and Catalog; confirm neither panel creates background polling traffic.
8. Hide the browser tab; confirm all live polling stops.
9. Return to the tab; confirm the selected live panel performs one immediate refresh.
10. Inspect browser Network activity; confirm unchanged live requests return `304` with empty bodies.
11. Switch repeatedly among live and non-live tabs; confirm duplicate polling loops do not accumulate.
