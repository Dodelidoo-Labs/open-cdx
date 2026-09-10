# Router deployment

This guide is the production operator runbook for the OpenCDX router service. The router is distributed as a multi-architecture container through [GitHub Packages](https://github.com/Dodelidoo-Labs/open-cdx/pkgs/container/open-cdx):

```text
ghcr.io/dodelidoo-labs/open-cdx
```

GitHub Releases contains the signed macOS companion app, its checksum, and its Sparkle update feed. It does not contain a Docker image.

## Requirements

- A host with Docker Engine and Docker Compose v2
- A DNS name resolving to that host
- TCP 80 and TCP/UDP 443 reachable by the included Caddy service
- Router port 8080 kept off the LAN and internet
- Persistent Docker volumes
- An independent backup location for the generated secret files

Production helpers require HTTPS. Do not point a helper at a plaintext LAN address.

## Install

Clone the repository on the Docker host:

```sh
git clone https://github.com/Dodelidoo-Labs/open-cdx.git
cd open-cdx
cp docker/.env.example docker/.env
```

Set the public DNS name, ACME contact address, and image version in `docker/.env`:

```dotenv
OPENCODEX_DOMAIN=router.example.com
ACME_EMAIL=admin@example.com
OPENCODEX_VERSION=latest
```

`OPENCODEX_VERSION` selects an image tag from GitHub Packages. Use `latest` for the newest stable image, or pin a complete version such as `1.0.0`. For a pinned deployment, check out the matching Git tag so the Compose and Caddy configuration comes from the same version:

```sh
git checkout v1.0.0
```

Generate the encryption key and administrator token, then start the stack:

```sh
./scripts/generate-docker-secrets.sh
docker compose --env-file docker/.env -f docker/compose.production.yml pull
docker compose --env-file docker/.env -f docker/compose.production.yml up -d
docker compose --env-file docker/.env -f docker/compose.production.yml ps
```

The production Compose stack pulls `ghcr.io/dodelidoo-labs/open-cdx`. It does not build the application from the checkout.

Verify the public endpoint:

```sh
curl --fail "https://router.example.com/readyz"
```

Then open `https://router.example.com/admin` and sign in with the value in `docker/secrets/admin_token`. Protect that token like a password.

## Network and TLS

Caddy is the only published service. It listens on ports 80 and 443, obtains and renews the public certificate, and forwards requests to the router over the private Docker network. The router's port 8080 must not be published by another Compose override or firewall rule.

For Tailscale, use a certificate-backed Tailscale DNS name or place Caddy on the tailnet. Do not point production helpers at `http://100.x.y.z:8080`.

The HTTPS requirement applies to Mac-to-router traffic. If the router connects to an Ollama server elsewhere on a trusted LAN, that provider has a separate **Allow HTTP** option. It is off by default and should be enabled only for that deliberate upstream connection. Loopback HTTP remains allowed without the option.

## Persistence and backup

The `router_data` named volume contains SQLite metadata, encrypted credentials, catalogs, quota state, devices, routing affinity, and aggregate telemetry. `docker/secrets/master_key` decrypts the stored credential envelopes.

Back up the data volume and master key together, but store the backup copies separately. Protect `docker/secrets/admin_token` independently. A database backup without the matching master key cannot recover provider credentials; changing the key without a planned re-encryption has the same effect.

Container recreation without volume deletion is safe. Never add `-v` to a Compose `down` command unless deleting all router data is explicitly intended.

## Upgrades

1. Back up the `router_data` volume and `docker/secrets/master_key`.
2. Choose a version published in [GitHub Packages](https://github.com/Dodelidoo-Labs/open-cdx/pkgs/container/open-cdx).
3. For a pinned deployment, fetch and check out the matching repository tag and set the same version in `docker/.env`.
4. Pull and recreate the services:

   ```sh
   docker compose --env-file docker/.env -f docker/compose.production.yml pull
   docker compose --env-file docker/.env -f docker/compose.production.yml up -d
   ```

5. Check `/readyz`, provider health in the dashboard, and one paired Mac before retiring the backup.

The version link below the dashboard logout control turns red when GitHub reports a newer stable release. The macOS companion updates separately through GitHub Releases and Sparkle.

## Environment reference

| Variable | Default | Purpose |
|---|---|---|
| `OPENCODEX_LISTEN` | `:8080` | Router listen address |
| `OPENCODEX_PUBLIC_URL` | Loopback development URL | Browser/dashboard URL; non-loopback production requires HTTPS |
| `OPENCODEX_DATABASE` | `/var/lib/opencdx/router.db` | SQLite path |
| `OPENCODEX_MASTER_KEY_FILE` | `/run/secrets/master_key` | 32-byte raw, base64, or hex encryption key |
| `OPENCODEX_ADMIN_TOKEN_FILE` | `/run/secrets/admin_token` | Dashboard administrator token |
| `OPENCODEX_INSECURE_DEV` | `false` | Explicit plaintext development override |
| `OPENCODEX_CATALOG_REFRESH_INTERVAL` | `1h` | Provider and account model-catalog refresh interval |
| `OPENCODEX_QUOTA_REFRESH_INTERVAL` | `5m` | Account quota refresh interval |

`OPENCODEX_VERSION` is a Docker Compose substitution that selects the container tag. It is not read by the router process.

The OpenAI authorization, ChatGPT API, and Codex Responses endpoints must always be absolute HTTPS URLs, including during development.

For the source-built HTTP stack and the isolated Multipass workflow, see [Development](development.md).

## Dashboard timezone and rolling usage

The dashboard defaults to the viewing browser's timezone. Its **Timezone**
selector can show statistics in any listed IANA timezone, including UTC. The
choice is remembered in that browser; **Automatic** follows the browser timezone
on page load. It applies independently of the Machine filter: a London viewer
can inspect any machine's usage using London calendar dates, while another viewer
uses Buenos Aires dates. Changing it refreshes charts, calendar ranges, reset
markers, timestamps, and CSV date grouping without changing stored UTC timestamps
or another viewer's settings. Rolling 24h/7d/30d durations remain identical.

Set the fallback `OPENCODEX_TIMEZONE` in the server environment (or in `docker/.env` for
production Compose). It accepts an IANA name, for example:

```dotenv
OPENCODEX_TIMEZONE=America/Argentina/Buenos_Aires
```

The fallback is explicitly `UTC` and is used when a client does not select a
timezone or browser timezone detection is unavailable. The server host's timezone
does not select the reporting timezone. `routerd --timezone`
overrides the environment; invalid zones and `Local` are rejected at startup.
Timezone data is embedded in the binary, including minimal Docker images.
Restart the router after changing this setting.

The dashboard's **24h**, **7d**, and **30d** controls select the preceding 24,
168, and 720 hours using the server clock. Calendar ranges, chart days, and
rendered timestamps use the selected viewing timezone. **Year** means the current
calendar year. Changing timezone never changes a rolling window's duration.

Upgrades retain older daily totals, but cannot invent their request timestamps.
Rolling totals that overlap that history are shown as unavailable instead of
showing a misleading partial number. After upgrading the router and helper,
reconcile each machine's original Codex history to restore timestamps. Preview
first with `router-helper reconcile-usage --dry-run`; the regular reconciliation
replaces that machine's telemetry. No telemetry reset is required for history that already has machine attribution.
For upgrades from v1.2.0, old unattributed totals would remain alongside new
per-machine imports: verify the original local histories are available, reset
server telemetry once, then reconcile every machine without resetting between
imports. The reset permanently removes history absent from those local files.
Imports from
older helpers remain readable as daily history. Calendar totals containing
older history are identified as retaining UTC day grouping until reconciliation.
