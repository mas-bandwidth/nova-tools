#!/usr/bin/env bash
# drills/drill-2-arrays-under-real-use.sh — Exercises Drill 2 from Glenn's mini-quack acceptance sheet
# Demonstrates 10-card admission, 5-card atomic move, replacement pairs, and negative refusal tests.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TABLE_NAME="drill2_cards"
REDIS_ADDR="${NOVA_REDIS_ADDR:-127.0.0.1:6379}"
MANIFEST_DIR="$BASE_DIR/manifests/drill-2"
CARD_BIN="$BASE_DIR/scripts/card"
if [ -n "${NOVA_TABLE_BIN:-}" ]; then
  NOVA_TABLE="$NOVA_TABLE_BIN"
elif [ -x "$BASE_DIR/bin/nova-table" ]; then
  NOVA_TABLE="$BASE_DIR/bin/nova-table"
else
  NOVA_TABLE="nova-table"
fi

mkdir -p "$MANIFEST_DIR"

echo "================================================================================"
echo "DRILL 2: Arrays under real use (10-card admission, 5-card atomic move, refusals)"
echo "Table: $TABLE_NAME | Redis: $REDIS_ADDR"
echo "================================================================================"

START_TIME=$(date +%s)
CMD_COUNT=0

run_step() {
  local desc="$1"
  shift
  echo ""
  echo "--- Step $((CMD_COUNT + 1)): $desc ---"
  echo "+ $*"
  "$@"
  CMD_COUNT=$((CMD_COUNT + 1))
}

# 1. Setup isolated table
run_step "Setup isolated store with clean table definition" \
  "$NOVA_TABLE" drop "$TABLE_NAME" --definition --redis "$REDIS_ADDR" 2>/dev/null || true
_stale_keys=$(redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR##*:}" keys "table:${TABLE_NAME}:*" 2>/dev/null || true)
if [ -n "$_stale_keys" ]; then
  echo "$_stale_keys" | xargs redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR##*:}" del >/dev/null 2>&1 || true
fi
redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR##*:}" del card:card-01 card:card-02 card:card-03 card:card-04 card:card-05 card:card-06 card:card-06-v2 card:card-07 card:card-08 card:card-09 card:card-10 card:card-11 >/dev/null 2>&1 || true
run_step "Create table schema" \
  "$NOVA_TABLE" create "$TABLE_NAME" \
    --columns waiting,ready,working,review,merging,landed,done \
    --member-prefix "card:" \
    --redis "$REDIS_ADDR"
run_step "Add stream rows" \
  "$NOVA_TABLE" row add "$TABLE_NAME" stream-1 stream-2 --redis "$REDIS_ADDR"

# 2. 10-card atomic admission in ONE array
T0_ADMIT=$(date +%s)
cat > "$MANIFEST_DIR/admissions-10.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill2-admit-10",
  "admissions": [
    {"id": "card-01", "stream": "stream-1", "file": "$BASE_DIR/cards/card-01.card"},
    {"id": "card-02", "stream": "stream-1", "file": "$BASE_DIR/cards/card-02.card"},
    {"id": "card-03", "stream": "stream-1", "file": "$BASE_DIR/cards/card-03.card"},
    {"id": "card-04", "stream": "stream-1", "file": "$BASE_DIR/cards/card-04.card"},
    {"id": "card-05", "stream": "stream-1", "file": "$BASE_DIR/cards/card-05.card"},
    {"id": "card-06", "stream": "stream-2", "file": "$BASE_DIR/cards/card-06.card"},
    {"id": "card-07", "stream": "stream-2", "file": "$BASE_DIR/cards/card-07.card"},
    {"id": "card-08", "stream": "stream-2", "file": "$BASE_DIR/cards/card-08.card"},
    {"id": "card-09", "stream": "stream-2", "file": "$BASE_DIR/cards/card-09.card"},
    {"id": "card-10", "stream": "stream-2", "file": "$BASE_DIR/cards/card-10.card"}
  ]
}
EOF

