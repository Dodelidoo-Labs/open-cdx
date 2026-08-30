# Releases

The repository has two GitHub Actions workflows:

- **CI** runs Go tests with the race detector, `go vet`, a Swift/macOS app
  build, and a Docker Buildx validation on pushes and pull requests.
- **Release** runs only for an exact `vX.Y.Z` tag. It requires the tag to match
  the root `VERSION` file, publishes multi-architecture Linux images to
  `ghcr.io/dodelidoo-labs/open-cdx`, builds a universal macOS app, signs it with
  hardened runtime, notarizes and staples it, emits SHA-256 checksums, and
  creates a GitHub Release with generated notes.

## Apple repository secrets

Configure these Actions secrets before creating a release tag:

| Secret | Value |
|---|---|
| `MACOS_CERTIFICATE_P12` | Base64-encoded Developer ID Application certificate and private key in PKCS#12 format |
| `MACOS_CERTIFICATE_PASSWORD` | Password protecting that PKCS#12 file |
| `MACOS_SIGNING_IDENTITY` | Exact identity, for example `Developer ID Application: TUKUTOI LLC (TEAMID)` |
| `APPLE_ID` | Apple ID used for notarization |
| `APPLE_TEAM_ID` | Apple Developer team identifier |
| `APPLE_APP_SPECIFIC_PASSWORD` | App-specific password for `notarytool` |

Do not store certificate files or passwords in the repository. The workflow
creates an ephemeral keychain and removes it even if the job fails. GitHub's
built-in `GITHUB_TOKEN` publishes the container and release assets; no personal
access token is required.

## Cut a release

1. Update `VERSION` and user-facing release notes/code as needed.
2. Merge a green `main` build.
3. Create and push an annotated tag matching the file exactly:

   ```sh
   VERSION=$(cat VERSION)
   git tag -a "v$VERSION" -m "OpenCDX v$VERSION"
   git push origin "v$VERSION"
   ```

4. Confirm the Release workflow publishes the GHCR manifest and the signed,
   notarized `OpenCDX-Router-X.Y.Z-macOS-universal.zip` asset.
5. Download the ZIP and verify it against `SHA256SUMS.txt` before announcing it.

The dashboard checks GitHub's latest stable release API asynchronously. A
failed or rate-limited check never affects routing and is retried from cache
later.
