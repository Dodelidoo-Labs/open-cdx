# Research gates and evidence

Checked against OpenAI Codex main and provider documentation on 2026-08-28. Source-code links intentionally point to current upstream `main`; pin a reviewed commit for a release audit.

## 1. Router-owned PKCE grant and pre-inference account data

Status: implementation/source gate passed; live-account acceptance remains human-dependent.

Current Codex uses browser authorization code + S256 PKCE on localhost port 1455, requests offline access and the connector scopes, exchanges at `/oauth/token`, and derives ChatGPT account/user/plan/FedRAMP claims from the ID token. See Codex [login server](https://github.com/openai/codex/blob/main/codex-rs/login/src/server.rs) and [auth manager](https://github.com/openai/codex/blob/main/codex-rs/login/src/auth/manager.rs).

Current Codex independently uses ChatGPT bearer/account headers for the quota endpoint and Codex models endpoint. See [rate-limit resets](https://github.com/openai/codex/blob/main/codex-rs/backend-client/src/client/rate_limit_resets.rs), [models endpoint](https://github.com/openai/codex/blob/main/codex-rs/model-provider/src/models_endpoint.rs), and the [model API bridge](https://github.com/openai/codex/blob/main/codex-rs/codex-api/src/api_bridge.rs).

The router implements these as three validation calls immediately after exchange, before account readiness. Mocked endpoint tests cover PKCE/state and refresh. A real grant cannot be performed without a user intentionally logging in; follow `docs/verification.md` to close the live portion.

## 2. Two independent accounts remain refreshable

Status: architecture and concurrency passed; two-real-account test remains human-dependent.

Each account has a separate encrypted refresh-token envelope and a per-account single-flight lock. A 20-way concurrent refresh test proves one rotation request for one account, while account selection/affinity tests prove independent pool entries. The live two-account sequence is deliberately not claimed without two browser logins.

## 3. Codex works signed out with custom command auth

Status: passed on installed Codex CLI 0.150.1.

Codex exposes command auth in its provider schema; see [provider config types](https://github.com/openai/codex/blob/main/codex-rs/protocol/src/config_types.rs) and [provider auth](https://github.com/openai/codex/blob/main/codex-rs/model-provider/src/auth.rs).

`scripts/test-codex-signed-out.sh` starts a loopback mock, creates a fresh temporary auth-free `CODEX_HOME`, loads an external catalog, and verifies that Codex sends the token returned by the configured command. It neither reads nor changes the user's Codex home.

## 4. External catalog preserves native behavior

Status: passed.

Codex loads `model_catalog_json` as a complete model catalog at startup; see [config loading](https://github.com/openai/codex/blob/main/codex-rs/core/src/config/mod.rs) and the [ModelInfo schema](https://github.com/openai/codex/blob/main/codex-rs/protocol/src/openai_models.rs).

OpenAI snapshots are stored raw. Merge tests retain unknown fields, upstream reasoning presets including `ultra`, and opaque safety/internal models without field merge. Conflicts retain a complete primary definition and are reported.

## 5. OpenRouter metadata is sufficient for conservative decisions

Status: passed conservatively.

OpenRouter documents the [Responses API](https://openrouter.ai/docs/api/api-reference/responses/create-responses) and publishes per-model context, architecture modalities, supported parameters, and reasoning metadata from its [models endpoint](https://openrouter.ai/docs/guides/overview/models). The router includes only models with established text input/output, streaming Responses support, tools, tool choice, and context. Unknowns become dashboard exclusions. A generic `reasoning` or `reasoning_effort` flag alone does not establish individual effort values. The Codex picker receives exactly each model's `reasoning.supported_efforts` and `reasoning.default_effort`; models without that exact metadata receive no invented picker levels.

Codex 0.150.1 treats `apply_patch_tool_type: "freeform"` as a client-executed local filesystem tool. An end-to-end probe through this router established that OpenRouter's Responses endpoint accepts that wire shape and a compatible GLM model can invoke it, producing Codex's normal local diff. OpenRouter entries are therefore given the patch tool only after `tools` and `tool_choice` have passed the compatibility gate. This does not claim that OpenRouter or the destination edits local files.

OpenRouter also documents that an OpenAI-compatible `web_search` tool is hoisted to its server-side search tool and works with any OpenRouter model. The Codex `web_search_tool_type` field selects text versus text-and-image request shape; it is not an enable switch. Conversely, `supports_search_tool` controls Codex's deferred MCP/app tool discovery and is not evidence of web-search support. Ollama requests remove Codex's hosted `web_search` entry while preserving client-executed tools because Ollama does not operate that hosted tool.

The remaining Codex fields are deliberately not guessed from model prestige or names. In Codex 0.150.1, `tool_mode` selects direct tools versus the Code Mode runtime, `multi_agent_version` selects a Codex orchestration implementation, and `experimental_supported_tools` currently gates named Codex experiments. `supports_parallel_tool_calls` and `multi_agent_reasoning_effort` are not fields consumed by that version's `ModelInfo`; Codex emits `parallel_tool_calls: true` for normal Responses requests independently. OpenRouter's live catalog cannot truthfully supply those Codex-internal selectors. `context_window` and `max_context_window` are both populated from OpenRouter's published context limit; the latter is Codex's ceiling for local config overrides, not a separate provider feature.

## 6. Codex gated/feature headers are preserved

Status: passed for current HTTP Responses transport.

The authoritative current list is assembled in Codex [core client constants and request construction](https://github.com/openai/codex/blob/main/codex-rs/core/src/client.rs), with attestation in [attestation.rs](https://github.com/openai/codex/blob/main/codex-rs/core/src/attestation.rs). The proxy is allow-by-default for end-to-end metadata and blocks only hop-by-hop/security headers. Tests cover `x-codex-*`, originator/version, session/thread IDs, beta, user agent, subagent, memory generation, Responses-lite, Responses API feature, and attestation headers.

## 7. Auto-review and safety models remain present

Status: passed at the catalog/routing boundary.

The router never hardcodes an OpenAI allowlist and does not discard unknown native slugs. Tests retain `codex-auto-review` and unknown internal fields in the entitlement union. The final real-account check must confirm those entries are present in the live entitled catalogs returned for the user's plans.

## Ollama preparation

Ollama documents non-stateful Responses compatibility and model capabilities in [OpenAI compatibility](https://docs.ollama.com/api/openai-compatibility) and [show model information](https://docs.ollama.com/api/show). The initial implementation requires Ollama 0.13.3 or newer, supports an endpoint reachable by the remote router, includes only completion+tools models with a published context, and rejects stateful or unproven reasoning/text controls. For a non-reasoning Ollama model, the Codex catalog publishes one `none` entry as a UI no-op sentinel so model selection completes in one step. This does not claim an upstream reasoning capability: the router removes the resulting `effort: none` object before forwarding and continues to reject every non-no-op reasoning control. Codex-hosted web search is likewise removed before forwarding, so a local model cannot hallucinate a call to a server tool that Ollama does not execute.
