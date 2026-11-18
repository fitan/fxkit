-- 0005: opt-in tables (idempotency + audit)
-- 只有启用 idempotency.Module / audit.Module(sink=db) 的服务才需要这两张表。

CREATE TABLE IF NOT EXISTS idempotency_keys (
    key             VARCHAR(128) PRIMARY KEY,
    request_hash    VARCHAR(64)  NOT NULL,
    status_code     INTEGER      NOT NULL DEFAULT 0,
    content_type    VARCHAR(255),
    response_body   BYTEA,
    completed       BOOLEAN      NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ  NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expires_at ON idempotency_keys (expires_at);

CREATE TABLE IF NOT EXISTS audit_events (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor       VARCHAR(128),
    org_id      VARCHAR(128),
    method      VARCHAR(16) NOT NULL,
    path        VARCHAR(512) NOT NULL,
    status      INTEGER NOT NULL,
    action      VARCHAR(64),
    resource    VARCHAR(128),
    request_id  VARCHAR(64),
    trace_id    VARCHAR(64),
    ip          VARCHAR(64),
    metadata    TEXT
);
CREATE INDEX IF NOT EXISTS idx_audit_events_at ON audit_events (at);
CREATE INDEX IF NOT EXISTS idx_audit_events_actor ON audit_events (actor);
CREATE INDEX IF NOT EXISTS idx_audit_events_org ON audit_events (org_id);
