-- rollback for 0002 (PostgreSQL)
-- 恢复 0001 中的表结构（仅结构，数据不可恢复）。
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
