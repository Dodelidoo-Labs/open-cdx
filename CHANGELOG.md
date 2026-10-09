# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [1.10.1] - 2026-10-09

### Changed

- Show Codex credits as a cent-sign mark with the balance, on the same row as
  the reset tickets, in the macOS HUD and on the dashboard. Hovering names
  them, for example "62'500 Codex credits"; unlimited credits show as ∞.
- Mark the primary account with a star in the HUD instead of the word
  "Primary".

### Fixed

- Keep reset tickets at the same distance after the email on every account.
  Credits were stacked under the tickets and centred, which shifted the
  tickets on accounts with credits.

Update the router image and the macOS companion to get both views.

## [1.10.0] - 2026-10-09

### Added

- Show an OpenAI account's Codex credits beside its reset tickets on the
  dashboard and in the macOS HUD, for example "43 credits" or "Unlimited
  credits". The balance comes from the usage response OpenCDX already polls;
  accounts without credits show nothing. Update the router and the macOS
  companion to see it in the HUD.

### Fixed

- Keep Codex running on credits once every account's allowance is used up.
  The router treated an account at 100% as unusable even when OpenAI would
  have charged its credits, so the request failed with `quota_exhausted`.
  Allowance on any account is still used first.
- Fail over through every eligible account after usage-limit rejections. A
  request stopped after one retry, so a second rejected account ended it even
  when a third account could serve it.

Redeploy the router image for the routing fixes. Update the macOS companion as
well to see credits in the HUD.

## [1.9.1] - 2026-10-09

### Fixed

- Turn on Anthropic's prompt cache for Claude models routed through
  OpenRouter, with a one-hour lifetime. Anthropic caches only on request, and
  Codex never asks, so every Claude request was billed at the full input price;
  a 21-turn Orchestra task read none of its 1.94 million input tokens from
  cache. A request that already sets `cache_control` is left as sent.
- Keep each Codex conversation on one OpenRouter upstream provider by sending
  its conversation ID as OpenRouter's `X-Session-Id`. Models that several
  providers serve, such as DeepSeek, missed their automatic prompt cache when a
  request landed on another host; Codex history showed DeepSeek V4.1 Flash
  missing on 48% of large requests sent one to five minutes after the previous
  one.

Only the router changes; the macOS companion is unchanged. Redeploy the router
image to apply both fixes.

## [1.9.0] - 2026-10-02

### Added

- **Launch ChatGPT** below **Add OpenAI Account…** in the macOS menu,
  **Open ChatGPT Without Routing** in Settings, and
  `router-helper open-chatgpt`: start the installed new ChatGPT app with its
  own native configuration and sign-in so dots and other account features
  can use the native connection while CLI and IDE clients keep using the
  router. The app has separate local history and settings, and must be
  started through this action each time. No existing Codex configuration or
  credentials are copied or changed.

### Fixed

- Fetch the public Sparkle build dependency without requesting saved GitHub
  credentials from Keychain or netrc.
- Use native SwiftPM for the companion bundle so Xcode 27 records the actual
  linked SDK, preserving the existing SDK 26+ requirement and macOS 13
  deployment target.

Only the macOS companion changes; the router server is unchanged. ChatGPT app
requests bypass routing, so allowance bars still follow OpenAI's readings for
connected accounts, while app requests do not appear in live router token or
model statistics. Live acceptance confirmed dots became available; restoring
the Chat/Work tabs is not established.

## [1.8.1] - 2026-09-30

### Fixed

- Keep the Claude allowance current while working in the Claude desktop app,
  which does not run status line commands. Every five minutes while Claude
  Code is connected, and on **Refresh Allowances**, the helper reads the
  allowance from `claude -p /usage`. Claude Code signs in itself; the check
  makes no model request, loads no settings, and saves no session. It is
  skipped while the status line reported recently.

Only the macOS companion changes; the router server is unchanged.

## [1.8.0] - 2026-09-29

### Added

