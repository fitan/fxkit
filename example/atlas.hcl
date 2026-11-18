# Atlas 配置（示例）。
#
#   export DATABASE_URL='postgres://fxkit:fxkit@localhost:5432/fxkit?sslmode=disable'
#   atlas migrate status --env local
#   atlas migrate lint   --env local --latest 1
#   atlas migrate apply  --env local
#
# 首次接入：
#   ./bin/catalog migrate baseline --dialect postgres --dir migrations/postgres

env "local" {
  url = getenv("DATABASE_URL")
  dev = "docker://postgres/16/dev"

  migration {
    dir = "file://migrations/postgres"
  }
}
