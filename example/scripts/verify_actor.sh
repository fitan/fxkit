#!/usr/bin/env bash
# Actor mailbox vs global-serial (hit one catalog replica, not Traefik LB).
# Inflight is process-local — pause extra workers (:8083) while this runs.
# - same actorId: no lost updates; holdMs overlap must NOT raise actorInflight
# - different ids: rows isolated; holdMs overlap MUST raise process inflight
# Requires running catalog with Hatchet (one worker process).
set -euo pipefail

CATALOG_URL="${CATALOG_URL:-http://127.0.0.1:8081}"
N="${ACTOR_VERIFY_N:-8}"
HOLD_MS="${ACTOR_VERIFY_HOLD_MS:-400}"
AUTH=(-H 'X-User: alice' -H 'Content-Type: application/json')
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

counter_get() {
  curl -sS -m 8 "${AUTH[@]}" "$CATALOG_URL/counters/$1" | python3 -c 'import json,sys; print(int(json.load(sys.stdin).get("value",-1)))'
}

counter_inc() {
  curl -sS -f -m 60 "${AUTH[@]}" -d "{\"delta\":$2}" -X POST "$CATALOG_URL/counters/$1" >/dev/null
}

counter_inc_hold() {
  curl -sS -f -m 60 "${AUTH[@]}" -d "{\"delta\":$2,\"holdMs\":$3}" -X POST "$CATALOG_URL/counters/$1" >"$4"
}

stamp="$(date +%s)-$$"
aid1="actor-serial-$stamp"
aid2="actor-other-$stamp"

echo "1) lost-updates: $N parallel +1 on $aid1 (want value=$N)"
pids=()
for _ in $(seq 1 "$N"); do
  counter_inc "$aid1" 1 &
  pids+=("$!")
done
failn=0
for pid in "${pids[@]}"; do
  if ! wait "$pid"; then
    failn=$((failn + 1))
  fi
done
if [[ "$failn" -ne 0 ]]; then
  echo "FAIL  $failn/$N parallel POSTs failed"
  exit 1
fi
got="$(counter_get "$aid1")"
if [[ "$got" != "$N" ]]; then
  echo "FAIL  same actorId: got value=$got want $N (lost updates)"
  exit 1
fi
echo "PASS  same actorId no lost updates: value=$got"

echo "2) same actorId mailbox: two ${HOLD_MS}ms holds in parallel (actorInflight must stay 1, wall >= ~2x hold)"
t0="$(python3 -c 'import time; print(time.time())')"
counter_inc_hold "$aid1" 1 "$HOLD_MS" "$TMPDIR/same-a.json" &
p1=$!
counter_inc_hold "$aid1" 1 "$HOLD_MS" "$TMPDIR/same-b.json" &
p2=$!
wait "$p1"
wait "$p2"
elapsed="$(python3 -c "import time; print(time.time() - $t0)")"
python3 - "$TMPDIR/same-a.json" "$TMPDIR/same-b.json" "$HOLD_MS" "$elapsed" <<'PY'
import json, sys
a, b = json.load(open(sys.argv[1])), json.load(open(sys.argv[2]))
hold, elapsed = int(sys.argv[3]), float(sys.argv[4])
for name, d in ("a", a), ("b", b):
    ai = int(d.get("actorInflight") or 0)
    if ai != 1:
        print(f"FAIL  same id {name} actorInflight={ai} want 1 (MaxRuns=1 broken)")
        sys.exit(1)
    inf = int(d.get("inflight") or 0)
    if inf != 1:
        print(f"FAIL  same id {name} inflight={inf} want 1 (should not overlap on one actorId)")
        sys.exit(1)
# Two holdMs sleeps must queue: wall at least ~1.6x one hold (not ~1x).
min_serial = (hold / 1000.0) * 1.6
if elapsed < min_serial:
    print(f"FAIL  same id wall={elapsed:.3f}s < {min_serial:.3f}s (looks concurrent / two workers)")
    sys.exit(1)
print(f"PASS  same actorId queued: actorInflight=1 inflight=1 wall={elapsed:.3f}s")
PY

echo "3) different actorIds parallel: two ${HOLD_MS}ms holds (process inflight must reach 2)"
t0="$(python3 -c 'import time; print(time.time())')"
counter_inc_hold "$aid1" 1 "$HOLD_MS" "$TMPDIR/diff-a.json" &
p1=$!
counter_inc_hold "$aid2" 1 "$HOLD_MS" "$TMPDIR/diff-b.json" &
p2=$!
wait "$p1"
wait "$p2"
elapsed="$(python3 -c "import time; print(time.time() - $t0)")"
python3 - "$TMPDIR/diff-a.json" "$TMPDIR/diff-b.json" "$HOLD_MS" "$elapsed" <<'PY'
import json, sys
a, b = json.load(open(sys.argv[1])), json.load(open(sys.argv[2]))
hold, elapsed = int(sys.argv[3]), float(sys.argv[4])
for name, d in ("a", a), ("b", b):
    ai = int(d.get("actorInflight") or 0)
    if ai != 1:
        print(f"FAIL  diff id {name} actorInflight={ai} want 1")
        sys.exit(1)
mx = max(int(a.get("inflight") or 0), int(b.get("inflight") or 0))
if mx < 2:
    print(f"FAIL  diff ids max inflight={mx} want 2 (global-serial worker, not per-actorId mailbox)")
    sys.exit(1)
# Inflight=2 is the proof. Wall is only a backstop for "two full holds queued"
# (Hatchet dispatch can add hundreds of ms even when handlers overlap).
max_serial = (2 * hold / 1000.0) * 1.4
if elapsed > max_serial:
    print(f"FAIL  diff ids wall={elapsed:.3f}s > {max_serial:.3f}s despite inflight={mx}")
    sys.exit(1)
print(f"PASS  different actorIds overlapped: max inflight={mx} wall={elapsed:.3f}s")
PY

echo "4) isolation: mixed increments on $aid1 vs $aid2"
v1="$(counter_get "$aid1")"
counter_inc "$aid2" 5
v2="$(counter_get "$aid2")"
# aid1: N + 1+1 (step2) + 1 (step3) + we didn't add +3 this time
# Let's recount:
# step1: aid1 = N
# step2: aid1 += 1+1 -> N+2
# step3: aid1 += 1, aid2 += 1 -> aid1=N+3, aid2=1
# step4: aid2 += 5 -> aid2=6
want1=$((N + 3))
if [[ "$v1" != "$want1" ]]; then
  echo "FAIL  $aid1 value=$v1 want $want1"
  exit 1
fi
if [[ "$v2" != "6" ]]; then
  echo "FAIL  $aid2 value=$v2 want 6"
  exit 1
fi
echo "PASS  different actorIds isolated: $aid1=$v1 $aid2=$v2"
echo "actor mailbox ok"
