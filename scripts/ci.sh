#!/usr/bin/env bash
#
# 与 CI 提供方无关的检查入口。
#
#   scripts/ci.sh          快检：vet + gofmt + race 测试 + 覆盖率门禁 + example
#   scripts/ci.sh --full   追加：方言 / 集成测试（需外部服务）、atlas lint
#
# 任何 CI 只需一行：
#   ./scripts/ci.sh            # GitLab: script: ["./scripts/ci.sh"]
#   ./scripts/ci.sh --full     # Jenkins: sh './scripts/ci.sh --full'
#
# 依赖说明：
#   快检     只需要 Go 工具链
#   --full   方言：FXKIT_TEST_POSTGRES_DSN / FXKIT_TEST_MYSQL_DSN
#            集成：FXKIT_TEST_REDIS_ADDR / FXKIT_TEST_CONSUL_ADDR / FXKIT_TEST_HATCHET_TOKEN
#            DDL ：atlas CLI
#   缺失的环境变量会「跳过并说明」，不会静默通过。

set -euo pipefail
cd "$(dirname "$0")/.."

FULL=0
if [ "${1:-}" = "--full" ]; then
  FULL=1
fi

# 严格装配：Option 闭包里读 group 直接 panic（CI 与本地都应保持一致）。
export FXKIT_STRICT_WIRING=1

say() { printf '\n\033[1m== %s ==\033[0m\n' "$*"; }
skip() { printf '  SKIP: %s\n' "$*"; }

say "go vet"
go vet ./...

say "gofmt"
unformatted=$(gofmt -l . | grep -v '^example/' || true)
if [ -n "$unformatted" ]; then
  echo "以下文件需要 gofmt:"
  echo "$unformatted"
  exit 1
fi
echo "  ok"

say "静态检查（尽力而为）"
# staticcheck / govulncheck 需要与 Go 版本匹配的构建；不匹配时只提示，不阻断。
# 需要硬门禁时在 CI 里单独建 job 并使用官方镜像。
if command -v staticcheck >/dev/null 2>&1; then
  if staticcheck ./... >/tmp/staticcheck.out 2>&1; then
    echo "  staticcheck ok"
  elif grep -qi "staticcheck was built with\|export data version" /tmp/staticcheck.out; then
    skip "staticcheck 与当前 Go 版本不匹配（$(grep -m1 -i 'built with' /tmp/staticcheck.out || echo 'export data version mismatch')）"
  else
    cat /tmp/staticcheck.out
    echo "  FAIL: staticcheck 报告问题"
    exit 1
  fi
else
  skip "staticcheck 未安装（go install honnef.co/go/tools/cmd/staticcheck@latest）"
fi
if command -v govulncheck >/dev/null 2>&1; then
  govulncheck ./... || { echo "  FAIL: govulncheck 报告漏洞"; exit 1; }
else
  skip "govulncheck 未安装（go install golang.org/x/vuln/cmd/govulncheck@latest）"
fi

say "go test -race"
go test -race -count=1 ./...

# 覆盖率门禁：只覆盖「核心抽象 + 关键基础设施」。
# 胶水层（cli / docs / otelx）门槛更低，防止继续下滑即可。
say "覆盖率门禁"
pct_of() {
  go test -cover -count=1 "$1" 2>/dev/null | sed -n 's/.*coverage: \([0-9.]*\)%.*/\1/p'
}
check_cover() {
  pkg="$1"; min="$2"
  pct=$(pct_of "$pkg")
  if [ -z "$pct" ]; then
    echo "  FAIL: $pkg 没有覆盖率输出"
    exit 1
  fi
  printf '  %-12s %s%% (min %s%%)\n' "$pkg" "$pct" "$min"
  awk -v a="$pct" -v b="$min" 'BEGIN{exit !(a+0 < b+0)}' && {
    echo "  FAIL: $pkg 覆盖率 ${pct}% 低于 ${min}%"
    exit 1
  }
  return 0
}
check_cover ./di 75
check_cover ./config 70
check_cover ./crudx 70
check_cover ./reqx 75
check_cover ./outbox 55
check_cover ./cachex 55
check_cover ./server 50
check_cover ./gormx 45
check_cover ./cli 40
check_cover ./docs 40
check_cover ./otelx 30
# 之前遗漏的包：不低于当前值 5 个点，防止下滑。
check_cover ./authz 45
check_cover ./hatchetx 40
check_cover ./huma 45
check_cover ./consulx 55
check_cover ./mcpx 65
check_cover ./openapi 65
check_cover ./buildinfo 50
check_cover ./fxerrors 55
check_cover ./logx 30

