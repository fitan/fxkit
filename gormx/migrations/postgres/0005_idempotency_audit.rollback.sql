-- rollback 0005: opt-in tables (idempotency + audit)
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS idempotency_keys;
