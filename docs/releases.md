# Releases

The repository has two GitHub Actions workflows:

- **CI** runs Go tests with the race detector, `go vet`, a Swift/macOS app
  build, and a Docker Buildx validation on pushes and pull requests.
- **Release** runs only for an exact `vX.Y.Z` tag. It requires the tag to match
  the root `VERSION` file, publishes multi-architecture Linux images to
  `ghcr.io/dodelidoo-labs/open-cdx`, builds a universal macOS app, signs it with
  hardened runtime, notarizes and staples it, emits SHA-256 checksums and a
  Sparkle appcast with a signed enclosure, and creates a GitHub Release with
  generated notes.

The macOS app uses the official open-source Sparkle 2 Swift package, pinned to
reviewed stable version `2.9.6`. Sparkle is an embedded application dependency,
not an update hosting service. Both the appcast and update archive continue to
come directly from Dodelidoo Labs' GitHub Releases over HTTPS.

The shipped application identifiers are fixed release invariants:

| Component | Identifier |
|---|---|
| Main application | `com.dodelidoo.opencdx` |
| Embedded router helper | `com.dodelidoo.opencdx.helper` |
| OAuth URL name | `com.dodelidoo.opencdx.oauth` |
| OAuth URL scheme | `com.dodelidoo.opencdx` |

Future OpenCDX-owned extensions must use
`com.dodelidoo.opencdx.<component>`. Sparkle's bundled components retain their
upstream identifiers.

## Release repository secrets

Configure these Actions secrets before creating a release tag:

| Secret | Value |
|---|---|
| `MACOS_CERTIFICATE_P12` | Base64-encoded Developer ID Application certificate and private key in PKCS#12 format |
| `MACOS_CERTIFICATE_PASSWORD` | Password protecting that PKCS#12 file |
| `MACOS_SIGNING_IDENTITY` | Exact identity, for example `Developer ID Application: TUKUTOI LLC (TEAMID)` |
| `APPLE_ID` | Apple ID used for notarization |
| `APPLE_TEAM_ID` | Apple Developer team identifier |
| `APPLE_APP_SPECIFIC_PASSWORD` | App-specific password for `notarytool` |
| `SPARKLE_PRIVATE_KEY_BASE64` | Base64 encoding of the private key file exported by Sparkle's `generate_keys` tool |

Do not store certificate files or passwords in the repository. The workflow
creates an ephemeral keychain and removes it even if the job fails. GitHub's
built-in `GITHUB_TOKEN` publishes the container and release assets; no personal
access token is required.

## Sparkle signing key

Sparkle uses a separate EdDSA (Ed25519) key pair. It is not an Apple signing
certificate. The production pair must be generated once with the
`bin/generate_keys` tool from the pinned Sparkle distribution. Export its
private key with that tool's `-x` option, make an offline encrypted backup, and
base64-encode the complete exported file into the dedicated
`SPARKLE_PRIVATE_KEY_BASE64` Actions secret. Do this only on a trusted machine;
never paste the private key into a workflow, issue, terminal log, or repository
file. Remove any unencrypted export after the Actions secret and backup have
been verified.

Only this public key is committed in the app's `Info.plist`:

```text
i+JDg7U+GX4CUOSnzOJXIvoovrLA49cR1Hz3GqgcAzA=
```

The release workflow fails at its initial validation gate if the secret is
missing. On the macOS runner it decodes the secret into a mode-`0600` file under
`RUNNER_TEMP`, uses it only with Sparkle's official tools, and removes it on
success, failure, or cancellation cleanup. Pull-request CI neither receives nor
requires this secret. Release publication jobs wait for the appcast signature
and key-pair checks to pass.

Keep at least two access-controlled backups in separate locations and record
which public key shipped in each release. Rotate only when necessary. For a
normal Developer ID-signed application update, Sparkle supports changing the
EdDSA key in a signed update as long as the Apple signing identity is not also
changed in that same update. Plan and test that transition against Sparkle's
current key-rotation guidance before publishing it. Losing both the private key
and usable backups turns recovery into a security-sensitive rotation procedure;
do not generate a replacement casually.

If the production key cannot be recovered, the secret does not match the
committed public key, or the appcast cannot be signed and verified, stop. Do not
substitute a placeholder key, tag a release, or publish release artifacts.

## Appcast and update behavior

The app's `SUFeedURL` is:

```text
https://github.com/Dodelidoo-Labs/open-cdx/releases/latest/download/appcast.xml
```

