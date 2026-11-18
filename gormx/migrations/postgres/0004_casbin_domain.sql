-- 0004: Casbin domain（多租户）
-- 旧数据：p=(sub,obj,act)，g=(user,role)
-- 新数据：p=(sub,dom,obj,act)，g=(user,role,dom)
-- 迁移后旧策略变为全局 domain="*"。

DROP INDEX IF EXISTS uk_casbin_rule;

UPDATE casbin_rule SET v3 = v2 WHERE ptype = 'p' AND (v3 IS NULL OR v3 = '');
UPDATE casbin_rule SET v2 = v1 WHERE ptype = 'p';
UPDATE casbin_rule SET v1 = '*' WHERE ptype = 'p';

UPDATE casbin_rule SET v2 = '*' WHERE ptype = 'g' AND v2 = '';

CREATE UNIQUE INDEX IF NOT EXISTS uk_casbin_rule ON casbin_rule (ptype, v0, v1, v2, v3);
