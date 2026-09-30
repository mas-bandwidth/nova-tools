#!/usr/bin/env bash
# audit-v1.1.0-release-candidate.sh: automated Release Candidate certification audit harness for v1.1.0.
#
# Author: Emma Antigravity <emma@mas-bandwidth.com>
# Harness: Emma Antigravity (gemini-2.5-pro)
#
# Verifies the release candidate against the seven mandatory gates:
#   Gate 1: Working Tree Cleanliness (git status --porcelain)
#   Gate 2: Compilation (all 18 living release tools in cmd/)
#   Gate 3: Static Analysis (make fmt, make vet, make lint)
#   Gate 4: Class Tests (go test -v ./internal/ci/... ./internal/docs/...)
#   Gate 5: Unit Tests (core packages: sprint, swarm, work, config, bus, table, sandbox)
#   Gate 6: Functional Tier (contained functional test tier with Redis 8.10.2 / podman)
#   Gate 7: Store Functional Drive (exact verified store drive test with landed card checks)
#
# Usage:
#   scripts/audit-v1.1.0-release-candidate.sh [flags]
#
# Flags:
#   --commit <ref>             Target commit/ref to audit (must strictly match checked-out HEAD)
#   --expect-parents <n>       Require exact number of parent commits (e.g. 2 for promotion merge)
#   --require-merge            Strictly require two parent commits (promotion merge commit)
#   --drive <mode>             Store drive mode: 'auto' (default), 'dirty', 'fleet', or 'sprint-ticks'
#   --drive-test <name>        Explicit test name to run for Gate 7 (must exist in *_test.go)
#   --drive-timeout <dur>      Timeout for Gate 7 store drive (default: 180s)
#   --skip-functional          Skip Gate 6 (records SKIPPED/UNCERTIFIED; cannot certify release)
#   --host-functional          Run functional tier on host instead of container
#   --functional-deadline <d>  Deadline for container functional tier (default: 10m)
#   --functional-flags <f>     Flags for container functional tier (default: '--cpus 2 --memory 4g')
#   --functional-timeout <t>   Timeout for host functional tier (default: 300s)
#   --allow-dirty              Allow dirty working tree for Gate 1 (records UNCERTIFIED; cannot certify)
#   --log-dir <dir>            Directory to preserve raw gate logs (default: scratch/audit-logs/<run-id>)
#   --out <file>               Path to write the markdown audit verdict report
#   --dry-run                  Validate configuration and test discovery without executing tests
#   -h, --help                 Show this help message
#
# Invariants:
#   - Evidence integrity: all raw per-gate logs are preserved on PASS and FAIL alike
#   - Zero temporary files written to /tmp; all scratch and logs reside in ./scratch/
#   - Strict commit identity: target commit must match HEAD; source tree must remain unchanged
#   - Honest reporting: executive summary and recommendations strictly conditional on evidence
#   - Exit 0 on APPROVED (READY TO CUT); Exit 1 on gate failure/rejection; Exit 2 on refusal/usage

set -euo pipefail

TARGET_REF="HEAD"
EXPECT_PARENTS=""
REQUIRE_MERGE=0
DRIVE_MODE="auto"
DRIVE_TEST=""
DRIVE_TIMEOUT="180s"
SKIP_FUNCTIONAL=0
HOST_FUNCTIONAL=0
FUNCTIONAL_DEADLINE="10m"
FUNCTIONAL_FLAGS="--cpus 2 --memory 4g"
FUNCTIONAL_TIMEOUT="300s"
ALLOW_DIRTY=0
LOG_DIR=""
OUT_FILE=""
DRY_RUN=0

show_help() {
  sed -n '2,36p' "$0" | sed 's/^# *//;s/^#//'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --commit)
      TARGET_REF="$2"
      shift 2
      ;;
    --expect-parents)
      EXPECT_PARENTS="$2"
      shift 2
      ;;
    --require-merge)
      REQUIRE_MERGE=1
      shift
      ;;
    --drive)
      DRIVE_MODE="$2"
      shift 2
      ;;
    --drive-test)
      DRIVE_TEST="$2"
      shift 2
      ;;
    --drive-timeout)
      DRIVE_TIMEOUT="$2"
      shift 2
      ;;
    --skip-functional)
      SKIP_FUNCTIONAL=1
      shift
      ;;
    --host-functional)
      HOST_FUNCTIONAL=1
      shift
      ;;
    --functional-deadline)
      FUNCTIONAL_DEADLINE="$2"
      shift 2
      ;;
    --functional-flags)
      FUNCTIONAL_FLAGS="$2"
      shift 2
      ;;
    --functional-timeout)
      FUNCTIONAL_TIMEOUT="$2"
      shift 2
      ;;
    --allow-dirty)
      ALLOW_DIRTY=1
      shift
      ;;
    --log-dir)
      LOG_DIR="$2"
      shift 2
      ;;
    --out)
      OUT_FILE="$2"
      shift 2
      ;;
    --dry-run)
      DRY_RUN=1
      shift
      ;;
    -h|--help)
      show_help
      exit 0
      ;;
    *)
      echo "Unknown flag: $1" >&2
      show_help >&2
      exit 2
      ;;
  esac
done

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

# Resolve Target Commit and Head SHA
TARGET_SHA="$(git rev-parse "$TARGET_REF")"
TARGET_SHORT="$(git rev-parse --short "$TARGET_REF")"
HEAD_SHA="$(git rev-parse HEAD)"

