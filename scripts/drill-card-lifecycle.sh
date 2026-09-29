#!/usr/bin/env bash
# scripts/drill-card-lifecycle.sh: Drill 1 - Card dispatch and lease acquisition lifecycle.
#
# Exercises the full card lifecycle through nova-table:
#   1. Card Admission (independent card in ready, dependent card in waiting)
#   2. Dispatch & Lease Acquisition (ready -> working with lease token and runner)
#   3. Execution & Bound Result (working -> review with PR head)
#   4. Review Quorum (review -> merging after two reader approvals + CI green)
#   5. Landing Verification (merging -> landed)
#   6. Dependency Resolution (waiting -> ready for downstream dependent card)
#   7. Full terminal verification and cleanup
#
# Usage:
#   drill-card-lifecycle.sh [options]
#
# Options:
#   --table <name>      Table name (default: drill_lifecycle_<pid>)
#   --redis <addr>      Redis address (default: 127.0.0.1:6379)
#   --stream <name>     Stream partition name (default: stream-alpha)
#   --keep              Do not clean up table and keys after the drill
#   -h, --help          Show this help

set -eu
set -o pipefail

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)

TABLE="drill_lifecycle_$$"
REDIS="${NOVA_SPRINT_REDIS:-${NOVA_REDIS_ADDR:-}}"
STREAM="stream-alpha"
KEEP=0

usage() {
    cat <<EOF
usage: drill-card-lifecycle.sh [options]

Drill 1: Card dispatch and lease acquisition lifecycle.

options:
  --table <name>   Table name (default: drill_lifecycle_<pid>)
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
if [ -x "$REPO_ROOT/bin/nova-table" ]; then
    NOVA_TABLE="$REPO_ROOT/bin/nova-table"
elif command -v nova-table >/dev/null 2>&1; then
    NOVA_TABLE="nova-table"
else
    mkdir -p "$REPO_ROOT/bin"
    (cd "$REPO_ROOT" && go build -o bin/nova-table ./cmd/nova-table)
    NOVA_TABLE="$REPO_ROOT/bin/nova-table"
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
    # Delete card keys and drop table
    redis-cli -u "redis://$REDIS" del "card:card-001" "card:card-002" >/dev/null 2>&1 || true
    "$NOVA_TABLE" drop "$TABLE" --redis "$REDIS" >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

echo "=== DRILL 1: Card Dispatch and Lease Acquisition Lifecycle ==="
echo "Target Table: $TABLE | Redis: $REDIS | Partition: $STREAM"

# Setup schema
step "Initializing card layer schema and member partitions"
"$SCRIPT_DIR/seed-card-layer.sh" --table "$TABLE" --redis "$REDIS" --streams "$STREAM" >/dev/null
redis-cli -u "redis://$REDIS" del "card:card-001" "card:card-002" >/dev/null 2>&1 || true

# 1. Admission of Card 1 (Root, no dependencies) and Card 2 (Dependent)
step "Admitting card-001 (ready) and card-002 (waiting, depends on card-001)"
"$NOVA_TABLE" member create "$TABLE" "card-001" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell add "$TABLE" "$STREAM" ready "card-001" --redis "$REDIS" >/dev/null
redis-cli -u "redis://$REDIS" hset "card:card-001" id "card-001" stream "$STREAM" depends_on "-" kind "fix" status "ready" >/dev/null

"$NOVA_TABLE" member create "$TABLE" "card-002" --redis "$REDIS" >/dev/null
"$NOVA_TABLE" cell add "$TABLE" "$STREAM" waiting "card-002" --redis "$REDIS" >/dev/null
redis-cli -u "redis://$REDIS" hset "card:card-002" id "card-002" stream "$STREAM" depends_on "card-001" kind "fix" status "waiting" >/dev/null

# Verify admission counts
show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -q "waiting=1 ready=1" <<< "$show_out"; then
    echo "ERROR: admission counts mismatch: $show_out" >&2
    exit 1
fi

# 2. Dispatch and Lease Acquisition (ready -> working)
step "Runner-alpha claims card-001 (dispatch & lease acquisition: ready -> working)"
LEASE_EXP=$(( $(date +%s) + 300 ))
redis-cli -u "redis://$REDIS" hset "card:card-001" \
    runner "runner-alpha" \
    attempt "1" \
    lease_token "lease-tok-001" \
    lease_expires "$LEASE_EXP" \
    status "working" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" ready working "card-001" --redis "$REDIS" >/dev/null

# Verify move to working
show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -q "ready=0 working=1" <<< "$show_out"; then
    echo "ERROR: dispatch to working failed: $show_out" >&2
    exit 1
fi

# 3. Execution completion and bound result (working -> review)
step "Runner-alpha yields execution result with PR head (working -> review)"
PR_HEAD="commit-sha-alpha-001"
redis-cli -u "redis://$REDIS" hset "card:card-001" \
    pr_head "$PR_HEAD" \
    result "bound" \
    status "review" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" working review "card-001" --redis "$REDIS" >/dev/null

show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -q "working=0 review=1" <<< "$show_out"; then
    echo "ERROR: result yield to review failed: $show_out" >&2
    exit 1
fi

# 4. Review quorum and transition to merging (review -> merging)
step "Reviewers record two independent approvals and green CI (review -> merging)"
redis-cli -u "redis://$REDIS" hset "card:card-001" \
    "review:reviewer-1" "accept" \
    "review:reviewer-2" "accept" \
    "ci:status" "pass" \
    "status" "merging" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" review merging "card-001" --redis "$REDIS" >/dev/null

show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -q "review=0 merging=1" <<< "$show_out"; then
    echo "ERROR: review quorum to merging failed: $show_out" >&2
    exit 1
fi

# 5. Landing verification (merging -> landed)
step "PR merged to main branch; landing verified (merging -> landed)"
LANDED_SHA="commit-sha-landed-001"
redis-cli -u "redis://$REDIS" hset "card:card-001" \
    "landed_sha" "$LANDED_SHA" \
    "status" "landed" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" merging landed "card-001" --redis "$REDIS" >/dev/null

show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -q "merging=0 landed=1" <<< "$show_out"; then
    echo "ERROR: move to landed failed: $show_out" >&2
    exit 1
fi

# 6. Dependency resolution: card-002 waiting -> ready
step "Dependency resolved: card-001 landed unblocks card-002 (waiting -> ready)"
redis-cli -u "redis://$REDIS" hset "card:card-002" status "ready" >/dev/null
"$NOVA_TABLE" cell move "$TABLE" "$STREAM" waiting ready "card-002" --redis "$REDIS" >/dev/null

show_out=$("$NOVA_TABLE" show "$TABLE" --redis "$REDIS")
if ! grep -qE "waiting=0.*ready=1.*landed=1" <<< "$show_out"; then
    echo "ERROR: dependency unblock failed: $show_out" >&2
    exit 1
fi

# 7. Render final table state
step "Rendering verified final table state"
"$NOVA_TABLE" render "$TABLE" --redis "$REDIS"

END_TIME=$(date +%s)
ELAPSED=$((END_TIME - START_TIME))

echo "--------------------------------------------------------"
echo "DRILL 1 VERIFIED: SUCCESS"
echo "  Table:             $TABLE"
echo "  Operations:        $OP_COUNT"
echo "  Elapsed:           ${ELAPSED}s"
echo "  Landed Card:       card-001 (pr_head=$PR_HEAD landed_sha=$LANDED_SHA)"
echo "  Unblocked Card:    card-002 (ready for execution)"
echo "--------------------------------------------------------"
exit 0
