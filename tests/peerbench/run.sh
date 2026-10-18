#!/usr/bin/env bash
# Same-host peer benchmark: shark-socket vs gnet vs cloudwego/netpoll.
#
# Why same-host and same-client: cross-machine and cross-client numbers compare
# the network and the load generator, not the servers. Every arm here runs on
# this machine, speaks the same fixed-size byte echo, and is driven by the same
# load binary (cmd/load).
#
# Caveat when reading the output: the client shares this machine with the
# server, so every arm competes with an identical client. Relative numbers
# between arms are meaningful; the absolute ones are not a capacity claim.
#
# Two phases per arm:
#   <arm>        throughput: 1/16/64 connections, 16 messages in flight
#   <arm>-rtt    latency:    1 connection, 1 message in flight
# The latency columns of a deep-pipeline row are queueing, not round trip --
# by the time the read starts, the echo is usually already buffered.
#
# Usage (Linux):  bash tests/peerbench/run.sh
# Overrides: CONNS, SIZE, PIPELINE, DURATION, WARMUP, REPEATS, OUT.

set -euo pipefail

cd "$(dirname "$0")"

# Linux only, and not out of laziness: netpoll v0.7.5 ships no Windows event
# loop, and Windows' monotonic clock ticks at ~0.5ms here, which turns every
# sub-millisecond round trip into an exact zero and makes the latency columns
# meaningless. Both facts are properties of the platform, not of the servers.
case "$(uname -s)" in
    Linux) ;;
    *) echo "this benchmark must run on Linux (uname -s = $(uname -s))" >&2; exit 1;;
esac

CONNS="${CONNS:-1 16 64}"
SIZE="${SIZE:-256}"
PIPELINE="${PIPELINE:-16}"
DURATION="${DURATION:-5s}"
RTT_DURATION="${RTT_DURATION:-3s}"
WARMUP="${WARMUP:-1s}"
REPEATS="${REPEATS:-3}"
OUT="${OUT:-/tmp/peerbench.tsv}"

BIN=./bin
mkdir -p "$BIN"
: > "$OUT"

echo "building arms ..."
go build -o "$BIN/load" ./cmd/load
go build -o "$BIN/shark" ./cmd/shark
go build -o "$BIN/gnet" ./cmd/gnet
go build -o "$BIN/netpoll" ./cmd/netpoll

wait_port() {
    local port="$1"
    for _ in $(seq 1 100); do
        if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
            exec 3<&- 3>&-
            return 0
        fi
        sleep 0.05
    done
    return 1
}

# run_arm <label> <binary> <port> <conns-list> <pipeline> <duration> [extra args...]
run_arm() {
    local label="$1" bin="$2" port="$3" conns_list="$4" pipeline="$5" dur="$6"
    shift 6

    local server_pid
    "$BIN/$bin" -addr "127.0.0.1:$port" "$@" >"/tmp/peerbench-$label.log" 2>&1 &
    server_pid=$!
    if ! wait_port "$port"; then
        echo "$label: server did not open $port" >&2
        cat "/tmp/peerbench-$label.log" >&2
        kill "$server_pid" 2>/dev/null || true
        return 1
    fi
    # Let the listener settle: gnet's event loops and shark's worker pool start
    # asynchronously, and the first connection should not land mid-startup.
    sleep 0.3
    for c in $conns_list; do
        for _ in $(seq 1 "$REPEATS"); do
            "$BIN/load" -addr "127.0.0.1:$port" -conns "$c" -size "$SIZE" \
                -pipeline "$pipeline" -duration "$dur" -warmup "$WARMUP" \
                -label "$label" | tee -a "$OUT"
        done
    done
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
}

printf 'arm\tconns\tsize\tpipeline\tmsgs\tsecs\tmsg/s\tMB/s\tp50_round_us\tp99_round_us\n' | tee -a "$OUT"

for arm in shark gnet netpoll; do
    case $arm in
        shark) port=19001;;
        gnet) port=19002;;
        *) port=19003;;
    esac
    # A failed arm must not take the other two down with it: one unusable arm is
    # a result too (netpoll has no Windows implementation), and the remaining
    # rows are still comparable with each other.
    run_arm "$arm" "$arm" "$port" "$CONNS" "$PIPELINE" "$DURATION" || true
    run_arm "$arm-rtt" "$arm" "$port" "1" "1" "$RTT_DURATION" || true
done

echo
echo "raw rows: $OUT"
echo "msg/s and MB/s are throughput; p50/p99 are only meaningful on the -rtt rows."
