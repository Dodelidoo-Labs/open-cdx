# Instruction history

Open **Instructions** under **Observe** to inspect instruction changes over time. The router takes an initial baseline from each account's stored OpenAI catalogue, or its first successful catalogue fetch. Later changes create a history entry per account and model, with every changed field included. Account labels, plans, observation times, and the client versions used for catalogue requests are retained.

The tracker covers `base_instructions`, fields whose names contain `instruction` (including instruction-related controls), and all values in `model_messages`. The latter includes instruction templates, interpolation variables, approval and collaboration policies, permissions, and other provider-supplied instruction material. Catalogue-wide instruction fields are recorded under `(catalog)`. Duplicate model definitions remain separately identifiable.

Changing an unrelated model description, model ordering, or a third-party catalogue does not create an entry. Adding or removing instruction fields, changing whitespace inside instruction text, removing a model with tracked instructions, and reverting to an earlier instruction version do create entries. Failed catalogue fetches do not change the history.

## Notifications and diffs

The navigation badge counts unseen instruction updates while the dashboard is open. It refreshes every 15 seconds while the page is visible. Baselines do not count as updates. **Mark all seen** clears the badge in the current browser and synchronizes other tabs in that browser; merely opening a diff does not mark other updates seen.

Filter by model, account, and changes/baselines. **View diff** lists every changed field. Expand a field to load its before/after text and line diff. Removed lines are red, added lines are green, and unchanged context is collapsed. **Show full field** expands the context. Large results load additional lines on demand; large rewrites can be shown as whole removed/added blocks without dropping text. Empty text, missing fields, and JSON `null` remain distinct.

## Scope and persistence

Times indicate when the router observed a catalogue, not when OpenAI published it. Catalogues can vary with account, plan, and client version; these observations do not establish that an instruction changed for every Codex client. Each account is compared with its own previous stored snapshot, with old/new client versions shown when available.

Only instructions received in OpenAI's catalogues are visible here. Instructions built into the Codex application, workspace files, plugins, skills, and conversation prompts are outside this tracker unless their text is supplied in a tracked catalogue field. History before the starting cached snapshot cannot be reconstructed.

History and catalogue updates commit in the same database transaction. Instruction content is compressed and deduplicated across accounts, models, and revisions. Identical refreshes add no history entries. Lists return metadata only; content loads when a field is opened.

History is retained without automatic expiration, including after an account is removed. Telemetry resets and reconciliation do not affect it. Back up the router database to preserve instruction history across a server wipe; request-log exports do not include it. The history contains provider-supplied instruction text, not user prompts, generated answers, or account credentials.
