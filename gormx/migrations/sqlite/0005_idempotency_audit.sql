-- 0005: opt-in tables (idempotency + audit)
-- 只有启用 idempotency.Module / audit.Module(sink=db) 的服务才需要这两张表。

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key             TEXT PRIMARY KEY,
    request_hash    TEXT NOT NULL,
    status_code     INTEGER NOT NULL DEFAULT 0,
    content_type    TEXT,
    response_body   BLOB,
    completed       INTEGER NOT NULL DEFAULT 0,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at      DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expires_at ON idempotency_keys (expires_at);

CREATE TABLE IF NOT EXISTS audit_events (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    actor       TEXT,
    org_id      TEXT,
    method      TEXT NOT NULL,
    path        TEXT NOT NULL,
    status      INTEGER NOT NULL,
    action      TEXT,
    resource    TEXT,
    request_id  TEXT,
    trace_id    TEXT,
    ip          TEXT,
    metadata    TEXT
);
CREATE INDEX IF NOT EXISTS idx_audit_events_at ON audit_events (at);
CREATE INDEX IF NOT EXISTS idx_audit_events_actor ON audit_events (actor);
CREATE INDEX IF NOT EXISTS idx_audit_events_org ON audit_events (org_id);