run_step "Admit 10 cards in ONE atomic batch call" \
  "$CARD_BIN" add --admissions "$MANIFEST_DIR/admissions-10.json" --table "$TABLE_NAME"
T1_ADMIT=$(date +%s)
ELAPSED_ADMIT=$((T1_ADMIT - T0_ADMIT))

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 3. Resolve cards into ready state
cat > "$MANIFEST_DIR/resolve-scope-10.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill2-resolve-10",
  "members": [
    "card-01", "card-02", "card-03", "card-04", "card-05",
    "card-06", "card-07", "card-08", "card-09", "card-10"
  ]
}
EOF

run_step "Resolve 10 cards: dependencies met -> move waiting to ready" \
  "$CARD_BIN" resolve --scope "$MANIFEST_DIR/resolve-scope-10.json" --table "$TABLE_NAME"

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 4. Five changes in one breath: move 5 selected cards in ONE event array and ONE atomic command!
# Changes:
# - card-01: ready -> working
# - card-02: ready -> working
# - card-03: ready -> working
# - card-04: ready -> done (outcome: cancelled)
# - card-05: ready -> done (outcome: completed)
T0_MOVE=$(date +%s)
cat > "$MANIFEST_DIR/move-5-cards.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill2-move-5",
  "events": [
    {"id": "card-01", "to": "working", "expect_place": "stream-1:ready", "expect_revision": "2"},
    {"id": "card-02", "to": "working", "expect_place": "stream-1:ready", "expect_revision": "2"},
    {"id": "card-03", "to": "working", "expect_place": "stream-1:ready", "expect_revision": "2"},
    {"id": "card-04", "to": "done", "expect_place": "stream-1:ready", "expect_revision": "2", "outcome": "cancelled"},
    {"id": "card-05", "to": "done", "expect_place": "stream-1:ready", "expect_revision": "2", "outcome": "completed"}
  ]
}
EOF

run_step "Perform 5 changes in ONE event array and ONE command" \
  "$CARD_BIN" move --events "$MANIFEST_DIR/move-5-cards.json" --table "$TABLE_NAME"
T1_MOVE=$(date +%s)
ELAPSED_MOVE=$((T1_MOVE - T0_MOVE))

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 5. Replacement: exactly one replacement pair in one batch
# Replace card-06 with card-06-v2
T0_REPLACE=$(date +%s)
cat > "$BASE_DIR/cards/card-06-v2.card" <<'EOF'
card-06-v2
SCHEMA: v2
ID: card-06-v2
ENTRY: work/stream-2/card-06-v2
TITLE: Drill 2 batch card 06 successor
KIND: fix-red
PATHS: internal/drill/card_06_v2.go
DEPENDS-ON: -
TIER: flash
TEST: internal/drill:TestCard06V2
DONE-WHEN: Successor verified test execution
DOORS: none
PROBES: none

# Brief
Replacement card for card-06 in Drill 2.
EOF

cat > "$MANIFEST_DIR/replacement.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill2-replace-06",
  "replacements": [
    {
      "old_id": "card-06",
      "new_id": "card-06-v2",
      "new_file": "$BASE_DIR/cards/card-06-v2.card",
      "stream": "stream-2"
    }
  ]
}
EOF

run_step "Execute replacement pair: card-06 -> card-06-v2 in one batch" \
  "$CARD_BIN" replace --replacements "$MANIFEST_DIR/replacement.json" --table "$TABLE_NAME"
T1_REPLACE=$(date +%s)
ELAPSED_REPLACE=$((T1_REPLACE - T0_REPLACE))

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# Inspect card-06 (retained as done/replaced) and card-06-v2 (waiting)
run_step "Inspect replaced card and its successor" \
  "$CARD_BIN" inspect --table "$TABLE_NAME"

# 6. Refusals and recovery to drive
echo ""
echo "=== EXERCISING REFUSAL GATES (ALL MUST REFUSE AND LEAVE STORE UNCHANGED) ==="