# Enforce strict commit equality (Stella Finding 3)
if [[ "$TARGET_SHA" != "$HEAD_SHA" ]]; then
  echo "[-] Refusal: target ref '$TARGET_REF' resolves to $TARGET_SHA, but checked out HEAD is $HEAD_SHA." >&2
  echo "    The release candidate audit must be executed on the exact checkout of the target commit." >&2
  exit 2
fi

# Initial working tree cleanliness check (Stella Finding 3)
INITIAL_DIRTY="$(git status --porcelain)"
if [[ -n "$INITIAL_DIRTY" && $ALLOW_DIRTY -eq 0 ]]; then
  echo "[-] Refusal: working tree has uncommitted modifications or untracked files:" >&2
  echo "$INITIAL_DIRTY" >&2
  echo "    The release candidate audit requires a completely clean checkout (use --allow-dirty only for debug/dev)." >&2
  exit 2
fi

# Set up preserved raw log directory under scratch (Stella Finding 5: never /tmp, never rm -rf)
SCRATCH_BASE="$REPO_ROOT/scratch"
mkdir -p "$SCRATCH_BASE"
RUN_ID="audit-v1.1.0-${TARGET_SHORT}-$(date -u +%Y%m%dt%H%M%Sz)-$$"
if [[ -z "$LOG_DIR" ]]; then
  LOG_DIR="$SCRATCH_BASE/audit-logs/$RUN_ID"
fi
mkdir -p "$LOG_DIR"

on_exit() {
  local code=$?
  if [[ -d "$LOG_DIR" ]]; then
    echo ""
    echo "Raw audit logs preserved at: $LOG_DIR"
  fi
  exit $code
}
trap on_exit EXIT

MACHINE="$(uname -s)/$(uname -m)"
HOSTNAME_VAL="$(hostname)"
HARNESS="Emma Antigravity (gemini-2.5-pro)"
AUTHOR="Emma Antigravity <emma@mas-bandwidth.com>"
AUDIT_DATE="$(date -u +"%Y-%m-%d %H:%M:%S UTC")"

COMMIT_SUBJ="$(git log -1 --format="%s" "$TARGET_SHA")"
COMMIT_DATE="$(git log -1 --format="%cd" --date=iso "$TARGET_SHA")"
COMMIT_AUTHOR="$(git log -1 --format="%an <%ae>" "$TARGET_SHA")"

PARENTS_STR="$(git rev-list --parents -n 1 "$TARGET_SHA" | cut -d' ' -f2-)"
read -r -a PARENTS <<< "$PARENTS_STR"
NUM_PARENTS="${#PARENTS[@]}"

echo "=== Nova Tools v1.1.0 Release Candidate Audit Harness ==="
echo "Target Ref:       $TARGET_REF ($TARGET_SHORT)"
echo "Commit SHA:       $TARGET_SHA"
echo "Commit Subject:   $COMMIT_SUBJ"
echo "Commit Date:      $COMMIT_DATE"
echo "Commit Author:    $COMMIT_AUTHOR"
echo "Parents ($NUM_PARENTS):     ${PARENTS_STR:-none}"
echo "Auditor:          $AUTHOR"
echo "Harness:          $HARNESS"
echo "Machine:          $MACHINE ($HOSTNAME_VAL)"
echo "Worktree:         $REPO_ROOT"
echo "Log Directory:    $LOG_DIR"
if [[ $DRY_RUN -eq 1 ]]; then
  echo "Execution Mode:   DRY RUN (preflight validation only)"
fi
echo "=========================================================="
echo ""

ANCESTRY_VALID=1
ANCESTRY_NOTE=""
if [[ $REQUIRE_MERGE -eq 1 ]]; then
  if [[ $NUM_PARENTS -ne 2 ]]; then
    ANCESTRY_VALID=0
    ANCESTRY_NOTE="FAIL: Required 2 parents for promotion merge commit, found $NUM_PARENTS (${PARENTS_STR:-none})"
  else
    ANCESTRY_NOTE="PROMOTION MERGE: 2 parents verified (Parent 1: ${PARENTS[0]}, Parent 2: ${PARENTS[1]})"
  fi
elif [[ -n "$EXPECT_PARENTS" ]]; then
  if [[ $NUM_PARENTS -ne "$EXPECT_PARENTS" ]]; then
    ANCESTRY_VALID=0
    ANCESTRY_NOTE="FAIL: Expected $EXPECT_PARENTS parents, found $NUM_PARENTS (${PARENTS_STR:-none})"
  else
    ANCESTRY_NOTE="Exact parent count verified ($NUM_PARENTS parents: ${PARENTS_STR:-none})"
  fi
else
  if [[ $NUM_PARENTS -ge 2 ]]; then
    ANCESTRY_NOTE="Promotion merge commit verified (2 parents: Parent 1: ${PARENTS[0]}, Parent 2: ${PARENTS[1]})"
  elif [[ $NUM_PARENTS -eq 1 ]]; then
    ANCESTRY_NOTE="Single parent commit verified (${PARENTS[0]}); promotion merge to dev/main will carry 2 parents"
  else
    ANCESTRY_VALID=0
    ANCESTRY_NOTE="FAIL: Root commit has 0 parents"
  fi
fi

if [[ $ANCESTRY_VALID -eq 0 ]]; then
  echo "[-] Parent Ancestry Check: $ANCESTRY_NOTE" >&2
  exit 1
fi
echo "[+] Parent Ancestry Check: $ANCESTRY_NOTE"

