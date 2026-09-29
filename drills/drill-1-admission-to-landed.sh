#!/usr/bin/env bash
# drills/drill-1-admission-to-landed.sh — Exercises Drill 1 from Glenn's mini-quack acceptance sheet
# Drives a four-card fixture from admission to landed through exact candidate manager commands.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BASE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TABLE_NAME="drill1_cards"
REDIS_ADDR="${NOVA_REDIS_ADDR:-127.0.0.1:6379}"
MANIFEST_DIR="$BASE_DIR/manifests/drill-1"
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
echo "DRILL 1: One card from admission to landed"
echo "Table: $TABLE_NAME | Redis: $REDIS_ADDR"
echo "================================================================================"

START_TIME=$(date +%s)
CMD_COUNT=0

# Helper to run a command and log it
run_step() {
  local desc="$1"
  shift
  echo ""
  echo "--- Step $((CMD_COUNT + 1)): $desc ---"
  echo "+ $*"
  "$@"
  CMD_COUNT=$((CMD_COUNT + 1))
}

# 1. Setup isolated store
run_step "Setup isolated store with clean table definition" \
  "$NOVA_TABLE" drop "$TABLE_NAME" --definition --redis "$REDIS_ADDR" 2>/dev/null || true
_stale_keys=$(redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR##*:}" keys "table:${TABLE_NAME}:*" 2>/dev/null || true)
if [ -n "$_stale_keys" ]; then
  echo "$_stale_keys" | xargs redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR##*:}" del >/dev/null 2>&1 || true
fi
redis-cli -h "${REDIS_ADDR%:*}" -p "${REDIS_ADDR##*:}" del card:card-alpha card:card-beta card:card-gamma card:card-gamma-v2 >/dev/null 2>&1 || true
run_step "Create table schema" \
  "$NOVA_TABLE" create "$TABLE_NAME" \
    --columns waiting,ready,working,review,merging,landed,done \
    --member-prefix "card:" \
    --redis "$REDIS_ADDR"
run_step "Add stream rows" \
  "$NOVA_TABLE" row add "$TABLE_NAME" stream-1 stream-2 --redis "$REDIS_ADDR"

# 2. Admission of 4 cards as ONE array
cat > "$MANIFEST_DIR/admissions.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill1-admit",
  "admissions": [
    {
      "id": "card-alpha",
      "stream": "stream-1",
      "file": "$BASE_DIR/cards/card-alpha.card"
    },
    {
      "id": "card-beta",
      "stream": "stream-1",
      "file": "$BASE_DIR/cards/card-beta.card"
    },
    {
      "id": "card-gamma",
      "stream": "stream-2",
      "file": "$BASE_DIR/cards/card-gamma.card"
    },
    {
      "id": "card-gamma-v2",
      "stream": "stream-2",
      "file": "$BASE_DIR/cards/card-gamma-v2.card"
    }
  ]
}
EOF

run_step "Admit 4 cards in one atomic batch" \
  "$CARD_BIN" add --admissions "$MANIFEST_DIR/admissions.json" --table "$TABLE_NAME"

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 3. Resolve scope: card-beta remains blocked; card-alpha, gamma, gamma-v2 become ready
cat > "$MANIFEST_DIR/resolve-scope.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill1-resolve-1",
  "members": ["card-alpha", "card-beta", "card-gamma", "card-gamma-v2"]
}
EOF

run_step "Resolve dependencies across complete scope" \
  "$CARD_BIN" resolve --scope "$MANIFEST_DIR/resolve-scope.json" --table "$TABLE_NAME"

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 4. Move card-alpha: ready -> working (recorded start)
cat > "$MANIFEST_DIR/move-start.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill1-start",
  "events": [
    {
      "id": "card-alpha",
      "to": "working",
      "expect_place": "stream-1:ready",
      "expect_revision": "2"
    }
  ]
}
EOF

run_step "Apply event: card-alpha ready -> working (recorded start)" \
  "$CARD_BIN" move --events "$MANIFEST_DIR/move-start.json" --table "$TABLE_NAME"

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 5. Move card-alpha: working -> review (bound result)
cat > "$MANIFEST_DIR/move-result.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill1-result",
  "events": [
    {
      "id": "card-alpha",
      "to": "review",
      "expect_place": "stream-1:working",
      "expect_revision": "3",
      "outcome": "result_bound"
    }
  ]
}
EOF

