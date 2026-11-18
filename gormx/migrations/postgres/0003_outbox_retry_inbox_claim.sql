-- 0003: outbox 退避 + inbox 两阶段认领
-- outbox_events: next_attempt_at 用于指数退避，未到期的 pending 不会被认领。
ALTER TABLE outbox_events ADD COLUMN IF NOT EXISTS next_attempt_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_outbox_retry ON outbox_events (status, next_attempt_at);

-- outbox_inbox_events: 认领状态。旧行默认 done，保持兼容。
ALTER TABLE outbox_inbox_events ADD COLUMN IF NOT EXISTS status VARCHAR(16) NOT NULL DEFAULT 'done';
ALTER TABLE outbox_inbox_events ADD COLUMN IF NOT EXISTS claimed_at TIMESTAMPTZ;
ALTER TABLE outbox_inbox_events ADD COLUMN IF NOT EXISTS lease_id VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_outbox_inbox_status ON outbox_inbox_events (status, claimed_at);
