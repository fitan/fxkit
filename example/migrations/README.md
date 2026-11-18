# Atlas 迁移

示例使用 Atlas 管理框架表 + 业务表的版本化迁移。

```bash
# 1) 生成框架 baseline
make migrate-baseline

# 2) 查看 / 校验 / 执行
export DATABASE_URL='postgres://fxkit:fxkit@localhost:5432/fxkit?sslmode=disable'
make atlas-status
make atlas-lint
make atlas-apply
```

- `migrations/postgres/0001_fxkit_core.sql` 由 `fxkit migrate baseline` 生成；
- 业务迁移按版本追加到同一目录；
- 应用启动只做 `migrate check`，不执行 DDL；
- 完整说明见 [`docs/migrations-atlas.md`](../../docs/migrations-atlas.md)。