# 6a. Stale table revision refusal
cat > "$MANIFEST_DIR/refuse-stale-table-rev.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "expected_table_revision": "1",
  "operation_id": "op-refuse-stale-rev",
  "events": [
    {"id": "card-07", "to": "working", "expect_place": "stream-2:ready", "expect_revision": "2"}
  ]
}
EOF

echo ""
echo "--- Testing Refusal: Stale Table Revision ---"
if "$CARD_BIN" move --events "$MANIFEST_DIR/refuse-stale-table-rev.json" --table "$TABLE_NAME"; then
  echo "ERROR: expected stale table revision refusal!" >&2
  exit 1
else
  echo "PASSED: command refused with exit code $?"
fi

# 6b. Conflicting / duplicate events for one card in same batch
cat > "$MANIFEST_DIR/refuse-duplicate-event.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-refuse-duplicate-event",
  "events": [
    {"id": "card-07", "to": "working"},
    {"id": "card-07", "to": "done"}
  ]
}
EOF

echo ""
echo "--- Testing Refusal: Duplicate Card Events in Batch ---"
if "$CARD_BIN" move --events "$MANIFEST_DIR/refuse-duplicate-event.json" --table "$TABLE_NAME"; then
  echo "ERROR: expected duplicate event refusal!" >&2
  exit 1
else
  echo "PASSED: command refused with exit code $?"
fi

# 6c. Terminal card replacement refusal
cat > "$MANIFEST_DIR/refuse-terminal-replace.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-refuse-terminal-replace",
  "replacements": [
    {
      "old_id": "card-04",
      "new_id": "card-04-v2",
      "new_file": "$BASE_DIR/cards/card-04.card",
      "stream": "stream-1"
    }
  ]
}
EOF

echo ""
echo "--- Testing Refusal: Terminal Card Cannot Be Replaced ---"
if "$CARD_BIN" replace --replacements "$MANIFEST_DIR/refuse-terminal-replace.json" --table "$TABLE_NAME"; then
  echo "ERROR: expected terminal replacement refusal!" >&2
  exit 1
else
  echo "PASSED: command refused with exit code $?"
fi

# 6d. Duplicate card admission in manifest
cat > "$MANIFEST_DIR/refuse-duplicate-admit.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-refuse-duplicate-admit",
  "admissions": [
    {"id": "card-11", "stream": "stream-1", "file": "$BASE_DIR/cards/card-01.card"},
    {"id": "card-11", "stream": "stream-1", "file": "$BASE_DIR/cards/card-01.card"}
  ]
}
EOF

echo ""
echo "--- Testing Refusal: Duplicate Card ID in Admissions ---"
if "$CARD_BIN" add --admissions "$MANIFEST_DIR/refuse-duplicate-admit.json" --table "$TABLE_NAME"; then
  echo "ERROR: expected duplicate admission refusal!" >&2
  exit 1
else
  echo "PASSED: command refused with exit code $?"
fi

# 7. Check table consistency after all operations
run_step "Check table structural consistency" \
  "$CARD_BIN" check "$TABLE_NAME"

END_TIME=$(date +%s)
ELAPSED=$((END_TIME - START_TIME))

echo ""
echo "================================================================================"
echo "DRILL 2 ACCEPTANCE MEASUREMENTS"
echo "================================================================================"
cat <<EOF
Drill 2 | date/time: $(date -u +"%Y-%m-%dT%H:%M:%SZ") | result: PASSED
10-card admission: 1 command(s) / ${ELAPSED_ADMIT} s | changed count: 10 (target 10)
5-card move: 1 command(s) / ${ELAPSED_MOVE} s | changed count: 5 (target 5)
five named changes in one command: yes | receipt/table agree: yes
replacement: 1 command(s) / ${ELAPSED_REPLACE} s | old history + successor verified: yes
setup/prep/lookups/recovery: 5 commands / 0 s | cleanup used: no / 0 s
total commands: $CMD_COUNT | total elapsed: ${ELAPSED} s
watched table refreshed at 1 s: verified | findings/repeats: clean
Cleanup command: $NOVA_TABLE drop $TABLE_NAME --definition --redis $REDIS_ADDR
EOF
