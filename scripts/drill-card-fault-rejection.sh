#!/usr/bin/env bash
# scripts/drill-card-fault-rejection.sh: Drill 2 - Negative evidence / fault rejection handling.
#
# Exercises fault and rejection handling paths on nova-table:
#   1. Review HOLD / Negative evidence rejection (review -> ready rework)
#   2. Queue rejection in merge queue (merging -> review recovery)
#   3. Worker execution fault / lease timeout recovery (working -> ready retry)
#   4. Prerequisite dependency failure (waiting -> done outcome=depfailed)
#   5. Invariant guard enforcement (refusal of invalid transitions and duplicate IDs)
#
# Usage:
#   drill-card-fault-rejection.sh [options]
#
# Options:
#   --table <name>      Table name (default: drill_fault_<pid>)
#   --redis <addr>      Redis address (default: 127.0.0.1:6379)
#   --stream <name>     Stream partition name (default: stream-alpha)
#   --keep              Do not clean up table and keys after the drill
#   -h, --help          Show this help

set -eu
set -o pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)

TABLE="drill_fault_$$"
REDIS="${NOVA_SPRINT_REDIS:-${NOVA_REDIS_ADDR:-}}"
STREAM="stream-alpha"
KEEP=0

usage() {
    cat <<EOF
usage: drill-card-fault-rejection.sh [options]

Drill 2: Negative evidence / fault rejection handling (hold/fail).

options:
  --table <name>   Table name (default: drill_fault_<pid>)
  --redis <addr>   Redis address (default: 127.0.0.1:6379)
  --stream <name>  Stream partition name (default: stream-alpha)
  --keep           Retain table and Redis keys after completion
  -h, --help       Show this help
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
        --table)
            [ $# -ge 2 ] || { echo "error: --table requires an argument" >&2; exit 2; }
            TABLE="$2"; shift 2 ;;
        --redis)
            [ $# -ge 2 ] || { echo "error: --redis requires an argument" >&2; exit 2; }
            REDIS="$2"; shift 2 ;;
        --stream)
            [ $# -ge 2 ] || { echo "error: --stream requires an argument" >&2; exit 2; }
            STREAM="$2"; shift 2 ;;
        --keep)
            KEEP=1; shift ;;
        -h|--help)
            usage; exit 0 ;;
        *)
            echo "error: unknown argument $1" >&2; usage >&2; exit 2 ;;
    esac
done

if [ -z "$REDIS" ]; then
    echo "error: explicit Redis address required (--redis or NOVA_REDIS_ADDR); ambient autodiscovery disabled for test confinement" >&2
    exit 2
fi

# Locate nova-table
if [ -n "${NOVA_TABLE_BIN:-}" ] && [ -x "${NOVA_TABLE_BIN:-}" ]; then
    NOVA_TABLE="$NOVA_TABLE_BIN"
elif command -v nova-table >/dev/null 2>&1; then
    NOVA_TABLE="nova-table"
else
    echo "error: NOVA_TABLE_BIN not set to executable and nova-table not found in PATH" >&2
    exit 1
fi

START_TIME=$(date +%s)
OP_COUNT=0

step() {
    OP_COUNT=$((OP_COUNT + 1))
    printf "  [%02d] %s\n" "$OP_COUNT" "$1"
}

