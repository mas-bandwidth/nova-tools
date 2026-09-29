#!/usr/bin/env sh
# scripts/card_layer_drills_test.sh: test suite for card layer seed script and drills.
#
# Tests:
#   1. seed-card-layer.sh --help (clean exit 0)
#   2. seed-card-layer.sh --dry-run (clean exit 0, no mutations)
#   3. seed-card-layer.sh create & check (schema initialization)
#   4. seed-card-layer.sh idempotency (repeated create returns exit 0)
#   5. seed-card-layer.sh drop (clean table removal)
#   6. drill-card-lifecycle.sh (Drill 1: dispatch & lease lifecycle)
#   7. drill-card-fault-rejection.sh (Drill 2: negative evidence & fault handling)
#   8. Hygiene check: zero personal or bench names across card layer scripts

set -u

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)

REDIS="${NOVA_SPRINT_REDIS:-${NOVA_REDIS_ADDR:-}}"

while [ $# -gt 0 ]; do
    case "$1" in
        --redis)
            [ $# -ge 2 ] || { echo "error: --redis requires an argument" >&2; exit 2; }
            REDIS="$2"; shift 2 ;;
        *)
            shift ;;
    esac
done

if [ -n "$REDIS" ]; then
    export NOVA_SPRINT_REDIS="$REDIS"
    export NOVA_REDIS_ADDR="$REDIS"
fi

fails=0
n=0
ok()   { n=$((n+1)); printf 'ok %s - %s\n' "$n" "$1"; }
skip() { n=$((n+1)); printf 'ok %s - %s # SKIP %s\n' "$n" "$1" "$2"; }
bad()  { n=$((n+1)); printf 'not ok %s - %s\n' "$n" "$1"; fails=1; }

echo "TAP version 13"
echo "1..8"

# Check if Redis is reachable at $REDIS
redis_alive=0
redis_host="${REDIS%%:*}"
redis_port="${REDIS##*:}"
[ -z "$redis_host" ] && redis_host="127.0.0.1"
[ "$redis_host" = "$REDIS" ] && redis_port="6379"

if command -v redis-cli >/dev/null 2>&1; then
    if redis-cli -h "$redis_host" -p "$redis_port" ping >/dev/null 2>&1; then
        redis_alive=1
    fi
elif command -v nc >/dev/null 2>&1; then
    if nc -z "$redis_host" "$redis_port" >/dev/null 2>&1; then
        redis_alive=1
    fi
fi

# 1. seed-card-layer.sh --help
if "$SCRIPT_DIR/seed-card-layer.sh" --help >/dev/null 2>&1; then
    ok "seed-card-layer.sh --help exits 0"
else
    bad "seed-card-layer.sh --help failed"
fi

# 2. seed-card-layer.sh --dry-run
if "$SCRIPT_DIR/seed-card-layer.sh" --table "test_tap_$$" --redis "$REDIS" --dry-run >/dev/null 2>&1; then
    ok "seed-card-layer.sh --dry-run exits 0"
else
    bad "seed-card-layer.sh --dry-run failed"
fi

# 3. seed-card-layer.sh create & check
TEST_TABLE="test_tap_$$"
if [ "$redis_alive" -eq 1 ]; then
    if "$SCRIPT_DIR/seed-card-layer.sh" --table "$TEST_TABLE" --redis "$REDIS" >/dev/null 2>&1 && \
       "$SCRIPT_DIR/seed-card-layer.sh" --table "$TEST_TABLE" --redis "$REDIS" --check >/dev/null 2>&1; then
        ok "seed-card-layer.sh creates table and passes check"
    else
        bad "seed-card-layer.sh create or check failed"
    fi
else
    skip "seed-card-layer.sh creates table and passes check" "redis unavailable at $REDIS"
fi

# 4. seed-card-layer.sh idempotency
if [ "$redis_alive" -eq 1 ]; then
    if "$SCRIPT_DIR/seed-card-layer.sh" --table "$TEST_TABLE" --redis "$REDIS" >/dev/null 2>&1; then
        ok "seed-card-layer.sh is idempotent on repeated execution"
    else
        bad "seed-card-layer.sh idempotency re-run failed"
    fi
else
    skip "seed-card-layer.sh is idempotent on repeated execution" "redis unavailable at $REDIS"
fi

# 5. seed-card-layer.sh drop
if [ "$redis_alive" -eq 1 ]; then
    if "$SCRIPT_DIR/seed-card-layer.sh" --table "$TEST_TABLE" --redis "$REDIS" --drop >/dev/null 2>&1; then
        ok "seed-card-layer.sh drop removes table cleanly"
    else
        bad "seed-card-layer.sh drop failed"
    fi
else
    skip "seed-card-layer.sh drop removes table cleanly" "redis unavailable at $REDIS"
fi

# 6. drill-card-lifecycle.sh (Drill 1)
if [ "$redis_alive" -eq 1 ]; then
    if "$SCRIPT_DIR/drill-card-lifecycle.sh" --table "tap_drill1_$$" --redis "$REDIS" >/dev/null 2>&1; then
        ok "drill-card-lifecycle.sh (Drill 1: dispatch & lease lifecycle) passes"
    else
        bad "drill-card-lifecycle.sh failed"
    fi
else
    skip "drill-card-lifecycle.sh (Drill 1: dispatch & lease lifecycle) passes" "redis unavailable at $REDIS"
fi

# 7. drill-card-fault-rejection.sh (Drill 2)
if [ "$redis_alive" -eq 1 ]; then
    if "$SCRIPT_DIR/drill-card-fault-rejection.sh" --table "tap_drill2_$$" --redis "$REDIS" >/dev/null 2>&1; then
        ok "drill-card-fault-rejection.sh (Drill 2: negative evidence & fault handling) passes"
    else
        bad "drill-card-fault-rejection.sh failed"
    fi
else
    skip "drill-card-fault-rejection.sh (Drill 2: negative evidence & fault handling) passes" "redis unavailable at $REDIS"
fi

# 8. Hygiene check: zero personal or real bench names
BANNED_NAMES="glenn|rowan|stella|johnny|gaffer|studio|hulk"
matches=$(grep -iE "\b($BANNED_NAMES)\b" \
    "$SCRIPT_DIR/seed-card-layer.sh" \
    "$SCRIPT_DIR/drill-card-lifecycle.sh" \
    "$SCRIPT_DIR/drill-card-fault-rejection.sh" \
    2>&1 || true)

if [ -z "$matches" ]; then
    ok "zero personal or bench names across card layer scripts"
else
    bad "personal or bench names found in scripts: $matches"
fi

if [ "$fails" -eq 0 ]; then
    echo "# All card layer scaffolding and drill tests completed cleanly"
    exit 0
else
    echo "# Some tests FAILED"
    exit 1
fi
