-- 0002: 删除 authz_api_permission
--
-- 该表与 casbin_rule / Huma spec 重复，目录已改为运行时派生。
DROP TABLE IF EXISTS authz_api_permission;
