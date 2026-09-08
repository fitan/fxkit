#!/usr/bin/env bash
# Probe middleware, start catalog + storefront, exercise HTTP / CRUD / reqx / authz.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"

HOST="${FXKIT_HOST:-10.170.34.223}"
CONSUL_HOST="${CONSUL_HOST:-localhost}"
CATALOG_URL="${CATALOG_URL:-http://127.0.0.1:8081}"
CATALOG2_URL="${CATALOG2_URL:-http://127.0.0.1:8083}"
STOREFRONT_URL="${STOREFRONT_URL:-http://127.0.0.1:8082}"
EDGE_URL="${EDGE_URL:-http://127.0.0.1:8880}"
TRAEFIK_API="${TRAEFIK_API:-http://127.0.0.1:8881}"
CAT_PID=""
CAT2_PID=""
SF_PID=""
TMP="$ROOT/tmp"
mkdir -p "$TMP"

PASS=0
FAIL=0
SKIP=0
log() { printf '%s\n' "$*"; }
ok() { PASS=$((PASS + 1)); log "  PASS  $*"; }
fail() { FAIL=$((FAIL + 1)); log "  FAIL  $*"; }
skip() { SKIP=$((SKIP + 1)); log "  SKIP  $*"; }

probe_tcp() {
  local host="$1" port="$2"
  python3 - "$host" "$port" <<'PY'
import socket, sys
host, port = sys.argv[1], int(sys.argv[2])
try:
    s = socket.create_connection((host, port), 3)
    s.close()
    sys.exit(0)
except Exception:
    sys.exit(1)
PY
}

http_code() {
  curl -sS -m 8 -o /dev/null -w '%{http_code}' "$@" || echo "000"
}

wait_http() {
  local url="$1" n="${2:-40}"
  local i
  for i in $(seq 1 "$n"); do
    if curl -sf -m 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}

stop_example_bins() {
  pkill -f "$ROOT/bin/catalog serve" >/dev/null 2>&1 || true
  pkill -f "$ROOT/bin/storefront serve" >/dev/null 2>&1 || true
  pkill -f '/bin/catalog serve' >/dev/null 2>&1 || true
  pkill -f '/bin/storefront serve' >/dev/null 2>&1 || true
  for p in 8081 8082 8083; do
    if command -v lsof >/dev/null 2>&1; then
      pids="$(lsof -ti tcp:$p -sTCP:LISTEN 2>/dev/null || true)"
      if [[ -n "$pids" ]]; then
        # shellcheck disable=SC2086
        kill $pids >/dev/null 2>&1 || true
      fi
    fi
  done
}

stop_example_bins
	sleep 0.8

log "== probe middleware =="
PG_OK=0
OTEL_OK=0
CONSUL_OK=0
HATCHET_OK=0
LOGTO_OK=0
if probe_tcp "$HOST" 5435; then PG_OK=1; ok "postgres $HOST:5435"; else fail "postgres $HOST:5435"; fi
if probe_tcp "$HOST" 4318; then OTEL_OK=1; ok "otel $HOST:4318"; else fail "otel $HOST:4318"; fi
if probe_tcp "$CONSUL_HOST" 8500; then CONSUL_OK=1; ok "consul $CONSUL_HOST:8500"; else skip "consul $CONSUL_HOST:8500 (connection refused)"; fi
if probe_tcp "$HOST" 7077; then HATCHET_OK=1; ok "hatchet $HOST:7077"
elif probe_tcp "127.0.0.1" 7077; then HATCHET_OK=1; ok "hatchet 127.0.0.1:7077"
else skip "hatchet 7077 (connection refused)"; fi
if curl -skS -m 4 -o /dev/null -w '%{http_code}' "https://$HOST:3001/oidc/jwks" | grep -q '^200$'; then
  LOGTO_OK=1
  ok "logto JWKS https://$HOST:3001/oidc/jwks"
else
  skip "logto JWKS (unreachable; JWT validator would block startup)"
fi

if [[ "$PG_OK" != 1 ]]; then
  log "postgres is required; abort"
  exit 1
fi

