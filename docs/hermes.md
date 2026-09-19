# Hermes and live model discovery

The local helper exposes authenticated `GET /v1/models` alongside the Responses
API. It reads the atomically synchronized catalog on every request. Model IDs,
context windows, advertised reasoning levels and defaults, modalities, and
service tiers come from the same catalog Codex reads. Hidden models are omitted.
No model list needs to be copied into another application's configuration.

The helper synchronizes with the router every minute. Discovery reflects the
latest completed synchronization; provider refresh intervals on the router
still apply. The endpoint returns an ETag that also changes for metadata-only
updates and `Cache-Control: private, no-cache`. Missing or invalid catalogs
return `503`; an empty catalog returns an empty `data` array. Discovery does not
count as inference activity or usage.

## Configuration

```yaml
model:
  provider: opencdx
  default: gpt-6-astra
providers:
  opencdx:
    base_url: http://127.0.0.1:17464/v1
    api_mode: codex_responses
    discover_models: true
    key_cmd: >-
      "/Applications/OpenCDX Router.app/Contents/Resources/router-helper" token --json
```

Select an available default model. Do not add a `models:` snapshot. The token
command returns `access_token` and `expires_in`; Hermes can renew credentials
before expiry. The helper's default `token` output remains a bare token for
Codex command authentication. No provider API key is stored in Hermes.

## Hermes compatibility patch

Hermes revision `1879088d3fadb885d04035f5fa658b6efc08a7a1` supports Responses
and command credentials, but its custom endpoint discovery discards model
metadata. Its Desktop picker uses a generic reasoning ladder and can reinsert
the selected model after discovery removes it. Its endpoint Test button also
does not use saved command credentials.

The accompanying [compatibility patch](../integrations/hermes/live-catalog.patch)
addresses those client behaviors for custom endpoints:

- Cache model IDs and metadata together, scoped to endpoint and credentials;
  respect the endpoint's cache lifetime.
- Treat successful discovery as authoritative, including empty catalogs and
  removals of the selected model.
- Use advertised effort lists, defaults, service tiers, and context limits in
  the picker and Responses request path.
- Refresh the Desktop picker each minute while mounted.
- Use saved credentials for endpoint validation only when its destination
  matches the saved endpoint.
- Send official Codex affinity headers on native Codex and the named `opencdx`
  Responses route, including auxiliary requests (compression and memory work).
  `session-id` matches the effective body `prompt_cache_key`; `thread-id` and
  `x-client-request-id` carry the physical conversation ID. Cache scope survives
  Hermes compression rotation. The router keeps account selection separate from
  upstream cache affinity; upstream cache reuse is still determined by the provider.
- Keep OpenRouter/Ollama backend classification unchanged. Auxiliary requests
  inherit the main openCDX route identity only when their complete endpoints match;
  an explicitly selected `opencdx` auxiliary provider also enables the headers.
- Prevent stale mixed-case configured headers from creating duplicate affinity
  fields in the OpenAI SDK. Explicit body overrides remain authoritative, keys
  unsafe for HTTP headers are hashed consistently, and omitting a body key also
  omits its cache-affinity header.

This is a local Hermes source patch, not upstream Hermes functionality. Hermes
updates may replace or conflict with it. Keep the patch and re-check client
compatibility when updating; do not silently fall back to copied model lists.
The patch is version-specific: use `git apply --check` in the Hermes checkout
before applying it. The full live-catalog patch includes Desktop changes and needs
a Desktop rebuild. Updates limited to the Python cache/header fix only need a
Hermes backend restart; an already running backend retains its imported code.

## Keeping the changes across Hermes updates

OpenCDX updates do not modify Hermes. Keep this OpenCDX checkout: the combined
patch is versioned here, outside Hermes's updater, and is also recoverable from
the OpenCDX release tag. It contains both live discovery and cache-header fixes.
Keep the `providers.opencdx` configuration above. Restart the current Hermes
backend to activate installed Python changes; no update or patch reapplication
is needed just to activate those changes.

For future Hermes updates, use a controlled update instead of assuming its
updater will retain local changes. Hermes may stash them (including newly added
files) and leave that stash unapplied. An update can also replace the patched
Desktop build. The maintained patch was tested against Hermes commit
`1879088d3fadb885d04035f5fa658b6efc08a7a1`; it is not guaranteed to apply to future
revisions.

1. Quit Hermes Desktop and stop any Hermes gateway/backend using this checkout.
   Back up `~/.hermes/config.yaml` and any unrelated local Hermes edits. From the
   **OpenCDX repository root**, select the externally stored patch:

   ```sh
   HERMES_PATCH="$PWD/integrations/hermes/live-catalog.patch"
   HERMES_REPO="$HOME/.hermes/hermes-agent"
   ```

2. Remove only this patch before updating. The check must succeed; if it fails,
   stop and inspect the differences instead of resetting the Hermes checkout:

   ```sh
   git -C "$HERMES_REPO" apply --reverse --check "$HERMES_PATCH" &&
     git -C "$HERMES_REPO" apply --reverse "$HERMES_PATCH"
   ```

3. Run `hermes update`. After it succeeds, check compatibility and reapply:

   ```sh
   git -C "$HERMES_REPO" apply --check "$HERMES_PATCH" &&
     git -C "$HERMES_REPO" apply "$HERMES_PATCH"
   ```

   If the check fails, the patch needs review against that Hermes revision;
   upstream may have changed the code or already implemented part of the fix.
   Do not force it or assume the updated app still has the integration. Adapt
   and retest the patch, or restore the previously backed-up installation.

4. Rebuild Desktop from the patched source, then launch it:

   ```sh
   hermes desktop --force-build --build-only && hermes desktop
   ```

5. Verify the live model list, advertised reasoning choices, and cache headers
   again. Python-only users can skip the Desktop build, but must restart their
   CLI/gateway processes. A clean patch application alone does not prove runtime
   compatibility with a newer Hermes version.

If Hermes has already updated and parked your changes, inspect `git stash list`
and `git status` in its checkout before applying anything. Do not apply both the
old stash and this patch; that duplicates the same changes. Keep the stash until
the repaired installation is verified. Longer term, these changes need upstream
Hermes support or a maintained fork to eliminate patch maintenance.

## Verification

The helper regression test replaces a catalog while serving requests and checks
additions, removals (including the final model), metadata-only ETag changes,
authentication, and invalid-file handling. Hermes tests cover the discovery-to-
picker-to-request path, credential cache isolation, an empty live catalog,
endpoint validation credentials, and rendered reasoning option changes.

Cache-header tests exercise isolated configuration and real runtime resolution,
AIAgent request construction, preflight, and OpenAI SDK HTTP serialization. They
cover follow-up turns, compression rotation, all three openCDX model families,
auxiliary route isolation, explicit body overrides, and stale mixed-case headers.
They use a local HTTP transport stub and do not spend inference tokens; they prove
request compatibility, not a particular production cache-hit rate.