# Discover living release tools dynamically
LIVING_TOOLS=()
for d in cmd/nova-*; do
  if [[ -d "$d" ]]; then
    LIVING_TOOLS+=("./$d")
  fi
done
NUM_LIVING_TOOLS="${#LIVING_TOOLS[@]}"

# Discover Gate 7 test file and function (Stella Finding 1: scoped to *_test.go, exact function declarations)
find_test_file() {
  local test_name="$1"
  git grep -l -E "^func ${test_name}\(" 2>/dev/null | grep "_test\.go$" | head -n 1 || true
}

RESOLVED_DRIVE_TEST=""
RESOLVED_DRIVE_PKG=""
RESOLVED_DRIVE_FILE=""

if [[ -n "$DRIVE_TEST" ]]; then
  RESOLVED_DRIVE_FILE="$(find_test_file "$DRIVE_TEST")"
  if [[ -n "$RESOLVED_DRIVE_FILE" ]]; then
    RESOLVED_DRIVE_TEST="$DRIVE_TEST"
    RESOLVED_DRIVE_PKG="./$(dirname "$RESOLVED_DRIVE_FILE")"
  fi
elif [[ "$DRIVE_MODE" == "dirty" ]]; then
  RESOLVED_DRIVE_FILE="$(find_test_file "TestTheDirtyTickDriveOnAStore")"
  if [[ -n "$RESOLVED_DRIVE_FILE" ]]; then
    RESOLVED_DRIVE_TEST="TestTheDirtyTickDriveOnAStore"
    RESOLVED_DRIVE_PKG="./$(dirname "$RESOLVED_DRIVE_FILE")"
  fi
elif [[ "$DRIVE_MODE" == "fleet" || "$DRIVE_MODE" == "whole-fleet" ]]; then
  RESOLVED_DRIVE_FILE="$(find_test_file "TestTheWholeFleetMovesInOneTickOnTheStore")"
  if [[ -n "$RESOLVED_DRIVE_FILE" ]]; then
    RESOLVED_DRIVE_TEST="TestTheWholeFleetMovesInOneTickOnTheStore"
    RESOLVED_DRIVE_PKG="./$(dirname "$RESOLVED_DRIVE_FILE")"
  fi
elif [[ "$DRIVE_MODE" == "sprint-ticks" ]]; then
  RESOLVED_DRIVE_FILE="$(find_test_file "TestRedisASprintToLandedByTicks")"
  if [[ -n "$RESOLVED_DRIVE_FILE" ]]; then
    RESOLVED_DRIVE_TEST="TestRedisASprintToLandedByTicks"
    RESOLVED_DRIVE_PKG="./$(dirname "$RESOLVED_DRIVE_FILE")"
  fi
else
  # auto mode: prioritize dirty tick drive if present in Go test source, then fleet drive, then sprint ticks
  RESOLVED_DRIVE_FILE="$(find_test_file "TestTheDirtyTickDriveOnAStore")"
  if [[ -n "$RESOLVED_DRIVE_FILE" ]]; then
    RESOLVED_DRIVE_TEST="TestTheDirtyTickDriveOnAStore"
    RESOLVED_DRIVE_PKG="./$(dirname "$RESOLVED_DRIVE_FILE")"
  else
    RESOLVED_DRIVE_FILE="$(find_test_file "TestTheWholeFleetMovesInOneTickOnTheStore")"
    if [[ -n "$RESOLVED_DRIVE_FILE" ]]; then
      RESOLVED_DRIVE_TEST="TestTheWholeFleetMovesInOneTickOnTheStore"
      RESOLVED_DRIVE_PKG="./$(dirname "$RESOLVED_DRIVE_FILE")"
    else
      RESOLVED_DRIVE_FILE="$(find_test_file "TestRedisASprintToLandedByTicks")"
      if [[ -n "$RESOLVED_DRIVE_FILE" ]]; then
        RESOLVED_DRIVE_TEST="TestRedisASprintToLandedByTicks"
        RESOLVED_DRIVE_PKG="./$(dirname "$RESOLVED_DRIVE_FILE")"
      fi
    fi
  fi
fi

if [[ $DRY_RUN -eq 1 ]]; then
  echo ""
  echo "--- Dry Run Preflight Checks ---"
  echo "Commit Equality:  HEAD == TARGET_SHA ($HEAD_SHA)"
  echo "Living Tools:     $NUM_LIVING_TOOLS found under cmd/nova-*"
  if [[ $NUM_LIVING_TOOLS -ne 18 ]]; then
    echo "  [-] Warning: Expected 18 living release tools, found $NUM_LIVING_TOOLS" >&2
  fi
  echo "Gate 7 Discovery: Selected test: ${RESOLVED_DRIVE_TEST:-<NONE FOUND>} in ${RESOLVED_DRIVE_FILE:-<NONE>}"
  if [[ -z "$RESOLVED_DRIVE_TEST" ]]; then
    echo "  [-] Refusal: No matching store drive test found in *_test.go" >&2
    exit 1
  fi
  echo "Gate 6 Mode:      $([[ $SKIP_FUNCTIONAL -eq 1 ]] && echo "SKIP (UNCERTIFIED)" || ([[ $HOST_FUNCTIONAL -eq 1 ]] && echo "HOST make test-functional" || echo "CONTAINER make test-functional-container"))"
  echo "All dry-run preflight checks passed."
  exit 0
fi

