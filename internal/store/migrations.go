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

const schemaV5 = `
ALTER TABLE policy_states
ADD COLUMN last_credit_recovered_ms INTEGER NOT NULL DEFAULT 0 CHECK (last_credit_recovered_ms >= 0);
`

const schemaV6 = `
CREATE TABLE player_sessions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id TEXT NOT NULL REFERENCES players(user_id) ON DELETE CASCADE,
    started_at TEXT NOT NULL,
    ended_at TEXT,
    last_observed_at TEXT NOT NULL,
    close_reason TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX player_sessions_one_open ON player_sessions(user_id) WHERE ended_at IS NULL;
CREATE INDEX player_sessions_range ON player_sessions(started_at, ended_at);
CREATE INDEX player_sessions_ended_at ON player_sessions(ended_at) WHERE ended_at IS NOT NULL;

CREATE TABLE concurrency_buckets (
    bucket_start TEXT PRIMARY KEY,
    weighted_count_ms INTEGER NOT NULL DEFAULT 0 CHECK(weighted_count_ms >= 0),
    observed_ms INTEGER NOT NULL DEFAULT 0 CHECK(observed_ms >= 0),
    max_count INTEGER NOT NULL DEFAULT 0 CHECK(max_count >= 0),
    max_observed_at TEXT
);

CREATE TABLE player_daily_stats (
    user_id TEXT NOT NULL REFERENCES players(user_id) ON DELETE CASCADE,
    local_date TEXT NOT NULL,
    observed_ms INTEGER NOT NULL DEFAULT 0 CHECK(observed_ms >= 0),
    first_observed_at TEXT NOT NULL,
    last_observed_at TEXT NOT NULL,
    session_count INTEGER NOT NULL DEFAULT 0 CHECK(session_count >= 0),
    PRIMARY KEY(user_id, local_date)
);
CREATE INDEX player_daily_stats_range ON player_daily_stats(local_date, observed_ms DESC, user_id);
`
