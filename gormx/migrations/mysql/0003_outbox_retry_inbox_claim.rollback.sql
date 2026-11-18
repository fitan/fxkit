-- 0003 rollback: outbox 退避 + inbox 两阶段认领
DROP INDEX idx_outbox_inbox_status ON outbox_inbox_events;
ALTER TABLE outbox_inbox_events DROP COLUMN lease_id;
ALTER TABLE outbox_inbox_events DROP COLUMN claimed_at;
ALTER TABLE outbox_inbox_events DROP COLUMN status;

DROP INDEX idx_outbox_retry ON outbox_events;
ALTER TABLE outbox_events DROP COLUMN next_attempt_at;
