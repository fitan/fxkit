-- rollback for 0001_init (SQLite)
DROP TABLE IF EXISTS authz_role;
DROP TABLE IF EXISTS authz_api_permission;
DROP TABLE IF EXISTS casbin_rule;
DROP TABLE IF EXISTS outbox_inbox_events;
DROP TABLE IF EXISTS outbox_events;
