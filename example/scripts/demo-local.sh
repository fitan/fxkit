#!/usr/bin/env bash
# 本地 demo：PostgreSQL catalog + storefront，供 Nuxt dashboard 使用。
# 需要 Docker（自动起 Postgres/Consul/Redis/Traefik）；Hatchet 有 token 时自动启用。
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
TMP="$ROOT/tmp"
mkdir -p "$TMP"

export NO_PROXY="127.0.0.1,localhost${NO_PROXY:+,$NO_PROXY}"
export no_proxy="$NO_PROXY"

CAT_CFG="$TMP/catalog-demo.yaml"
SF_CFG="$TMP/storefront-demo.yaml"
CAT_LOG="$TMP/catalog-demo.log"
SF_LOG="$TMP/storefront-demo.log"

write_configs() {
  cat > "$CAT_CFG" <<'YAML'
app:
  name: "catalog"

server:
  port: "8081"
  security_headers:
    enabled: true
  pprof:
    enabled: true
    prefix: "/debug/pprof/"

db:
  driver: "postgres"
  dsn: "postgres://fxkit:fxkit@127.0.0.1:5432/fxkit?sslmode=disable"
  log_level: "error"
  slow_threshold: 0s
  auto_migrate: true

cache:
  driver: ""

outbox:
  enabled: false
  purge_interval: 1h
  purge_before: 168h
  inbox_purge_before: 168h
  partition_field: "id"

hatchet:
  enabled: false

auth:
  enabled: true
  dev_header_user: true
  dev_org_id: "demo"          # Casbin domain（不是数据隔离）
  public_paths:
    - "/public"
    - "/payments/webhook"
  casbin:
    enabled: true
    table_name: "casbin_rule"
    auto_register_routes: true
    bootstrap_role: "admin"
    bootstrap_users: ["alice"]
    auto_bind_bootstrap: false
    reload_interval: 0s

otel:
  enabled: true
  service_name: "catalog"
  endpoint: "10.170.34.223:4317"
  protocol: "grpc"
  insecure: true
  traces:
    enabled: true
    exporter: "otlp"
  metrics:
    enabled: true
    exporter: "otlp"
  logs:
    enabled: true
    exporter: "otlp"

discovery:
  consul_address: ""
  consul_passing_only: true
  register: false
  advertise_address: ""
  tags:
    - "traefik.enable=true"
    - "traefik.http.routers.catalog.rule=PathPrefix(`/catalog`) || PathPrefix(`/articles`) || PathPrefix(`/comments`)"
    - "traefik.http.routers.catalog.middlewares=catalog-strip@file"

audit:
  enabled: true
  sink: "slog"

idempotency:
  enabled: true
  store: "db"

payments:
  provider: "mock"
  webhook_secret: "dev-webhook-secret"

catalog:
  reminder_period: "*/15 * * * * *"

demo:
  welcome: "hello from demo config"

mcp:
  enabled: true
  path: "/mcp"
YAML

  cat > "$SF_CFG" <<'YAML'
app:
  name: "storefront"

server:
  port: "8082"
  security_headers:
    enabled: true
  pprof:
    enabled: false

db:
  driver: "postgres"
  dsn: "postgres://fxkit:fxkit@127.0.0.1:5432/fxkit?sslmode=disable"
  log_level: "error"
  slow_threshold: 0s
  auto_migrate: true

auth:
  enabled: true
  dev_header_user: true
  dev_org_id: "demo"          # Casbin domain（不是数据隔离）
  casbin:
    enabled: true
    table_name: "casbin_rule"
    auto_register_routes: true
    bootstrap_role: "admin"
    bootstrap_users: ["alice"]
    auto_bind_bootstrap: false
    reload_interval: 0s

otel:
  enabled: true
  service_name: "storefront"
  endpoint: "10.170.34.223:4317"
  protocol: "grpc"
  insecure: true
  traces:
    enabled: true
    exporter: "otlp"
  metrics:
    enabled: true
    exporter: "otlp"
  logs:
    enabled: true
    exporter: "otlp"

discovery:
  consul_address: ""
  consul_passing_only: true
  register: false
  advertise_address: ""
  tags:
    - "traefik.enable=true"
    - "traefik.http.routers.storefront.rule=PathPrefix(`/storefront`) || PathPrefix(`/digest`)"
    - "traefik.http.routers.storefront.middlewares=storefront-strip@file"

audit:
  enabled: false
  sink: "slog"

storefront:
  catalog_name: "catalog"
  catalog_seed: "127.0.0.1:8081"

mcp:
  enabled: false
YAML
}

