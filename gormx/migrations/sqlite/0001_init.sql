-- fxkit framework tables (SQLite)
--
-- 本地开发/单机测试用；生产请用 PostgreSQL/MySQL。
-- 版本：0001

CREATE TABLE IF NOT EXISTS outbox_events (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    pubsub          TEXT    NOT NULL,
    topic           TEXT    NOT NULL,
    payload         BLOB    NOT NULL,
    idempotency_key TEXT,
    status          TEXT    NOT NULL,
    attempts        INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT,
    lease_id        TEXT,
    locked_at       DATETIME,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    published_at    DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_outbox_events_idempotency_key ON outbox_events (idempotency_key);
CREATE INDEX IF NOT EXISTS idx_outbox_events_lease_id ON outbox_events (lease_id);
CREATE INDEX IF NOT EXISTS idx_outbox_claim ON outbox_events (status, locked_at);
CREATE INDEX IF NOT EXISTS idx_outbox_poll ON outbox_events (status, topic, created_at);

CREATE TABLE IF NOT EXISTS outbox_inbox_events (
    id           TEXT NOT NULL PRIMARY KEY,
    topic        TEXT NOT NULL,
    processed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS casbin_rule (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    ptype TEXT,
    v0    TEXT,
    v1    TEXT,
    v2    TEXT,
    v3    TEXT,
    v4    TEXT,
    v5    TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_casbin_rule ON casbin_rule (ptype, v0, v1, v2);

CREATE TABLE IF NOT EXISTS authz_api_permission (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    method       TEXT NOT NULL,
    path         TEXT NOT NULL,
    pattern      TEXT,
    operation_id TEXT,
    summary      TEXT,
    description  TEXT,
    tags         TEXT,
    updated_at   DATETIME
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_authz_api ON authz_api_permission (method, path);

CREATE TABLE IF NOT EXISTS authz_role (
    name         TEXT NOT NULL PRIMARY KEY,
    display_name TEXT,
    description  TEXT,
    created_at   DATETIME,
    updated_at   DATETIME
);
