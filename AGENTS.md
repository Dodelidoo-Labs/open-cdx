# Agent notes

Start with [docs/README.md](docs/README.md). [Development](docs/development.md)
and [Verification](docs/verification.md) are authoritative for building and
testing; this file lists only what is easy to get wrong.

## Checks

```sh
go test ./... && go vet ./...
node --test web/tests/*.test.cjs
swift test --disable-sandbox -c release --package-path mac/RouterMenu
```

Docker and restart validation run in the Multipass VM `opencdx-docker-test`.
Recreate it with the commands in Development when it is missing.

## Protect the operator's Mac

- Never install or launch another copy of the app. Extra copies and unstable
  signing identities create permanent macOS privacy entries. Ad-hoc signing is
  only for non-installed CI validation. Installing is the operator's decision,
  through `scripts/install-macos-app.sh` with the stable signing identity.
- Keep test routers and helpers on loopback, with their own configuration,
  port, and `catalog_path`, and set `OPENCODEX_HELPER_SECRET_FILE`. Otherwise a
  test helper replaces the installed helper's Keychain credential.
- Never edit the operator's `~/.codex/config.toml` or `~/.claude/settings.json`.
  Test Claude Code setup against a separate `--settings` file.

## Product boundaries

- Claude Code support observes allowance and usage only. Do not add Claude
  sign-in, Claude credential storage, or routing of Claude Code traffic; see
  [Claude Code](docs/claude-code.md#not-included).
- Prompts and responses are never stored or logged.

## Releases

Releases are tag-driven through GitHub Actions ([Releases](docs/releases.md)).
Update `VERSION` and `CHANGELOG.md`, and do not tag, push, or publish without
the operator's explicit approval.
