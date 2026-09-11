# Request logs

Open **Logs** under **Observe** in the administrator dashboard. The page records authenticated inference requests handled by the router, including `/v1/responses/compact`, request validation failures, routing failures, and upstream errors. A row appears when a request finishes; an interrupted server process cannot save requests still in flight. Enrollment, dashboard traffic, and requests rejected by device authentication are outside this inference log.

Rows show the start time, requested model and reasoning effort, provider and selected account, machine, input/cached/output/reasoning tokens, approximate output tokens per second, HTTP status, outcome, and duration. **Details** adds request and response identifiers, thread/session identifiers, client version, service tier, byte counts, error type/code/message, and each upstream attempt's account, status, header latency, first-byte latency, and duration. A `200` response can still have an error or incomplete outcome if its stream fails.

Filter by provider, outcome, exact model ID, or machine. The page loads 50 rows at a time, newest start time first, and refreshes the newest page every five seconds while visible. Older pages remain stable while new requests arrive. Dates use the timezone selected in Telemetry, or the browser timezone by default.

## Privacy and performance

The router stores selected metadata, not prompt bodies, instructions, generated output, arbitrary headers, or credential objects. Provider diagnostic messages are limited to 2,048 bytes and common credential patterns are redacted. These messages can include provider-supplied context; treat logs and downloaded backups as private operational data.

Collection reuses a bounded 256 KiB in-memory response tail, without buffering an entire response or writing each stream chunk to disk. One metadata record is inserted after completion, with a two-second storage timeout; a storage failure is reported in the router's operational log without changing the upstream response. Token counts are **Unreported** when the provider omits them or the terminal metadata exceeds the captured tail. Zero is shown only for a captured usage object. Tokens/s is an approximation based on output tokens divided by elapsed time after the first response byte, not a provider billing or latency guarantee.

SQLite indexes support pagination and filters. Logs are retained without automatic expiration, so database size grows with request volume. Listing and backup downloads read bounded pages, and imports write bounded batches. Backups and logs do not contain account credentials and cannot restore accounts or device access.

## Backup and restore

**Download all logs** exports all stored logs as versioned NDJSON, regardless of the active page filters. Download it before wiping the router database. On a fresh server, sign in, open **Logs**, choose **Import logs**, and select the downloaded file.

The browser reads and uploads the file in small batches. Each batch commits atomically; if an import is interrupted, retry the same file. Existing request IDs are skipped, so repeated imports do not create duplicates. Restored records keep their original request timestamps, machine names, account labels, and IDs even when the corresponding devices or accounts no longer exist.

Request logs are independent of usage telemetry. Importing logs does not increase token totals, and resetting or reconciling telemetry does not remove logs. Local Codex history reconciliation cannot reconstruct these proxy observations. Deleting the router database removes them unless you restore a log export or a full database backup.
