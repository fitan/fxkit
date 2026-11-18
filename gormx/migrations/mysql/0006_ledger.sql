-- 0006: 支付原语（复式记账）
-- 只有使用 ledger.Module / ledger.New 的服务才需要这三张表。

CREATE TABLE IF NOT EXISTS ledger_accounts (
    id          VARCHAR(128) PRIMARY KEY,
    currency    VARCHAR(3)   NOT NULL,
    type        VARCHAR(32),
    created_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ledger_transactions (
    id          VARCHAR(128) PRIMARY KEY,
    reference   VARCHAR(128),
    created_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_ledger_transactions_reference (reference)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS ledger_postings (
    id          BIGINT UNSIGNED AUTO_INCREMENT PRIMARY KEY,
    txn_id      VARCHAR(128) NOT NULL,
    account_id  VARCHAR(128) NOT NULL,
    amount      BIGINT       NOT NULL,
    currency    VARCHAR(3)   NOT NULL,
    created_at  DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    KEY idx_ledger_postings_txn (txn_id),
    KEY idx_ledger_postings_account (account_id, currency)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
