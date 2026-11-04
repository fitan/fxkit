-- fxkit framework tables (PostgreSQL)
--
-- 由你们的 SQL 上线平台执行；应用启动不建表（db.auto_migrate=false）。
-- 版本：0001

CREATE TABLE IF NOT EXISTS outbox_events (
    id              BIGSERIAL PRIMARY KEY,
    pubsub          VARCHAR(128) NOT NULL,
    topic           VARCHAR(255) NOT NULL,
    payload         BYTEA        NOT NULL,
    idempotency_key VARCHAR(128),
    status          VARCHAR(32)  NOT NULL,
    attempts        INTEGER      NOT NULL DEFAULT 0,
    last_error      TEXT,
    lease_id        VARCHAR(64),
    locked_at       TIMESTAMPTZ,
    created_at      TIMESTAMPTZ  NOT NULL DEFAULT now(),
    published_at    TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_outbox_events_idempotency_key ON outbox_events (idempotency_key);
CREATE INDEX IF NOT EXISTS idx_outbox_events_lease_id ON outbox_events (lease_id);
CREATE INDEX IF NOT EXISTS idx_outbox_claim ON outbox_events (status, locked_at);
CREATE INDEX IF NOT EXISTS idx_outbox_poll ON outbox_events (status, topic, created_at);

CREATE TABLE IF NOT EXISTS outbox_inbox_events (
    id           VARCHAR(128) PRIMARY KEY,
    topic        VARCHAR(255) NOT NULL,
    processed_at TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS casbin_rule (
    id    BIGSERIAL PRIMARY KEY,
    ptype VARCHAR(100),
    v0    VARCHAR(100),
    v1    VARCHAR(255),
    v2    VARCHAR(100),
    v3    VARCHAR(100),
    v4    VARCHAR(100),
    v5    VARCHAR(100)
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_casbin_rule ON casbin_rule (ptype, v0, v1, v2);

CREATE TABLE IF NOT EXISTS authz_api_permission (
    id           BIGSERIAL PRIMARY KEY,
    method       VARCHAR(16)  NOT NULL,
    path         VARCHAR(255) NOT NULL,
    pattern      VARCHAR(255),
    operation_id VARCHAR(128),
    summary      VARCHAR(255),
    description  VARCHAR(1024),
    tags         VARCHAR(255),
    updated_at   TIMESTAMPTZ
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_authz_api ON authz_api_permission (method, path);

CREATE TABLE IF NOT EXISTS authz_role (
    name         VARCHAR(64) PRIMARY KEY,
    display_name VARCHAR(128),
    description  VARCHAR(512),
    created_at   TIMESTAMPTZ,
    updated_at   TIMESTAMPTZ
);
