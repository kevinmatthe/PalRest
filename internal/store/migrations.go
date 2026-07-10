package store

const schemaV1 = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS players (
    user_id TEXT PRIMARY KEY,
    player_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    account_name TEXT NOT NULL DEFAULT '',
    first_seen TEXT NOT NULL,
    last_online TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS usage_periods (
    user_id TEXT NOT NULL REFERENCES players(user_id) ON DELETE CASCADE,
    period_key TEXT NOT NULL,
    period_start TEXT NOT NULL,
    period_end TEXT NOT NULL,
    used_ms INTEGER NOT NULL CHECK (used_ms >= 0),
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, period_key)
);

CREATE TABLE IF NOT EXISTS warning_events (
    user_id TEXT NOT NULL REFERENCES players(user_id) ON DELETE CASCADE,
    period_key TEXT NOT NULL,
    threshold_ms INTEGER NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INTEGER NOT NULL DEFAULT 0,
    next_attempt TEXT,
    last_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, period_key, threshold_ms)
);

CREATE TABLE IF NOT EXISTS enforcement_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES players(user_id) ON DELETE CASCADE,
    period_key TEXT NOT NULL,
    action TEXT NOT NULL,
    result TEXT NOT NULL,
    policy_revision TEXT NOT NULL DEFAULT '',
    generation INTEGER NOT NULL DEFAULT 0,
    error_summary TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS enforcement_lookup
ON enforcement_events(user_id, period_key, generation, created_at);
`
