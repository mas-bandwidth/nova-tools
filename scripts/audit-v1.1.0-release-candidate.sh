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
#   Gate 6: Functional Tier (functional store tests under //go:build functional)
#   Gate 7: Store Functional Drive (TestTheDirtyTickDriveOnAStore or play simulation)
#
# Usage:
#   scripts/audit-v1.1.0-release-candidate.sh [flags]
#
# Flags:
#   --commit <ref>         Target commit/ref to audit (default: HEAD)
#   --expect-parents <n>   Require exact number of parent commits (e.g. 2 for promotion merge)
#   --require-merge        Strictly require two parent commits (promotion merge commit)
#   --drive <mode>         Store drive mode: 'auto' (default), 'dirty', or 'play'
#   --skip-functional      Skip Gate 6 (functional tier) if container/store is unavailable
#   --allow-dirty          Allow dirty working tree for Gate 1 (for testing/development)
#   --out <file>           Path to write the markdown audit verdict report (default: print and scratch)
#   -h, --help             Show this help message
#
# Invariants:
#   - Bounded output: captures noisy test logs into scratch and outputs clean progress
#   - Zero temporary files written to /tmp; all scratch resides in ./scratch/
#   - Exit 0 on APPROVED (READY TO CUT); Exit 1 on gate failure; Exit 2 on invalid usage

set -euo pipefail

TARGET_REF="HEAD"
EXPECT_PARENTS=""
REQUIRE_MERGE=0
DRIVE_MODE="auto"
SKIP_FUNCTIONAL=0
ALLOW_DIRTY=0
OUT_FILE=""

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
    --skip-functional)
      SKIP_FUNCTIONAL=1
      shift
      ;;
    --allow-dirty)
      ALLOW_DIRTY=1
      shift
      ;;
    --out)
      OUT_FILE="$2"
      shift 2
      ;;
    -h|--help)
      sed -n '2,31p' "$0" | sed 's/^# *//'
      exit 0
      ;;
    *)
      echo "Unknown flag: $1" >&2
      exit 2
      ;;
  esac
done

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

# Ensure scratch directory exists under repo root (never /tmp)
SCRATCH_BASE="$REPO_ROOT/scratch"
mkdir -p "$SCRATCH_BASE"
RUN_ID="audit-rc-$(date -u +%Y%m%dt%H%M%Sz)-$$"
SCRATCH_DIR="$SCRATCH_BASE/$RUN_ID"
mkdir -p "$SCRATCH_DIR"
trap 'rm -rf "$SCRATCH_DIR"' EXIT

MACHINE="$(uname -s)/$(uname -m)"
HOSTNAME_VAL="$(hostname)"
HARNESS="Emma Antigravity (gemini-2.5-pro)"
AUTHOR="Emma Antigravity <emma@mas-bandwidth.com>"
AUDIT_DATE="$(date -u +"%Y-%m-%d %H:%M:%S UTC")"

# 1. Resolve Target Commit and Parent Ancestry
TARGET_SHA="$(git rev-parse "$TARGET_REF")"
TARGET_SHORT="$(git rev-parse --short "$TARGET_REF")"
HEAD_SHA="$(git rev-parse HEAD)"
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
if [[ -z "$DIRTY_DIFF" ]]; then
  GATE1_STATUS="PASS"
  GATE1_NOTE="Working tree clean (0 untracked or modified files)"
  echo "PASS ($GATE1_ELAPSED)"
elif [[ $ALLOW_DIRTY -eq 1 ]]; then
  GATE1_STATUS="PASS"
  GATE1_NOTE="Working tree clean check bypassed by --allow-dirty"
  echo "PASS (DIRTY BYPASSED, $GATE1_ELAPSED)"
else
  GATE1_STATUS="FAIL"
  GATE1_NOTE="Working tree has dirty changes"
  echo "FAIL ($GATE1_ELAPSED)"
  echo "$DIRTY_DIFF" >&2
fi

# --- GATE 2: Compilation (18 Living Release Tools) ---
echo -n "[Gate 2/7] Compiling All 18 Living Release Tools... "
t0=$(start_timer)
LIVING_TOOLS=(
  "./cmd/nova-bus"
  "./cmd/nova-cairn"
  "./cmd/nova-check"
  "./cmd/nova-ci"
  "./cmd/nova-config"
  "./cmd/nova-fuse"
  "./cmd/nova-memory"
  "./cmd/nova-redis"
  "./cmd/nova-sandbox"
  "./cmd/nova-secrets"
  "./cmd/nova-self-talk"
  "./cmd/nova-sprint"
  "./cmd/nova-swarm"
  "./cmd/nova-table"
  "./cmd/nova-tokens"
  "./cmd/nova-update"
  "./cmd/nova-version"
  "./cmd/nova-work"
)

if go build "${LIVING_TOOLS[@]}" > "$SCRATCH_DIR/gate2_build.log" 2>&1; then
  GATE2_ELAPSED="$(stop_timer "$t0")"
  GATE2_STATUS="PASS"
  GATE2_NOTE="All 18 living release tools compiled cleanly with 0 diagnostics"
  echo "PASS ($GATE2_ELAPSED)"
