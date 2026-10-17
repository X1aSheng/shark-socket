#!/bin/bash
# Cross-host capacity ladder for a single protocol.
#
# Walks offered load upward in fixed steps and reports each rung, so the knee is
# visible as the rung where achieved throughput stops tracking offered load.
# A single-point measurement answers "how fast at this rate" but not "where does
# it break", which is the question a capacity plan needs.
#
# Split the two sides across hosts: this script and protocolgen run on the
# generator, serve_multi.sh runs on the target. Same-host would let the
# generator steal the server's CPU.
#
# Usage:
#   SERVER_HOST=10.0.0.1 PROTOCOL=tcp RATES="80000 120000 180000" \
#     bash ladder.sh
#
# Environment:
#   SERVER_HOST  target address (required)
#   REPO         generator-side checkout   (default /root/shark-socket)
#   PROTOCOL     tcp | udp | ws | http | coap | grpcweb
#   CONNS        concurrent connections    (default 50)
#   DURATION     seconds per rung          (default 10s)
#   SIZE         payload bytes             (default 256)
#   RATES        space-separated ladder    (default 20000 60000 120000)
#   TIMEOUT      per-rung hard bound       (default 120s)
set -uo pipefail

SERVER_HOST="${SERVER_HOST:?set SERVER_HOST, e.g. SERVER_HOST=10.0.0.1}"
REPO="${REPO:-/root/shark-socket}"
PROTOCOL="${PROTOCOL:-tcp}"
CONNS="${CONNS:-50}"
DURATION="${DURATION:-10s}"
SIZE="${SIZE:-256}"
RATES="${RATES:-20000 60000 120000}"
# A healthy rung always finishes inside TIMEOUT. One that does not is itself a
# finding, and the bound turns it into a reported result instead of a hang that
# takes the whole ladder with it.
TIMEOUT="${TIMEOUT:-120}"

case "$PROTOCOL" in
  tcp)     PORT=18000; ARGS=() ;;
  udp)     PORT=18002; ARGS=() ;;
  http)    PORT=18003; ARGS=(-path /grpc) ;;
  ws)      PORT=18004; ARGS=(-path /ws) ;;
  coap)    PORT=18005; ARGS=() ;;
  grpcweb) PORT=18006; ARGS=(-path /grpc/ws) ;;
  *) echo "unknown PROTOCOL=$PROTOCOL" >&2; exit 1 ;;
esac

cd "$REPO" 2>/dev/null || { echo "REPO $REPO not found" >&2; exit 1; }
go build -o /root/protocolgen ./scripts/protocolgen || exit 1
echo "LADDER gen built"

# The generator competes with nothing if the target is idle, but a busy
# generator is the mirror image of a busy server: it becomes the bottleneck and
# the reported figure stops describing the server. An unrelated job on this host
# produced a 119k-to-65k swing in an otherwise identical measurement, so the
# check runs before the ladder rather than being diagnosed afterwards.
cores=$(nproc)
load=$(cut -d' ' -f1 /proc/loadavg)
echo "LADDER host=$(hostname) nproc=$cores load=$load"
busy=$(ps -eo pcpu,rss,comm --sort=-pcpu --no-headers 2>/dev/null \
       | awk -v c="$cores" -v self="protocolgen" '$1 >= 50*c && $3 != self {print "  " $1"% " $3}' | head -5)
if [ -n "$busy" ]; then
  echo "LADDER WARNING: competing load on the generator host:"
  echo "$busy"
  echo "LADDER figures will describe the contention, not the server"
fi

echo "LADDER protocol=$PROTOCOL target=$SERVER_HOST:$PORT conns=$CONNS size=$SIZE dur=$DURATION"
echo "LADDER host=$(hostname) nproc=$(nproc) rates=$RATES"

# Connectionless protocols are not probed: a TCP connect to a UDP port proves
# nothing, and the first rung is the real test. The other four are probed so an
# unreachable target produces one clear line instead of a ladder full of
# connection-refused rows that read like a server that cannot serve load.
if [ "$PROTOCOL" != "udp" ] && [ "$PROTOCOL" != "coap" ]; then
  if ! (exec 3<>"/dev/tcp/$SERVER_HOST/$PORT") 2>/dev/null; then
    echo "LADDER target $SERVER_HOST:$PORT UNREACHABLE"
    exit 1
  fi
  exec 3<&- 2>/dev/null || true
  echo "LADDER target reachable"
fi

echo "LADDER === begin ==="
for r in $RATES; do
  echo "--- $PROTOCOL offered=$r ---"
  out=$(timeout "$TIMEOUT" /root/protocolgen -protocol "$PROTOCOL" \
    -host "$SERVER_HOST:$PORT" -conns "$CONNS" -rate "$r" -duration "$DURATION" \
    -size "$SIZE" -label "ladder-$PROTOCOL-$r" "${ARGS[@]}" 2>&1)
  rc=$?

  # The summary lines carry the finding; the per-second series stays in the log
  # because a rung that saturates shows its shape there while the counters alone
  # only look low. Distinguishing saturation from backpressure needs both:
  # backpressure shows errs=0 with recv equal to sent and a flat per-second
  # line, a collapse shows errors and a p50 that jumps.
  echo "$out" | grep -E 'protocol=|achieved_ratio|latency_samples=|tail_ratio|GEN'
  if [ $rc -eq 124 ]; then
    echo "GEN_TIMEOUT $PROTOCOL at $r (no summary within ${TIMEOUT}s)"
  fi
  echo "$out" > "/root/ladder-$PROTOCOL-$r.log"
  sleep 2   # let TIME_WAIT drain between rungs
done
echo "LADDER === end ==="