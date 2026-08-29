# Deployment

## Development

The development stack is intentionally loopback-only and HTTP:

```sh
./scripts/generate-docker-secrets.sh
docker compose -f docker/compose.dev.yml up -d --build
curl --fail http://127.0.0.1:8080/readyz
```

Do not change the port binding to `0.0.0.0` on a shared LAN. A helper may use a plaintext non-loopback URL only when both the router and helper were explicitly started in insecure development mode.

### Access from the macOS host through Multipass

The safe Compose default publishes only on the VM's loopback interface, so
`http://VM_IP:8080` will not answer. Explicitly expose the development port on
the private Multipass interface and make that address the router's public URL:

```sh
VM_IP=$(multipass exec opencdx-docker-test -- hostname -I | awk '{print $1}')
printf 'VM_IP=%s\n' "$VM_IP"
multipass exec opencdx-docker-test -- sh -lc "cd /home/ubuntu/opencdx && sudo env OPENCODEX_DEV_BIND=0.0.0.0 OPENCODEX_PUBLIC_URL=http://$VM_IP:8080 docker compose -f docker/compose.dev.yml up -d --build --force-recreate"
curl --fail "http://$VM_IP:8080/readyz"
```

The variable assignment itself prints nothing; the `printf` line confirms the
detected address. Recreating is necessary when `restart: unless-stopped` has
already brought back a container with the default loopback-only port mapping.

Open `http://$VM_IP:8080/admin` on the Mac. In the menu app, use the same base
URL and enable **Allow plaintext LAN router for development**. Do not use this
mode on a bridged or otherwise untrusted network.

## Internet or LAN production

Requirements:

- A DNS name resolving to the deployment host
- TCP 80 and TCP/UDP 443 reachable by Caddy
- Router port 8080 not published by another Compose override or host firewall rule
- Persistent Docker volumes
- Separately backed-up Docker secret files

Create `docker/.env`:

```dotenv
OPENCODEX_DOMAIN=router.example.com
ACME_EMAIL=admin@example.com
```

Then start the HTTPS stack:

```sh
./scripts/generate-docker-secrets.sh
docker compose --env-file docker/.env -f docker/compose.production.yml up -d --build
docker compose --env-file docker/.env -f docker/compose.production.yml ps
curl --fail "https://router.example.com/readyz"
```

The router listens over HTTP only on its private Docker network. Caddy is the sole published ingress and streams Responses without buffering. HSTS is emitted by both the application and Caddy.

For Tailscale, use a certificate-backed Tailscale DNS name or put Caddy on the tailnet. Do not point helpers at `http://100.x.y.z:8080` in production.

## Persistence and backup

The `router_data` named volume contains SQLite metadata, encrypted credentials, catalog snapshots, quota state, devices, and affinities. `docker/secrets/master_key` decrypts credential envelopes. Back up both, store them separately, and protect the administrator token independently.

Restoring only the database intentionally leaves credentials unreadable. Changing the master key without re-encrypting the database has the same effect.

Container recreation without volume deletion is safe:

```sh
docker compose -f docker/compose.dev.yml down
docker compose -f docker/compose.dev.yml up -d
```

Never add `-v` unless deleting all router data is explicitly intended.

## Upgrades

Build the new image, back up the database volume and master key, then recreate the service. SQLite migrations run at startup and are additive. Check `/readyz`, the dashboard provider health, and one helper status before retiring the old image.

## Environment reference

| Variable | Default | Purpose |
|---|---|---|
| `OPENCODEX_LISTEN` | `:8080` | Router listen address |
| `OPENCODEX_PUBLIC_URL` | loopback development URL | Browser/dashboard URL; non-loopback production requires HTTPS |
| `OPENCODEX_DATABASE` | `/var/lib/opencdx/router.db` | SQLite path |
| `OPENCODEX_MASTER_KEY_FILE` | `/run/secrets/master_key` | 32-byte raw/base64/hex encryption key |
| `OPENCODEX_ADMIN_TOKEN_FILE` | `/run/secrets/admin_token` | Dashboard administrator token |
| `OPENCODEX_INSECURE_DEV` | `false` | Explicit plaintext development override |
| `OPENCODEX_CATALOG_REFRESH_INTERVAL` | `15m` | Provider catalog refresh interval |
| `OPENCODEX_QUOTA_REFRESH_INTERVAL` | `5m` | Account quota refresh interval |

The OpenAI auth, ChatGPT API, and Codex Responses endpoints are always required to be absolute HTTPS URLs, including in development.