if [[ -z "${HATCHET_CLIENT_TOKEN:-}" && -f "$ROOT/tmp/hatchet.token" ]]; then
  export HATCHET_CLIENT_TOKEN="$(tr -d '\r\n' < "$ROOT/tmp/hatchet.token")"
fi
TOKEN="${HATCHET_CLIENT_TOKEN:-}"
HATCHET_ENABLE=0
if [[ "$HATCHET_OK" == 1 && -n "$TOKEN" ]]; then
  HATCHET_ENABLE=1
  ok "hatchet will be enabled (token from env)"
elif [[ "$HATCHET_OK" == 1 && -z "$TOKEN" ]]; then
  skip "hatchet tcp open but HATCHET_CLIENT_TOKEN unset — leaving hatchet/outbox disabled"
else
  skip "hatchet disabled (engine down)"
fi

write_runtime_yaml() {
  local src="$1" dest="$2"
  python3 - "$src" "$dest" "$HATCHET_ENABLE" "$LOGTO_OK" <<'PY'
import sys
src, dest, hatchet, logto = sys.argv[1], sys.argv[2], sys.argv[3] == "1", sys.argv[4] == "1"
text = open(src).read()
if not hatchet:
    text = text.replace("outbox:\n  enabled: true", "outbox:\n  enabled: false", 1)
    text = text.replace("hatchet:\n  enabled: true", "hatchet:\n  enabled: false", 1)
if not logto:
    # Casbin + X-User still work; skip JWKS fetch so the process can start.
    text = text.replace('issuer: "https://10.170.34.223:3001/oidc"', 'issuer: ""')
    text = text.replace('audience: "https://fxkit.showcase/api"', 'audience: ""')
    text = text.replace('jwks_url: "https://10.170.34.223:3001/oidc/jwks"', 'jwks_url: ""')
open(dest, "w").write(text)
PY
}

CATALOG_CFG="$TMP/catalog.yaml"
STOREFRONT_CFG="$TMP/storefront.yaml"
write_runtime_yaml "$ROOT/services/catalog/configs/config.yaml" "$CATALOG_CFG"
write_runtime_yaml "$ROOT/services/storefront/configs/config.yaml" "$STOREFRONT_CFG"

if ./bin/catalog doctor --config "$CATALOG_CFG" | grep -q 'app.name=catalog'; then
  ok "catalog doctor (cli.AddCommand)"
else
  fail "catalog doctor"
fi

log "== start catalog (2 replicas for Consul/Traefik LB) =="
./bin/catalog serve --config "$CATALOG_CFG" --port 8081 >"$TMP/catalog.log" 2>&1 &
CAT_PID=$!
cleanup() {
  kill ${CAT_PID:-} ${CAT2_PID:-} ${SF_PID:-} >/dev/null 2>&1 || true
  wait ${CAT_PID:-} ${CAT2_PID:-} ${SF_PID:-} >/dev/null 2>&1 || true
}
# Ctrl-C 才停；正常跑完把服务留着，方便打开浏览器。
trap cleanup INT TERM

if ! wait_http "$CATALOG_URL/healthz" 80; then
  log "catalog :8081 failed to become healthy; last log:"
  tail -n 80 "$TMP/catalog.log" || true
  fail "catalog /healthz"
  exit 1
fi
ok "catalog :8081 /healthz"
# 等第一副本 Fx 启动完成（含 authz 写库），再起第二副本，避免 Postgres deadlock。
ready1=0
for _ in $(seq 1 40); do
  if grep -q "consul service registered" "$TMP/catalog.log" 2>/dev/null; then
    ready1=1
    break
  fi
  if ! kill -0 "$CAT_PID" 2>/dev/null; then
    break
  fi
  sleep 0.25
done
if [[ "$ready1" != 1 ]]; then
  log "catalog :8081 did not finish consul register; last log:"
  tail -n 80 "$TMP/catalog.log" || true
  fail "catalog consul register"
  exit 1
fi
ok "catalog :8081 consul register"

