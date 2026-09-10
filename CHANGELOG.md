# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.3.0...HEAD
[1.3.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.2.0...v1.3.0
