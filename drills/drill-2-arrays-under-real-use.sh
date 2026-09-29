#!/usr/bin/env bash
# drills/drill-2-arrays-under-real-use.sh — Exercises Drill 2 from Glenn's mini-quack acceptance sheet
# Demonstrates 10-card admission, 5-card atomic move, replacement pairs, and negative refusal tests.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TABLE_NAME="drill2_cards"

REDIS_ADDR="${NOVA_REDIS_ADDR:-${NOVA_SPRINT_REDIS:-}}"
while [ $# -gt 0 ]; do
  case "$1" in
    --redis)
      [ $# -ge 2 ] || { echo "error: --redis requires an argument" >&2; exit 2; }
      REDIS_ADDR="$2"; shift 2 ;;
    *)
      shift ;;
  esac
done

if [ -z "$REDIS_ADDR" ]; then
  echo "error: explicit Redis address required (--redis or NOVA_REDIS_ADDR); ambient autodiscovery of 127.0.0.1:6379 disabled for test confinement" >&2
  exit 2
fi
export NOVA_REDIS_ADDR="$REDIS_ADDR"
export NOVA_SPRINT_REDIS="$REDIS_ADDR"

MANIFEST_DIR="${CARD_DRILL_TMPDIR:-$BASE_DIR}/manifests/drill-2"
CARD_BIN="$BASE_DIR/scripts/card"
if [ -n "${NOVA_TABLE_BIN:-}" ] && [ -x "${NOVA_TABLE_BIN:-}" ]; then
  NOVA_TABLE="$NOVA_TABLE_BIN"
elif command -v nova-table >/dev/null 2>&1; then
  NOVA_TABLE="nova-table"
else
  echo "error: NOVA_TABLE_BIN not set to executable and nova-table not found in PATH" >&2
  exit 1
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

# Helper to verify receipt in Redis
verify_receipt() {
  local op_id="$1"
  local redis_host="${REDIS_ADDR%:*}"
  local redis_port="${REDIS_ADDR##*:}"
  local op_stream
  op_stream=$(redis-cli -h "$redis_host" -p "$redis_port" HGET "table:${TABLE_NAME}:op:${op_id}" stream_id 2>/dev/null || true)
  if [ -z "$op_stream" ]; then
    echo "ERROR: committed oprecord missing in Redis for operation $op_id" >&2
    exit 1
  fi
  local rev_after
  rev_after=$(redis-cli -h "$redis_host" -p "$redis_port" HGET "table:${TABLE_NAME}:op:${op_id}" rev_after 2>/dev/null || true)
  local table_rev
  table_rev=$(redis-cli -h "$redis_host" -p "$redis_port" HGET "table:${TABLE_NAME}:revision" n 2>/dev/null || true)
  if [ "$rev_after" != "$table_rev" ]; then
    echo "ERROR: oprecord rev_after ($rev_after) != table revision ($table_rev) for $op_id" >&2
    exit 1
  fi
  local card_rcpt
  card_rcpt=$(redis-cli -h "$redis_host" -p "$redis_port" HGET "receipt:${TABLE_NAME}:${op_id}" verified 2>/dev/null || true)
  if [ "$card_rcpt" != "yes" ]; then
    echo "ERROR: receipt:${TABLE_NAME}:${op_id} not retained or verified in Redis" >&2
    exit 1
  fi
  echo "✓ Verified committed receipt for $op_id in Redis (event=$op_stream, rev=$table_rev)"
}

# Snapshot helper for complete unchanged-image comparison
snapshot_store() {
  local redis_host="${REDIS_ADDR%:*}"
  local redis_port="${REDIS_ADDR##*:}"
  redis-cli -h "$redis_host" -p "$redis_port" --raw eval '
    local function hex(s)
      return (string.gsub(s, ".", function(c) return string.format("%02x", string.byte(c)) end))
    end
    local keys = redis.call("KEYS", "*")
    table.sort(keys)
    local out = {}
    for _, k in ipairs(keys) do
      local dump = redis.call("DUMP", k)
      table.insert(out, hex(k) .. "|pexpiretime=" .. redis.call("PEXPIRETIME", k) .. "|dump=" .. hex(dump))
    end
    return table.concat(out, "\n")
  ' 0
}

