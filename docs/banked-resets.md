# Banked Codex resets

Each available earned reset appears as a ticket beside its account's email in
the macOS HUD and the server's Accounts dashboard. The HUD uses the native SF
Symbol `ticket.fill`. Click a ticket, check the account shown, and choose
**Apply Reset** or **Cancel**. Applying uses one reset only. Zero available
resets produces no tickets.

Known expiration timestamps remove tickets automatically, including while the
confirmation is open. If OpenAI supplies only a count, OpenCDX shows that many
tickets and discovers expiration through quota polling. The default upstream
quota refresh interval is five minutes; **Refresh Quotas** requests an update.
When detail rows are capped, the remaining count appears as tickets for which
OpenAI selects the next credit on redemption.

After redemption, OpenCDX refreshes the account's quota and reset bank. If that
refresh fails, it hides stale reset metadata until a later successful refresh.
An uncertain redemption can be retried with the same ticket; the HUD and
dashboard retain its idempotency key so the retry cannot consume another reset.

Update both the server and macOS companion to use redemption from the HUD.
Account credentials stay on the server. Both administrator sessions (with CSRF)
and approved paired devices may redeem resets from the shared account pool.

## API and helper command

`GET /api/v1/device/status` includes each account's opaque OpenCDX `id`,
`reset_credits`, and `reset_tickets`. A ticket can contain its opaque `id` and an
RFC 3339 `expires_at`; either field can be absent. These IDs are not credentials.

With paired-device bearer authentication, send:

```http
POST /api/v1/accounts/{account-id}/resets/consume
Content-Type: application/json
Authorization: Bearer <paired-device-token>

{"idempotency_key":"<UUID>","credit_id":"<optional-ticket-id>"}
```

The dashboard uses `POST /admin/accounts/{account-id}/resets/consume` with its
administrator cookie and `X-CSRF-Token`. The response includes `outcome` and
`quotas_refreshed`. Outcomes are `reset`, `alreadyRedeemed`, `nothingToReset`,
and `noCredit`. The latter two do not consume a reset. Treat `alreadyRedeemed`
as successful completion of the same logical attempt.

The local helper also supports:

```sh
router-helper consume-reset --account ACCOUNT_ID --credit TICKET_ID \
  --idempotency-key REDEMPTION_UUID --confirm
```

The helper daemon must be running. Omit `--credit` to let OpenAI select the next
available reset. Generate a new UUID for a new redemption, and reuse that exact
UUID for retries after a timeout, connection loss, or other uncertain result.
The command prints JSON containing the outcome and refreshed helper status.

## Upstream contract and verification

The public [Codex app-server documentation](https://developers.openai.com/codex/app-server#auth-endpoints)
describes availability and redemption semantics. OpenCDX uses the underlying
account-authenticated HTTP endpoints, consistent with its existing quota
integration, without requiring a Codex process on the server:

- `GET /wham/usage` supplies `rate_limit_reset_credits.available_count`.
- `GET /wham/rate-limit-reset-credits` supplies individual credit details.
- `POST /wham/rate-limit-reset-credits/consume` accepts `redeem_request_id` and
  optional `credit_id`; the response's `code` maps to the outcomes above.

These HTTP endpoints are an upstream implementation detail. Their request and
response mapping was checked with Codex 0.153.4 against an isolated localhost
mock using fake account credentials. No real reset was consumed. Provider,
account-manager, HTTP-authentication, helper, and Swift tests cover account
selection, idempotency, expiration, refresh failure, and status propagation.

For manual acceptance, use an account with multiple banked resets: cancel once,
apply one, and verify the count drops by one while other accounts remain
unchanged. Check expiration with the HUD open and verify an account without
resets has no tickets.
