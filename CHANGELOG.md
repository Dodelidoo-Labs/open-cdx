# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.4.1] - 2026-09-11

### Fixed

- Preserve successful request log outcomes when a client closes the connection
  after receiving response completion. Actual interruptions, provider failures,
  and incomplete responses retain their respective outcomes. Update the router
  server to apply this fix to new requests; existing log entries are unchanged,
  and the macOS companion does not require an update for this fix.

## [1.4.0] - 2026-09-11

### Added

- Inspect authenticated inference requests in the dashboard's **Logs** view,
  including model settings, token usage, timings, routing attempts, and errors.
  Export and restore logs with portable, duplicate-safe backups.
- Track provider-supplied Codex catalog instructions with per-account baselines,
  persistent history, unread update notifications, and complete per-field diffs.
- Compare differing model catalog fields across accounts and identify the
  complete upstream definition retained for routing.
- Apply one banked Codex reset from an account's ticket in the macOS menu or
  Accounts dashboard, or through the authenticated API and helper command.
  Confirm the account before redemption; retries reuse an idempotency key,
  expired tickets disappear, and quota data refreshes afterward. Update both
  the server and macOS companion to redeem resets from the menu.

### Changed

- Retain selected request metadata and bounded, credential-redacted provider
  diagnostics without storing conversation bodies. Request logs and catalog
  instruction history survive telemetry resets and have no automatic expiration;
  include the router database in backups and monitor its disk usage. Log exports
  contain private operational metadata and do not include instruction history.
- Use Xcode 26 and macOS SDK 26 or newer for macOS builds, with an SDK check on
  every executable architecture. The minimum supported macOS version remains 13.0.

### Fixed

- Use the native rounded menu surface on macOS Tahoe by linking the application
  with the current SDK, eliminating the square legacy backdrop.

## [1.3.0] - 2026-09-10

### Added

- Show Spark allowance windows, remaining quota, and pace in the macOS menu.
- Attribute routed usage and imported history to the authenticated device.
- Filter dashboard totals, charts, activity, and CSV exports by machine.
- Select a reporting timezone independently in each browser, defaulting to the
  browser's timezone and remembering an explicit selection. Apply it to calendar
  ranges, charts, timestamps, and CSV date grouping while storing timestamps in UTC.
- Show weekly Codex allowance window transitions and recorded usage per cycle,
  using observations from quota polls and reconciled history.
- Configure a fallback reporting timezone with `OPENCODEX_TIMEZONE` or
  `routerd --timezone` when a viewer does not select one.

### Changed

- Move the macOS server-history deletion action from the HUD to Settings, with
  an explicit warning that it deletes history for every machine and a confirmation
  that defaults to Cancel. Label reconciliation as affecting only this Mac.
- Reconcile usage history per machine, preserving other machines' telemetry and
  reconciliation metadata. Update the server before reconciling history from
  multiple machines; older servers replace telemetry globally.
- Show existing usage without device attribution under **Unknown device**.
  When upgrading from v1.2.0, first upgrade the server and every helper and
  verify that the original local histories are available. To rebuild attribution,
  reset server telemetry once, then reconcile each machine's own history without
  resetting between imports. A reset permanently removes any server history
  that cannot be recovered from those files. Importing copied history from
  multiple machines counts it once for each importing device.
- Preserve original request timestamps in reconciled history. Existing daily-only
  history stays readable. If history already has machine attribution, reconcile
  each machine with the updated helper to add timestamps without a reset.

### Fixed

- Prevent duplicate enrollment from an already enrolled Mac, including during
  connection failures. Allow enrollment for a different server or after the
  configured server rejects the device credential.
- Make the 24h, 7d, and 30d filters use exact rolling durations, independent of
  timezone and daylight-saving changes. Show overlapping daily-only history as
  unavailable for rolling totals instead of presenting incomplete counts.

[Unreleased]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.4.1...HEAD
[1.4.1]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.2.0...v1.3.0
