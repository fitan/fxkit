-- 0006: 支付原语（复式记账）
-- 只有使用 ledger.Module / ledger.New 的服务才需要这三张表。

CREATE TABLE IF NOT EXISTS ledger_accounts (
    id          TEXT PRIMARY KEY,
    currency    TEXT NOT NULL,
    type        TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS ledger_transactions (
    id          TEXT PRIMARY KEY,
    reference   TEXT,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_ledger_transactions_reference ON ledger_transactions (reference);

CREATE TABLE IF NOT EXISTS ledger_postings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    txn_id      TEXT    NOT NULL,
    account_id  TEXT    NOT NULL,
    amount      INTEGER NOT NULL,
    currency    TEXT    NOT NULL,
    created_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_ledger_postings_txn ON ledger_postings (txn_id);
CREATE INDEX IF NOT EXISTS idx_ledger_postings_account ON ledger_postings (account_id, currency);
