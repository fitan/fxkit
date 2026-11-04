#!/usr/bin/env bash
# 本地端到端冒烟：SQLite 单机 catalog，不依赖 Postgres/Consul/Hatchet/OTel/Logto。
#
# 覆盖：健康检查、版本、chi 裸路由与宿主中间件、鉴权矩阵、写路径与冲突、
# 游标分页（desc 第二页）、q= 过滤、非法排序、/docs live spec、MCP 默认关闭、authz 管理 API。
#
# 用法：
#   bash example/scripts/verify-local.sh
#   PORT=18096 bash example/scripts/verify-local.sh
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
EXAMPLE_DIR=$(cd "$SCRIPT_DIR/.." && pwd)

PORT=${PORT:-18095}
BASE="http://127.0.0.1:${PORT}"

# 本机/CI 可能设置 http_proxy，会把 127.0.0.1 也走代理并返回 503。
export NO_PROXY="127.0.0.1,localhost${NO_PROXY:+,$NO_PROXY}"
export no_proxy="$NO_PROXY"

WORK=$(mktemp -d)
BIN="$WORK/catalog"
CFG="$WORK/config.yaml"
DB="$WORK/verify.db"
LOG="$WORK/catalog.log"
BODY="$WORK/body.out"
PID=""
FAIL=0

cleanup() {
  if [ -n "$PID" ]; then
    kill "$PID" 2>/dev/null || true
    wait "$PID" 2>/dev/null || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

pass() { printf 'PASS  %s\n' "$1"; }
flop() { printf 'FAIL  %s  (%s)\n' "$1" "$2"; FAIL=$((FAIL + 1)); }
expect() {
  if [ "$2" = "$3" ]; then pass "$1 [$3]"; else flop "$1" "want $2 got $3"; fi
}
code() { curl -sS -o "$BODY" -w '%{http_code}' "$@"; }

cat > "$CFG" <<YAML
app:
  name: "catalog-verify"

server:
  port: "${PORT}"

db:
  driver: "sqlite"
  dsn: "file:${DB}?_fk=1&_busy_timeout=5000"
  log_level: "error"
  slow_threshold: 0s
  auto_migrate: true   # 本地冒烟用 AutoMigrate；生产由 SQL 平台执行 `migrate print` 的 DDL

outbox:
  enabled: false

hatchet:
  enabled: false

auth:
  enabled: true
  dev_header_user: true
  casbin:
    enabled: true
    table_name: "casbin_rule"
    auto_register_routes: true
    bootstrap_role: "admin"
    bootstrap_users: ["alice"]
    auto_bind_bootstrap: false
    reload_interval: 0s

otel:
  enabled: false

discovery:
  consul_address: ""
  register: false
YAML

(cd "$EXAMPLE_DIR" && go build -o "$BIN" ./services/catalog/cmd)

# 新命令：导出框架 DDL（生产交给 SQL 平台执行）。
"$BIN" migrate print --dialect sqlite > "$WORK/schema.sql"
if grep -q 'outbox_events' "$WORK/schema.sql"; then
  pass "migrate print 输出框架 DDL"
else
  flop "migrate print" "missing outbox_events"
fi

"$BIN" serve -c "$CFG" >"$LOG" 2>&1 &
PID=$!

for _ in $(seq 1 120); do
  c=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/healthz" 2>/dev/null || true)
  [ "$c" = "200" ] && break
  sleep 0.25
done

echo "== 基础探针 =="
expect "GET /healthz" 200 "$(code "$BASE/healthz")"
expect "GET /readyz" 200 "$(code "$BASE/readyz")"
expect "GET /version" 200 "$(code "$BASE/version")"
grep -q '"version"' "$BODY" && pass "version JSON" || flop "version JSON" "$(head -c 160 "$BODY")"
expect "GET /public/ping (匿名)" 200 "$(code "$BASE/public/ping")"

echo "== chi 裸路由与中间件 =="
HDR=$(curl -sS -D - -o /dev/null "$BASE/chi/echo?msg=hi")
echo "$HDR" | head -1 | grep -q ' 200 ' && pass "GET /chi/echo" || flop "GET /chi/echo" "$(echo "$HDR" | head -1)"
echo "$HDR" | grep -qi 'X-Fxkit-Chi' && pass "宿主 chi 中间件生效" || flop "宿主 chi 中间件" "missing X-Fxkit-Chi"

echo "== 鉴权矩阵 =="
expect "GET /articles 匿名 -> 401" 401 "$(code "$BASE/articles")"
expect "GET /articles bob -> 200" 200 "$(code -H 'X-User: bob' "$BASE/articles")"
expect "POST /articles bob -> 403" 403 \
  "$(code -X POST -H 'X-User: bob' -H 'Content-Type: application/json' -d '{"title":"bob-post"}' "$BASE/articles")"

echo "== 写路径 =="
CREATED=$(code -X POST -H 'X-User: alice' -H 'Content-Type: application/json' \
  -d '{"title":"verify-1","body":"a"}' "$BASE/articles")
case "$CREATED" in
  200 | 201) pass "POST /articles alice [$CREATED]" ;;
  *) flop "POST /articles alice" "got $CREATED $(head -c 160 "$BODY")" ;;