wait_http() {
  local url="$1"
  for _ in $(seq 1 120); do
    if curl -sf "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.25
  done
  return 1
}

write_configs

# 有 Docker 就起 Postgres + Consul + Traefik + Redis；没有 Docker 时需要本机 127.0.0.1:5432 已有 Postgres。
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then
  docker compose up -d postgres consul traefik redis >/dev/null 2>&1 || true
  for _ in $(seq 1 120); do
    if nc -z 127.0.0.1 5432 2>/dev/null && nc -z 127.0.0.1 8500 2>/dev/null && nc -z 127.0.0.1 6379 2>/dev/null && nc -z 127.0.0.1 8880 2>/dev/null; then
      break
    fi
    sleep 0.25
  done
fi

if ! nc -z 127.0.0.1 5432 2>/dev/null; then
  echo "postgres 127.0.0.1:5432 不可达；请先启动 docker compose postgres 或本机 Postgres"
  exit 1
fi
echo "postgres: 127.0.0.1:5432 (fxkit/fxkit)"

if nc -z 127.0.0.1 8500 2>/dev/null; then
  python3 - "$CAT_CFG" "$SF_CFG" <<'PY'
import sys
for path in sys.argv[1:]:
    text = open(path).read()
    text = text.replace('consul_address: ""', 'consul_address: "http://127.0.0.1:8500"', 1)
    text = text.replace('register: false', 'register: true', 1)
    open(path, "w").write(text)
PY
  echo "consul: register via 127.0.0.1:8500 (advertise auto)"
else
  echo "consul: disabled (no consul)"
fi

if nc -z 127.0.0.1 6379 2>/dev/null; then
  python3 - "$CAT_CFG" <<'PY'
import sys
path = sys.argv[1]
text = open(path).read()
old = 'cache:\n  driver: ""'
new = 'cache:\n  driver: "redis"\n  redis:\n    addr: "127.0.0.1:6379"'
if old not in text:
    raise SystemExit(f"cache block not found in {path}")
open(path, "w").write(text.replace(old, new, 1))
PY
  echo "cache: redis 127.0.0.1:6379"
else
  echo "cache: disabled (no redis)"
fi

# 有 token 就启用 Hatchet；否则保持 disabled（平台页 counter 会 503）。
if [ -n "${HATCHET_CLIENT_TOKEN:-}" ] || [ -f "$ROOT/tmp/hatchet.token" ]; then
  HATCHET_HOST_PORT="${HATCHET_HOST_PORT:-10.170.34.223:7077}"
  HATCHET_SERVER_URL="${HATCHET_SERVER_URL:-http://10.170.34.223:8088}"
  python3 - "$CAT_CFG" "$HATCHET_HOST_PORT" "$HATCHET_SERVER_URL" <<'PY'
import sys
path, host_port, server_url = sys.argv[1], sys.argv[2], sys.argv[3]
text = open(path).read()
text = text.replace("outbox:\n  enabled: false", "outbox:\n  enabled: true", 1)
old = "hatchet:\n  enabled: false"
new = f'''hatchet:
  enabled: true
  token: ""
  host_port: "{host_port}"
  server_url: "{server_url}"
  tls_strategy: "none"
  worker_name: "catalog-worker"
  outbox_publisher: true
  otel: true'''
if old not in text:
    raise SystemExit(f"hatchet block not found in {path}")
