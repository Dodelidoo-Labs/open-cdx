package storage

const schema = `
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS accounts (
    id TEXT PRIMARY KEY,
    stable_hash BLOB NOT NULL UNIQUE,
    credential_blob BLOB NOT NULL,
    masked_email TEXT NOT NULL,
    plan TEXT NOT NULL,
    status TEXT NOT NULL,
    paused INTEGER NOT NULL DEFAULT 0,
    primary_account INTEGER NOT NULL DEFAULT 0,
    route_order INTEGER NOT NULL DEFAULT 0,
    quota_used_percent REAL NOT NULL DEFAULT 0,
    quota_reset_at INTEGER NOT NULL DEFAULT 0,
    reset_credits INTEGER NOT NULL DEFAULT 0,
    raw_quota_blob BLOB,
    last_error TEXT NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS entitlements (
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model_id TEXT NOT NULL,
    PRIMARY KEY (account_id, model_id)
);

CREATE TABLE IF NOT EXISTS devices (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL,
    enrollment_hash BLOB NOT NULL,
    credential_hash BLOB,
    issued_credential_blob BLOB,
    credential_acknowledged INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL,
    approved_at INTEGER NOT NULL DEFAULT 0,
    last_seen_at INTEGER NOT NULL DEFAULT 0,
    revoked_at INTEGER NOT NULL DEFAULT 0,
    catalog_synced_at INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS oauth_transactions (
    id TEXT PRIMARY KEY,
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    state_hash BLOB NOT NULL,
    verifier_blob BLOB NOT NULL,
    redirect_uri TEXT NOT NULL,
    expires_at INTEGER NOT NULL,
    used_at INTEGER NOT NULL DEFAULT 0,
    created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS providers (
    name TEXT PRIMARY KEY,
    base_url TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 0,
    secret_blob BLOB,
    config_json BLOB,
    health TEXT NOT NULL DEFAULT 'unconfigured',
    last_error TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS catalog_snapshots (
    provider TEXT NOT NULL,
    account_id TEXT NOT NULL DEFAULT '',
    etag TEXT NOT NULL DEFAULT '',
    raw_json BLOB NOT NULL,
    fetched_at INTEGER NOT NULL,
    PRIMARY KEY (provider, account_id)
);

CREATE TABLE IF NOT EXISTS merged_catalogs (
    device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    codex_version TEXT NOT NULL,
    content_hash TEXT NOT NULL,
    raw_json BLOB NOT NULL,
    created_at INTEGER NOT NULL,
    PRIMARY KEY (device_id, codex_version)
);

CREATE TABLE IF NOT EXISTS catalog_exclusions (
    provider TEXT NOT NULL,
    model_id TEXT NOT NULL,
    reason TEXT NOT NULL,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (provider, model_id)
);

CREATE TABLE IF NOT EXISTS instruction_contents (
    hash TEXT PRIMARY KEY,
    content_gzip BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS instruction_catalog_state (
    account_id TEXT PRIMARY KEY,
    fields_json BLOB NOT NULL,
    client_version TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS instruction_revisions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id TEXT NOT NULL,
    model TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('baseline','changed')),
    metadata BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS instruction_revisions_model_idx ON instruction_revisions(model,id);
CREATE INDEX IF NOT EXISTS instruction_revisions_account_idx ON instruction_revisions(account_id,id);
CREATE INDEX IF NOT EXISTS instruction_revisions_kind_idx ON instruction_revisions(kind,id);

CREATE TABLE IF NOT EXISTS catalog_conflicts (
    model_id TEXT PRIMARY KEY,
    detail TEXT NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS affinities (
    affinity_hash BLOB NOT NULL,
    model_id TEXT NOT NULL,
    account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    updated_at INTEGER NOT NULL,
    PRIMARY KEY (affinity_hash, model_id)
);

CREATE TABLE IF NOT EXISTS usage_aggregate (
    recorded_at TEXT NOT NULL DEFAULT '',
    device_id TEXT NOT NULL DEFAULT '',
    day TEXT NOT NULL,
    provider TEXT NOT NULL,
    model_id TEXT NOT NULL,
    account_id TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'routed' CHECK(source IN ('routed','reconciled')),
    routing TEXT NOT NULL DEFAULT 'routed' CHECK(routing IN ('routed','native')),
    requests INTEGER NOT NULL DEFAULT 0,
    input_tokens INTEGER NOT NULL DEFAULT 0,
    cached_input_tokens INTEGER NOT NULL DEFAULT 0,
    cache_write_input_tokens INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    reasoning_output_tokens INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (day, provider, model_id, account_id, routing, device_id, recorded_at)
);

CREATE TABLE IF NOT EXISTS request_logs (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    started_at TEXT NOT NULL,
    provider TEXT NOT NULL,
    model TEXT NOT NULL,
    device_id TEXT NOT NULL,
    outcome TEXT NOT NULL,
    metadata BLOB NOT NULL
);
CREATE INDEX IF NOT EXISTS request_logs_time_idx ON request_logs(started_at, id);
CREATE INDEX IF NOT EXISTS request_logs_provider_idx ON request_logs(provider, started_at, id);
CREATE INDEX IF NOT EXISTS request_logs_model_idx ON request_logs(model, started_at, id);
CREATE INDEX IF NOT EXISTS request_logs_device_idx ON request_logs(device_id, started_at, id);
CREATE INDEX IF NOT EXISTS request_logs_outcome_idx ON request_logs(outcome, started_at, id);

CREATE TABLE IF NOT EXISTS allowance_observations (
    source TEXT NOT NULL CHECK(source IN ('live','history')),
    account_id TEXT NOT NULL DEFAULT '',
    device_id TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL,
    reset_at TEXT NOT NULL,
    used_percent REAL NOT NULL,
    PRIMARY KEY(source, account_id, device_id, observed_at, reset_at, used_percent)
);

CREATE TABLE IF NOT EXISTS usage_reconciliation (
    device_id TEXT PRIMARY KEY NOT NULL DEFAULT '',
    reconciled_at INTEGER NOT NULL,
    files_scanned INTEGER NOT NULL,
    events_imported INTEGER NOT NULL,
    rows_imported INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS accounts_ready_idx ON accounts(status, paused, quota_used_percent);
CREATE INDEX IF NOT EXISTS oauth_expiry_idx ON oauth_transactions(expires_at);
CREATE INDEX IF NOT EXISTS affinities_updated_idx ON affinities(updated_at);

DELETE FROM devices WHERE status='revoked';

INSERT OR IGNORE INTO schema_migrations(version) VALUES (1);
INSERT OR IGNORE INTO schema_migrations(version) VALUES (2);
`
