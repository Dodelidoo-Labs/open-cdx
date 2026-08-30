# OpenCDX Router

[GitHub](https://github.com/Dodelidoo-Labs/open-cdx) · [Releases](https://github.com/Dodelidoo-Labs/open-cdx/releases) · `ghcr.io/dodelidoo-labs/open-cdx`

OpenCDX Router is a credential-isolating provider router for Codex. A central Go service owns OpenAI OAuth grants, OpenRouter credentials, catalogs, quota state, and routing. Each Mac runs a small loopback helper and a native SwiftUI menu-bar app.

```text
Codex → 127.0.0.1 helper → HTTPS → router → OpenAI / OpenRouter / Ollama
```

The project does not replace Codex, read an existing Codex or ChatGPT login, import browser cookies, or edit `~/.codex/config.toml`. Every OpenAI account performs a fresh browser PKCE login, and only the router ever receives or refreshes its access and refresh tokens.

## What is implemented

- Encrypted SQLite account, provider, catalog, device, affinity, and aggregate-usage storage
- Explicit browser authorization-code login with PKCE, five-minute state, duplicate detection, and replace flow
- Per-account identity, plan, quota, reset-credit, and entitled-model validation before an account becomes ready
- Exact native OpenAI catalog-entry preservation, union routing by entitlement, primary-account conflict resolution, and visible conflicts
- Conservative OpenRouter discovery/translation and visible exclusions; remote-router Ollama support
- Sticky thread/session affinity, primary-first operator-ordered account selection, quota failover, refresh-token single-flight, and stream-safe retry rules
- Per-device enrollment, administrator approval, one-time credential delivery, acknowledgement, and revocation
- Loopback helper with short-lived command authentication, Keychain storage on macOS, atomic catalogs, browser callbacks, and no inference-body inspection
- Native `MenuBarExtra` application with no Dock icon and normal macOS login-item registration
- Administration dashboard with Home telemetry, OpenAI account quotas, provider health, devices, sortable catalog diagnostics, and credential removal
- One-year aggregate activity plus per-model token charts without storing prompts or responses
- Optional one-time Codex history reconciliation from local rollout files, with cumulative-counter deduplication and a strict aggregate-only wire format
- HTTP-only loopback development Compose stack and a production Caddy stack that exposes clients only over HTTPS

## Development quick start

Docker validation for this repository was performed in the `opencdx-docker-test` Multipass VM. To reproduce that isolation:

```sh
multipass launch 24.04 --name opencdx-docker-test --cpus 2 --memory 4G --disk 20G
multipass exec opencdx-docker-test -- sudo apt-get update
multipass exec opencdx-docker-test -- sudo apt-get install -y docker.io docker-compose-v2 docker-buildx
multipass mount "$PWD" opencdx-docker-test:/home/ubuntu/opencdx
./scripts/generate-docker-secrets.sh
VM_IP=$(multipass exec opencdx-docker-test -- hostname -I | awk '{print $1}')
printf 'VM_IP=%s\n' "$VM_IP"
multipass exec opencdx-docker-test -- sh -lc "cd /home/ubuntu/opencdx && sudo env OPENCODEX_DEV_BIND=0.0.0.0 OPENCODEX_PUBLIC_URL=http://$VM_IP:8080 docker compose -f docker/compose.dev.yml up -d --build --force-recreate"
```

From the Mac, open `http://$VM_IP:8080/admin`. The administrator token is in
`docker/secrets/admin_token`. The non-loopback bind is an explicit insecure
development choice for the private Multipass network; the Compose default stays
bound to loopback when `OPENCODEX_DEV_BIND` is omitted. Assigning `VM_IP` is
silent; the following `printf` confirms the value. `--force-recreate` replaces
any loopback-bound container that Multipass restarted from an earlier run.

For normal local development with Go installed:

```sh
go test ./...
go build ./cmd/routerd ./cmd/router-helper
```

## macOS app

Build and install without administrator privileges:

```sh
./scripts/build-macos-app.sh
./scripts/install-macos-app.sh
```

For stable macOS privacy identity across local rebuilds, configure an Apple-issued
development certificate by placing its SHA-1 fingerprint in the ignored
`.opencdx-codesign-identity` file, or set `OPENCODEX_CODESIGN_IDENTITY` for one
build. When exactly one Apple identity is available, the script selects it. With
none or more than one, it fails instead of silently changing identity. Explicit
`OPENCODEX_CODESIGN_IDENTITY=-` remains available for non-installed CI artifacts;
the installer refuses ad-hoc builds and unintentional signer changes.

Go must be available for every app build so a stale bundled helper can never be
reused. If Go is not on `PATH`, set `OPENCODEX_GO_BINARY` or put its absolute path
in the ignored `.opencdx-go-binary` file.

The built artifact is `dist/OpenCDX Router.app`. The install script atomically
moves that bundle to `~/Applications` rather than leaving a second copy behind;
[Apple documents](https://developer.apple.com/documentation/Technotes/tn3179-understanding-local-network-privacy)
that multiple app copies can produce unexpected Local Network privacy entries.
The same technote notes that macOS has no supported per-app reset, so these
safeguards prevent new duplicate entries but cannot erase historical entries.
On first launch, open Settings, enter the router's HTTPS address and a device
name, request enrollment, then approve the pending Mac in the dashboard. The app
detects approval and starts the bundled helper.

End users can download the signed, notarized universal macOS ZIP from the
[latest GitHub release](https://github.com/Dodelidoo-Labs/open-cdx/releases/latest).
Release builds support both Apple silicon and Intel Macs on macOS 13 or newer.

Use **Add OpenAI Account…** for each account. The browser is forced through a fresh login, and duplicate accounts are rejected without replacing stored credentials. Account allowance changes appear in the menu within about 30 seconds; catalog changes are checked every minute or immediately with **Refresh Model Catalog**.

After the first catalog sync, choose **Copy Codex Configuration** and paste the snippet into `~/.codex/config.toml` yourself. Restart Codex whenever the menu reports that the catalog changed, then choose **Done** beside the reminder; Codex loads `model_catalog_json` at startup.

After pairing, the app asks once whether to import existing Codex usage history. The scan stays on the Mac and extracts only the UTC day, provider, model, routing classification, request count, and input/cached/cache-write/output/reasoning token counters from `~/.codex/sessions` and `~/.codex/archived_sessions` (or `CODEX_HOME`). Prompt and response records are ignored and the server rejects fields outside the aggregate schema. Reconciliation transactionally replaces existing telemetry and records the import instant. Telemetry keeps ingestion source (`reconciled` or live proxy `routed`) separate from routing classification (`routed` or `native`). The latter is derived from Codex's durable `model_provider = "opencdx"` rollout metadata, so the dashboard's **Group by → Routed / native** view remains accurate after any later full reconciliation. Running reconciliation again replaces both the aggregates and that boundary. Run it at any time with **Reconcile Usage History…** in the menu or:

```sh
router-helper reconcile-usage
```

See [helper and Codex setup](docs/helper-and-codex.md) and [deployment](docs/deployment.md) for the complete runbook.

## Production

Production clients must never use plaintext LAN HTTP. Set a real DNS name and ACME email, keep router port 8080 off the LAN, and run:

```sh
cp docker/.env.example docker/.env
# Edit docker/.env.
./scripts/generate-docker-secrets.sh
docker compose --env-file docker/.env -f docker/compose.production.yml pull
docker compose --env-file docker/.env -f docker/compose.production.yml up -d
```

The production Compose file pulls `ghcr.io/dodelidoo-labs/open-cdx`; set
`OPENCODEX_VERSION` to a release such as `1.0.0`, or use `latest`. Caddy is the
only published service and terminates HTTPS. Back up the named SQLite volume
and `docker/secrets/master_key` together; neither is useful alone.

Maintainers: see [release automation](docs/releases.md) for tag, signing, and
notarization setup.

## Verification status

Automated Go tests, a real signed-out Codex command-auth integration, the Swift release build, and Docker/Compose restart recovery have been run. A human must still complete the account-dependent acceptance steps with two real OpenAI accounts and an OpenRouter key; no credentials were available or inferred for that test. The exact checklist is in [verification](docs/verification.md), and the evidence/remaining gate is recorded in [research gates](docs/research-gates.md).

## Security boundary

Prompts and responses are streamed and never logged. The router retains only daily request/token aggregates. Stable ChatGPT account IDs are hashed outside encrypted credential envelopes; display uses masked email. OAuth codes, bearer tokens, refresh tokens, provider keys, and device credentials are never written to logs.

See [security](docs/security.md) for secrets, backup, revocation, and uninstall behavior.