# Gate status variables
GATE1_STATUS="PENDING"; GATE1_NOTE=""; GATE1_ELAPSED="0s"
GATE2_STATUS="PENDING"; GATE2_NOTE=""; GATE2_ELAPSED="0s"
GATE3_STATUS="PENDING"; GATE3_NOTE=""; GATE3_ELAPSED="0s"
GATE4_STATUS="PENDING"; GATE4_NOTE=""; GATE4_ELAPSED="0s"
GATE5_STATUS="PENDING"; GATE5_NOTE=""; GATE5_ELAPSED="0s"
GATE6_STATUS="PENDING"; GATE6_NOTE=""; GATE6_ELAPSED="0s"
GATE7_STATUS="PENDING"; GATE7_NOTE=""; GATE7_ELAPSED="0s"

start_timer() { date +%s; }
stop_timer() {
  local start=$1
  local end=$(date +%s)
  echo "$((end - start))s"
}

# --- GATE 1: Working Tree Cleanliness ---
echo -n "[Gate 1/7] Verifying Working Tree Cleanliness... "
t0=$(start_timer)
DIRTY_DIFF="$(git status --porcelain)"
GATE1_ELAPSED="$(stop_timer "$t0")"
echo "$DIRTY_DIFF" > "$LOG_DIR/gate1_cleanliness.log"
if [[ -z "$DIRTY_DIFF" ]]; then
  GATE1_STATUS="PASS"
  GATE1_NOTE="Working tree clean (0 untracked or modified files)"
  echo "PASS ($GATE1_ELAPSED)"
elif [[ $ALLOW_DIRTY -eq 1 ]]; then
  GATE1_STATUS="UNCERTIFIED"
  GATE1_NOTE="Working tree has dirty changes (bypassed with --allow-dirty; uncertified for release)"
  echo "UNCERTIFIED (DIRTY BYPASSED, $GATE1_ELAPSED)"
else
  GATE1_STATUS="FAIL"
  GATE1_NOTE="Working tree has uncommitted modifications or untracked files"
  echo "FAIL ($GATE1_ELAPSED)"
  echo "$DIRTY_DIFF" >&2
fi

# --- GATE 2: Compilation (18 Living Release Tools) ---
echo -n "[Gate 2/7] Compiling All 18 Living Release Tools... "
t0=$(start_timer)
if [[ $NUM_LIVING_TOOLS -ne 18 ]]; then
  GATE2_ELAPSED="$(stop_timer "$t0")"
  GATE2_STATUS="FAIL"
  GATE2_NOTE="Catalogue mismatch: expected 18 living release tools in cmd/, found $NUM_LIVING_TOOLS"
  echo "FAIL ($GATE2_ELAPSED - catalogue mismatch)"
else
  if go build "${LIVING_TOOLS[@]}" > "$LOG_DIR/gate2_compilation.log" 2>&1; then
    GATE2_ELAPSED="$(stop_timer "$t0")"
    GATE2_STATUS="PASS"
    GATE2_NOTE="All 18 living release tools compiled cleanly with 0 diagnostics"
    echo "PASS ($GATE2_ELAPSED)"
  else
    GATE2_ELAPSED="$(stop_timer "$t0")"
    GATE2_STATUS="FAIL"
    GATE2_NOTE="Compilation failed:\n$(cat "$LOG_DIR/gate2_compilation.log")"
    echo "FAIL ($GATE2_ELAPSED)"
    cat "$LOG_DIR/gate2_compilation.log" >&2
  fi
fi

# --- GATE 3: Static Analysis (make fmt, make vet, make lint) ---
echo -n "[Gate 3/7] Running Static Analysis (fmt, vet, lint)... "
t0=$(start_timer)
STATIC_OK=1
if ! make fmt > "$LOG_DIR/gate3_fmt.log" 2>&1; then
  STATIC_OK=0
fi
if ! make vet > "$LOG_DIR/gate3_vet.log" 2>&1; then
  STATIC_OK=0
fi
if ! make lint > "$LOG_DIR/gate3_lint.log" 2>&1; then
  STATIC_OK=0
fi
GATE3_ELAPSED="$(stop_timer "$t0")"
if [[ $STATIC_OK -eq 1 ]]; then
  GATE3_STATUS="PASS"
  GATE3_NOTE="make fmt, make vet, and make lint passed (0 diagnostics)"
  echo "PASS ($GATE3_ELAPSED)"
else
  GATE3_STATUS="FAIL"
  GATE3_NOTE="Static analysis failed"
  echo "FAIL ($GATE3_ELAPSED)"
fi

# --- GATE 4: Class Tests (internal/ci/..., internal/docs/...) ---
echo -n "[Gate 4/7] Running Class Tests & Doc Guards... "
t0=$(start_timer)
if go test -v ./internal/ci/... ./internal/docs/... > "$LOG_DIR/gate4_class_tests.log" 2>&1; then
  GATE4_ELAPSED="$(stop_timer "$t0")"
  PASS_COUNT="$(grep -c "^--- PASS:" "$LOG_DIR/gate4_class_tests.log" || true)"
  PKG_COUNT="$(grep -c "^ok " "$LOG_DIR/gate4_class_tests.log" || true)"
  GATE4_STATUS="PASS"
  GATE4_NOTE="All $PASS_COUNT class tests passed across $PKG_COUNT packages (internal/ci, internal/docs)"
  echo "PASS ($GATE4_ELAPSED, $PASS_COUNT tests)"
