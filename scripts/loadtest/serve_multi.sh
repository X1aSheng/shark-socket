#!/bin/bash
# Server side of the cross-host load tests: generate a multi-protocol config
# and run the server under test.
#
# The load generator deliberately runs on a different host. Same-host testing
# lets the generator share CPU with the server and fills its own socket
# buffers, so the result describes the generator rather than the server -- a
# 47x gap was measured between loopback and cross-host gRPC-Web throughput in
# PROTOCOL-CAPACITY-261017.
#
# Usage:
#   REPO=/root/shark-socket BIND_ADDR=0.0.0.0 bash serve_multi.sh
#
# Environment:
#   REPO       server-side checkout path        (default /root/shark-socket)
#   BIND_ADDR  address the listeners bind to    (default 0.0.0.0, needed for
#                                               cross-host access)
#   CONFIG_OUT where to write the config       (default /root/multi.json)
#   LOG        server log path                 (default /root/server-multi.log)
set -uo pipefail

REPO="${REPO:-/root/shark-socket}"
BIND_ADDR="${BIND_ADDR:-0.0.0.0}"
CONFIG_OUT="${CONFIG_OUT:-/root/multi.json}"
LOG="${LOG:-/root/server-multi.log}"

# Port assignment must match protoladder.sh. Keeping the two in separate files
# is a duplication risk; if a port moves here it must move there too, and
# check_protocols.sh is the cheapest way to notice.
#
#   tcp 18000, udp 18002, http 18003, websocket 18004, coap 18005, grpc-web 18006
# UDP and CoAP carry no listener state to probe with a TCP connect, which is
# why check_protocols.sh treats them separately.

if [ ! -d "$REPO" ]; then
  echo "REPO $REPO does not exist" >&2
  exit 1
fi

cat > "$CONFIG_OUT" <<JSON
{
  "shutdown_timeout": "10s",
  "health_addr": "$BIND_ADDR:18081",
  "metrics_addr": "$BIND_ADDR:18080",
  "protocols": [
    { "name": "tcp",       "addr": "$BIND_ADDR:18000" },
    { "name": "udp",       "addr": "$BIND_ADDR:18002" },
    { "name": "http",      "addr": "$BIND_ADDR:18003" },
    { "name": "websocket", "addr": "$BIND_ADDR:18004", "path": "/ws",
      "allowed_origins": ["*"] },
    { "name": "coap",      "addr": "$BIND_ADDR:18005" },
    { "name": "grpc-web",  "addr": "$BIND_ADDR:18006", "path": "/grpc/ws",
      "allowed_origins": ["*"] }
  ]
}
JSON
echo "CONFIG written to $CONFIG_OUT"

# allowed_origins is not optional for the two WebSocket transports, and the
# failure looks like a server fault rather than a configuration requirement.
# gRPC-Web rejects every Origin by default -- its CheckOrigin default is a
# literal "return false", commented as intentional in grpcweb/options.go --
# and plain WebSocket falls back to gorilla's same-origin comparison, which has
# nothing to compare when a client sends no Origin header at all.

echo "BUILD $REPO"
( cd "$REPO" && go build -o /root/shark-multi ./cmd/shark-socket ) || {
  echo "BUILD_FAILED" >&2
  exit 1
}

# Refuse to start while an old instance holds the ports. Silently measuring a
# stale binary, or a binary from a different commit, invalidates every number
# that follows.
STALE=$(ss -ltnp 2>/dev/null | grep -E ':(18000|18003|18004|18006)' \
        | sed -E 's/.*pid=([0-9]+).*/\1/' | sort -u | head -1)
if [ -n "$STALE" ]; then
  echo "STOPPING stale server pid $STALE"
  kill "$STALE" 2>/dev/null
  sleep 2
fi

# setsid detaches the server from this shell's process group so it survives the
# SSH session that started it. Killing it later by pattern would also match the
# SSH command line itself.
setsid /root/shark-multi -config "$CONFIG_OUT" > "$LOG" 2>&1 &
PID=$!
echo "SERVER pid $PID log $LOG"

# Wait for the listeners rather than sleeping a fixed interval: a fixed sleep
# either wastes time on a fast start or races a slow one, and the resulting
# connection refusals get attributed to the server.
for _ in $(seq 1 40); do
  if ! kill -0 "$PID" 2>/dev/null; then
    echo "SERVER_DIED"
    tail -20 "$LOG"
    exit 1
  fi
  up=0
  for p in 18000 18003 18004 18006; do
    (exec 3<>"/dev/tcp/127.0.0.1/$p") 2>/dev/null && up=$((up + 1))
  done
  if [ "$up" -ge 4 ]; then
    echo "LISTENING tcp=$up/4"
    break
  fi
  sleep 1
done

echo "PORTS"
ss -ltn 2>/dev/null | grep -E '1800[0-6]' | awk '{print "  tcp", $4}'
ss -lun 2>/dev/null | grep -E '1800[0-6]' | awk '{print "  udp", $4}'
tail -3 "$LOG"

# Report the socket state after startup. A server that has been up for hours can
# hold thousands of established connections left by earlier runs, and those
# compete for the same resources as the load about to be generated. The symptom
# is a throughput number lower than one recorded before, with nothing in the
# load-generator logs to explain it, so the state is printed here where the next
# measurement will look for it.
echo "SOCKET STATE"
ss -s 2>/dev/null | head -2
ss -tan 2>/dev/null | awk '$4 ~ /:1800[0-6]/ {print $1}' | sort | uniq -c \
  | sed 's/^/  /'
echo "  (a fresh server should show estab 0; a large count means earlier runs"
echo "   are still holding connections and the next figure will be depressed)"

# Refuse to serve load on a busy box, or warn loudly.
#
# This check exists because of a measurement that quietly went wrong. The server
# had run for 9.5 hours with 2111 established sockets on the host, and TCP
# throughput had fallen from 119k to 65k msg/s with nothing in the generator's
# own logs to explain it. The cause was an unrelated soak test on the same host
# using 126% of a two-core CPU. No error appeared anywhere: the run simply
# reported a lower number, and the natural reading -- that the server had
# degraded -- was wrong.
#
# So the state of the machine is checked before any load is generated, and a
# loaded target is reported as such rather than left to corrupt a figure.
echo "HOST LOAD CHECK"
load=$(cut -d' ' -f1-3 /proc/loadavg)
cores=$(nproc)
echo "  load average: $load (cores=$cores)"
busy=$(ps -eo pcpu,rss,comm --sort=-pcpu --no-headers 2>/dev/null \
       | awk -v c="$cores" '$1 >= 50*c {print $1"% " $3}' | head -5)
if [ -n "$busy" ]; then
  echo "  WARNING: processes using at least half a core each:"
  echo "$busy" | sed 's/^/    /'
  echo "  A target this loaded will report a throughput that describes the"
  echo "  contention, not the server. Stop or reschedule that work first."
else
  echo "  no competing load detected"
fi