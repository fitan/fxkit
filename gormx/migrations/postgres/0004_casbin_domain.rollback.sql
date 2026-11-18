-- 0004 rollback: Casbin domain -> 单租户
-- 注意：非 "*" 的 domain 信息会丢失。
DROP INDEX IF EXISTS uk_casbin_rule;

UPDATE casbin_rule SET v1 = v2 WHERE ptype = 'p';
UPDATE casbin_rule SET v2 = v3 WHERE ptype = 'p';
UPDATE casbin_rule SET v3 = '' WHERE ptype = 'p';
UPDATE casbin_rule SET v2 = '' WHERE ptype = 'g';

CREATE UNIQUE INDEX IF NOT EXISTS uk_casbin_rule ON casbin_rule (ptype, v0, v1, v2);