cleanup() {
    if [ "$KEEP" -eq 1 ]; then
        echo "Retaining drill table $TABLE and card keys (--keep)."
        return 0
    fi
    redis-cli -u "redis://$REDIS" del \
        "card:card-f201" "card:card-f202" "card:card-f203" "card:card-f204" "card:card-f205" >/dev/null 2>&1 || true
    "$NOVA_TABLE" drop "$TABLE" --redis "$REDIS" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

echo "=== DRILL 2: Negative Evidence & Fault Rejection Handling ==="
echo "Target Table: $TABLE | Redis: $REDIS | Partition: $STREAM"

# Setup schema
step "Initializing card layer schema and member partitions"
"$SCRIPT_DIR/seed-card-layer.sh" --table "$TABLE" --redis "$REDIS" --streams "$STREAM" >/dev/null
redis-cli -u "redis://$REDIS" del \
    "card:card-f201" "card:card-f202" "card:card-f203" "card:card-f204" "card:card-f205" >/dev/null 2>&1 || true

# -------------------------------------------------------------
# Case 1: Review HOLD / Negative Evidence Rejection (review -> ready rework)
# -------------------------------------------------------------
step "Case 1: Review HOLD triggers rework transition (review -> ready)"
"$NOVA_TABLE" member create "$TABLE" "card-f201" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell add "$TABLE" "$STREAM" ready "card-f201" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" ready working "card-f201" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" working review "card-f201" --redis "$REDIS" >/dev/null

# Reviewer records HOLD verdict
redis-cli -u "redis://$REDIS" hset "card:card-f201" \
    "review:reviewer-1" "hold: missing test coverage on failure path" \
    "status" "rework" >/dev/null

# Manager executes rework transition: review -> ready (old acceptance invalidated)
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" review ready "card-f201" --redis "$REDIS" >/dev/null

show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -qE "row=$STREAM.*ready=1.*review=0" <<< "$show_out"; then
    echo "ERROR: Case 1 review hold to ready failed: $show_out" >&2
    exit 1
fi
echo "      -> Card card-f201 returned to ready for rework (review invalidated)"

# -------------------------------------------------------------
# Case 2: Queue Rejection from Merge Queue (merging -> review)
# -------------------------------------------------------------
step "Case 2: Queue rejection recovers card to review (merging -> review)"
"$NOVA_TABLE" member create "$TABLE" "card-f202" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell add "$TABLE" "$STREAM" ready "card-f202" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" ready working "card-f202" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" working review "card-f202" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" review merging "card-f202" --redis "$REDIS" >/dev/null

# Queue rejected (e.g. merge conflict or upstream base head changed)
redis-cli -u "redis://$REDIS" hset "card:card-f202" \
    "queue_status" "rejected: merge conflict with origin/main" \
    "status" "review" >/dev/null

# Recovery transition: merging -> review (retains head without inventing new head)
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" merging review "card-f202" --redis "$REDIS" >/dev/null

show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -qE "row=$STREAM.*review=1.*merging=0" <<< "$show_out"; then
    echo "ERROR: Case 2 queue rejection recovery failed: $show_out" >&2
    exit 1
fi
echo "      -> Card card-f202 returned to review without head mutation"

# -------------------------------------------------------------
# Case 3: Worker Execution Fault / Lease Expiry (working -> ready retry)
# -------------------------------------------------------------
step "Case 3: Worker execution fault triggers retry (working -> ready)"
"$NOVA_TABLE" member create "$TABLE" "card-f203" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell add "$TABLE" "$STREAM" ready "card-f203" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" ready working "card-f203" --redis "$REDIS" >/dev/null

# Worker fault detected (attempt 1 failed)
redis-cli -u "redis://$REDIS" hset "card:card-f203" \
    "attempt_1_outcome" "fail: process terminated with exit code 1" \
    "attempt" "2" \
    "status" "ready" >/dev/null

# Recovery: move back to ready for retry
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" working ready "card-f203" --redis "$REDIS" >/dev/null

show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -qE "row=$STREAM.*ready=2.*working=0" <<< "$show_out"; then
    echo "ERROR: Case 3 worker fault retry failed: $show_out" >&2
    exit 1
fi
echo "      -> Card card-f203 recovered to ready (attempt 2 queued)"

# -------------------------------------------------------------
# Case 4: Prerequisite Dependency Failure (waiting -> done outcome=depfailed)
# -------------------------------------------------------------
step "Case 4: Prerequisite cancellation terminates dependant (waiting -> done depfailed)"
# Root card f204 in working, dependent card f205 in waiting
"$NOVA_TABLE" member create "$TABLE" "card-f204" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell add "$TABLE" "$STREAM" working "card-f204" --redis "$REDIS" >/dev/null

"$NOVA_TABLE" member create "$TABLE" "card-f205" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell add "$TABLE" "$STREAM" waiting "card-f205" --redis "$REDIS" >/dev/null
redis-cli -u "redis://$REDIS" hset "card:card-f205" depends_on "card-f204" >/dev/null

# Root card cancelled / permanently failed
redis-cli -u "redis://$REDIS" hset "card:card-f204" outcome "cancelled" status "done" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" working done "card-f204" --redis "$REDIS" >/dev/null

# Dependency resolver: root did not land (it is done/cancelled) -> dependant fails
redis-cli -u "redis://$REDIS" hset "card:card-f205" outcome "depfailed" status "done" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" waiting done "card-f205" --redis "$REDIS" >/dev/null

show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -qE "row=$STREAM.*waiting=0.*done=2" <<< "$show_out"; then
    echo "ERROR: Case 4 dependency failure handling failed: $show_out" >&2
    exit 1
fi
echo "      -> Card card-f205 transitioned waiting -> done (outcome=depfailed)"

# -------------------------------------------------------------
# Case 5: Invariant Guard Enforcement & Refusal Proof
# -------------------------------------------------------------
step "Case 5: Strict guard refusal on invalid mutation"
# 1. Duplicate member creation must be refused with exit code 1
refusal_out=$("$NOVA_TABLE" member create "$TABLE" "card-f201" --redis "$REDIS" 2>&1) || true
if ! grep -q "member identity already exists" <<< "$refusal_out"; then
    echo "ERROR: duplicate member create was not refused properly: $refusal_out" >&2
    exit 1
fi

# 2. Moving member from wrong source cell must be refused
refusal_out2=$("$NOVA_TABLE" cell move "$TABLE" "$STREAM" landed ready "card-f201" --redis "$REDIS" 2>&1) || true
if ! grep -q "NOTMEMBER" <<< "$refusal_out2"; then
    echo "ERROR: cell move from wrong cell was not refused properly: $refusal_out2" >&2
    exit 1
fi
echo "      -> Verified refusals: duplicate member create and illegal cell move refused cleanly"

# Final table render
step "Rendering verified table state after fault rejection drill"
"$NOVA_TABLE" render "$TABLE" --redis "$REDIS"

END_TIME=$(date +%s)
ELAPSED=$((END_TIME - START_TIME))

echo "--------------------------------------------------------"
echo "DRILL 2 VERIFIED: SUCCESS"
echo "  Table:             $TABLE"
echo "  Operations:        $OP_COUNT"
echo "  Elapsed:           ${ELAPSED}s"
echo "  Rework Test:       card-f201 (review -> ready)"
echo "  Queue Reject Test: card-f202 (merging -> review)"
echo "  Worker Retry Test: card-f203 (working -> ready)"
echo "  Dep-Fail Test:     card-f205 (waiting -> done depfailed)"
echo "  Guard Invariants:  Duplicate create & invalid cell moves refused"
echo "--------------------------------------------------------"
exit 0
