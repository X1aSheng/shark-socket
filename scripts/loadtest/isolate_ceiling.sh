#!/bin/bash
# Determine what actually limits throughput once a ceiling has been found.
#
# A ladder locates the knee but not its cause. When achieved throughput plateaus
# there are three candidates and they have different fixes: bandwidth (buy more
# link), per-message cost (batch or reduce syscalls), or a per-connection limit
# (raise a concurrency cap or shard). This script separates them by varying the
# two variables that distinguish them.
#
#   - Payload size. Bandwidth-limited throughput is inversely proportional to
#     bytes per message. Multiplying payload by sixteen must therefore cost
#     roughly sixteen times throughput. A small cost means bytes are not what
#     the ceiling is made of.
#
#   - Connection count. A per-connection limit shows up as throughput rising
#     with connections until a plateau. A flat curve from the start means
#     concurrency is not the constraint.
#
# Usage:
#   SERVER_HOST=10.0.0.1 PROTOCOL=tcp bash isolate_ceiling.sh
set -uo pipefail

SERVER_HOST="${SERVER_HOST:?set SERVER_HOST, e.g. SERVER_HOST=10.0.0.1}"
REPO="${REPO:-/root/shark-socket}"
PROTOCOL="${PROTOCOL:-tcp}"
CONNS="${CONNS:-50}"
DURATION="${DURATION:-6s}"
# Offered rate deliberately above every measured ceiling. The point is what the
# server does when it cannot follow, so the generator has to ask for more than
# it can get; a rate below the ceiling just re-measures the ladder.
OVERLOAD="${OVERLOAD:-200000}"
TIMEOUT="${TIMEOUT:-120}"

case "$PROTOCOL" in
  tcp)     PORT=18000 ;;
  udp)     PORT=18002 ;;
  http)    PORT=18003 ;;
  ws)      PORT=18004 ;;
  coap)    PORT=18005 ;;
  grpcweb) PORT=18006 ;;
  *) echo "unknown PROTOCOL=$PROTOCOL" >&2; exit 1 ;;
esac

cd "$REPO" 2>/dev/null || { echo "REPO $REPO not found" >&2; exit 1; }
go build -o /root/protocolgen ./scripts/protocolgen || exit 1
echo "ISOLATE built"
echo "ISOLATE protocol=$PROTOCOL target=$SERVER_HOST:$PORT offered=$OVERLOAD conns=$CONNS"

run() {
  # label conns size -> achieved msg/s on stdout
  local label="$1" c="$2" sz="$3" out achieved
  out=$(timeout "$TIMEOUT" /root/protocolgen -protocol "$PROTOCOL" \
    -host "$SERVER_HOST:$PORT" -conns "$c" -rate "$OVERLOAD" -duration "$DURATION" \
    -size "$sz" -label "$label" 2>&1)
  achieved=$(echo "$out" | grep -oE 'achieved [0-9]+/s' | head -1 \
             | grep -oE '[0-9]+' | head -1)
  echo "$out" > "/root/isolate-$label.log"
  echo "${achieved:-0}"
}

echo "=== payload sweep (connections fixed at $CONNS) ==="
echo "verdict: if throughput barely moves as bytes grow, bytes are not the limit"
base=""
for sz in 64 256 1024; do
  got=$(run "size-$sz" "$CONNS" "$sz")
  if [ -z "$base" ]; then
    base="$got"
    pct="baseline"
  elif [ "$base" -gt 0 ] 2>/dev/null; then
    # Integer percent change: a calculator would be clearer but this runs where
    # python may not be, and a sign plus one decimal is enough to see the shape.
    pct=$(awk -v g="$got" -v b="$base" 'BEGIN{printf "%+.1f%%", (g/b-1)*100}')
  else
    pct="n/a"
  fi
  printf "  size=%5s B  achieved=%8s/s  vs 64B: %s\n" "$sz" "$got" "$pct"
done

echo "=== connection sweep (payload fixed at 256 B) ==="
echo "verdict: if throughput plateaus, concurrency is not the constraint"
for c in 10 50 200; do
  got=$(run "conns-$c" "$c" 256)
  printf "  conns=%4s  achieved=%8s/s\n" "$c" "$got"
done

echo "=== reading the result ==="
cat <<'NOTE'
bandwidth-limited:     payload 16x costs roughly 16x throughput, and adding
                      connections does not help
per-message cost:      payload 16x costs little; throughput plateaus somewhere
                      between the lowest and highest connection count
per-connection limit:  throughput rises steeply with connections, then flattens

A flat p50 across the saturating rungs is the signature of backpressure rather
than collapse: the generator is synchronous, so response latency paces it and
errs stays 0. A collapse shows errors and a p50 that jumps.
NOTE
echo
echo "logs: /root/isolate-<label>.log"