esac
expect "POST 重复标题 -> 409" 409 \
  "$(code -X POST -H 'X-User: alice' -H 'Content-Type: application/json' -d '{"title":"verify-1"}' "$BASE/articles")"

for i in 2 3 4 5 6; do
  curl -sS -o /dev/null -X POST -H 'X-User: alice' -H 'Content-Type: application/json' \
    -d "{\"title\":\"verify-$i\"}" "$BASE/articles"
done

echo "== 游标分页（desc 第二页）=="
expect "desc page1 (useCursor)" 200 \
  "$(code -H 'X-User: alice' "$BASE/articles?sortBy=id&sortDirection=desc&useCursor=true&limit=2")"
CURSOR=$(sed -n 's/.*"nextCursor":"\([^"]*\)".*/\1/p' "$BODY")
[ -n "$CURSOR" ] && pass "page1 返回 nextCursor" || flop "page1 nextCursor" "$(head -c 200 "$BODY")"

if [ -n "$CURSOR" ]; then
  expect "desc page2 仅带 cursor" 200 "$(code -H 'X-User: alice' "$BASE/articles?cursor=$CURSOR")"
  if ! grep -q '"items":\[' "$BODY"; then
    flop "page2 返回 items" "$(head -c 200 "$BODY")"
  elif grep -q '"items":\[\]' "$BODY"; then
    flop "page2 非空" "items 为空"
  else
    pass "page2 非空"
  fi
fi

echo "== 查询过滤 / 排序校验 =="
expect "q=title~verify" 200 \
  "$(code -H 'X-User: alice' --get --data-urlencode 'q=title~verify' "$BASE/articles")"
expect "q 字面量 % 不再 400" 200 \
  "$(code -H 'X-User: alice' --get --data-urlencode 'q=title~50%' "$BASE/articles")"
expect "非法 sortBy -> 400" 400 "$(code -H 'X-User: alice' "$BASE/articles?sortBy=nope")"

echo "== docs / openapi =="
expect "GET /docs" 200 "$(code "$BASE/docs")"
grep -qi 'api-reference' "$BODY" && pass "/docs Scalar" || flop "/docs Scalar" "no api-reference"
expect "GET /openapi.yaml" 200 "$(code "$BASE/openapi.yaml")"
grep -q '/articles' "$BODY" && pass "openapi live /articles" || flop "openapi live" "missing /articles"

echo "== MCP 默认关闭 / authz =="
expect "GET /mcp/sse -> 404" 404 "$(code "$BASE/mcp/sse")"
expect "GET /authz/roles alice" 200 "$(code -H 'X-User: alice' "$BASE/authz/roles")"

echo
if [ "$FAIL" -eq 0 ]; then
  echo "ALL CHECKS PASSED"
  exit 0
fi
echo "$FAIL CHECK(S) FAILED"
echo "---- catalog log (tail) ----"
tail -n 40 "$LOG" || true
exit 1