- Make the Telemetry chart a scrollable timeline. **24h**, **7d**, **30d**,
  **Year**, and **All** now zoom instead of filtering: drag or swipe to move
  through all recorded history, pinch or ⌘/Ctrl-scroll to zoom, use the arrow
  and +/− keys, and choose **Now →** to return. Totals, the breakdown, and CSV
  export follow the visible window.
- Show OpenAI and Claude logos before account emails in the macOS menu and in
  the allowance legend.

### Changed

- Bars are hourly, daily, weekly, or monthly by zoom level, aligned to calendar
  boundaries in the selected timezone, so equal bars are evenly spaced. The
  server now provides hourly usage for all timestamped history.
- Attribute Claude allowance to the signed-in account, shown as its masked
  email, even in sessions without telemetry. A machine placeholder is replaced
  by the first identified reading from that Mac.
- Remove the Claude Code status row and **Import Codex History…** from the
  menu; both imports are in Settings.

### Fixed

- Replace a helper daemon left running by an earlier app version, so updates
  take effect without quitting the app. The helper now reports its build.

Update both the router server and the macOS companion. Updating from 1.7.0
still requires quitting and reopening the app once, because the 1.7.0 app
cannot yet replace its running helper.

## [1.7.0] - 2026-09-29

### Added

- Show Claude plan allowance and Claude Code usage. **Connect Claude Code…**
  in the menu or Settings previews, then adds a status line wrapper and a local
  OpenTelemetry log export to `~/.claude/settings.json`. Your existing status
  line keeps rendering unchanged, and telemetry settings you configured
  yourself are never replaced. Disconnecting restores the original entries.
- Show weekly and 5-hour allowance bars with pace markers for each Claude
  subscription in the macOS menu, alongside the OpenAI account rows.
- Include Claude Code requests and tokens in Telemetry under the **Claude
  Code** provider, add Claude subscriptions to the allowance overlay, and show
  observed weekly Claude resets with cycle totals.
- Add **Import Claude Code History…** in Settings and
  `router-helper claude-import` to import usage from local Claude Code
  transcripts. Live telemetry and imports share Anthropic request IDs, so
  repeated imports never double-count.

### Changed

- Rename **Reconcile This Mac’s History…** to **Import Codex History…** so it
  is clearly separate from **Import Claude Code History…**.
- Codex history import now replaces only a machine's Codex rows and rejects
  the reserved `claude-code` provider. Resetting telemetry also clears Claude
  Code request deduplication, so transcripts can rebuild history.
- The uninstall script removes the OpenCDX entries from
  `~/.claude/settings.json` and restores the original status line.

OpenCDX only observes Claude Code: it does not route Claude Code, offer a
Claude sign-in, or handle Claude credentials, and it never reads or sends
prompts or responses. Update both the router server and the macOS companion,
then start a new Claude Code session after connecting. See
[Claude Code](docs/claude-code.md).

## [1.6.2] - 2026-09-23

### Fixed

- Keep explicit OpenAI access-program selections scoped to each account's own
  model catalog during initial routing, thread affinity, and quota fallback.
  Accounts without the requested access are never used as substitutes.
- Advertise model access programs available through any eligible account,
  including secondary accounts, regardless of which account is primary.
  Exclude these expected account-access differences from definition conflicts
  and preserve every account’s original catalog and entitlements.

Update the router server to apply these fixes. Sync the updated catalog with
the macOS companion, then restart Codex to load the available access programs.
No companion update is required for these fixes.

## [1.6.1] - 2026-09-22

### Fixed

- Preserve each OpenAI account's newest successfully used Codex catalog
  version across router restarts. Background and dashboard refreshes reuse
  that version instead of requesting catalogs as `0.0.0`, and older devices
  cannot downgrade it and remove newer models from routing and pickers.
- Serialize catalog refreshes per account so an older in-flight response
  cannot overwrite a catalog fetched for a newer Codex version.
- Discover models after a Codex upgrade during normal helper synchronization,
  without requiring a manual catalog refresh. Repeated syncs reuse the stored
  catalog until a newer client version is detected.
- Preserve the working catalog when an automatic upgrade check fails, and
  retry on the next sync. Refreshes without a known client version retain
  the cached catalog until a helper supplies one.

