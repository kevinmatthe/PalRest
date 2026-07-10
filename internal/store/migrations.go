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
    generation INTEGER NOT NULL DEFAULT 0,
    error_summary TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS enforcement_lookup
ON enforcement_events(user_id, period_key, generation, created_at);
`

const schemaV2 = `
ALTER TABLE enforcement_events ADD COLUMN policy_revision TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS enforcement_policy_lookup
ON enforcement_events(user_id, period_key, policy_revision, created_at);
`

const schemaV3 = `
CREATE TABLE IF NOT EXISTS policy_states (
    user_id TEXT NOT NULL REFERENCES players(user_id) ON DELETE CASCADE,
    policy_revision TEXT NOT NULL,
    strategy TEXT NOT NULL,
    window_start TEXT,
    used_ms INTEGER NOT NULL DEFAULT 0 CHECK (used_ms >= 0),
    cooldown_until TEXT,
    credit_ms INTEGER NOT NULL DEFAULT 0 CHECK (credit_ms >= 0),
    last_credit_at TEXT,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, policy_revision)
);

CREATE INDEX IF NOT EXISTS policy_states_user_lookup
ON policy_states(user_id, updated_at);
`

const schemaV4 = `
CREATE TABLE IF NOT EXISTS policy_documents (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    policy_yaml TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
`
