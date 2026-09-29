# Claude Code allowance and usage

OpenCDX can show a Claude subscription's plan allowance and Claude Code's
token usage next to your Codex accounts. It only observes: Claude Code keeps
signing in with its own login and talks to Anthropic directly. OpenCDX never
routes, proxies, or authenticates Claude Code requests, never offers a Claude
sign-in, and never reads or stores Claude credentials.

```text
Claude Code ──► Anthropic (unchanged, direct)
     │
     ├─ status line command ──► local helper ──► router   plan allowance
     └─ OpenTelemetry logs  ──► local helper ──► router   request token counts
```

Both inputs are features Claude Code documents for local tooling: the
[status line](https://code.claude.com/docs/en/statusline) receives the
current `rate_limits` for Pro and Max subscribers, and
[OpenTelemetry export](https://code.claude.com/docs/en/monitoring-usage)
emits one `api_request` event per model request.

## What you get

- **macOS menu:** a **Claude Code** status row and one allowance row per
  Claude subscription, with weekly and 5-hour bars, reset times, and the same
  pace marker as OpenAI accounts. The row shows how old the reading is.
- **Dashboard telemetry:** Claude Code requests and tokens by model, machine,
  and provider (**Claude Code**), in the same charts, totals, breakdown, and
  CSV export as Codex usage.
- **Allowance overlay:** **Show allowances** on Telemetry includes a line per
  Claude subscription (labelled `Claude · <masked email>`) for each window.
  Observed weekly Claude resets appear as `↻` markers with cycle totals.
- **History import:** existing usage from local Claude Code transcripts.

Requirements: a paired Mac, a router and companion at version 1.7.0 or later,
and Claude Code with a Claude Pro or Max login for allowance readings. Usage
telemetry also works with other Claude Code logins.

## Connect Claude Code

Choose **Connect Claude Code…** in the menu or in **Settings → Claude Code**.
The app shows the exact changes and asks before writing
`~/.claude/settings.json`. The equivalent commands are:

```sh
router-helper claude-setup            # preview only
router-helper claude-setup --apply    # write the settings
```

`claude-setup` adds only these entries (the helper path and port are yours):

```json
{
  "statusLine": {
    "type": "command",
    "command": "'/Applications/OpenCDX Router.app/Contents/Resources/router-helper' claude-statusline"
  },
  "otelHeadersHelper": "'/Applications/OpenCDX Router.app/Contents/Resources/router-helper' claude-otel-headers",
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "OTEL_LOGS_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_LOGS_PROTOCOL": "http/json",
    "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://127.0.0.1:17464/claude/otlp/v1/logs"
  }
}
```

- **Your status line stays.** An existing status line is wrapped: the helper
  runs your original command with the same input and prints its output
  unchanged. Other status line options such as `padding` are kept. Without a
  previous status line, the wrapper prints nothing.
- **Your telemetry settings stay.** If `env` already sets
  `CLAUDE_CODE_ENABLE_TELEMETRY` or any `OTEL_*` variable, or
  `otelHeadersHelper` is already set, OpenCDX lists the conflicts and changes
  nothing. Configure the export manually in that case.
- Other settings keep their order and values. The previous file is saved next
  to it as `settings.json.opencdx-backup`, and a symlinked settings file is
  edited at its target.
- Start a new Claude Code session afterwards so both entries take effect.
- No metrics exporter is configured, so Claude Code exports only log events,
  and only to the local helper.

Run setup again after moving the app or changing the helper port; it updates
the OpenCDX entries in place and keeps the originally wrapped status line.

## Disconnect

Choose **Disconnect Claude Code…** in **Settings → Claude Code**, or:

```sh
router-helper claude-setup --remove --apply
```

This removes only the OpenCDX entries and restores the original status line
exactly. Recorded usage and allowance history stay on the router until you
reset telemetry.

## What is sent

From the status line input, the wrapper reads only `session_id` and the
`five_hour` and `seven_day` rate-limit windows. Paths, workspace, cost, and
all other fields are ignored.

From OpenTelemetry, the helper keeps only `api_request` events: request ID,
model, timestamp, and input, output, cache-read, and cache-creation token
counts. Prompt, response, and tool events are discarded on arrival.
Claude Code redacts prompt text by default; OpenCDX never asks it to include
content.

The account UUID is replaced on the Mac by a one-way digest, and the email is
masked there (`s***a@g***.com`). The router stores the digest, the masked
email, the machine that last reported, the latest windows, and token counters.
The helper holds unsent data in memory only; see [Security](security.md).

The OpenTelemetry endpoint accepts only a telemetry-scoped local credential
from `claude-otel-headers`. It lasts one hour, covering Claude Code's
29-minute refresh, and cannot authenticate inference. The status line command
uses the normal five-minute local credential.

## How it is counted

- **Tokens** follow OpenCDX's existing meaning: input includes cached context.
  Claude's cache reads appear as cached input, and cache writes as cache-write
  input. Claude Code sessions reuse large cached contexts, so token totals can
  dwarf request counts.
- **Requests** are counted once. Live telemetry and imported transcripts share
  Anthropic's request ID, so importing again, or importing after live
  reporting, never double-counts. Resetting telemetry forgets these IDs, so a
  later import can rebuild history.
- **Transcripts** omit auxiliary requests such as session titles. Those appear
  only in live telemetry. Thinking tokens appear only for imported history,
  because the live event does not report them.
- **Codex reconciliation** replaces only a machine's Codex rows; Claude Code
  rows are never replaced by it.

## Allowance readings

Claude Code reports allowance after each response while a session runs. When
Claude Code is closed, the menu keeps the last reading and shows its age; a
window whose reset time has passed shows as fully available until the next
reading. Usage in claude.ai or the Claude apps consumes the same allowance and
is included in the next reading, but not in token counts.

Unchanged readings are stored at most every five minutes. The overlay does not
connect readings more than 15 minutes apart, so idle periods appear as gaps.

Readings are attributed to a subscription through the session's telemetry
account. If telemetry is disabled, the reading is attributed to the machine
and shown as `Claude on <Mac name>`.

## Import history

Choose **Import Claude Code History…** in **Settings → Claude Code**, or:

```sh
router-helper claude-import --dry-run
router-helper claude-import [--claude-home /absolute/path]
```

The scan reads `projects/**/*.jsonl` under `CLAUDE_CONFIG_DIR` or `~/.claude`,
including subagent transcripts, and decodes only assistant usage records. It
uploads request IDs, timestamps, model IDs, and token counts, without account
attribution. Claude Code deletes transcripts after its `cleanupPeriodDays`
(30 days by default), so import soon after connecting to keep older usage.

## Troubleshooting

| Symptom | Check |
|---|---|
| **Claude Code — Waiting for Usage** | Normal between sessions. Start a new session after connecting. |
| No allowance rows | Allowance requires a Pro or Max login and appears after the first response. |
| **Upload Pending** | The router is unreachable; the helper retries and keeps up to 20,000 requests in memory. |
| Telemetry missing, allowance present | Run `/status` in Claude Code: it reports `otelHeadersHelper` failures. Confirm the helper is running. |
| Setup reports conflicts | Your settings already export telemetry. OpenCDX will not replace them. |
| Managed settings set an OTLP endpoint | Claude Code then ignores per-user logs endpoints; ask the administrator. |

## Commands

| Command | Effect |
|---|---|
| `router-helper claude-setup [--settings PATH] [--remove] [--apply] [--preview-json]` | Preview or change the OpenCDX entries in Claude Code settings |
| `router-helper claude-import [--claude-home PATH] [--dry-run] [--preview-json]` | Preview or import transcript usage |
| `router-helper claude-statusline` | Status line wrapper, run by Claude Code |
| `router-helper claude-otel-headers` | `otelHeadersHelper`, run by Claude Code |

## Not included

Claude Code cannot use OpenRouter or local models through OpenCDX, and
OpenCDX cannot sign in to or pool Claude subscriptions. Anthropic's
[legal and compliance terms](https://code.claude.com/docs/en/legal-and-compliance)
do not permit third-party applications to offer Claude.ai login or to collect,
store, or intermediate Claude.ai credentials.