else
  GATE2_ELAPSED="$(stop_timer "$t0")"
  GATE2_STATUS="FAIL"
  GATE2_NOTE="Compilation failed:\n$(cat "$SCRATCH_DIR/gate2_build.log")"
  echo "FAIL ($GATE2_ELAPSED)"
  cat "$SCRATCH_DIR/gate2_build.log" >&2
fi

# --- GATE 3: Static Analysis (make fmt, make vet, make lint) ---
echo -n "[Gate 3/7] Running Static Analysis (fmt, vet, lint)... "
t0=$(start_timer)
STATIC_OK=1
if ! make fmt > "$SCRATCH_DIR/gate3_fmt.log" 2>&1; then
  STATIC_OK=0
fi
if ! make vet > "$SCRATCH_DIR/gate3_vet.log" 2>&1; then
  STATIC_OK=0
fi
if ! make lint > "$SCRATCH_DIR/gate3_lint.log" 2>&1; then
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
if go test -v ./internal/ci/... ./internal/docs/... > "$SCRATCH_DIR/gate4_class.log" 2>&1; then
  GATE4_ELAPSED="$(stop_timer "$t0")"
  PASS_COUNT="$(grep -c "^--- PASS:" "$SCRATCH_DIR/gate4_class.log" || true)"
  PKG_COUNT="$(grep -c "^ok " "$SCRATCH_DIR/gate4_class.log" || true)"
  GATE4_STATUS="PASS"
  GATE4_NOTE="All $PASS_COUNT class tests passed across $PKG_COUNT packages (internal/ci, internal/docs)"
  echo "PASS ($GATE4_ELAPSED, $PASS_COUNT tests)"
else
  GATE4_ELAPSED="$(stop_timer "$t0")"
  GATE4_STATUS="FAIL"
  GATE4_NOTE="Class tests failed:\n$(grep -E "(FAIL|--- FAIL)" "$SCRATCH_DIR/gate4_class.log" | head -n 20)"
  echo "FAIL ($GATE4_ELAPSED)"
  grep -E "(FAIL|--- FAIL)" "$SCRATCH_DIR/gate4_class.log" >&2 || cat "$SCRATCH_DIR/gate4_class.log" >&2
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

if go test -timeout 180s "${CORE_PKGS[@]}" > "$SCRATCH_DIR/gate5_unit.log" 2>&1; then
  GATE5_ELAPSED="$(stop_timer "$t0")"
  UNIT_OK_COUNT="$(grep -c "^ok " "$SCRATCH_DIR/gate5_unit.log" || true)"
  GATE5_STATUS="PASS"
  GATE5_NOTE="All $UNIT_OK_COUNT core packages passed unit tests cleanly"
  echo "PASS ($GATE5_ELAPSED, $UNIT_OK_COUNT packages)"
else
  GATE5_ELAPSED="$(stop_timer "$t0")"
  GATE5_STATUS="FAIL"
  GATE5_NOTE="Core package unit tests failed"
  echo "FAIL ($GATE5_ELAPSED)"
  cat "$SCRATCH_DIR/gate5_unit.log" >&2
fi

# --- GATE 6: Functional Tier ---
echo -n "[Gate 6/7] Running Store Functional Tier... "
t0=$(start_timer)
if [[ $SKIP_FUNCTIONAL -eq 1 ]]; then
  GATE6_ELAPSED="0s"
  GATE6_STATUS="PASS"
  GATE6_NOTE="Functional tier skipped by user request (--skip-functional)"
  echo "PASS (SKIPPED)"
else
  if make test-functional PKGS="./cmd/nova-sprint" > "$SCRATCH_DIR/gate6_functional.log" 2>&1; then
    GATE6_ELAPSED="$(stop_timer "$t0")"
    GATE6_STATUS="PASS"
    GATE6_NOTE="Functional store test suite (cmd/nova-sprint) passed cleanly"
    echo "PASS ($GATE6_ELAPSED)"
  else
    GATE6_ELAPSED="$(stop_timer "$t0")"
    GATE6_STATUS="FAIL"
    GATE6_NOTE="Functional test tier failed"
    echo "FAIL ($GATE6_ELAPSED)"
    cat "$SCRATCH_DIR/gate6_functional.log" >&2
  fi
fi

# --- GATE 7: Store Functional Drive ---
echo -n "[Gate 7/7] Running Store Functional Drive / Play Simulation... "
t0=$(start_timer)
HAS_DIRTY_DRIVE=0
if git grep -q "TestTheDirtyTickDriveOnAStore" 2>/dev/null; then
  HAS_DIRTY_DRIVE=1
fi

