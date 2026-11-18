-- 0005: opt-in tables (idempotency + audit)
-- 只有启用 idempotency.Module / audit.Module(sink=db) 的服务才需要这两张表。

CREATE TABLE IF NOT EXISTS idempotency_keys (
    `key`           VARCHAR(128) PRIMARY KEY,
    request_hash    VARCHAR(64)  NOT NULL,
    status_code     INT          NOT NULL DEFAULT 0,
    content_type    VARCHAR(255),
    response_body   LONGBLOB,
    completed       TINYINT(1)   NOT NULL DEFAULT 0,
    created_at      DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    expires_at      DATETIME(6)  NOT NULL,
    KEY idx_idempotency_keys_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS audit_events (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    at          DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    actor       VARCHAR(128),
    org_id      VARCHAR(128),
    method      VARCHAR(16) NOT NULL,
    path        VARCHAR(512) NOT NULL,
    status      INT NOT NULL,
    action      VARCHAR(64),
    resource    VARCHAR(128),
    request_id  VARCHAR(64),
    trace_id    VARCHAR(64),
    ip          VARCHAR(64),
    metadata    TEXT,
    KEY idx_audit_events_at (at),
    KEY idx_audit_events_actor (actor),
    KEY idx_audit_events_org (org_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