CATALOG2_CMD=(./bin/catalog serve --config "$CATALOG_CFG" --port 8083)
if [[ "$CONSUL_OK" == 1 ]]; then
  if curl -sf -X PUT --data-binary @"$CATALOG_CFG" "http://${CONSUL_HOST}:8500/v1/kv/fxkit-example/catalog.yaml" >/dev/null; then
    ok "consul KV put fxkit-example/catalog.yaml"
    CATALOG2_CMD=(./bin/catalog serve --consul "${CONSUL_HOST}:8500" --consul-key fxkit-example/catalog.yaml --port 8083)
  else
    fail "consul KV put fxkit-example/catalog.yaml"
  fi
fi
"${CATALOG2_CMD[@]}" >"$TMP/catalog2.log" 2>&1 &
CAT2_PID=$!
if ! wait_http "$CATALOG2_URL/healthz" 80; then
  log "catalog :8083 failed to become healthy; last log:"
  tail -n 80 "$TMP/catalog2.log" || true
  fail "catalog2 /healthz"
  exit 1
fi
ok "catalog :8083 /healthz"
ready2=0
for _ in $(seq 1 40); do
  if grep -q "consul service registered" "$TMP/catalog2.log" 2>/dev/null; then
    ready2=1
    break
  fi
  if ! kill -0 "$CAT2_PID" 2>/dev/null; then
    break
  fi
  sleep 0.25
done
if [[ "$ready2" != 1 ]]; then
  log "catalog :8083 did not finish consul register; last log:"
  tail -n 80 "$TMP/catalog2.log" || true
  fail "catalog2 consul register"
  exit 1
fi

code="$(http_code "$CATALOG_URL/readyz")"
if [[ "$code" == "200" ]]; then ok "catalog /readyz"; else fail "catalog /readyz ($code)"; fi
if curl -sS -m 8 "$CATALOG_URL/version" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert "commit" in d and "version" in d'; then
  ok "catalog /version JSON (buildinfo)"
else
  fail "catalog /version JSON"
fi
code="$(http_code "$CATALOG_URL/docs")"
if [[ "$code" == "200" ]]; then ok "catalog /docs"; else fail "catalog /docs ($code)"; fi
if curl -sS -m 15 "$CATALOG_URL/openapi.yaml" | grep -q '/articles'; then
  ok "merged /openapi.yaml contains /articles"
else
  fail "merged /openapi.yaml missing /articles"
fi
if curl -sS -m 15 "$CATALOG_URL/openapi.yaml" | grep -q 'fxkit showcase catalog'; then
  ok "merged /openapi.yaml includes base overlay"
else
  fail "merged /openapi.yaml missing base description"
fi

cors="$(curl -sS -m 8 -D - -o /dev/null -X OPTIONS \
  -H 'Origin: http://127.0.0.1:8880' -H 'Access-Control-Request-Method: GET' \
  "$CATALOG_URL/articles" || true)"
if echo "$cors" | grep -qi 'Access-Control-Allow-Origin: http://127.0.0.1:8880'; then
  ok "CORS OPTIONS allows Traefik origin"
else
  fail "CORS missing Allow-Origin"
fi

code="$(http_code "$CATALOG_URL/articles")"
if [[ "$code" == "401" ]]; then ok "GET /articles without auth → 401"; else fail "GET /articles without auth expected 401 got $code"; fi

code="$(http_code "$CATALOG_URL/public/ping")"
if [[ "$code" == "200" ]]; then ok "GET /public/ping anonymous (authz.public)"; else fail "GET /public/ping ($code)"; fi

code="$(http_code "$CATALOG_URL/chi/echo?msg=hi")"
if [[ "$code" == "200" ]]; then ok "GET /chi/echo (bare chi, no authz)"; else fail "GET /chi/echo ($code)"; fi
if curl -sS -m 8 "$CATALOG_URL/chi/tree/x" | grep -q PrefixRoute; then
  ok "GET /chi/tree/x PrefixRoute"
else
  fail "PrefixRoute /chi/tree/"
fi
nf="$(http_code "$CATALOG_URL/chi/errors/notfound")"
cf="$(http_code "$CATALOG_URL/chi/errors/conflict")"
vl="$(http_code "$CATALOG_URL/chi/errors/validation")"
bm="$(http_code "$CATALOG_URL/chi/errors/boom")"
if [[ "$nf" == "404" && "$cf" == "409" && "$vl" == "400" && "$bm" == "500" ]]; then
  ok "chi fxerrors 404/409/400/500"
