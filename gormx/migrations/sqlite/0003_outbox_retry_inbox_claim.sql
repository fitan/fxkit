-- 0003: outbox 退避 + inbox 两阶段认领
ALTER TABLE outbox_events ADD COLUMN next_attempt_at DATETIME;
CREATE INDEX IF NOT EXISTS idx_outbox_retry ON outbox_events (status, next_attempt_at);

ALTER TABLE outbox_inbox_events ADD COLUMN status VARCHAR(16) NOT NULL DEFAULT 'done';
ALTER TABLE outbox_inbox_events ADD COLUMN claimed_at DATETIME;
ALTER TABLE outbox_inbox_events ADD COLUMN lease_id VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_outbox_inbox_status ON outbox_inbox_events (status, claimed_at);
