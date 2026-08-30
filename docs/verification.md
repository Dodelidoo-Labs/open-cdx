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
4. Add OpenAI account A through a fresh explicit browser login.
5. Confirm masked email, plan, quota/reset windows, credits, and model count appear before inference.
6. Add OpenAI account B through a separate explicit browser login and confirm a second pool entry.
7. Attempt to add A again; confirm duplicate rejection, then explicitly choose replacement and confirm only A's credential changes.
8. Configure an OpenRouter key, refresh, and inspect compatible and excluded models/reasons.
9. Copy the generated TOML manually into the isolated Codex config and restart Codex.
10. Confirm `/model` contains the complete entitled native union, native auto-review/safety entries, and only compatible namespaced OpenRouter entries.
11. Run one native model and one OpenRouter model.
12. Pause A and confirm a shared entitled model routes through B while an A-only model becomes unavailable.
13. Restart/recreate the Docker router without deleting its volume and confirm both accounts still refresh and route.
14. Enroll and approve a second Mac; revoke it and confirm only that helper loses access.
15. Sign normal Codex/ChatGPT into an unrelated account and confirm the isolated router provider behavior is unchanged.
16. Run the uninstall script and confirm the Codex executable, native auth, and `~/.codex` content are untouched.

Record account labels only as masked values. Never paste tokens, OAuth codes, raw account IDs, prompts, or responses into a test log.