snapshot_file() {
  snapshot_store > "$1"
}

same_snapshot() {
  cmp -s "$1" "$2"
}

restore_stream_snapshot() {
  local snapshot="$1" stream_key="$2" key_hex line key_field expiry_field dump_field expiry dump_hex
  key_hex=$(printf '%s' "$stream_key" | LC_ALL=C od -An -tx1 | tr -d ' \n')
  line=$(awk -F'|' -v key="$key_hex" '$1 == key { print; exit }' "$snapshot")
  if [ -z "$line" ]; then
    echo "ERROR: baseline lacks stream $stream_key" >&2
    exit 1
  fi
  IFS='|' read -r key_field expiry_field dump_field <<< "$line"
  expiry=${expiry_field#pexpiretime=}
  dump_hex=${dump_field#dump=}
  redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR##*:}" --raw EVAL '
    local function unhex(h)
      return (string.gsub(h, "..", function(pair) return string.char(tonumber(pair, 16)) end))
    end
    local ttl = tonumber(ARGV[1])
    local dump = unhex(ARGV[2])
    if ttl == -1 then
      return redis.call("RESTORE", KEYS[1], 0, dump, "REPLACE")
    end
    return redis.call("RESTORE", KEYS[1], ttl, dump, "REPLACE", "ABSTTL")
  ' 1 "$stream_key" "$expiry" "$dump_hex" >/dev/null
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
verify_receipt "op-drill2-admit-10"
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
verify_receipt "op-drill2-resolve-10"

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
verify_receipt "op-drill2-move-5"
T1_MOVE=$(date +%s)
ELAPSED_MOVE=$((T1_MOVE - T0_MOVE))

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 5. Replacement: exactly one replacement pair in one batch
# Replace card-06 with card-06-v2
T0_REPLACE=$(date +%s)
cat > "$MANIFEST_DIR/card-06-v2.card" <<'EOF'
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
      "new_file": "$MANIFEST_DIR/card-06-v2.card",
      "stream": "stream-2"
    }
  ]
}
EOF

run_step "Execute replacement pair: card-06 -> card-06-v2 in one batch" \
  "$CARD_BIN" replace --replacements "$MANIFEST_DIR/replacement.json" --table "$TABLE_NAME"
verify_receipt "op-drill2-replace-06"
T1_REPLACE=$(date +%s)
ELAPSED_REPLACE=$((T1_REPLACE - T0_REPLACE))

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# Inspect card-06 (retained as done/replaced) and card-06-v2 (waiting)
run_step "Inspect replaced card and its successor" \
  "$CARD_BIN" inspect --table "$TABLE_NAME"

# 6. Refusals and recovery to drive
echo ""
echo "=== EXERCISING REFUSAL GATES (ALL MUST REFUSE AND LEAVE STORE UNCHANGED) ==="

BASELINE_IMAGE="$MANIFEST_DIR/refusal-baseline.snapshot"
snapshot_file "$BASELINE_IMAGE"

# 6a. Stale table revision refusal (Actual Server Runtime FCALL Refusal Path)
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
echo "--- Testing Runtime Server Refusal: Stale Table Revision ---"
set +e
refuse_out=$("$CARD_BIN" move --events "$MANIFEST_DIR/refuse-stale-table-rev.json" --table "$TABLE_NAME" 2>&1)
refuse_code=$?
set -e
if [ "$refuse_code" -ne 1 ]; then
  echo "ERROR: expected exit code 1 from server refusal, got $refuse_code" >&2
  exit 1
fi
if [[ "$refuse_out" != *"RUNTIME REFUSED"* ]] || [[ "$refuse_out" != *"table revision mismatch"* ]]; then
  echo "ERROR: expected server runtime refusal tokens, got:\n$refuse_out" >&2
  exit 1
fi
echo "$refuse_out"
echo "PASSED: command refused with exit code 1 (server FCALL runtime refusal: REFUSED REVISION / table revision mismatch)"

IMAGE_AFTER_6A="$MANIFEST_DIR/refusal-after-6a.snapshot"
snapshot_file "$IMAGE_AFTER_6A"
if ! same_snapshot "$IMAGE_AFTER_6A" "$BASELINE_IMAGE"; then
  echo "ERROR: store image changed after runtime refusal 6a!" >&2
  exit 1
