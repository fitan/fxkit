-- 0006: 支付原语（复式记账）
-- 只有使用 ledger.Module / ledger.New 的服务才需要这三张表。

CREATE TABLE IF NOT EXISTS ledger_accounts (
    id          VARCHAR(128) PRIMARY KEY,
    currency    VARCHAR(3)   NOT NULL,
    type        VARCHAR(32),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS ledger_transactions (
    id          VARCHAR(128) PRIMARY KEY,
    reference   VARCHAR(128),
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ledger_transactions_reference ON ledger_transactions (reference);

CREATE TABLE IF NOT EXISTS ledger_postings (
    id          BIGSERIAL PRIMARY KEY,
    txn_id      VARCHAR(128) NOT NULL,
    account_id  VARCHAR(128) NOT NULL,
    amount      BIGINT       NOT NULL,
    currency    VARCHAR(3)   NOT NULL,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_ledger_postings_txn ON ledger_postings (txn_id);
CREATE INDEX IF NOT EXISTS idx_ledger_postings_account ON ledger_postings (account_id, currency);