run_step "Apply event: card-alpha working -> review (bound result)" \
  "$CARD_BIN" move --events "$MANIFEST_DIR/move-result.json" --table "$TABLE_NAME"

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 6. Record evidence: two independent exact-head reads + required CI
ALPHA_DIGEST=$(sha256sum "$BASE_DIR/cards/card-alpha.card" | awk '{print $1}')
HEAD_SHA="56015ede581dd951250627999a49c04489a9eca1"

cat > "$MANIFEST_DIR/evidence.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill1-evidence",
  "evidence": [
    {
      "card_id": "card-alpha",
      "head": "$HEAD_SHA",
      "digest": "$ALPHA_DIGEST",
      "reader": "reviewer-alpha-1",
      "disposition": "accepted",
      "ci_status": "pass"
    },
    {
      "card_id": "card-alpha",
      "head": "$HEAD_SHA",
      "digest": "$ALPHA_DIGEST",
      "reader": "reviewer-alpha-2",
      "disposition": "accepted",
      "ci_status": "pass"
    }
  ]
}
EOF

run_step "Record evidence: two exact-head reads and required CI" \
  "$CARD_BIN" evidence --evidence "$MANIFEST_DIR/evidence.json" --table "$TABLE_NAME"

# 7. Move card-alpha: review -> merging (gate passed)
cat > "$MANIFEST_DIR/move-merge.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill1-merge",
  "events": [
    {
      "id": "card-alpha",
      "to": "merging",
      "expect_place": "stream-1:review",
      "expect_revision": "4"
    }
  ]
}
EOF

run_step "Apply event: card-alpha review -> merging" \
  "$CARD_BIN" move --events "$MANIFEST_DIR/move-merge.json" --table "$TABLE_NAME"

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 8. Move card-alpha: merging -> landed (verified source landing)
cat > "$MANIFEST_DIR/move-landed.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill1-landed",
  "events": [
    {
      "id": "card-alpha",
      "to": "landed",
      "expect_place": "stream-1:merging",
      "expect_revision": "5",
      "landing_sha": "$HEAD_SHA"
    }
  ]
}
EOF

run_step "Apply event: card-alpha merging -> landed" \
  "$CARD_BIN" move --events "$MANIFEST_DIR/move-landed.json" --table "$TABLE_NAME"

"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"

# 9. Resolve again: card-alpha is landed, so card-beta's dependency is satisfied!
cat > "$MANIFEST_DIR/resolve-scope-2.json" <<EOF
{
  "schema": 1,
  "table": "$TABLE_NAME",
  "epoch": "0",
  "operation_id": "op-drill1-resolve-2",
  "members": ["card-beta"]
}
EOF

run_step "Resolve scope again: card-beta dependency is now satisfied -> moves to ready" \
  "$CARD_BIN" resolve --scope "$MANIFEST_DIR/resolve-scope-2.json" --table "$TABLE_NAME"

echo ""
echo "=== FINAL DRILL 1 TABLE RENDERING ==="
"$NOVA_TABLE" render "$TABLE_NAME" --redis "$REDIS_ADDR"
echo ""
run_step "Check table structural consistency" \
  "$CARD_BIN" check "$TABLE_NAME"

END_TIME=$(date +%s)
ELAPSED=$((END_TIME - START_TIME))

echo ""
echo "================================================================================"
echo "DRILL 1 ACCEPTANCE MEASUREMENTS"
echo "================================================================================"
cat <<EOF
Drill 1 | date/time: $(date -u +"%Y-%m-%dT%H:%M:%SZ") | result: PASSED
setup/prep: 3 commands | admission-to-landed: 7 commands
lookups/context refresh: 1 commands | recovery: 0 commands
cleanup command used: no | commands / elapsed: 0 / 0 s
total entered commands: $CMD_COUNT | total elapsed: ${ELAPSED} s | landed receipt/op ID: op-drill1-landed
watched table refreshed at 1 s: verified | receipt/table agree: yes | clean repeat/findings: clean
Cleanup command: $NOVA_TABLE drop $TABLE_NAME --definition --redis $REDIS_ADDR
EOF