else
  fail "chi fxerrors notfound=$nf conflict=$cf validation=$vl boom=$bm"
fi
if curl -sS -m 8 "$CATALOG_URL/chi/errors/boom" | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("message")=="internal error"'; then
  ok "chi Internal hides cause"
else
  fail "chi Internal leaked cause"
fi

hdrs="$(curl -sS -m 8 -D - -o /dev/null -H 'X-User: alice' "$CATALOG_URL/me" || true)"
if echo "$hdrs" | grep -qi 'X-Fxkit-Chi: 1' && echo "$hdrs" | grep -qi 'X-Fxkit-Huma: 1'; then
  ok "chi + huma middleware headers"
else
  fail "missing X-Fxkit-Chi / X-Fxkit-Huma"
fi

if [[ "$LOGTO_OK" == 1 ]]; then
  if grep -q "auth jwt validator initialized" "$TMP/catalog.log"; then
    ok "catalog registered Logto JWKS"
  else
    fail "catalog log missing jwt validator initialized"
    grep -E "auth jwt|jwks" "$TMP/catalog.log" || true
  fi
  code="$(http_code -H 'Authorization: Bearer not-a-jwt' "$CATALOG_URL/articles")"
  if [[ "$code" == "401" ]]; then ok "GET /articles invalid Bearer → 401"; else fail "invalid Bearer expected 401 got $code"; fi
  if [[ -n "${LOGTO_ACCESS_TOKEN:-}" ]]; then
    jwt_code="$(http_code -H "Authorization: Bearer $LOGTO_ACCESS_TOKEN" "$CATALOG_URL/articles")"
    case "$jwt_code" in
      200) ok "GET /articles with Logto JWT → 200" ;;
      403) ok "Logto JWT accepted (Casbin 403: bind token sub to admin)" ;;
      *) fail "GET /articles with Logto JWT expected 200/403 got $jwt_code" ;;
    esac
  else
    skip "real Logto JWT (set LOGTO_ACCESS_TOKEN to exercise Bearer path)"
  fi
fi

TITLE="fxkit-$(date +%s)-$$"
create_body="{\"title\":\"$TITLE\",\"body\":\"hello from example\"}"
create_json="$(curl -sS -m 15 -H 'X-User: alice' -H 'Content-Type: application/json' \
  -d "$create_body" "$CATALOG_URL/articles" || true)"
id="$(python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("id",""))' <<<"$create_json" 2>/dev/null || true)"
if [[ -n "$id" ]]; then
  ok "POST /articles id=$id"
else
  fail "POST /articles body=$create_json"
  tail -n 40 "$TMP/catalog.log" || true
fi

dup="$(curl -sS -m 8 -o /dev/null -w '%{http_code}' -H 'X-User: alice' -H 'Content-Type: application/json' \
  -d "$create_body" "$CATALOG_URL/articles" || echo 000)"
if [[ "$dup" == "409" ]]; then ok "POST duplicate title → 409 Conflict"; else fail "duplicate title expected 409 got $dup"; fi

code="$(http_code -H 'X-User: alice' "$CATALOG_URL/articles/$id")"
if [[ -n "$id" && "$code" == "200" ]]; then ok "GET /articles/$id"; else fail "GET /articles/$id ($code)"; fi

me="$(curl -sS -m 8 -H 'X-User: alice' "$CATALOG_URL/me" || true)"
if python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("id")=="alice"' <<<"$me"; then
  ok "GET /me alice"
else
  fail "GET /me body=$me"
fi

bob_get="000"
for _ in $(seq 1 20); do
  bob_get="$(http_code -H 'X-User: bob' "$CATALOG_URL/articles")"
  if [[ "$bob_get" == "200" ]]; then
    break
  fi
  sleep 0.25
done
if [[ "$bob_get" == "200" ]]; then ok "bob GET /articles (reader)"; else fail "bob GET /articles ($bob_get)"; fi
bob_post="$(curl -sS -m 8 -o /dev/null -w '%{http_code}' -H 'X-User: bob' -H 'Content-Type: application/json' \
  -d '{"title":"bob-denied"}' "$CATALOG_URL/articles" || echo 000)"