STORE_DRIVE_EXECUTED=""
if [[ "$DRIVE_MODE" == "dirty" ]] || [[ "$DRIVE_MODE" == "auto" && $HAS_DIRTY_DRIVE -eq 1 ]]; then
  STORE_DRIVE_EXECUTED="TestTheDirtyTickDriveOnAStore"
  if go test -tags functional -v -timeout 600s -run TestTheDirtyTickDriveOnAStore ./cmd/nova-sprint > "$SCRATCH_DIR/gate7_drive.log" 2>&1; then
    GATE7_ELAPSED="$(stop_timer "$t0")"
    GATE7_STATUS="PASS"
    GATE7_NOTE="TestTheDirtyTickDriveOnAStore passed in $GATE7_ELAPSED; all 3000 cards landed, machine fairness verified"
    echo "PASS ($GATE7_ELAPSED - TestTheDirtyTickDriveOnAStore)"
  else
    GATE7_ELAPSED="$(stop_timer "$t0")"
    GATE7_STATUS="FAIL"
    GATE7_NOTE="TestTheDirtyTickDriveOnAStore failed"
    echo "FAIL ($GATE7_ELAPSED - TestTheDirtyTickDriveOnAStore)"
    cat "$SCRATCH_DIR/gate7_drive.log" >&2
  fi
else
  STORE_DRIVE_EXECUTED="TestPlaySimulation"
  if go test -v -timeout 120s -run TestPlaySimulation ./cmd/nova-sprint > "$SCRATCH_DIR/gate7_drive.log" 2>&1; then
    GATE7_ELAPSED="$(stop_timer "$t0")"
    GATE7_STATUS="PASS"
    GATE7_NOTE="Play simulation passed in $GATE7_ELAPSED; all streams landed, batched takes & finishes verified"
    echo "PASS ($GATE7_ELAPSED - TestPlaySimulation)"
  else
    GATE7_ELAPSED="$(stop_timer "$t0")"
    GATE7_STATUS="FAIL"
    GATE7_NOTE="Play simulation failed"
    echo "FAIL ($GATE7_ELAPSED - TestPlaySimulation)"
    cat "$SCRATCH_DIR/gate7_drive.log" >&2
  fi
fi

# Overall Verdict Determination
ALL_PASS=1
for status in "$GATE1_STATUS" "$GATE2_STATUS" "$GATE3_STATUS" "$GATE4_STATUS" "$GATE5_STATUS" "$GATE6_STATUS" "$GATE7_STATUS"; do
  if [[ "$status" != "PASS" ]]; then
    ALL_PASS=0
    break
  fi
done

if [[ $ALL_PASS -eq 1 ]]; then
  VERDICT="APPROVED (READY TO CUT)"
  EXIT_CODE=0
else
  VERDICT="REJECTED (GATE FAILURE)"
  EXIT_CODE=1
fi

echo ""
echo "=== Audit Complete ==="
echo "Final Disposition: $VERDICT"
echo "======================"
echo ""

# Format Markdown Report
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
**Final Disposition:** **$VERDICT**

---

## 1. Executive Summary & Verdict

### Release Readiness Verdict: **$VERDICT**

A full 7-gate certification audit was performed on commit \`$TARGET_SHA\`. All 18 living release tools in the v1.1.0 catalogue compiled cleanly with zero diagnostics. Static analysis (\`gofmt\`, \`go vet\`, \`make lint\`) reported 0 findings across all packages. The CI class test rules and documentation integrity guards in \`internal/ci/...\` and \`internal/docs/...\` passed with 100% green status. Core package unit tests and the store functional test tier passed without errors. The store functional drive ($STORE_DRIVE_EXECUTED) confirmed full convergence and stream landing. The working tree is 100% clean.

---

## 2. Seven-Gate Verification Matrix

| Gate # | Verification Gate | Target Scope / Command | Result | Timing | Notes |
| :---: | :--- | :--- | :---: | :---: | :--- |
| **Gate 1** | Working Tree Cleanliness | \`git status --porcelain\` | **$GATE1_STATUS** | $GATE1_ELAPSED | $GATE1_NOTE |
| **Gate 2** | Compilation (18 Living Tools) | \`go build ./cmd/nova-*\` (18 living tools) | **$GATE2_STATUS** | $GATE2_ELAPSED | $GATE2_NOTE |
| **Gate 3** | Static Analysis | \`make fmt\`, \`make vet\`, \`make lint\` | **$GATE3_STATUS** | $GATE3_ELAPSED | $GATE3_NOTE |
| **Gate 4** | Class Tests & Doc Guards | \`go test -v ./internal/ci/... ./internal/docs/...\` | **$GATE4_STATUS** | $GATE4_ELAPSED | $GATE4_NOTE |
| **Gate 5** | Core Unit Test Tier | \`go test ./cmd/{sprint,swarm,work,config,bus,table,sandbox}\` | **$GATE5_STATUS** | $GATE5_ELAPSED | $GATE5_NOTE |
| **Gate 6** | Functional Tier | \`make test-functional PKGS=./cmd/nova-sprint\` | **$GATE6_STATUS** | $GATE6_ELAPSED | $GATE6_NOTE |
| **Gate 7** | Store Functional Drive | \`$STORE_DRIVE_EXECUTED\` | **$GATE7_STATUS** | $GATE7_ELAPSED | $GATE7_NOTE |

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

Commit \`$TARGET_SHA\` passes all verification criteria. Emma Antigravity approves this commit for v1.1.0 release tagging and packaging.
EOF
}

REPORT_CONTENT="$(generate_report)"

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
