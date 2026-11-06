-- rollback for 0002 (SQLite)
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
