# Development

This guide covers local repository work. End users should follow the installation steps in the main [README](../README.md); production operators should use [Deployment](deployment.md).

## Requirements

- Go 1.22 or newer
- Docker Engine with Docker Compose v2 for container development
- Xcode 26 or newer (macOS SDK 26+) and Swift Package Manager for the macOS app
- Multipass only when reproducing the isolated Linux/Docker test environment

## Go services and helper

Run the normal Go checks and build both commands:

```sh
go test ./...
go vet ./...
make build
```

`make build` writes `bin/routerd` and `bin/router-helper`. The direct package builds used for a quick compile check are:

```sh
go build ./cmd/routerd ./cmd/router-helper
```

## Local Docker stack

The development Compose stack builds the current checkout, uses HTTP, and binds to IPv4 loopback by default:

```sh
./scripts/generate-docker-secrets.sh
docker compose -f docker/compose.dev.yml up -d --build
curl --fail http://127.0.0.1:8080/readyz
```

Open `http://127.0.0.1:8080/admin` and use the token in `docker/secrets/admin_token`. Do not expose the development stack on a shared or untrusted network.

## Isolated Docker validation with Multipass

Docker validation can be reproduced in an Ubuntu 24.04 VM:

```sh
multipass launch 24.04 --name opencdx-docker-test --cpus 2 --memory 4G --disk 20G
multipass exec opencdx-docker-test -- sudo apt-get update
multipass exec opencdx-docker-test -- sudo apt-get install -y docker.io docker-compose-v2 docker-buildx
multipass mount "$PWD" opencdx-docker-test:/home/ubuntu/opencdx
./scripts/generate-docker-secrets.sh
VM_IP=$(multipass exec opencdx-docker-test -- hostname -I | awk '{print $1}')
printf 'VM_IP=%s\n' "$VM_IP"
multipass exec opencdx-docker-test -- sh -lc "cd /home/ubuntu/opencdx && sudo env OPENCODEX_DEV_BIND=0.0.0.0 OPENCODEX_PUBLIC_URL=http://$VM_IP:8080 docker compose -f docker/compose.dev.yml up -d --build --force-recreate"
curl --fail "http://$VM_IP:8080/readyz"
```

From the Mac, open `http://$VM_IP:8080/admin`. In the menu app, use the same base URL and enable **Allow plaintext LAN router for development**.

Assigning `VM_IP` is silent; the `printf` line confirms the detected address. The non-loopback binding is an explicit insecure choice for the private Multipass network. The Compose default remains loopback-only when `OPENCODEX_DEV_BIND` is omitted. `--force-recreate` replaces a loopback-bound container that Multipass may have restarted from an earlier run.

## macOS app

Use Xcode 26 or newer for both local and release builds. On macOS Tahoe,
SwiftUI selects the menu window's presentation using the executable's linked
SDK. Builds linked against SDK 15.5 use a square legacy backdrop over the
native rounded surface. The build script checks the SDK in every architecture
of the finished menu executable so an older toolchain or cached output cannot
reintroduce this defect. The deployment target remains macOS 13.0.

Build and install a local app, reusing its existing location:

```sh
./scripts/build-macos-app.sh
./scripts/install-macos-app.sh --check
./scripts/install-macos-app.sh
```

The installer reuses the location of the one existing app—`/Applications` or
`~/Applications`—so a test build replaces that bundle in place instead of
creating a second Local Network privacy identity. It stops if copies exist in
both locations. Set `OPENCODEX_INSTALL_APP_PATH` only when intentionally
choosing another absolute `OpenCDX Router.app` path. The `--check` form verifies
the signature, bundle identifiers, helper identity, and signer compatibility
without stopping, replacing, or launching the installed app.

Go must be available for every app build so a stale bundled helper cannot be reused. If Go is not on `PATH`, set `OPENCODEX_GO_BINARY` or place its absolute path in the ignored `.opencdx-go-binary` file.

Installed local builds require a stable Apple-issued signing identity. Put the certificate's SHA-1 fingerprint in the ignored `.opencdx-codesign-identity` file or set `OPENCODEX_CODESIGN_IDENTITY` for one build. When exactly one Apple identity is available, the build script selects it. With none or more than one, it stops instead of silently changing identity.

Use `OPENCODEX_CODESIGN_IDENTITY=-` only for a non-installed CI validation artifact. The installer rejects ad-hoc builds and unintended signer changes because unstable signing identity can produce duplicate macOS Local Network privacy entries.

[Apple's Local Network privacy guidance](https://developer.apple.com/documentation/Technotes/tn3179-understanding-local-network-privacy) explains that multiple copies of one app can create unexpected privacy entries and that macOS has no supported per-app reset. Keeping one installed copy and a stable signing identity prevents new duplicates but cannot remove historical entries.

The built artifact is `dist/OpenCDX Router.app`. The install script atomically moves it to `~/Applications`, leaving only one app copy. The fixed identifiers are:

| Component | Identifier |
|---|---|
| Application | `com.dodelidoo.opencdx` |
| Bundled helper and Keychain service | `com.dodelidoo.opencdx.helper` |

Local helper state lives under `~/Library/Application Support/com.dodelidoo.opencdx`.

See [Verification](verification.md) for the complete automated and manual test matrix. Release builds, notarization, Sparkle signing, and publication are documented separately in [Releases](releases.md).

## Synthetic allowance preview

The fixture in `scripts/testing/allowance-preview/` runs the actual dashboard
and API with two paused synthetic accounts, two machines, short and weekly
windows, a burst of usage, a missing collection interval, and allowance resets.
It creates its own database and encryption key, and refuses to seed an existing
data directory. Provider/OAuth URLs are deliberately unusable.

```sh
docker build -t opencdx-allowance-preview -f scripts/testing/allowance-preview/Dockerfile .
docker run --rm --name opencdx-allowance-preview -p 127.0.0.1:18081:8080 \
  -e OPENCODEX_PUBLIC_URL=http://127.0.0.1:18081 opencdx-allowance-preview
```

Open the browser at `http://127.0.0.1:18081/admin`; the demo-only administrator
token is `opencdx-allowance-preview-only`. Select **24h**, then **Show allowances** to
compare the sudden drain with the usage spike. Select **All** to see the longer
history, resets and a gap. Try the window selector and machine filter.

For a Multipass preview, run this container inside a dedicated disposable VM,
publish port `8080:8080`, and set `OPENCODEX_PUBLIC_URL=http://VM_IP:8080`.
Use a code-only copy of the repository. Do not mount real databases, secrets,
Codex history, or helper state. Open the VM dashboard in a browser only;
**do not connect the installed macOS app or helper to the preview**.