if [[ "$bob_post" == "403" ]]; then ok "bob POST /articles → 403"; else fail "bob POST expected 403 got $bob_post"; fi
charlie="$(http_code -H 'X-User: charlie' "$CATALOG_URL/articles")"
if [[ "$charlie" == "403" ]]; then ok "charlie GET /articles → 403 (no role)"; else fail "charlie expected 403 got $charlie"; fi

alice_roles="$(http_code -H 'X-User: alice' "$CATALOG_URL/authz/roles")"
if [[ "$alice_roles" == "200" ]]; then ok "alice GET /authz/roles"; else fail "alice /authz/roles ($alice_roles)"; fi
bob_roles="$(http_code -H 'X-User: bob' "$CATALOG_URL/authz/roles")"
if [[ "$bob_roles" == "403" ]]; then ok "bob GET /authz/roles → 403"; else fail "bob /authz/roles expected 403 got $bob_roles"; fi

list_json="$(curl -sS -m 15 -H 'X-User: alice' \
  "$CATALOG_URL/articles?q=title~$TITLE&replyWithCount=true" || true)"
if python3 -c 'import json,sys; d=json.load(sys.stdin); assert "items" in d' <<<"$list_json"; then
  ok "GET /articles?q=title~$TITLE"
else
  fail "list q= body=$list_json"
fi

if [[ -n "$id" ]]; then
  c1="$(curl -sS -m 15 -H 'X-User: alice' -H 'Content-Type: application/json' \
    -d "{\"articleId\":$id,\"body\":\"hello comment\"}" "$CATALOG_URL/comments" || true)"
  c2="$(curl -sS -m 15 -H 'X-User: alice' -H 'Content-Type: application/json' \
    -d "{\"articleId\":$id,\"body\":\"world comment\",\"note\":\"pinned\"}" "$CATALOG_URL/comments" || true)"
  c3="$(curl -sS -m 15 -H 'X-User: alice' -H 'Content-Type: application/json' \
    -d "{\"articleId\":$id,\"body\":\"third\"}" "$CATALOG_URL/comments" || true)"
  cid="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))' <<<"$c1" 2>/dev/null || true)"
  if [[ -n "$cid" ]]; then ok "POST /comments id=$cid"; else fail "POST /comments $c1"; fi

  or_json="$(curl -sS -m 15 -H 'X-User: alice' \
    --get "$CATALOG_URL/comments" --data-urlencode 'q=or(body~hello,body~world)' --data-urlencode 'replyWithCount=true' || true)"
  if python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("total",0)>=2' <<<"$or_json"; then
    ok "GET /comments?q=or(...)"
  else
    fail "comments or() body=$or_json"
  fi

  null_json="$(curl -sS -m 15 -H 'X-User: alice' \
    --get "$CATALOG_URL/comments" --data-urlencode 'q=note.isnull' --data-urlencode "q=article_id=$id" --data-urlencode 'replyWithCount=true' || true)"
  if python3 -c 'import json,sys; d=json.load(sys.stdin); assert "items" in d' <<<"$null_json"; then
    ok "GET /comments?q=note.isnull"
  else
    fail "comments isnull body=$null_json"
  fi

  join_json="$(curl -sS -m 15 -H 'X-User: alice' \
    --get "$CATALOG_URL/comments" --data-urlencode "q=article.title~$TITLE" --data-urlencode 'replyWithCount=true' || true)"
  if python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("total",0)>=1' <<<"$join_json"; then
    ok "GET /comments?q=article.title (JOIN)"
  else
    fail "comments JOIN body=$join_json"
  fi

  page1="$(curl -sS -m 15 -H 'X-User: alice' \
    "$CATALOG_URL/comments?useCursor=true&limit=1&sortBy=id&sortDirection=asc" || true)"
  cursor="$(python3 -c 'import json,sys; print(json.load(sys.stdin).get("nextCursor") or "")' <<<"$page1" 2>/dev/null || true)"
  if [[ -n "$cursor" ]]; then
    page2="$(curl -sS -m 15 -H 'X-User: alice' --get "$CATALOG_URL/comments" --data-urlencode "cursor=$cursor" --data-urlencode 'limit=1' || true)"
    if python3 -c 'import json,sys; d=json.load(sys.stdin); assert "items" in d' <<<"$page2"; then
      ok "comments cursor pagination"
    else
      fail "comments cursor page2=$page2"
    fi
  else
    fail "comments cursor missing nextCursor body=$page1"
  fi

  badq="$(http_code -H 'X-User: alice' "$CATALOG_URL/comments?q=nonesuch=1")"
  if [[ "$badq" == "400" ]]; then ok "GET /comments invalid q= → 400"; else fail "invalid q expected 400 got $badq"; fi

  if [[ -n "$cid" ]]; then
    del="$(http_code -X DELETE -H 'X-User: alice' "$CATALOG_URL/comments/$cid")"
    if [[ "$del" == "204" || "$del" == "200" ]]; then ok "DELETE /comments/$cid (soft)"; else fail "DELETE comment ($del)"; fi
  fi
