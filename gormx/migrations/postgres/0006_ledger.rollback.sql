-- rollback 0006: 支付原语（复式记账）
DROP TABLE IF EXISTS ledger_postings;
DROP TABLE IF EXISTS ledger_transactions;
DROP TABLE IF EXISTS ledger_accounts;