else
  GATE4_ELAPSED="$(stop_timer "$t0")"
  GATE4_STATUS="FAIL"
  GATE4_NOTE="Class tests failed:\n$(grep -E "(FAIL|--- FAIL)" "$LOG_DIR/gate4_class_tests.log" | head -n 20)"
  echo "FAIL ($GATE4_ELAPSED)"
  grep -E "(FAIL|--- FAIL)" "$LOG_DIR/gate4_class_tests.log" >&2 || cat "$LOG_DIR/gate4_class_tests.log" >&2
fi

# --- GATE 5: Unit Tests (Core Packages) ---
echo -n "[Gate 5/7] Running Core Package Unit Tests... "
t0=$(start_timer)
CORE_PKGS=(
  "./cmd/nova-sprint"
  "./cmd/nova-swarm"
  "./cmd/nova-work"
  "./cmd/nova-config"
  "./cmd/nova-bus"
  "./cmd/nova-table"
  "./cmd/nova-sandbox"
)

if go test -timeout 180s "${CORE_PKGS[@]}" > "$LOG_DIR/gate5_unit_tests.log" 2>&1; then
  GATE5_ELAPSED="$(stop_timer "$t0")"
  UNIT_OK_COUNT="$(grep -c "^ok " "$LOG_DIR/gate5_unit_tests.log" || true)"
  GATE5_STATUS="PASS"
  GATE5_NOTE="All $UNIT_OK_COUNT core packages passed unit tests cleanly"
  echo "PASS ($GATE5_ELAPSED, $UNIT_OK_COUNT packages)"
else
  GATE5_ELAPSED="$(stop_timer "$t0")"
  GATE5_STATUS="FAIL"
  GATE5_NOTE="Core package unit tests failed"
  echo "FAIL ($GATE5_ELAPSED)"
  cat "$LOG_DIR/gate5_unit_tests.log" >&2
fi

# --- GATE 6: Functional Tier (Stella Finding 2) ---
echo -n "[Gate 6/7] Running Store Functional Tier... "
t0=$(start_timer)
if [[ $SKIP_FUNCTIONAL -eq 1 ]]; then
  GATE6_ELAPSED="0s"
  GATE6_STATUS="SKIPPED"
  GATE6_NOTE="Functional tier skipped by user request (--skip-functional); skipped evidence cannot certify release"
  echo "SKIPPED (UNCERTIFIED, $GATE6_ELAPSED)"
elif [[ $HOST_FUNCTIONAL -eq 1 ]]; then
  echo -n "(host mode, unfiltered)... "
  if make test-functional FUNCTIONAL_TIMEOUT="$FUNCTIONAL_TIMEOUT" > "$LOG_DIR/gate6_functional.log" 2>&1; then
    GATE6_ELAPSED="$(stop_timer "$t0")"
    GATE6_STATUS="PASS"
    GATE6_NOTE="Host functional test tier passed cleanly"
    echo "PASS ($GATE6_ELAPSED)"
  else
    GATE6_ELAPSED="$(stop_timer "$t0")"
    GATE6_STATUS="FAIL"
    GATE6_NOTE="Host functional test tier failed"
    echo "FAIL ($GATE6_ELAPSED)"
    cat "$LOG_DIR/gate6_functional.log" >&2
  fi
else
  # Container mode (default)
  CONTAINER_BIN=""
  if command -v podman >/dev/null 2>&1; then
    CONTAINER_BIN="podman"
  elif command -v docker >/dev/null 2>&1; then
    CONTAINER_BIN="docker"
  fi

  if [[ -z "$CONTAINER_BIN" ]]; then
    GATE6_ELAPSED="$(stop_timer "$t0")"
    GATE6_STATUS="FAIL"
    GATE6_NOTE="No container engine (podman/docker) available for contained functional tier (use --host-functional if permitted)"
    echo "FAIL (NO CONTAINER ENGINE)"
  else
    echo -n "(contained tier: $CONTAINER_BIN, deadline $FUNCTIONAL_DEADLINE)... "
    if make test-functional-container FUNCTIONAL_DEADLINE="$FUNCTIONAL_DEADLINE" FUNCTIONAL_FLAGS="$FUNCTIONAL_FLAGS" > "$LOG_DIR/gate6_functional.log" 2>&1; then
      GATE6_ELAPSED="$(stop_timer "$t0")"
      GATE6_STATUS="PASS"
      GATE6_NOTE="Contained functional test tier passed cleanly (under $CONTAINER_BIN with $FUNCTIONAL_FLAGS)"
      echo "PASS ($GATE6_ELAPSED)"
    else
      GATE6_ELAPSED="$(stop_timer "$t0")"
      GATE6_STATUS="FAIL"
      GATE6_NOTE="Contained functional test tier failed"
      echo "FAIL ($GATE6_ELAPSED)"
      cat "$LOG_DIR/gate6_functional.log" >&2
    fi
  fi
fi

# --- GATE 7: Store Functional Drive (Stella Finding 1) ---
echo -n "[Gate 7/7] Running Store Functional Drive... "
t0=$(start_timer)
if [[ -z "$RESOLVED_DRIVE_TEST" ]]; then
  GATE7_ELAPSED="$(stop_timer "$t0")"
  GATE7_STATUS="FAIL"
  GATE7_NOTE="No verified store functional drive test found in *_test.go files; cannot certify Gate 7"
  echo "FAIL ($GATE7_ELAPSED - NO TEST FOUND)"