fi

if curl -sS -m 15 "$CATALOG_URL/huma/openapi.yaml" | grep -q '/articles'; then
  ok "live OpenAPI contains /articles"
else
  fail "live OpenAPI missing /articles"
fi

if grep -q "consul_config=true" "$TMP/catalog2.log" 2>/dev/null; then
  ok "catalog :8083 loaded config from Consul KV"
elif [[ "$CONSUL_OK" != 1 ]]; then
  skip "catalog2 Consul KV (agent down)"
else
  fail "catalog2 log missing consul_config=true"
fi

if [[ "$CONSUL_OK" == 1 ]]; then
  n=0
  for _ in $(seq 1 20); do
    n="$(python3 - "$CONSUL_HOST" <<'PY'
import json, urllib.request, sys
host = sys.argv[1]
url = f"http://{host}:8500/v1/health/service/catalog?passing=true"
try:
    data = json.load(urllib.request.urlopen(url, timeout=8))
    print(len(data))
except Exception:
    print(0)
PY
)"
    if [[ "$n" -ge 2 ]]; then
      break
    fi
    sleep 0.5
  done
  if [[ "$n" -ge 2 ]]; then ok "consul catalog replicas=$n"; else fail "consul catalog replicas=$n want >=2"; fi
else
  skip "consul register (agent down)"
fi

log "== start storefront =="
./bin/storefront serve --config "$STOREFRONT_CFG" >"$TMP/storefront.log" 2>&1 &
SF_PID=$!

if ! wait_http "$STOREFRONT_URL/healthz" 80; then
  log "storefront failed to become healthy; last log:"
  tail -n 80 "$TMP/storefront.log" || true
  fail "storefront /healthz"
else
  ok "storefront /healthz"
  code="$(http_code -H 'X-User: alice' "$STOREFRONT_URL/digest")"
  if [[ "$code" == "200" ]]; then
    via="$(curl -sS -m 15 -H 'X-User: alice' "$STOREFRONT_URL/digest" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("via",""))' 2>/dev/null || true)"
    if [[ "$via" == "reqx.NewFromFactory" ]]; then
      ok "GET /digest via reqx.NewFromFactory"
    else
      fail "GET /digest via=$via"
    fi
  else
    fail "GET /digest ($code)"
    curl -sS -m 8 -H 'X-User: alice' "$STOREFRONT_URL/digest" || true
    tail -n 40 "$TMP/storefront.log" || true
  fi
fi

