-- 0004 rollback: Casbin domain -> 单租户
DROP INDEX uk_casbin_rule ON casbin_rule;

UPDATE casbin_rule SET v1 = v2 WHERE ptype = 'p';
UPDATE casbin_rule SET v2 = v3 WHERE ptype = 'p';
UPDATE casbin_rule SET v3 = '' WHERE ptype = 'p';
UPDATE casbin_rule SET v2 = '' WHERE ptype = 'g';

CREATE UNIQUE INDEX uk_casbin_rule ON casbin_rule (ptype, v0, v1, v2);