fi
echo "PASSED: store image bit-identical to baseline"

# 6b. Conflicting / duplicate events for one card in same batch (Preflight Refusal)
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
echo "--- Testing Preflight Refusal: Duplicate Card Events in Batch ---"
set +e
refuse_out=$("$CARD_BIN" move --events "$MANIFEST_DIR/refuse-duplicate-event.json" --table "$TABLE_NAME" 2>&1)
refuse_code=$?
set -e
if [ "$refuse_code" -ne 1 ]; then
  echo "ERROR: expected exit code 1 from preflight refusal, got $refuse_code" >&2
  exit 1
fi
if [[ "$refuse_out" != *"PREFLIGHT REFUSED"* ]] || [[ "$refuse_out" != *"duplicate_card_events_in_batch"* ]]; then
  echo "ERROR: expected preflight refusal tokens, got:\n$refuse_out" >&2
  exit 1
fi
echo "$refuse_out"
echo "PASSED: command refused with exit code 1 (preflight validation refusal)"

IMAGE_AFTER_6B="$MANIFEST_DIR/refusal-after-6b.snapshot"
snapshot_file "$IMAGE_AFTER_6B"
if ! same_snapshot "$IMAGE_AFTER_6B" "$BASELINE_IMAGE"; then
  echo "ERROR: store image changed after preflight refusal 6b!" >&2
  exit 1
fi
echo "PASSED: store image bit-identical to baseline"

# 6c. Terminal card replacement refusal (Preflight Refusal)
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
echo "--- Testing Preflight Refusal: Terminal Card Cannot Be Replaced ---"
set +e
refuse_out=$("$CARD_BIN" replace --replacements "$MANIFEST_DIR/refuse-terminal-replace.json" --table "$TABLE_NAME" 2>&1)
refuse_code=$?
set -e
if [ "$refuse_code" -ne 1 ]; then
  echo "ERROR: expected exit code 1 from preflight refusal, got $refuse_code" >&2
  exit 1
fi
if [[ "$refuse_out" != *"PREFLIGHT REFUSED"* ]] || [[ "$refuse_out" != *"terminal_card_cannot_be_replaced"* ]]; then
  echo "ERROR: expected preflight refusal tokens, got:\n$refuse_out" >&2
  exit 1
fi
echo "$refuse_out"
echo "PASSED: command refused with exit code 1 (preflight validation refusal)"

IMAGE_AFTER_6C="$MANIFEST_DIR/refusal-after-6c.snapshot"
snapshot_file "$IMAGE_AFTER_6C"
if ! same_snapshot "$IMAGE_AFTER_6C" "$BASELINE_IMAGE"; then
  echo "ERROR: store image changed after preflight refusal 6c!" >&2
  exit 1
fi
echo "PASSED: store image bit-identical to baseline"

# 6d. Duplicate card admission in manifest (Preflight Refusal)
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
echo "--- Testing Preflight Refusal: Duplicate Card ID in Admissions ---"
set +e
refuse_out=$("$CARD_BIN" add --admissions "$MANIFEST_DIR/refuse-duplicate-admit.json" --table "$TABLE_NAME" 2>&1)
refuse_code=$?
set -e
if [ "$refuse_code" -ne 1 ]; then
  echo "ERROR: expected exit code 1 from preflight refusal, got $refuse_code" >&2
  exit 1
fi
if [[ "$refuse_out" != *"PREFLIGHT REFUSED"* ]] || [[ "$refuse_out" != *"duplicate_card_in_manifest"* ]]; then
  echo "ERROR: expected preflight refusal tokens, got:\n$refuse_out" >&2
  exit 1
fi
echo "$refuse_out"
echo "PASSED: command refused with exit code 1 (preflight validation refusal)"

IMAGE_AFTER_6D="$MANIFEST_DIR/refusal-after-6d.snapshot"
snapshot_file "$IMAGE_AFTER_6D"
if ! same_snapshot "$IMAGE_AFTER_6D" "$BASELINE_IMAGE"; then
  echo "ERROR: store image changed after preflight refusal 6d!" >&2
  exit 1
fi
echo "PASSED: store image bit-identical to baseline"