if [[ "$HATCHET_ENABLE" == 1 && -n "$id" ]]; then
  log "== wait outbox/hatchet status=indexed =="
  indexed=0
  for _ in $(seq 1 20); do
    st="$(curl -sS -m 8 -H 'X-User: alice' "$CATALOG_URL/articles/$id" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))' 2>/dev/null || true)"
    if [[ "$st" == "indexed" ]]; then
      indexed=1
      break
    fi
    sleep 0.5
  done
  if [[ "$indexed" == 1 ]]; then ok "hatchet worker set status=indexed"; else fail "article status still not indexed"; fi

  fo="$(curl -sS -m 8 -H 'X-User: alice' "$CATALOG_URL/article-fanout" || true)"
  if python3 -c 'import json,sys; d=json.load(sys.stdin); assert isinstance(d.get("items"), list)' <<<"$fo"; then
    ok "GET /article-fanout (event fan-out)"
  else
    fail "GET /article-fanout body=$fo"
  fi

  inc="$(curl -sS -m 30 -H 'X-User: alice' -H 'Content-Type: application/json' -d '{"delta":2}' \
    "$CATALOG_URL/counters/$id" || true)"
  if python3 -c 'import json,sys; d=json.load(sys.stdin); assert d.get("value",0)>=2' <<<"$inc"; then
    ok "POST /counters/{id} hatchetx actor"
  else
    fail "actor inc body=$inc"
  fi
  # Inflight counters are per-process; pause replica :8083 so both holds land on one worker.
  extra_down=0
  if [[ -n "${CAT2_PID:-}" ]] && kill -0 "$CAT2_PID" 2>/dev/null; then
    kill "$CAT2_PID" >/dev/null 2>&1 || true
    wait "$CAT2_PID" 2>/dev/null || true
    extra_down=1
    CAT2_PID=""
  fi
  if bash "$ROOT/scripts/verify_actor.sh"; then
    ok "actor mailbox: lost-updates + same-id queue + different-id overlap"
  else
    fail "actor mailbox (serial vs overlap inflight/holdMs)"
  fi
  if [[ "$extra_down" == 1 ]]; then
    "${CATALOG2_CMD[@]}" >"$TMP/catalog2.log" 2>&1 &
    CAT2_PID=$!
    if ! wait_http "$CATALOG2_URL/healthz" 80; then
      fail "catalog :8083 restart after actor test"
    fi
  fi
  hb="$(http_code -H 'X-User: alice' "$CATALOG_URL/heartbeat")"
  if [[ "$hb" == "200" ]]; then ok "GET /heartbeat"; else fail "GET /heartbeat ($hb)"; fi
else
  skip "outbox/hatchet worker (engine or token missing)"
  code="$(http_code -H 'X-User: alice' -H 'Content-Type: application/json' -d '{"delta":1}' -X POST "$CATALOG_URL/counters/demo")"
  if [[ "$code" == "503" ]]; then ok "POST /counters without hatchet → 503"; else fail "counters without hatchet expected 503 got $code"; fi
fi

docker_sock() {
  local host
  host="$(docker context inspect --format '{{.Endpoints.docker.Host}}' 2>/dev/null || true)"
  host="${host#unix://}"
  if [[ -n "$host" && -S "$host" ]]; then
    printf '%s' "$host"
    return 0
  fi
  for p in /var/run/docker.sock "$HOME/.orbstack/run/docker.sock" "$HOME/.docker/run/docker.sock"; do
    if [[ -S "$p" ]]; then
      printf '%s' "$p"
      return 0
    fi
  done
  return 1
}

ensure_docker() {
  if docker info >/dev/null 2>&1; then
    return 0
  fi
  if [[ -d /Applications/OrbStack.app ]]; then
    log "starting OrbStack…"
    open -a OrbStack >/dev/null 2>&1 || true
    local i
    for i in $(seq 1 45); do
      if docker info >/dev/null 2>&1; then
        return 0
      fi
      sleep 1
    done
  fi
  return 1
}

log "== Traefik + frontend (docker compose) =="
if [[ "$CONSUL_OK" != 1 ]]; then
  skip "edge (need Consul)"
elif ! command -v docker >/dev/null 2>&1 || ! docker compose version >/dev/null 2>&1; then
  skip "docker compose not available"
elif ! ensure_docker; then
  skip "docker daemon not running (start OrbStack/Docker Desktop, then make edge && make verify)"
