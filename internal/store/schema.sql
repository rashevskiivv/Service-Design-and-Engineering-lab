-- local-generative-ai gateway: SQLite schema (user_version 1).
--
-- Embedded by internal/store and applied at every start; every statement is
-- idempotent. Adapted from docs/db-schema.sql for DECISIONS D5: api_keys has
-- expires_at (security #15), and key names are seat/role labels such as
-- "lab-07" or "team-demo", never real names (security #13).
--
-- Rules: no prompt or response content and no plaintext keys are ever stored.
-- Timestamps are INTEGER unix milliseconds, UTC. Keys are soft-revoked, never
-- deleted, so usage rows keep a valid key_id.
-- Connection pragmas (WAL, busy_timeout, foreign_keys, synchronous) are set in
-- the DSN by store.Open.

CREATE TABLE IF NOT EXISTS api_keys (
    id                INTEGER PRIMARY KEY,
    name              TEXT    NOT NULL CHECK (length(name) BETWEEN 1 AND 100),
    key_hash          BLOB    NOT NULL UNIQUE CHECK (length(key_hash) = 32), -- SHA-256 of the full key
    key_prefix        TEXT    NOT NULL,          -- first 12 chars, e.g. "lgai_Xy3kQ9a", for humans
    created_at        INTEGER NOT NULL,
    expires_at        INTEGER,                   -- NULL = never; an expired key gets the same 401 as an unknown one
    revoked_at        INTEGER,                   -- NULL = active
    rpm_limit         INTEGER CHECK (rpm_limit         IS NULL OR rpm_limit         >= 0), -- NULL = env default, 0 = unlimited
    daily_token_quota INTEGER CHECK (daily_token_quota IS NULL OR daily_token_quota >= 0),
    max_inflight      INTEGER CHECK (max_inflight      IS NULL OR max_inflight      >= 0),
    CHECK (expires_at IS NULL OR expires_at > created_at),
    CHECK (revoked_at IS NULL OR revoked_at >= created_at)
) STRICT;

-- One row per generation request (chat + tasks) that passed authentication.
-- 401s are logged, not stored. /v1/models, health and admin are not recorded.
CREATE TABLE IF NOT EXISTS usage (
    id                INTEGER PRIMARY KEY,
    ts                INTEGER NOT NULL,          -- request arrival
    request_id        TEXT    NOT NULL,
    key_id            INTEGER NOT NULL REFERENCES api_keys(id),
    endpoint          TEXT    NOT NULL CHECK (endpoint IN ('chat', 'explain', 'review', 'tests', 'fix')),
    model             TEXT,                      -- public alias; NULL if rejected before resolution
    language          TEXT CHECK (language IS NULL OR language IN ('python', 'java', 'go', 'c', 'cpp')),
    stream            INTEGER NOT NULL CHECK (stream IN (0, 1)),
    status            INTEGER NOT NULL CHECK (status BETWEEN 100 AND 599), -- effective: 499 = client gone
    error_code        TEXT,
    prompt_tokens     INTEGER NOT NULL DEFAULT 0 CHECK (prompt_tokens >= 0),
    completion_tokens INTEGER NOT NULL DEFAULT 0 CHECK (completion_tokens >= 0),
    usage_estimated   INTEGER NOT NULL DEFAULT 0 CHECK (usage_estimated IN (0, 1)),
    queue_ms          INTEGER CHECK (queue_ms IS NULL OR queue_ms >= 0),
    ttft_ms           INTEGER CHECK (ttft_ms  IS NULL OR ttft_ms  >= 0), -- streams only
    latency_ms        INTEGER NOT NULL CHECK (latency_ms >= 0)
) STRICT;

-- Quota check and per-key summary: WHERE key_id = ? AND ts >= ?
CREATE INDEX IF NOT EXISTS usage_key_ts ON usage (key_id, ts);

PRAGMA user_version = 1;