open(path, "w").write(text.replace(old, new, 1))
PY
  echo "hatchet: enabled ($HATCHET_HOST_PORT, api $HATCHET_SERVER_URL)"
else
  echo "hatchet: disabled (no token)"
fi

make build

# Consul 可用时：把 demo 段放进 KV，catalog 用 --consul 启动。
# 之后改 KV 即可触发热更新（/demo/config 无需重启）。
CAT_CONSUL_ARGS=""
if nc -z 127.0.0.1 8500 2>/dev/null; then
  curl -s --noproxy '*' -X PUT \
    --data-binary $'demo:\n  welcome: "hello from consul kv"\n' \
    http://127.0.0.1:8500/v1/kv/fxkit/demo/catalog >/dev/null || true
  CAT_CONSUL_ARGS="--consul http://127.0.0.1:8500 --consul-key fxkit/demo/catalog"
  echo "config watch: consul kv fxkit/demo/catalog"
  echo "hot reload:  curl -X PUT --data-binary $'demo:\\n  welcome: \"changed\"' http://127.0.0.1:8500/v1/kv/fxkit/demo/catalog"
fi

if wait_http http://127.0.0.1:8081/healthz && wait_http http://127.0.0.1:8082/healthz; then
  echo "catalog/storefront 已在运行"
else
  nohup "$ROOT/bin/catalog" serve -c "$CAT_CFG" $CAT_CONSUL_ARGS >"$CAT_LOG" 2>&1 &

  if ! wait_http http://127.0.0.1:8081/healthz; then
    echo "catalog 启动失败，见 $CAT_LOG"
    tail -n 40 "$CAT_LOG" || true
    exit 1
  fi

  # storefront 的 reqx 通过 Consul 发现 catalog；等 catalog 健康注册后再起 storefront。
  if nc -z 127.0.0.1 8500 2>/dev/null; then
    for _ in $(seq 1 120); do
      if curl -sf 'http://127.0.0.1:8500/v1/health/service/catalog?passing=true' | grep -q '"Service"'; then
        break
      fi
      sleep 0.25
    done
  fi

  nohup "$ROOT/bin/storefront" serve -c "$SF_CFG" >"$SF_LOG" 2>&1 &

  if ! wait_http http://127.0.0.1:8082/healthz; then
    echo "storefront 启动失败，见 $SF_LOG"
    tail -n 40 "$SF_LOG" || true
    exit 1
  fi
fi

if nc -z 127.0.0.1 8880 2>/dev/null; then
  if wait_http http://127.0.0.1:8880/catalog/public/ping && wait_http http://127.0.0.1:8880/storefront/healthz; then
    echo "traefik: catalog/storefront discovered via Consul"
  else
    echo "traefik: routes not ready yet; check docker compose logs traefik"
  fi
fi

TOTAL=$(curl -s -H 'X-User: alice' 'http://127.0.0.1:8081/articles?limit=1&replyWithCount=true' \
  | python3 -c 'import json,sys; print(json.load(sys.stdin).get("total") or 0)')

if [ "$TOTAL" = "0" ]; then
  for title in "Hello fxkit" "Nuxt UI dashboard" "Storefront digest" "Logto in Nuxt"; do
    curl -s -o /dev/null -X POST -H 'X-User: alice' -H 'Content-Type: application/json' \
      -d "{\"title\":\"$title\",\"body\":\"seed\"}" http://127.0.0.1:8081/articles || true
  done
  echo "已写入 4 篇示例文章"
fi

echo "catalog    http://127.0.0.1:8081  (log: $CAT_LOG)"
echo "storefront http://127.0.0.1:8082  (log: $SF_LOG)"
echo "postgres   127.0.0.1:5432  (fxkit/fxkit)"
echo "consul     http://127.0.0.1:8500  (UI)"
echo "traefik    http://127.0.0.1:8880  (dashboard: http://127.0.0.1:8881)"
echo "dashboard  http://127.0.0.1:8880  (make web-dev；直连 3000 用 make web-dev-direct)"