else
  if sock="$(docker_sock)"; then
    export DOCKER_SOCK="$sock"
  fi
  if docker compose up -d --remove-orphans; then
    ok "docker compose up traefik"
  else
    fail "docker compose up"
    log ""
    log "result: PASS=$PASS FAIL=$FAIL SKIP=$SKIP"
    log "logs: $TMP/catalog.log $TMP/storefront.log"
    exit 1
  fi
  if wait_http "$EDGE_URL/" 40; then
    ok "frontend via Traefik $EDGE_URL/"
  else
    fail "frontend $EDGE_URL/"
    docker compose logs --tail 40 traefik || true
  fi
  if curl -sS -m 8 "$EDGE_URL/" | grep -q "fxkit showcase"; then
    ok "frontend HTML"
  else
    fail "frontend HTML missing title"
  fi
  css="$(http_code "$EDGE_URL/styles.css")"
  js="$(http_code "$EDGE_URL/app.js")"
  sdk="$(http_code "$EDGE_URL/logto.js")"
  if [[ "$css" == "200" && "$js" == "200" && "$sdk" == "200" ]]; then
    ok "frontend CSS/JS/Logto SDK via Traefik"
  else
    fail "frontend assets CSS=$css JS=$js SDK=$sdk"
  fi
  cb_html="$(curl -sS -m 8 "$EDGE_URL/callback" || true)"
  if echo "$cb_html" | grep -q "fxkit showcase" && echo "$cb_html" | grep -q "/app.js"; then
    ok "Logto /callback serves SPA"
  else
    fail "Logto /callback missing SPA HTML (restart Traefik after plugin change)"
  fi

  edge_ok=0
  code="000"
  for _ in $(seq 1 24); do
    code="$(http_code -H 'X-User: alice' "$EDGE_URL/catalog/articles")"
    if [[ "$code" == "200" ]]; then
      edge_ok=1
      break
    fi
    sleep 1
  done
  if [[ "$edge_ok" == 1 ]]; then ok "Traefik → catalog GET /catalog/articles"; else fail "Traefik /catalog/articles (last HTTP $code)"; fi

  ping_ok=0
  code="000"
  for _ in $(seq 1 24); do
    code="$(http_code "$EDGE_URL/catalog/public/ping")"
    if [[ "$code" == "200" ]]; then
      ping_ok=1
      break
    fi
    sleep 1
  done
  if [[ "$ping_ok" == 1 ]]; then ok "Traefik → catalog GET /catalog/public/ping"; else fail "Traefik /catalog/public/ping ($code)"; fi

  digest_ok=0
  code="000"
  for _ in $(seq 1 24); do
    code="$(http_code -H 'X-User: alice' "$EDGE_URL/storefront/digest")"
    if [[ "$code" == "200" ]]; then
      digest_ok=1
      break
    fi
    sleep 1
  done
  if [[ "$digest_ok" == 1 ]]; then ok "Traefik → storefront GET /storefront/digest"; else fail "Traefik /storefront/digest ($code)"; fi

  lb=0
  for _ in $(seq 1 20); do
    lb="$(python3 - "$TRAEFIK_API" <<'PY'
import json, sys, urllib.request
base = sys.argv[1].rstrip("/")
try:
    data = json.load(urllib.request.urlopen(base + "/api/http/services", timeout=8))
except Exception:
    print("0")
    sys.exit(0)
n = 0
for svc in data:
    name = (svc.get("name") or "").lower()
    if "catalog@" in name or name == "catalog":
        servers = (svc.get("loadBalancer") or {}).get("servers") or []
        n = max(n, len(servers))
print(n)
PY
)"
    if [[ "$lb" -ge 2 ]]; then
      break
    fi
    sleep 1
  done
  if [[ "$lb" -ge 2 ]]; then
    ok "Traefik catalog servers=$lb (Consul LB)"
  else
    fail "Traefik catalog servers=$lb want >=2"
    curl -sS -m 8 "$TRAEFIK_API/api/http/services" | python3 -c "import json,sys; print(json.dumps(json.load(sys.stdin), indent=2)[:2000])" 2>/dev/null || true
  fi
fi

log ""
log "result: PASS=$PASS FAIL=$FAIL SKIP=$SKIP"
log "logs: $TMP/catalog.log $TMP/storefront.log"
log "open  $EDGE_URL   (Logto 登录 或 X-User: alice)"
log "dash  $TRAEFIK_API/dashboard/"
log "stop  make down"
if [[ "$FAIL" -gt 0 ]]; then
  exit 1
fi