Update the router server to apply these fixes. The existing macOS companion
can supply the detected Codex version on its next catalog sync; no companion
update is required. Restart Codex after the helper reports a changed catalog.
Successful upstream refreshes can still remove models OpenAI no longer lists.

## [1.6.0] - 2026-09-19

### Added

- Add account allowance lines to Telemetry. Use **Show allowances** beside the
  weekly transitions row, choose an available window, and click the account
  legends below the chart to show or hide individual lines. Visibility and
  window choices are remembered in the browser; the overlay is off by default.
- Plot remaining allowance against a fixed 0–100% right axis using the selected
  time range and timezone. Balances stay account-wide when usage is filtered
  by machine. Missing readings remain gaps, and observed resets appear as jumps.
- Record short-window allowance readings alongside existing weekly history.
  Only account-attributed server polls supply the overlay; imported machine
  history is not assigned to an account or converted into allowance estimates.
- Add an isolated synthetic-data dashboard fixture for previewing allowance
  drain, resets, collection gaps, and usage spikes without live credentials.

### Changed

- Show hourly usage bars in the 24-hour view so allowance drops can be compared
  with activity at the same time. Shared tooltips include allowance values and
  their observation times. Longer ranges retain calendar aggregation.
- Keep allowance controls compact beneath the chart, with the window selector
  visible only while the overlay is enabled.

Update the router server to enable this feature. Existing live weekly readings
are retained automatically; short-window history starts accumulating after the
upgrade. No telemetry reset or macOS companion change is required.

## [1.5.0] - 2026-09-18

### Added

- Expose authenticated `GET /v1/models` on the local helper, using the same
  synchronized catalog as Codex. Model additions, removals, context limits,
  reasoning levels, and service tiers are discovered without copied model lists.
  Metadata changes update the ETag; hidden models and private instructions are
  excluded.
- Add `router-helper token --json` for clients that renew command credentials
  using `access_token` and `expires_in`. The default bare-token output is unchanged.

### Fixed

- Retain OpenAI's `__oailb` routing cookie in memory per account and HTTPS
  upstream origin, honoring cookie scope and expiry. Authentication refresh
  retains the account's routing state; quota failover cannot share it with another
  account. Client cookies remain blocked and upstream cookies stay private.
- Bring macOS Settings to the foreground when opened from the menu-bar HUD.

### Changed

- Return native OpenAI inference redirects without following them, preventing
  selected-account credentials and routing cookies from reaching another
  destination.

Update the router server for the cookie-routing fix and the macOS companion for
live discovery, JSON credentials, and the Settings fix. Existing Codex headers
and request bodies remain intact. Clients can use the documented local API;
their implementations and update procedures are maintained separately.

## [1.4.2] - 2026-09-16

### Fixed

- Remove the router's 64 MiB request-body cap for inference and compaction.
  Large requests, including conversations with retained images, can now reach
  the provider without OpenCDX rejecting them with HTTP 413. Provider limits
  still apply.
- Report interrupted request-body reads as read failures instead of incorrectly
  reporting that the request exceeds a size limit.

Update the router server to apply these fixes, then resume affected threads;
their saved history does not need to be recreated. The macOS companion is
rebuilt for this release, but updating the companion alone does not fix HTTP 413.

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

[Unreleased]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.10.1...HEAD
[1.10.1]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.10.0...v1.10.1
[1.10.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.9.1...v1.10.0
[1.9.1]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.9.0...v1.9.1
[1.9.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.8.1...v1.9.0
[1.8.1]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.8.0...v1.8.1
[1.8.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.7.0...v1.8.0
[1.7.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.6.2...v1.7.0
[1.6.2]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.6.1...v1.6.2
[1.6.1]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.6.0...v1.6.1
[1.6.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.5.0...v1.6.0
[1.5.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.4.2...v1.5.0
[1.4.2]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.4.1...v1.4.2
[1.4.1]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.4.0...v1.4.1
[1.4.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/Dodelidoo-Labs/open-cdx/compare/v1.2.0...v1.3.0
