#!/bin/bash
# Verify that the generator speaks every protocol correctly before any capacity
# number is taken from it.
#
# Coverage, not measurement. A protocol that reports zero traffic here is either
# mis-implemented in the generator or unreachable, and both need to be found
# before a ladder run whose whole purpose is comparing protocols: a broken
# client at a high rung looks exactly like a server that saturated.
#
# Usage:
#   SERVER_HOST=10.0.0.1 bash check_protocols.sh
set -uo pipefail

SERVER_HOST="${SERVER_HOST:?set SERVER_HOST, e.g. SERVER_HOST=10.0.0.1}"
REPO="${REPO:-/root/shark-socket}"
CONNS="${CONNS:-20}"
RATE="${RATE:-5000}"
DURATION="${DURATION:-4s}"
SIZE="${SIZE:-256}"

cd "$REPO" 2>/dev/null || { echo "REPO $REPO not found" >&2; exit 1; }
go build -o /root/protocolgen ./scripts/protocolgen || exit 1
echo "CHECK built"

check() {
  local proto="$1" port="$2" path="${3:-}"
  local args=()
  [ -n "$path" ] && args=(-path "$path")
  local out
  out=$(timeout 60 /root/protocolgen -protocol "$proto" -host "$SERVER_HOST:$port" \
    -conns "$CONNS" -rate "$RATE" -duration "$DURATION" -size "$SIZE" \
    -label "check-$proto" "${args[@]}" 2>&1)

  # Count traffic rather than the achieved ratio: the ratio is relative to the
  # offered rate, and a low rate is a valid pass. What matters is that the
  # payload came back byte for byte, which the generator verifies per message.
  local sent recv errs
  sent=$(echo "$out" | grep -oE 'sent=[0-9]+' | head -1 | cut -d= -f2)
  recv=$(echo "$out" | grep -oE 'recv=[0-9]+' | head -1 | cut -d= -f2)
  errs=$(echo "$out" | grep -oE 'errs=[0-9]+' | head -1 | cut -d= -f2)

  if [ -z "$sent" ]; then
    local reason
    reason=$(echo "$out" | grep -oE 'first_failure=.*' | head -1)
    printf '%-9s FAIL  %s\n' "$proto" "${reason:-no output}"
    return 1
  fi
  if [ "$errs" = "0" ] && [ "$sent" = "$recv" ] && [ "$sent" != "0" ]; then
    printf '%-9s PASS  sent=%s recv=%s errs=0\n' "$proto" "$sent" "$recv"
    return 0
  fi
  printf '%-9s FAIL  sent=%s recv=%s errs=%s\n' "$proto" "$sent" "$recv" "$errs"
  return 1
}

fail=0
check tcp     18000          || fail=$((fail + 1))
check udp     18002          || fail=$((fail + 1))
check http    18003 /grpc    || fail=$((fail + 1))
check ws      18004 /ws      || fail=$((fail + 1))
check coap    18005          || fail=$((fail + 1))
check grpcweb 18006 /grpc/ws || fail=$((fail + 1))

if [ "$fail" -gt 0 ]; then
  echo "CHECK $fail protocol(s) failed; fix the generator or the server config first"
  exit 1
fi
echo "CHECK all protocols pass"