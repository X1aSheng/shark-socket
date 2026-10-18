#!/usr/bin/env bash
# Write-batching probe: the Linux half of ADR-013 / report item B2.
#
# Already proven platform-independently (internal/transport/tcp/writebuf_e2e_test.go):
# a real server over a real socket queues a 64-frame burst into ONE batch when
# WriteBufferBytes=4096, and one write path per frame without it. What that test
# cannot say is how much a syscall costs on the production OS.
#
# Why Linux:
#   On unix the two pieces of a frame (length header + payload) already leave in
#   ONE writev, so the acceptance number is "1 syscall per frame -> 1 per burst",
#   not "2 -> 1". On Windows framedWrite falls back to two Write calls, so the
#   ratios there do not describe the production path. Whether collapsing bursts
#   saves wall-clock time depends on syscall cost, which a Windows loopback
#   cannot show (ADR-013 recorded a 677-936us vs 724-855us tie).
#
# Usage (Linux host, repo root):
#   bash scripts/probe-write-batching.sh
# Requirements: go, strace, awk. Nothing is installed automatically.
#
# How to read the syscall table:
#   The benchmark runs client and server in ONE process, and the client always
#   uses the per-frame path (writev) because it has no write buffer. That is
#   what makes the split informative:
#     writev per frame ~2.0  with WriteBufferBytes=0    (client 1 + server 1)
#     writev per frame ~1.0  with WriteBufferBytes=4096 (client only)
#     write  per frame ~1/16 with WriteBufferBytes=4096 (one flush per burst)
#   If the buffered arm does not show that shape, batching is not engaging
#   under real load and ADR-013's re-evaluation condition applies.

set -euo pipefail

cd "$(dirname "$0")/.."

ITER="${ITER:-2000}"
BENCH='BenchmarkTCPEchoWriteBuffer'
PKG='./tests/benchmark'
OUT="${OUT:-/tmp/write-batching-probe}"
FRAMES_PER_OP=16

mkdir -p "$OUT"

for tool in go strace awk grep; do
    command -v "$tool" >/dev/null 2>&1 || { echo "$tool not found in PATH" >&2; exit 2; }
done
if [ "$(go env GOOS)" != "linux" ]; then
    echo "this probe must run on linux: writev coalescing is what it measures" >&2
    exit 2
fi

# Build once: tracing `go test` would also trace the compiler's writes and bury
# the measurement in noise.
echo "building $PKG ..."
go test -c -o "$OUT/bench.test" "$PKG" >/dev/null

frames=$((ITER * FRAMES_PER_OP))

probe_syscalls() {
    local tag="$1" pattern="$2" raw="$OUT/syscalls-$tag.txt"
    # strace distorts timing, so this run is for call counts only.
    strace -f -e trace=write,writev -o "$raw" \
        "$OUT/bench.test" -test.run '^$' -test.bench "$pattern" \
        -test.benchtime "${ITER}x" >/dev/null 2>&1 || true
    # Count call sites rather than parsing `strace -c`'s summary table: the
    # summary collapses its columns when the error count is empty.
    local w wr
    w=$(grep -c 'write(' "$raw" 2>/dev/null || true)
    wr=$(grep -c 'writev(' "$raw" 2>/dev/null || true)
    echo "${w:-0} ${wr:-0}"
}

probe_time() {
    local pattern="$1"
    "$OUT/bench.test" -test.run '^$' -test.bench "$pattern" \
        -test.benchtime "${ITER}x" -test.count 3 2>&1 |
        awk '/ns\/op/ { print $3, $4, $5, $6, $7, $8 }'
}

echo "== write batching probe =="
echo "host: $(uname -srm)"
echo "go:   $(go version | awk '{print $3}')"
echo "iterations: $ITER  frames measured: $frames (16 per op, both directions)"
echo

for arm in 0 4096; do
    pattern="^${BENCH}/buf${arm}$"
    read -r w wr < <(probe_syscalls "buf${arm}" "$pattern")

    per_write=$(awk -v t="$w" -v f="$frames" 'BEGIN { if (f == 0) print "n/a"; else printf "%.3f", t / f }')
    per_writev=$(awk -v t="$wr" -v f="$frames" 'BEGIN { if (f == 0) print "n/a"; else printf "%.3f", t / f }')

    echo "-- WriteBufferBytes=$arm"
    echo "   write=$w (${per_write}/frame)  writev=$wr (${per_writev}/frame)"
    echo "   throughput, ns/op over 3 runs:"
    probe_time "$pattern" | sed 's/^/     /'
    echo
done

cat <<NOTE
Raw trace summaries kept in $OUT/syscalls-buf*.txt.
Record the ns/op comparison in ADR-013 / report B2 only if the difference
exceeds the spread of the three runs; otherwise say so explicitly.
NOTE
