# Atlas 配置（仅开发 / CI 使用）
#
# 我们的 DDL 由自建 SQL 上线平台执行，因此这里**不使用** atlas 的 apply/版本表，
# 只用它来做：
#   - atlas migrate hash / lint   → 检查迁移是否可执行、是否包含破坏性变更
#   - atlas schema diff           → 对比 staging 库与迁移目录，检测漂移
#
# 用法：
#   atlas migrate hash --dir file://gormx/migrations/postgres
#   atlas migrate lint --dir file://gormx/migrations/postgres --latest 1
#   atlas schema diff --from "postgres://user:pass@host:5432/db?sslmode=disable" \
#                     --to file://gormx/migrations/postgres --dev-url "docker://postgres/16/dev?search_path=public"

env "postgres" {
  src = "file://gormx/migrations/postgres"
  dev = "docker://postgres/16/dev?search_path=public"
}

env "mysql" {
  src = "file://gormx/migrations/mysql"
  dev = "docker://mysql/8/dev"
}

env "sqlite" {
  src = "file://gormx/migrations/sqlite"
  dev = "sqlite://dev?mode=memory"
}