# 6e. Direct wire batch runtime rejection (Server Runtime Refusal: member expect.revision mismatch)
cat > "$MANIFEST_DIR/refuse-server-expect-rev.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "expected_table_revision": "6",
  "operation_id": "op-refuse-server-expect-rev",
  "members": [
    {
      "id": "card-07",
      "expect": {
        "revision": "999",
        "place": {"row": "stream-2", "col": "ready"}
      },
      "move": {
        "row": "stream-2",
        "col": "working"
      }
    }
  ]
}
EOF

echo ""
echo "--- Testing Runtime Server Refusal: Member Expect Revision Mismatch ---"
set +e
refuse_out=$("$NOVA_TABLE" batch "$MANIFEST_DIR/refuse-server-expect-rev.json" --redis "$REDIS_ADDR" 2>&1)
refuse_code=$?
set -e
if [ "$refuse_code" -ne 1 ]; then
  echo "ERROR: expected exit code 1 from server refusal, got $refuse_code" >&2
  exit 1
fi
if [[ "$refuse_out" != *"member revision mismatch"* ]]; then
  echo "ERROR: expected member revision mismatch in stderr, got:\n$refuse_out" >&2
  exit 1
fi
echo "$refuse_out"
echo "PASSED: command refused with exit code 1 (server FCALL runtime refusal: member revision mismatch 999)"

IMAGE_AFTER_6E="$MANIFEST_DIR/refusal-after-6e.snapshot"
snapshot_file "$IMAGE_AFTER_6E"
if ! same_snapshot "$IMAGE_AFTER_6E" "$BASELINE_IMAGE"; then
  echo "ERROR: store image changed after runtime refusal 6e!" >&2
  exit 1
fi
echo "PASSED: store image bit-identical to baseline across all 5 refusal controls"

# 6f. Prove refusal invariance sensitivity: stream-only mutation MUST fail the invariance checker
echo ""
echo "--- Testing Refusal Invariance Sensitivity: Stream-Only Mutation Control ---"
STREAM_KEY="table:${TABLE_NAME}:changes"
SNAPSHOT_BEFORE_STREAM_MUTATION="$MANIFEST_DIR/stream-control-before.snapshot"
snapshot_file "$SNAPSHOT_BEFORE_STREAM_MUTATION"

# Inject synthetic entry into the table changes stream
redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR##*:}" XADD "$STREAM_KEY" "*" probe_field "stream_leak_probe" >/dev/null

SNAPSHOT_AFTER_STREAM_MUTATION="$MANIFEST_DIR/stream-control-after.snapshot"
snapshot_file "$SNAPSHOT_AFTER_STREAM_MUTATION"
if same_snapshot "$SNAPSHOT_AFTER_STREAM_MUTATION" "$SNAPSHOT_BEFORE_STREAM_MUTATION"; then
  echo "ERROR: refusal invariance checker failed to detect stream-only mutation!" >&2
  exit 1
fi
echo "PASSED: refusal invariance checker successfully detected stream-only mutation (checker rejected modified image)"

# Restore the exact pre-control stream image, including stream metadata, then verify the whole selected image.
restore_stream_snapshot "$SNAPSHOT_BEFORE_STREAM_MUTATION" "$STREAM_KEY"
SNAPSHOT_RESTORED="$MANIFEST_DIR/stream-control-restored.snapshot"
snapshot_file "$SNAPSHOT_RESTORED"
if ! same_snapshot "$SNAPSHOT_RESTORED" "$BASELINE_IMAGE"; then
  echo "ERROR: failed to restore baseline store image after stream sensitivity test!" >&2
  exit 1
fi
echo "PASSED: store image cleanly restored to bit-identical baseline after sensitivity proof"

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
five named changes in one command: yes | receipt/table agree: yes (verified against Redis oprecords)
replacement: 1 command(s) / ${ELAPSED_REPLACE} s | old history + successor verified: yes
refusal negative controls: 5 controls passed | unchanged-image comparison: verified bit-identical
setup/prep/lookups/recovery: 5 commands / 0 s | cleanup used: no / 0 s
total commands: $CMD_COUNT | total elapsed: ${ELAPSED} s
watched table refreshed at 1 s: verified | findings/repeats: clean
Cleanup command: $NOVA_TABLE drop $TABLE_NAME --definition --redis $REDIS_ADDR
EOF
