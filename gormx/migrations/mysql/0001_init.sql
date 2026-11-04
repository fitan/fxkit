-- fxkit framework tables (MySQL 8)
--
-- 由你们的 SQL 上线平台执行；应用启动不建表（db.auto_migrate=false）。
-- 版本：0001

CREATE TABLE IF NOT EXISTS outbox_events (
    id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    pubsub          VARCHAR(128)    NOT NULL,
    topic           VARCHAR(255)    NOT NULL,
    payload         LONGBLOB        NOT NULL,
    idempotency_key VARCHAR(128)    NULL,
    status          VARCHAR(32)     NOT NULL,
    attempts        INT             NOT NULL DEFAULT 0,
    last_error      TEXT            NULL,
    lease_id        VARCHAR(64)     NULL,
    locked_at       DATETIME(3)     NULL,
    created_at      DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    published_at    DATETIME(3)     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_outbox_events_idempotency_key (idempotency_key),
    KEY idx_outbox_events_lease_id (lease_id),
    KEY idx_outbox_claim (status, locked_at),
    KEY idx_outbox_poll (status, topic, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS outbox_inbox_events (
    id           VARCHAR(128) NOT NULL,
    topic        VARCHAR(255) NOT NULL,
    processed_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS casbin_rule (
    id    BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    ptype VARCHAR(100)    NULL,
    v0    VARCHAR(100)    NULL,
    v1    VARCHAR(255)    NULL,
    v2    VARCHAR(100)    NULL,
    v3    VARCHAR(100)    NULL,
    v4    VARCHAR(100)    NULL,
    v5    VARCHAR(100)    NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_casbin_rule (ptype, v0, v1, v2)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS authz_api_permission (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    method       VARCHAR(16)     NOT NULL,
    path         VARCHAR(255)    NOT NULL,
    pattern      VARCHAR(255)    NULL,
    operation_id VARCHAR(128)    NULL,
    summary      VARCHAR(255)    NULL,
    description  VARCHAR(1024)   NULL,
    tags         VARCHAR(255)    NULL,
    updated_at   DATETIME(3)     NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_authz_api (method, path)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS authz_role (
    name         VARCHAR(64)  NOT NULL,
    display_name VARCHAR(128) NULL,
    description  VARCHAR(512) NULL,
    created_at   DATETIME(3)  NULL,
    updated_at   DATETIME(3)  NULL,
    PRIMARY KEY (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