Sparkle starts with its standard user driver. It follows the normal consent
flow before enabling scheduled checks, and automatic downloads default to off.
The user can always run **Check for Updates…** from the menu-bar window,
including when the router is unconfigured or disconnected.

System profiling is explicitly disabled. Update checks contact only the GitHub
HTTPS feed and do not add router, provider, account, device-profile, telemetry,
log, or local-network information. There is no update analytics integration.

For each tagged release, the workflow:

1. Builds a monotonically increasing `CFBundleVersion` from
   `github.run_number`, while `CFBundleShortVersionString` remains `X.Y.Z`.
2. Signs Sparkle's XPC services, updater tools, framework, router helper, and
   outer app in inside-out order with hardened runtime.
3. Notarizes and staples the app, then creates the final versioned ZIP.
4. Runs Sparkle's bundled `generate_appcast` against that exact final ZIP with
   delta generation disabled for the initial implementation.
5. Verifies the semantic and bundle versions, archive length, minimum macOS
   version, exact versioned GitHub URL, and EdDSA signature. Sparkle's official
   verifier must accept the ZIP and reject a deliberately modified copy.
6. Uploads `appcast.xml`, the ZIP, and `SHA256SUMS.txt` as assets of the same
   GitHub Release.

The enclosure URL has this exact form:

```text
https://github.com/Dodelidoo-Labs/open-cdx/releases/download/vX.Y.Z/OpenCDX-Router-X.Y.Z-macOS-universal.zip
```

## Cut a release

1. Update `VERSION` and user-facing release notes/code as needed.
2. Merge a green `main` build.
3. Create and push an annotated tag matching the file exactly:

   ```sh
   VERSION=$(cat VERSION)
   git tag -a "v$VERSION" -m "OpenCDX v$VERSION"
   git push origin "v$VERSION"
   ```

4. Confirm the Release workflow publishes the GHCR manifest plus
   `OpenCDX-Router-X.Y.Z-macOS-universal.zip`, `SHA256SUMS.txt`, and
   `appcast.xml`. Do not announce the release unless the remote workflow is
   green.
5. Download the assets and complete the checks below.

Do not create or push a release tag until normal CI is green and the production
Sparkle secret has been provisioned and backed up.

## Verify a macOS release

Perform these checks on the downloaded release artifacts before announcing the
release:

1. Verify the ZIP against `SHA256SUMS.txt`, extract it, and confirm
   `Contents/Frameworks/Sparkle.framework` is present. Its versioned-framework
   symlinks must be intact, including `Versions/Current`.
2. Confirm `arm64` and `x86_64` slices in the app executable, router helper,
   Sparkle framework executable, `Autoupdate`, `Updater.app`, `Installer.xpc`,
   and `Downloader.xpc` with `lipo -info` or `lipo -verify_arch`.
3. Run strict `codesign --verify` checks on both XPC services, `Updater.app`,
   `Sparkle.framework`, and the outer app. A `codesign --deep --strict` check on
   the finished app is useful verification, but is not a substitute for the
   workflow's explicit inside-out nested signing.
4. Confirm the outer bundle identifier is `com.dodelidoo.opencdx`, its URL
   scheme is `com.dodelidoo.opencdx`, and the router helper's code-signing
   identifier is `com.dodelidoo.opencdx.helper`.
5. Run `xcrun stapler validate` and `spctl --assess --type execute` on the app.
6. Parse `appcast.xml` and confirm it has one full-update enclosure containing
   the release's semantic version, monotonic bundle build number, byte-exact
   archive length, `13.0` minimum system version, nonempty
   `sparkle:edSignature`, and the exact versioned HTTPS asset URL shown above.
7. On a trusted release-verification machine with controlled access to the key,
   use the pinned Sparkle `sign_update --verify --ed-key-file` command to verify
   the downloaded ZIP against the enclosure signature. Never place the key on a
   command line or print tool output that could expose it. The release workflow
   already performs this positive check and a negative check against a tampered
   archive with tool output suppressed.
8. After the release is published, fetch
   `https://github.com/Dodelidoo-Labs/open-cdx/releases/latest/download/appcast.xml`
   over HTTPS and confirm it resolves to the newly uploaded feed and retains the
   same enclosure URL and signature.

The dashboard checks GitHub's latest stable release API asynchronously. A
failed or rate-limited check never affects routing and is retried from cache
later.