say "example 模块"
(
  cd example
  go build ./...
  go test -race -count=1 ./...
)

say "配置与代码一致性"
# validate --strict 会报出「配置里写了但没有模块读取」的键。
# 这是「文档/示例配置与代码漂移」最直接的检测点：某个 yaml 字段改了名或删了，
# 示例配置会立刻失败，而不是继续安静地失效。
(
  cd example
  go run ./services/catalog/cmd validate \
    --config services/catalog/configs/config.yaml --strict
)

say "生成代码可编译"
(
  set -euo pipefail
  repo="$PWD"
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT
  go build -o "$work/fxkit" ./cmd/fxkit
  cd "$work"
  cat > go.mod <<EOF
module github.com/example/scaffolded

go 1.27.0

require github.com/fitan/fxkit v0.0.0

replace github.com/fitan/fxkit => $repo
EOF
  cp "$repo/go.sum" .
  ./fxkit new hello --skip-tidy >/dev/null
  ./fxkit gen resource Article \
    --field title:string:required \
    --field body:text \
    --field author_id:int64:index \
    --out ./services/hello/internal/article >/dev/null
  # -mod=mod：生成器可能引入新依赖，让 go 自行补 go.mod（无需联网，模块已在缓存）。
  GOFLAGS=-mod=mod go build ./...
  GOFLAGS=-mod=mod go vet ./...
)

if [ "$FULL" = 1 ]; then
  say "方言测试"
  if [ -n "${FXKIT_TEST_POSTGRES_DSN:-}" ] || [ -n "${FXKIT_TEST_MYSQL_DSN:-}" ]; then
    go test -race -count=1 -run 'TestDialect' ./crudx
    go test -race -count=1 -run 'TestMigrationsPostgres|TestMigrationsMySQL' ./gormx
  else
    skip "未设置 FXKIT_TEST_POSTGRES_DSN / FXKIT_TEST_MYSQL_DSN"
  fi

  say "集成测试（Redis / Consul / Hatchet）"
  if [ -n "${FXKIT_TEST_REDIS_ADDR:-}" ]; then
    go test -race -count=1 -run 'TestRedisIntegration' ./cachex
  else
    skip "cachex：未设置 FXKIT_TEST_REDIS_ADDR"
  fi
  if [ -n "${FXKIT_TEST_CONSUL_ADDR:-}" ]; then
    go test -race -count=1 -run 'TestConsul.*Integration|TestConsulLockFailureModes' ./consulx
    go test -race -count=1 -run 'TestDiscoveryIntegration' ./reqx
  else
    skip "consulx / reqx：未设置 FXKIT_TEST_CONSUL_ADDR"
  fi
  if [ -n "${FXKIT_TEST_HATCHET_TOKEN:-}" ] || [ -n "${FXKIT_TEST_HATCHET_TOKEN_FILE:-}" ]; then
    go test -race -count=1 -run 'Integration|ParallelAcrossActorIDs|ReminderFires|OutboxToHatchet' ./hatchetx
  else
    skip "hatchetx：未设置 FXKIT_TEST_HATCHET_TOKEN(_FILE) —— 该包是唯一没有 e2e 覆盖的子系统"
  fi

  say "atlas migrate lint"
  if command -v atlas >/dev/null 2>&1; then
    for dialect in postgres mysql sqlite; do
      case "$dialect" in
        postgres) dev="docker://postgres/16/dev?search_path=public" ;;
        mysql) dev="docker://mysql/8/dev" ;;
        sqlite) dev="sqlite://dev?mode=memory" ;;
      esac
      atlas migrate hash --dir "file://gormx/migrations/$dialect"
      atlas migrate lint --dir "file://gormx/migrations/$dialect" --dev-url "$dev" --latest 1
    done
  else
    skip "atlas CLI 未安装"
  fi
fi

printf '\n\033[1m全部通过\033[0m\n'