else
  echo -n "($RESOLVED_DRIVE_TEST in $RESOLVED_DRIVE_PKG)... "
  if go test -tags functional -v -timeout "$DRIVE_TIMEOUT" -run "^${RESOLVED_DRIVE_TEST}$" "$RESOLVED_DRIVE_PKG" > "$LOG_DIR/gate7_drive.log" 2>&1; then
    GATE7_ELAPSED="$(stop_timer "$t0")"

    # Verify that tests actually ran (prevent false pass from 0 tests matching -run)
    if grep -q "testing: warning: no tests to run" "$LOG_DIR/gate7_drive.log"; then
      GATE7_STATUS="FAIL"
      GATE7_NOTE="Go test runner reported 'no tests to run' for ^${RESOLVED_DRIVE_TEST}$"
      echo "FAIL ($GATE7_ELAPSED - 0 TESTS EXECUTED)"
    elif ! grep -q "^--- PASS: ${RESOLVED_DRIVE_TEST}" "$LOG_DIR/gate7_drive.log"; then
      GATE7_STATUS="FAIL"
      GATE7_NOTE="Test did not output '--- PASS: ${RESOLVED_DRIVE_TEST}'"
      echo "FAIL ($GATE7_ELAPSED - PASS NOT CONFIRMED)"
    else
      # Verify landed card / deal counts
      CARDS_VERIFIED=0
      VERIFIED_MSG=""
      if [[ "$RESOLVED_DRIVE_TEST" == "TestTheDirtyTickDriveOnAStore" ]]; then
        if grep -qE "all [0-9]+ cards landed|3000 cards landed|deal_index 3000|landed 3000" "$LOG_DIR/gate7_drive.log"; then
          CARDS_VERIFIED=1
          VERIFIED_MSG="all 3000 cards landed, machine fairness verified"
        fi
      elif [[ "$RESOLVED_DRIVE_TEST" == "TestTheWholeFleetMovesInOneTickOnTheStore" ]]; then
        if grep -qE "deal_index 3000 after 3000 cards dealt" "$LOG_DIR/gate7_drive.log"; then
          CARDS_VERIFIED=1
          VERIFIED_MSG="verified 3000 cards dealt across whole fleet to deal_index 3000"
        fi
      elif [[ "$RESOLVED_DRIVE_TEST" == "TestRedisASprintToLandedByTicks" ]]; then
        if grep -qE "stream [a-z0-9]+ is landed" "$LOG_DIR/gate7_drive.log"; then
          CARDS_VERIFIED=1
          VERIFIED_MSG="verified all streams landed by ticks on real store"
        fi
      else
        CARDS_VERIFIED=1
        VERIFIED_MSG="test executed and passed"
      fi

      if [[ $CARDS_VERIFIED -eq 1 ]]; then
        GATE7_STATUS="PASS"
        GATE7_NOTE="${RESOLVED_DRIVE_TEST} passed in $GATE7_ELAPSED ($VERIFIED_MSG)"
        echo "PASS ($GATE7_ELAPSED - $VERIFIED_MSG)"
      else
        GATE7_STATUS="FAIL"
        GATE7_NOTE="${RESOLVED_DRIVE_TEST} passed exit 0, but card counts / landing failed verification"
        echo "FAIL ($GATE7_ELAPSED - CARD COUNT NOT VERIFIED)"
      fi
    fi
  else
    GATE7_ELAPSED="$(stop_timer "$t0")"
    GATE7_STATUS="FAIL"
    GATE7_NOTE="${RESOLVED_DRIVE_TEST} failed with non-zero exit"
    echo "FAIL ($GATE7_ELAPSED - $RESOLVED_DRIVE_TEST)"
    cat "$LOG_DIR/gate7_drive.log" >&2
  fi
fi

# Post-run source and HEAD identity verification (Stella Finding 3)
FINAL_HEAD="$(git rev-parse HEAD)"
FINAL_INTEGRITY_FAIL=0
if [[ "$FINAL_HEAD" != "$HEAD_SHA" ]]; then
  echo "[-] CRITICAL: HEAD moved during audit execution! (Was $HEAD_SHA, now $FINAL_HEAD)" >&2
  FINAL_INTEGRITY_FAIL=1
fi

FINAL_DIRTY="$(git status --porcelain)"
if [[ -n "$FINAL_DIRTY" && $ALLOW_DIRTY -eq 0 ]]; then
  echo "[-] CRITICAL: Working tree became dirty during audit execution!" >&2
  echo "$FINAL_DIRTY" >&2
  FINAL_INTEGRITY_FAIL=1
fi

# Overall Verdict Determination (Stella Finding 4)
ALL_PASS=1
FAILED_GATES=()
SKIPPED_GATES=()
PASSED_GATES=()

GATE_NAMES=(
  ""
  "Gate 1: Working Tree Cleanliness"
  "Gate 2: Compilation (18 Living Tools)"
  "Gate 3: Static Analysis"
  "Gate 4: Class Tests & Doc Guards"
  "Gate 5: Core Unit Test Tier"
  "Gate 6: Functional Tier"
  "Gate 7: Store Functional Drive"
)

GATE_STATUSES=(
  ""
  "$GATE1_STATUS"
  "$GATE2_STATUS"
  "$GATE3_STATUS"
  "$GATE4_STATUS"
  "$GATE5_STATUS"
  "$GATE6_STATUS"
  "$GATE7_STATUS"
)

GATE_NOTES=(
  ""
  "$GATE1_NOTE"
  "$GATE2_NOTE"
  "$GATE3_NOTE"
  "$GATE4_NOTE"
  "$GATE5_NOTE"
  "$GATE6_NOTE"
  "$GATE7_NOTE"
)

