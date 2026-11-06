-- 0002: 删除 authz_api_permission
--
-- 这张表曾是「API 权限目录」，与 casbin_rule（真正决定授权）和 Huma spec
-- （保存 summary/description/tags）重复。现在目录改为运行时派生，
-- 不再写入该表，因此可以安全删除。
DROP TABLE IF EXISTS authz_api_permission;
