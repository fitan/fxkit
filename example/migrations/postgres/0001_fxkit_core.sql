-- 0001_init.sql
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

-- 0002_drop_authz_api_permission.sql
-- 0002: 删除 authz_api_permission
--
-- 这张表曾是「API 权限目录」，与 casbin_rule（真正决定授权）和 Huma spec
-- （保存 summary/description/tags）重复。现在目录改为运行时派生，
-- 不再写入该表，因此可以安全删除。
DROP TABLE IF EXISTS authz_api_permission;

-- 0003_outbox_retry_inbox_claim.sql
-- 0003: outbox 退避 + inbox 两阶段认领
-- outbox_events: next_attempt_at 用于指数退避，未到期的 pending 不会被认领。
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_outbox_retry ON outbox_events (status, next_attempt_at);

-- outbox_inbox_events: 认领状态。旧行默认 done，保持兼容。
ALTER TABLE outbox_inbox_events ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'done';
ALTER TABLE outbox_inbox_events ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ;
ALTER TABLE outbox_inbox_events ADD COLUMN IF NOT EXISTS lease_id VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_outbox_inbox_status ON outbox_inbox_events (status, claimed_at);

-- 0004_casbin_domain.sql
-- 0004: Casbin domain（多租户）
-- 旧数据：p=(sub,obj,act)，g=(user,role)
-- 新数据：p=(sub,dom,obj,act)，g=(user,role,dom)
-- 迁移后旧策略变为全局 domain="*"。

DROP INDEX IF EXISTS uk_casbin_rule;

UPDATE casbin_rule SET v3 = v2 WHERE ptype = 'p' AND (v3 IS NULL OR v3 = '');
UPDATE casbin_rule SET v2 = v1 WHERE ptype = 'p';
UPDATE casbin_rule SET v1 = '*' WHERE ptype = 'p';

UPDATE casbin_rule SET v2 = '*' WHERE ptype = 'g' AND v2 = '';

CREATE UNIQUE INDEX IF NOT EXISTS uk_casbin_rule ON casbin_rule (ptype, v0, v1, v2, v3);

-- 0005_idempotency_audit.sql
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

-- 0006_ledger.sql
-- 0006: 支付原语（复式记账）
-- 只有使用 ledger.Module / ledger.New 的服务才需要这三张表。

CREATE TABLE IF NOT EXISTS ledger_accounts (
    id          VARCHAR(128) PRIMARY KEY,
    currency    VARCHAR(3)   NOT NULL,
    type        VARCHAR(32),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ledger_transactions (
    id          VARCHAR(128) PRIMARY KEY,
    reference   VARCHAR(128),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ledger_transactions_reference ON ledger_transactions (reference);

CREATE TABLE IF NOT EXISTS ledger_postings (
    id          BIGSERIAL PRIMARY KEY,
    txn_id      VARCHAR(128) NOT NULL,
    account_id  VARCHAR(128) NOT NULL,
    amount      BIGINT       NOT NULL,
    currency    VARCHAR(3)   NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ledger_postings_txn ON ledger_postings (txn_id);
CREATE INDEX IF NOT EXISTS idx_ledger_postings_account ON ledger_postings (account_id, currency);
