#!/usr/bin/env bash
#
# shark-socket test runner (Unix / Linux / macOS / Git Bash)
#
# Usage:
#   bash scripts/run_tests.sh                # run all tests
#   bash scripts/run_tests.sh --unit         # unit tests only
#   bash scripts/run_tests.sh --integration  # integration tests only
#   bash scripts/run_tests.sh --benchmark    # benchmarks only
#   bash scripts/run_tests.sh --cover        # coverage report
#   bash scripts/run_tests.sh --all          # same as default
#
# All logs are saved under ./logs/ with timestamped filenames.
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
LOGDIR="$PROJECT_DIR/logs"

mkdir -p "$LOGDIR"

TIMESTAMP=$(date +%Y%m%d_%H%M%S)

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
NC='\033[0m'

run_and_log() {
    local name=$1
    local label=$2
    shift 2

    local jsonfile="$LOGDIR/${TIMESTAMP}_${name}.json"
    local logfile="$LOGDIR/${TIMESTAMP}_${name}.log"

    echo ""
    echo -e "${CYAN}>>> [$label] Running...${NC}"
    echo -e "${CYAN}>>> JSON: $jsonfile${NC}"
    echo -e "${CYAN}>>> Log:  $logfile${NC}"
    echo ""

    # Run tests with JSON output
    cd "$PROJECT_DIR"
    go test "$@" -json -v -count=1 -timeout 300s 2>&1 | tee "$jsonfile" || true

    # Parse JSON to readable log
    cd "$PROJECT_DIR"
    go run scripts/parse_test_log.go "$jsonfile" > "$logfile" 2>/dev/null || true

    # Print the readable log
    if [ -f "$logfile" ]; then
        echo ""
        cat "$logfile"
    fi

    echo ""
    echo -e "${GREEN}>>> [$label] Done. Log saved.${NC}"
    echo ""
}

run_coverage() {
    local logfile="$LOGDIR/${TIMESTAMP}_cover.log"

    echo ""
    echo -e "${YELLOW}========================================${NC}"
    echo -e "${YELLOW}  Coverage Report${NC}"
    echo -e "${YELLOW}  $(date '+%Y-%m-%d %H:%M:%S')${NC}"
    echo -e "${YELLOW}========================================${NC}"
    echo ""

    cd "$PROJECT_DIR"
    go test ./... -count=1 -cover -timeout 300s 2>&1 | tee "$logfile"

    echo ""
    echo -e "${GREEN}>>> Coverage log: $logfile${NC}"
}

MODE="${1:---all}"

case "$MODE" in
    --unit)
        run_and_log unit "Unit Tests" \
            ./internal/... ./api/... ./tests/unit/...
        ;;
    --integration)
        run_and_log integration "Integration Tests" \
            ./tests/integration/...
        ;;
    --benchmark)
        run_and_log benchmark "Benchmarks" \
            -bench=. -benchmem -run=^$ \
            ./tests/benchmark/... ./internal/protocol/tcp/... \
            ./internal/infra/bufferpool/... ./internal/session/... \
            ./internal/plugin/...
        ;;
    --cover)
        run_coverage
        ;;
    --all)
        echo -e "${YELLOW}========================================${NC}"
        echo -e "${YELLOW}  shark-socket full test suite${NC}"
        echo -e "${YELLOW}  $(date '+%Y-%m-%d %H:%M:%S')${NC}"
        echo -e "${YELLOW}========================================${NC}"

        run_and_log unit "Unit Tests" \
            ./internal/... ./api/... ./tests/unit/...

        run_and_log integration "Integration Tests" \
            ./tests/integration/...

        run_and_log benchmark "Benchmarks" \
            -bench=. -benchmem -run=^$ \
            ./tests/benchmark/... ./internal/protocol/tcp/... \
            ./internal/infra/bufferpool/... ./internal/session/... \
            ./internal/plugin/...

        echo ""
        echo -e "${GREEN}========================================${NC}"
        echo -e "${GREEN}  All tests complete.${NC}"
        echo -e "${GREEN}  Logs in: $LOGDIR/${NC}"
        ls -lh "$LOGDIR/${TIMESTAMP}"* 2>/dev/null || true
        echo -e "${GREEN}========================================${NC}"
        ;;
    *)
        echo "Usage: bash scripts/run_tests.sh [--unit|--integration|--benchmark|--cover|--all]"
        exit 1
        ;;
esac
