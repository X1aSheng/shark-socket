#!/bin/bash
# Walk the same rate ladder across every protocol, to answer which protocol
# gives up first at the same offered load.
#
# A per-protocol sweep is not enough: each protocol's ceiling is a property of
# its own overhead, so the interesting number is the ordering, not any single
# figure. One rung per protocol also catches a protocol whose client is simply
# broken, which a longer ladder would report as "saturated" rather than as a
# generator defect.
#
# Usage:
#   SERVER_HOST=10.0.0.1 bash protoladder.sh
#
# Environment: see ladder.sh, plus
#   PROTOCOLS  subset to run (default: all six)
set -uo pipefail

SERVER_HOST="${SERVER_HOST:?set SERVER_HOST, e.g. SERVER_HOST=10.0.0.1}"
REPO="${REPO:-/root/shark-socket}"
CONNS="${CONNS:-50}"
DURATION="${DURATION:-10s}"
SIZE="${SIZE:-256}"
RATES="${RATES:-20000 60000 120000}"
PROTOCOLS="${PROTOCOLS:-tcp udp ws http coap grpcweb}"
TIMEOUT="${TIMEOUT:-120}"

cd "$REPO" 2>/dev/null || { echo "REPO $REPO not found" >&2; exit 1; }
go build -o /root/protocolgen ./scripts/protocolgen || exit 1
echo "PROTOLADDER gen built"

# Competing load on the generator invalidates the comparison, and more so here
# than in a single-protocol ladder: the whole point is ordering the protocols,
# and contention does not hit them equally. Checked before any load rather than
# diagnosed afterwards -- an unrelated soak test on the target produced a
# 119k-to-65k swing in an otherwise identical measurement.
cores=$(nproc)
echo "PROTOLADDER host=$(hostname) nproc=$cores load=$(cut -d' ' -f1 /proc/loadavg) target=$SERVER_HOST"
busy=$(ps -eo pcpu,rss,comm --sort=-pcpu --no-headers 2>/dev/null \
       | awk -v c="$cores" '$1 >= 50*c {print "  " $1"% " $3}' | head -5)
if [ -n "$busy" ]; then
  echo "PROTOLADDER WARNING: competing load on the generator host:"
  echo "$busy"
  echo "PROTOLADDER protocol ordering will not be trustworthy until that stops"
fi
echo "PROTOLADDER conns=$CONNS size=$SIZE dur=$DURATION"
echo "PROTOLADDER protocols=$PROTOCOLS rates=$RATES"

# One row per protocol so the ordering is readable at a glance. achieved_ratio
# is the column that matters: a protocol below 0.9 here is not a capacity
# finding, it is a generator or configuration problem.
printf '%-9s %10s %9s %9s %9s %8s\n' PROTOCOL OFFERED RATIO ACHIEVED P50 TAIL
printf '%s\n' "-------------------------------------------------------------"

for proto in $PROTOCOLS; do
  case "$proto" in
    tcp)     port=18000; args=() ;;
    udp)     port=18002; args=() ;;
    http)    port=18003; args=(-path /grpc) ;;
    ws)      port=18004; args=(-path /ws) ;;
    coap)    port=18005; args=() ;;
    grpcweb) port=18006; args=(-path /grpc/ws) ;;
    *) echo "skip unknown protocol $proto"; continue ;;
  esac

  # Fail fast per protocol rather than producing a full column of
  # connection-refused rows, which read as a server that cannot serve load.
  if [ "$proto" != "udp" ] && [ "$proto" != "coap" ]; then
    if ! (exec 3<>"/dev/tcp/$SERVER_HOST/$port") 2>/dev/null; then
      printf '%-9s %10s %9s\n' "$proto" "-" "UNREACH"
      continue
    fi
    exec 3<&- 2>/dev/null || true
  fi

  for r in $RATES; do
    out=$(timeout "$TIMEOUT" /root/protocolgen -protocol "$proto" \
      -host "$SERVER_HOST:$port" -conns "$CONNS" -rate "$r" \
      -duration "$DURATION" -size "$SIZE" -label "pl-$proto-$r" \
      "${args[@]}" 2>&1)

    ratio=$(echo "$out" | grep -oE 'achieved_ratio=[0-9.]+' | head -1 | cut -d= -f2)
    achieved=$(echo "$out" | grep -oE 'achieved [0-9]+/s' | head -1 | grep -oE '[0-9]+/s' | tr -d '/s')
    p50=$(echo "$out" | grep -oE 'p50=[0-9.]+[a-zµ]*s?' | head -1 | cut -d= -f2)
    tail=$(echo "$out" | grep -oE 'tail_ratio_p99_p50=[0-9.]+' | head -1 | cut -d= -f2)

    if [ -z "$ratio" ]; then
      # No summary line means the generator produced nothing: either it blocked
      # past TIMEOUT or the very first exchange failed. Both need surfacing.
      reason=$(echo "$out" | grep -oE 'first_failure=.*' | head -1)
      printf '%-9s %10s %9s %9s %9s %8s  %s\n' \
        "$proto" "$r" "FAIL" "-" "-" "-" "${reason:-no summary within ${TIMEOUT}s}"
    else
      printf '%-9s %10s %9s %9s %9s %8s\n' \
        "$proto" "$r" "$ratio" "${achieved:--}" "${p50:--}" "${tail:--}"
    fi
    echo "$out" > "/root/pl-$proto-$r.log"
    sleep 2   # let TIME_WAIT drain between rungs
  done
done

printf '%s\n' "-------------------------------------------------------------"
echo "logs: /root/pl-<protocol>-<rate>.log"