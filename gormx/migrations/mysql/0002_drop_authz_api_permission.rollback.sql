-- rollback for 0002 (MySQL)
CREATE TABLE IF NOT EXISTS authz_api_permission (
    id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    method       VARCHAR(16)  NOT NULL,
    path         VARCHAR(255) NOT NULL,
    pattern      VARCHAR(255),
    operation_id VARCHAR(128),
    summary      VARCHAR(255),
    description  VARCHAR(1024),
    tags         VARCHAR(255),
    updated_at   DATETIME(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_authz_api (method, path)
);