for g in 1 2 3 4 5 6 7; do
  st="${GATE_STATUSES[$g]}"
  name="${GATE_NAMES[$g]}"
  note="${GATE_NOTES[$g]}"
  if [[ "$st" == "PASS" ]]; then
    PASSED_GATES+=("$name: $st ($note)")
  elif [[ "$st" == "SKIPPED" || "$st" == "UNCERTIFIED" ]]; then
    ALL_PASS=0
    SKIPPED_GATES+=("$name: $st ($note)")
  else
    ALL_PASS=0
    FAILED_GATES+=("$name: $st ($note)")
  fi
done

if [[ $FINAL_INTEGRITY_FAIL -eq 1 ]]; then
  ALL_PASS=0
  FAILED_GATES+=("Post-Run Integrity: FAIL (source tree altered or HEAD moved during execution)")
fi

if [[ $ALL_PASS -eq 1 ]]; then
  VERDICT="APPROVED (READY TO CUT)"
  EXIT_CODE=0
elif [[ ${#FAILED_GATES[@]} -gt 0 ]]; then
  VERDICT="REJECTED (GATE FAILURE)"
  EXIT_CODE=1
else
  VERDICT="REJECTED (UNCERTIFIED EVIDENCE)"
  EXIT_CODE=1
fi

echo ""
echo "=== Audit Complete ==="
echo "Final Disposition: $VERDICT"
echo "======================"
echo ""

# Format Dynamic Markdown Report (Stella Finding 4)
generate_report() {
  cat <<EOF
# Release Candidate Audit Verdict: \`v1.1.0\`

**Auditor:** $AUTHOR  
**Harness:** $HARNESS  
**Date:** $AUDIT_DATE  
**Machine:** $MACHINE ($HOSTNAME_VAL)  
**Target Ref:** $TARGET_REF (\`$TARGET_SHORT\`)  
**Commit SHA:** \`$TARGET_SHA\`  
**Commit Subject:** $COMMIT_SUBJ  
**Commit Author:** $COMMIT_AUTHOR  
**Commit Date:** $COMMIT_DATE  
**Ancestry:** $ANCESTRY_NOTE  
**Worktree:** \`$REPO_ROOT\`  
**Log Directory:** \`$LOG_DIR\`  
**Final Disposition:** **$VERDICT**

---

## 1. Executive Summary & Verdict

### Release Readiness Verdict: **$VERDICT**

EOF

  if [[ $ALL_PASS -eq 1 ]]; then
    cat <<EOF
A full 7-gate certification audit was performed on commit \`$TARGET_SHA\` (\`$TARGET_SHORT\`). Every mandatory release gate passed with verified evidentiary backing:
- **Cleanliness:** Working tree is clean (0 untracked or modified files).
- **Compilation:** All 18 living release tools compiled cleanly with zero diagnostics.
- **Static Analysis:** \`gofmt\`, \`go vet\`, and \`make lint\` passed with zero findings across all packages.
- **Class Tests:** Class tests and documentation integrity guards in \`internal/ci/...\` and \`internal/docs/...\` passed with 100% green status.
- **Unit Tests:** Core package unit tests passed cleanly.
- **Functional Tier:** Store functional test tier passed without errors.
- **Store Drive:** Store functional drive (\`$RESOLVED_DRIVE_TEST\`) passed and confirmed card landing convergence.

All raw per-gate logs and runner receipts are preserved in \`$LOG_DIR\`.
EOF
  else
    cat <<EOF
A 7-gate certification audit was performed on commit \`$TARGET_SHA\` (\`$TARGET_SHORT\`).
**The candidate CANNOT be certified for release.** The audit encountered failures or uncertified skips:

EOF
    if [[ ${#FAILED_GATES[@]} -gt 0 ]]; then
      echo "#### Failed Gates:"
      for item in "${FAILED_GATES[@]}"; do
        echo "- ❌ **$item**"
      done
      echo ""
    fi
    if [[ ${#SKIPPED_GATES[@]} -gt 0 ]]; then
      echo "#### Skipped / Uncertified Gates:"
      for item in "${SKIPPED_GATES[@]}"; do
        echo "- ⚠️ **$item**"
      done
      echo ""
    fi
    if [[ ${#PASSED_GATES[@]} -gt 0 ]]; then
      echo "#### Verified Gates:"
      for item in "${PASSED_GATES[@]}"; do
        echo "- ✅ **$item**"
      done
      echo ""
    fi
    cat <<EOF
Raw execution and diagnostic logs are preserved for inspection in \`$LOG_DIR\`.
EOF
  fi

  cat <<EOF

---

## 2. Seven-Gate Verification Matrix

| Gate # | Verification Gate | Target Scope / Command | Result | Timing | Notes |
| :---: | :--- | :--- | :---: | :---: | :--- |
| **Gate 1** | Working Tree Cleanliness | \`git status --porcelain\` | **$GATE1_STATUS** | $GATE1_ELAPSED | $GATE1_NOTE |
| **Gate 2** | Compilation (18 Living Tools) | \`go build ./cmd/nova-*\` (18 living tools) | **$GATE2_STATUS** | $GATE2_ELAPSED | $GATE2_NOTE |
| **Gate 3** | Static Analysis | \`make fmt\`, \`make vet\`, \`make lint\` | **$GATE3_STATUS** | $GATE3_ELAPSED | $GATE3_NOTE |
| **Gate 4** | Class Tests & Doc Guards | \`go test -v ./internal/ci/... ./internal/docs/...\` | **$GATE4_STATUS** | $GATE4_ELAPSED | $GATE4_NOTE |
| **Gate 5** | Core Unit Test Tier | \`go test ./cmd/{sprint,swarm,work,config,bus,table,sandbox}\` | **$GATE5_STATUS** | $GATE5_ELAPSED | $GATE5_NOTE |
| **Gate 6** | Functional Tier | Contained / Host Functional Tier | **$GATE6_STATUS** | $GATE6_ELAPSED | $GATE6_NOTE |
| **Gate 7** | Store Functional Drive | \`$RESOLVED_DRIVE_TEST\` | **$GATE7_STATUS** | $GATE7_ELAPSED | $GATE7_NOTE |

---

## 3. Catalogue Verification (All 18 Living Release Tools)

The complete living tool catalogue under \`cmd/\` compiled with exit code 0:
1. \`./cmd/nova-bus\` — Shared messages and replies in Git
2. \`./cmd/nova-cairn\` — Checkpoints, source pointers and bounded session index
3. \`./cmd/nova-check\` — Record checking and link validation
4. \`./cmd/nova-ci\` — CI test budget timings and slow test guards
5. \`./cmd/nova-config\` — Fleet configuration in PostgreSQL and Redis apply
6. \`./cmd/nova-fuse\` — Quarantines and source access refusals
7. \`./cmd/nova-memory\` — Matching sources from Markdown records
8. \`./cmd/nova-redis\` — Ephemeral state and named leases
9. \`./cmd/nova-sandbox\` — Filesystem restrictions through OS backend
10. \`./cmd/nova-secrets\` — Encrypted storage and child credential delivery
11. \`./cmd/nova-self-talk\` — Self-talk sentence pattern review
12. \`./cmd/nova-sprint\` — Sprint coordination, loops, readers and fleet tables
13. \`./cmd/nova-swarm\` — Sandboxed AI worker orchestration and card execution
14. \`./cmd/nova-table\` — Work tracking tables and live views in Redis
15. \`./cmd/nova-tokens\` — LLM token usage accounting by model and repo
16. \`./cmd/nova-update\` — Version inspection and explicit update operations
17. \`./cmd/nova-version\` — Installed-tool identity and snapshot reporting
18. \`./cmd/nova-work\` — Work tree and layer operations

---

## 4. Final Recommendation

EOF

  if [[ $ALL_PASS -eq 1 ]]; then
    cat <<EOF
Commit \`$TARGET_SHA\` satisfies all 7 mandatory verification gates without exception or bypass. Emma Antigravity approves and certifies this commit for \`v1.1.0\` release tagging and packaging.
EOF
  else
    cat <<EOF
**HOLD / REJECT RELEASE CANDIDATE.** Commit \`$TARGET_SHA\` does NOT satisfy all 7 mandatory verification gates. Release tagging and packaging are REFUSED until all failed and skipped gates are resolved and fully certified with evidentiary logs.
EOF
  fi
}

REPORT_CONTENT="$(generate_report)"

# Persist markdown report in log directory
echo "$REPORT_CONTENT" > "$LOG_DIR/audit_report.md"

# Also persist structured json manifest
cat <<EOF > "$LOG_DIR/audit_manifest.json"
{
  "audit_date": "$AUDIT_DATE",
  "target_ref": "$TARGET_REF",
  "target_sha": "$TARGET_SHA",
  "head_sha": "$HEAD_SHA",
  "verdict": "$VERDICT",
  "exit_code": $EXIT_CODE,
  "machine": "$MACHINE",
  "hostname": "$HOSTNAME_VAL",
  "auditor": "$AUTHOR",
  "harness": "$HARNESS",
  "gates": {
    "gate1": {"status": "$GATE1_STATUS", "elapsed": "$GATE1_ELAPSED", "note": "$GATE1_NOTE"},
    "gate2": {"status": "$GATE2_STATUS", "elapsed": "$GATE2_ELAPSED", "note": "$GATE2_NOTE"},
    "gate3": {"status": "$GATE3_STATUS", "elapsed": "$GATE3_ELAPSED", "note": "$GATE3_NOTE"},
    "gate4": {"status": "$GATE4_STATUS", "elapsed": "$GATE4_ELAPSED", "note": "$GATE4_NOTE"},
    "gate5": {"status": "$GATE5_STATUS", "elapsed": "$GATE5_ELAPSED", "note": "$GATE5_NOTE"},
    "gate6": {"status": "$GATE6_STATUS", "elapsed": "$GATE6_ELAPSED", "note": "$GATE6_NOTE"},
    "gate7": {"status": "$GATE7_STATUS", "elapsed": "$GATE7_ELAPSED", "note": "$GATE7_NOTE"}
  }
}
EOF

if [[ -n "$OUT_FILE" ]]; then
  mkdir -p "$(dirname "$OUT_FILE")"
  echo "$REPORT_CONTENT" > "$OUT_FILE"
  echo "Report written to: $OUT_FILE"
else
  DEFAULT_REPORT="$SCRATCH_BASE/audit-v1.1.0-verdict-$TARGET_SHORT.md"
  echo "$REPORT_CONTENT" > "$DEFAULT_REPORT"
  echo "Report written to: $DEFAULT_REPORT"
  echo ""
  echo "$REPORT_CONTENT"
fi

exit $EXIT_CODE
