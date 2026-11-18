-- 0003 rollback: outbox 退避 + inbox 两阶段认领
DROP INDEX IF EXISTS idx_outbox_inbox_status;
ALTER TABLE outbox_inbox_events DROP COLUMN IF EXISTS lease_id;
ALTER TABLE outbox_inbox_events DROP COLUMN IF EXISTS claimed_at;
ALTER TABLE outbox_inbox_events DROP COLUMN IF EXISTS status;

DROP INDEX IF EXISTS idx_outbox_retry;
ALTER TABLE outbox_events DROP COLUMN IF EXISTS next_attempt_at;
