#!/bin/zsh
. "$HOME/rowan-working/bin/safe-rm.sh"
# local-gate.sh — THE FAST LANE, RUN ON THE STUDIO.
#
# Runs the EXACT steps of .github/workflows/ci-fast.yml against one pull
# request — or against one BRANCH that has no pull request — locally, so a
# green can come from this bench instead of a hosted run.  Glenn, 2026-09-11:
# "...or to decide to run your own tests locally manually (via scripts) so you
# don't need to run CI to get results, while quickly iterating." and, the same
# day: "check in to branches without PRs. Only run tests when merging into
# main, local runners elsewhere."
#
#   local-gate.sh <pr>     [--base fixed-table-form] [--gc]
#   local-gate.sh <branch> [--base fixed-table-form] [--gc]
#
# With a branch, the clone is the branch itself (child-clone.sh gate-<name>
# --branch <branch>) and the plan step's diff range is the same one the hosted
# job uses, origin/<base>...HEAD — i.e. origin/fixed-table-form...HEAD — so the
# branch gets exactly the selection its own diff against the base earns.
#
# The order is the lane's order, and each step is the workflow's own commands,
# copied verbatim — never a looser version:
#
#   plan            the `pick` step's script, byte for byte, including the
#                   broad rule and the leg/package selection, with
#                   BASE=<base> and GITHUB_OUTPUT pointed at a scratch file.
#   lint-fast       gofmt -l . / go mod tidy -diff / go vet ./... /
#                   go test ./internal/ci/ / bash -n bench/run.sh
#   block-gate      make tables-block-zero-cost
#   go-test-touched go test [-short] $PACKAGES, skipped only when plan
#                   selected no packages (the job's own `if:`)
#   leg-gate        one run per selected leg, the workflow's case statement
#                   with its make target lists unchanged.
#
# THE ONE THING THAT IS NOT VERBATIM, AND WHY: the workflow hands each leg the
# location of a toolchain it just installed on a clean Ubuntu runner
# (RUSTUP_BIN=/usr/bin, NODE=node, JAVA=java, the elixir pair off PATH).  Those
# are addresses, not gates.  Here the addresses are the Studio's — the child
# kit's env.sh and repo/dist — and the TARGET LISTS are untouched.  A step that
# would run a different or a smaller set of make targets than the hosted job is
# a bug in this file.
#
# It stops at the first red, prints the first failing line, writes a bounded
# summary to $HOME/rowan-working/merge-lane/gates/<pr|branch>-<head>.txt, and
# on all-green records the verdict into the merge lane:
#
#   merge-lane.sh gate <pr|branch> <head> green <path>
#
# which `merge-lane.sh run --local-gates` accepts as "checks green" for that
# head, so the lane merges on local green + read without waiting on hosted CI.

emulate -L zsh
setopt pipe_fail
zmodload zsh/datetime 2>/dev/null || true   # EPOCHREALTIME, for the per-step timing

KIT=${0:A:h}
WORKING=${LOCAL_GATE_WORKING:-$HOME/rowan-working}
GATES_DIR=${LOCAL_GATE_DIR:-$WORKING/merge-lane/gates}
SCRATCH_ROOT=${CHILD_SCRATCH:-/private/tmp/claude-501/-Users-glenn-rowan-new/349e5152-f719-4496-b740-73cccb87f927/scratchpad}
RUSTUP_BIN_LOCAL=${RUSTUP_BIN_LOCAL:-/opt/homebrew/opt/rustup/bin}

PR=''
BRANCH=''
TARGET=''
KIND=''
LABEL=''
SLUG=''
BASE='fixed-table-form'
GC=0

die() { print -u2 -- "local-gate: $*"; exit 1 }

while (( $# )); do
  case "$1" in
    --base) BASE="${2-}"; shift 2 || die '--base needs a branch' ;;
    --gc)   GC=1; shift ;;
    -h|--help) sed -n '2,52p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    -*) die "unknown flag $1" ;;
    *)  [[ -z $TARGET ]] || die "two targets given ($TARGET, $1)"; TARGET="$1"; shift ;;
  esac
done

[[ -n $TARGET ]] || die 'usage: local-gate.sh <pr|branch> [--base fixed-table-form] [--gc]'
if [[ $TARGET == <-> ]]; then
  KIND=pr; PR="$TARGET"; LABEL="PR #$PR"; SLUG="$PR"
  (( ${+commands[gh]} )) || die 'gh not on PATH'
else
  KIND=branch; BRANCH="$TARGET"; LABEL="branch $BRANCH"
  # one path segment for the clone name, and nothing that is a revision
  # expression or a flag
  case "$BRANCH" in
    -*|*' '*|*'..'*|*'~'*|*'^'*|*':'*|*'?'*|*'*'*|/*|*/) die "not a branch name: $BRANCH" ;;
  esac
  SLUG="${BRANCH//\//-}"
fi

NAME="gate-$SLUG-$$"   # unique per run: two runs on one PR must never share a tree
HOME_DIR="$SCRATCH_ROOT/$NAME"

# ---------------------------------------------------------------- slots ---
# At most LOCAL_GATE_SLOTS gates run on this machine at once (default 2). A
# gate fans ten legs out; six gates at once put the load at 235 on 32 cores
# (2026-09-11 15:25Z) and turned green tests into 30 s timeouts. The throttle
# lives in the tool so no caller has to remember it. A slot whose holder is
# dead is reclaimed; mkdir decides who gets it.
SLOTS=${LOCAL_GATE_SLOTS:-2}; SLOT_DIR=$GATES_DIR/slots; SLOT=''
mkdir -p $SLOT_DIR
slot_release() { [[ -n $SLOT ]] && rmdir $SLOT 2>/dev/null; SLOT='' }
slot_acquire() {
  local i p waited=0
  while true; do
    for i in {1..$SLOTS}; do
      if mkdir $SLOT_DIR/$i 2>/dev/null; then
        SLOT=$SLOT_DIR/$i; print -- $$ > $SLOT/pid; return 0
      fi
      p=$(cat $SLOT_DIR/$i/pid 2>/dev/null)
      [[ -n $p ]] && ! kill -0 $p 2>/dev/null && safe_rm "$SLOT_DIR/$i"
    done
    (( waited % 60 == 0 )) && print -- "local-gate: waiting for a gate slot ($SLOTS busy)"
    sleep 5; (( waited += 5 ))
    (( waited > 5400 )) && die "no gate slot free in 90 minutes"
  done
}
trap slot_release EXIT
slot_acquire
REPO="$HOME_DIR/repo"

# ------------------------------------------------------------- the clone ---
# One clone per gate run, from the child kit, so the environment is the one
# every child here already runs under.
if [[ -e $HOME_DIR ]]; then
  print -- "local-gate: reclaiming the previous $NAME clone"
  "$KIT/child-clone.sh" --gc "$NAME" >/dev/null || die "could not reclaim $HOME_DIR"
fi
print -- "local-gate: $LABEL onto $BASE — clone $NAME"
if [[ $KIND == pr ]]; then
  "$KIT/child-clone.sh" "$NAME" --branch "$BASE" --pr "$PR" >/dev/null \
    || die "child-clone.sh $NAME --pr $PR failed"
else
  "$KIT/child-clone.sh" "$NAME" --branch "$BRANCH" >/dev/null \
    || die "child-clone.sh $NAME --branch $BRANCH failed"
fi
[[ -d $REPO ]] || die "no clone at $REPO"

HEAD_OID=$( git -C "$REPO" rev-parse HEAD ) || die 'cannot read HEAD'
HEAD_SHORT=${HEAD_OID[1,12]}
mkdir -p -- "$GATES_DIR"
OUT="$GATES_DIR/$SLUG-$HEAD_OID.txt"
LOGS="$HOME_DIR/scratch/gate-logs"
mkdir -p -- "$LOGS"

# the kit's environment, exactly as every child sources it
source "$HOME_DIR/env.sh" || die 'could not source env.sh'
cd -- "$REPO" || die "cannot cd $REPO"

# env.sh exports SCHEMA_REQUIRE_CORPUS=1 for a child's whole shell; the HOSTED
# runner does not, and the workflow says so in go-test-touched's own block: the
# corpus-reading suites skip themselves there, and "the leg gates below are
# where they are made to run" (SCHEMA_REQUIRE_CORPUS=1 is inside those make
# targets). Measured on #949: with it set for go-test-touched, the run reddens
# one step EARLY — TestFixedVersioningNewReadsOld in ./internal/codegen/rusttable
# — while the hosted lane passes that job and reddens on the rust leg row. Same
# failure, wrong step, so the gate would not name the row a reader must open.
# So the early jobs run the runner's environment, and the legs get it back.
CHILD_REQUIRE_CORPUS=${SCHEMA_REQUIRE_CORPUS-}
unset SCHEMA_REQUIRE_CORPUS

# -------------------------------------------------------------- plumbing ---
# GLENN, 2026-09-11: "If you have 6 tests that are independent, RUN THEM IN
# PARALLEL."  The steps below are the workflow's jobs, and on the hosted lane
# they ARE parallel — lint-fast, go-test-touched and every leg-gate matrix row
# are separate runners.  Running them one after another here was this file's
# own invention, and it cost about six minutes.  So the groups below run at
# once on the Studio's cores, and the summary still keeps ONE LINE PER STEP
# with its own seconds and its own first failing line; TOTAL is now WALL TIME,
# the number a child waits.
typeset -a ROWS
typeset -a REDS
TOTAL=0
GATE_T0=$EPOCHREALTIME
FAILED=''
FAILLINE=''
NCPU=${LOCAL_GATE_JOBS:-$(sysctl -n hw.ncpu 2>/dev/null || print 8)}

# the first line that names the failure, else the first line of the tail
first_failing_line() {
  local log=$1 line
  line=$( grep -vE '^[[:space:]]*#' "$log" 2>/dev/null \
          | grep -nE '(^|[^[:alnum:]_])(FAIL|FAILED|error|Error|ERROR|::error|fatal|panic:|Assertion|assert|mismatch|differs|\*\*\*)' \
          | head -1 )
  [[ -n $line ]] || line=$( grep -n . "$log" 2>/dev/null | tail -1 )
  [[ -n $line ]] || line='(no output)'
  print -r -- "${line[1,400]}"
}

# step <name> <command...> — timed, logged, stops the run on a red
step() {
  local name=$1; shift
  local log="$LOGS/${name//\//_}.log"
  local t0=$EPOCHREALTIME
  print -n -- "  $name ... "
  "$@" >"$log" 2>&1
  local rc=$?
  local secs=$(( EPOCHREALTIME - t0 ))
  if (( rc == 0 )); then
    ROWS+=( "$(printf '%-34s | %-4s | %6.1f' "$name" ok "$secs")" )
    printf 'ok %.1fs\n' "$secs"
    return 0
  fi
  ROWS+=( "$(printf '%-34s | %-4s | %6.1f' "$name" RED "$secs")" )
  printf 'RED %.1fs\n' "$secs"
  [[ -n $FAILED ]] || FAILED="$name"
  local fl="$( first_failing_line "$log" )"
  [[ -n $FAILLINE ]] || FAILLINE="$fl"
  REDS+=( "$name: $fl" )
  print -- "    first failing line: $fl"
  print -- "    full log: $log"
  return 1
}

# ---------------------------------------------------------- steps at once ---
# pstep starts a step in the background; pwait collects the whole group.  A
# backgrounded step writes its rc and its seconds to one status file, so the
# row it earns is the same row `step` would have written — and NOTHING in a
# group short-circuits, which is `make -k` at the step level: one red leg must
# not hide the other eight.
typeset -a PGROUP
PGROUP=()

pstep_run() {          # <status file> <log> <command...>
  local st=$1 log=$2; shift 2
  local t0=$EPOCHREALTIME rc=0
  "$@" >"$log" 2>&1 || rc=$?
  printf '%d %.3f\n' $rc $(( EPOCHREALTIME - t0 )) > "$st"
}

pstep() {              # <name> <command...>
  local name=$1; shift
  local safe=${name//\//_}
  rm -f -- "$LOGS/$safe.status"
  pstep_run "$LOGS/$safe.status" "$LOGS/$safe.log" "$@" &
  PGROUP+=( "$name" )
}

# pwait <group label> — wait for every step pstep started, then write their
# rows in the order they were started.  Returns 1 if any of them was red.
pwait() {
  local group=$1 name safe rc secs fl bad=0
  (( ${#PGROUP} )) || { print -- "  $group ... (nothing to run)"; return 0 }
  print -- "  $group ... ${#PGROUP} at once: ${PGROUP}"
  wait
  for name in $PGROUP; do
    safe=${name//\//_}
    rc=1; secs=0
    [[ -r "$LOGS/$safe.status" ]] && read -r rc secs < "$LOGS/$safe.status"
    if (( rc == 0 )); then
      ROWS+=( "$(printf '%-34s | %-4s | %6.1f' "$name" ok "$secs")" )
      printf '    %-30s ok  %.1fs\n' "$name" "$secs"
    else
      bad=1
      ROWS+=( "$(printf '%-34s | %-4s | %6.1f' "$name" RED "$secs")" )
      printf '    %-30s RED %.1fs\n' "$name" "$secs"
      fl="$( first_failing_line "$LOGS/$safe.log" )"
      [[ -n $FAILED ]]   || FAILED="$name"
      [[ -n $FAILLINE ]] || FAILLINE="$fl"
      REDS+=( "$name: $fl" )
      print -- "      first failing line: $fl"
      print -- "      full log: $LOGS/$safe.log"
    fi
  done
  PGROUP=()
  return $bad
}

write_summary() {
  local verdict=$1
  # TOTAL is WALL TIME now, not the sum of the steps: the groups overlap.
  TOTAL=$(( EPOCHREALTIME - GATE_T0 ))
  {
    print -- "local fast lane — $LABEL  head $HEAD_OID"
    print -- "base        origin/$BASE"
    print -- "clone       $REPO"
    print -- "when        $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    print -- "workflow    .github/workflows/ci-fast.yml (plan, lint-fast, block-gate, go-test-touched, leg-gate)"
    print -- "selection   broad=${PLAN_BROAD:-?}  legs=${PLAN_LEGS_JSON:-?}  packages=${PLAN_PACKAGES:-(none)}"
    print --
    printf '%-34s | %-4s | %6s\n' step result secs
    printf '%-34s-+-%-4s-+-%6s\n' '----------------------------------' '----' '------'
    local r; for r in $ROWS; do print -r -- "$r"; done
    printf '%-34s | %-4s | %6.1f\n' 'TOTAL (wall)' "$verdict" "$TOTAL"
    if [[ -n $FAILED ]]; then
      print --
      print -- "first red   $FAILED"
      print -- "first line  $FAILLINE"
      if (( ${#REDS} > 1 )); then
        print --
        print -- "every red in the group (${#REDS}):"
        local d; for d in $REDS; do print -r -- "  ${d[1,400]}"; done
      fi
    fi
  } > "$OUT"
  print --
  print -- "--- local fast lane, $LABEL ($HEAD_SHORT) -------------------------------"
  cat -- "$OUT"
  print -- "-----------------------------------------------------------------------"
  print -- "summary: $OUT"
}

finish() {
  local verdict=$1
  write_summary "$verdict"
  if [[ $verdict == ok ]]; then
    "$KIT/merge-lane.sh" gate "$TARGET" "$HEAD_OID" green "$OUT" || print -u2 -- 'local-gate: merge-lane.sh gate failed'
  fi
  if (( GC )); then
    "$KIT/child-clone.sh" --gc "$NAME" >/dev/null && print -- "local-gate: gate clone removed"
  else
    print -- "local-gate: clone kept at $REPO (--gc removes it)"
  fi
  [[ $verdict == ok ]] && exit 0
  exit 1
}

# ======================================================= job: plan =========
# ci-fast.yml's `plan` job, step "the diff, the packages and the legs",
# verbatim, dedented the way YAML's `run: |` block scalar dedents it before bash
# ever sees it (an indented heredoc terminator is not one).  BASE is the job's
# own env expression resolved to this run's base — for a branch target that is
# the lane's base, so the diff range is origin/fixed-table-form...HEAD, the
# branch's whole divergence; GITHUB_OUTPUT and GITHUB_STEP_SUMMARY are files
# instead of a runner's.
PLAN_OUT="$HOME_DIR/scratch/plan.output"
PLAN_SUM="$HOME_DIR/scratch/plan.summary"
: > "$PLAN_OUT"; : > "$PLAN_SUM"
cat > "$HOME_DIR/scratch/plan.sh" <<'PLAN_EOF'
set -euo pipefail
git fetch --quiet origin "$BASE"
files=$(git diff --name-only "origin/$BASE...HEAD")
if [ -z "$files" ]; then
  echo "::notice::the diff is empty against origin/$BASE — running the broad selection"
  files="ir/"
fi
echo "--- files in the diff"
echo "$files"

# THE BROAD RULE. These paths can change what EVERY leg emits or what
# every package compiles, so a diff that touches one of them gets the
# whole of both selections. The direction of the error is deliberate:
# a path nobody classified lands here, never in "skip it".
broad=no
if echo "$files" | grep -qE '^(ir/|compiler/|tables/|Makefile$|go\.mod$|docs/FIXED-FORM-|internal/codegen/cpp|internal/codegen/ccommon|test/cpp|\.github/workflows/ci-fast\.yml$)'; then
  broad=yes
fi

# THE LEGS. One line per leg: the codegen packages that emit it, the
# generated tree it owns, its own test directory and its make include.
# A leg whose paths are untouched does not run here; ci-full.yml runs
# every leg on the merge regardless.
legs=""
add_leg() { case " $legs " in *" $1 "*) ;; *) legs="$legs $1" ;; esac; }
while read -r f; do
  [ -n "$f" ] || continue
  case "$f" in
    internal/codegen/gotable/*|internal/codegen/golang/*|generated/go/*|test/go-tables/*|make/go.mk) add_leg go ;;
  esac
  case "$f" in
    internal/codegen/ctable/*|internal/codegen/c/*|generated/c/*|test/c-tables/*|make/c.mk) add_leg c ;;
  esac
  case "$f" in
    internal/codegen/cpptable/*|test/cpp-tables/*) add_leg cpp ;;
  esac
  case "$f" in
    internal/codegen/rusttable/*|internal/codegen/rust/*|generated/rust/*|test/rust-fixedform/*|make/rust.mk) add_leg rust ;;
  esac
  case "$f" in
    internal/codegen/jstable/*|internal/codegen/js/*|generated/js/*|test/js-tables/*|make/js.mk) add_leg js ;;
  esac
  case "$f" in
    internal/codegen/cstable/*|internal/codegen/csharp/*|generated/cs/*|test/cs-tables/*|make/cs.mk) add_leg cs ;;
  esac
  case "$f" in
    internal/codegen/javatable/*|internal/codegen/java/*|generated/java/*|test/java-tables/*|test/java-fixedform/*|make/java.mk) add_leg java ;;
  esac
  case "$f" in
    internal/codegen/darttable/*|internal/codegen/dart/*|generated/dart/*|test/dart-tables/*|make/dart.mk) add_leg dart ;;
  esac
  case "$f" in
    internal/codegen/elixirtable/*|internal/codegen/elixir/*|generated/elixir/*|test/elixir-tables/*|test/elixir-fixedform/*|make/elixir.mk) add_leg elixir ;;
  esac
done <<EOF
$files
EOF
if [ "$broad" = yes ]; then
  legs="cpp go c rust js cs java dart elixir"
fi

# THE PACKAGES. Every directory holding a changed .go file, as the
# package path `go test` takes, plus the codegen package of each
# selected leg — a make include or a generated tree changing is a
# reason to run that leg's harness even with no .go file in the diff.
#
# ONLY A PACKAGE OF THE MAIN MODULE, the set `./...` names. A
# generated tree (generated/go, generated/bench/go, test/go) is its
# own module, and a testdata/ directory is invisible to the go tool;
# a .go file there is the leg gate's business, and naming its
# directory here fails by name — #958 and #960, go-test-touched:
#   main module (github.com/mas-bandwidth/schema/v2) does not
#   contain package .../generated/go
if [ "$broad" = yes ]; then
  packages="./..."
else
  packages=""
  for f in $files; do
    case "$f" in
      *.go) d=$(dirname "$f"); [ -d "$d" ] || continue
            case "/$d/" in */testdata/*) continue ;; esac
            m=$d; while [ "$m" != . ]; do [ -f "$m/go.mod" ] && continue 2; m=$(dirname "$m"); done
            case " $packages " in *" ./$d "*) ;; *) packages="$packages ./$d" ;; esac ;;
    esac
  done
  for leg in $legs; do
    case "$leg" in
      cpp) p=./internal/codegen/cpptable ;;
      *)   p=./internal/codegen/${leg}table ;;
    esac
    [ -d "${p#./}" ] || continue
    case " $packages " in *" $p "*) ;; *) packages="$packages $p" ;; esac
  done
  packages=$(echo "$packages" | xargs || true)
fi

# the legs as a JSON array, which is what a matrix reads
json=$(printf '%s' "$legs" | xargs -n1 2>/dev/null | sort -u | awk 'BEGIN{printf "["} {printf "%s\"%s\"", (NR>1?",":""), $1} END{printf "]"}')
[ -n "$json" ] || json='[]'

echo "broad=$broad"        >> "$GITHUB_OUTPUT"
echo "packages=$packages"  >> "$GITHUB_OUTPUT"
echo "legs=$json"          >> "$GITHUB_OUTPUT"
{
  echo "### what this push runs on the fast lane"
  echo
  echo "* broad selection: **$broad**"
  echo "* packages: \`${packages:-(none)}\`"
  echo "* legs: \`$json\`"
} >> "$GITHUB_STEP_SUMMARY"
PLAN_EOF

run_plan() {
  BASE="$BASE" GITHUB_OUTPUT="$PLAN_OUT" GITHUB_STEP_SUMMARY="$PLAN_SUM" \
    bash "$HOME_DIR/scratch/plan.sh"
}
step plan run_plan || finish red

PLAN_BROAD=$(   sed -n 's/^broad=//p'    "$PLAN_OUT" | tail -1 )
PLAN_PACKAGES=$( sed -n 's/^packages=//p' "$PLAN_OUT" | tail -1 )
PLAN_LEGS_JSON=$( sed -n 's/^legs=//p'   "$PLAN_OUT" | tail -1 )
typeset -a PLAN_LEGS
PLAN_LEGS=( ${(s: :)${${PLAN_LEGS_JSON//[\[\]\"]/}//,/ }} )
print -- "  selection: broad=$PLAN_BROAD legs=$PLAN_LEGS_JSON packages=${PLAN_PACKAGES:-(none)}"

# =================================================== job: lint-fast =======
# Every step of the hosted job that is a gate. The Go build-cache restore is a
# runner's cache, not a check, and has no local counterpart.
# each hosted `run:` body stays inside a subshell, so its own `exit` and its
# own `set -euo pipefail` end the STEP and never this script
lint_gofmt() { (
  out=$(gofmt -l .)
  if [ -n "$out" ]; then
    echo "$out"
    echo "::error::gofmt differs — run gofmt -w on the files above"
    exit 1
  fi
) }
# =============================================== job: go-test-touched ====
# The job's `if: needs.plan.outputs.packages != ''`, and its own -short rule
# for a broad diff.
go_test_touched() { (
  set -euo pipefail
  echo "packages: $PACKAGES"
  if [ "$BROAD" = yes ]; then
    echo "::notice::broad diff — the whole suite runs in ci-full.yml; this lane runs it with -short"
    go test -short ${=PACKAGES}
  else
    go test ${=PACKAGES}
  fi
) }

# ---- GROUP 1: the early jobs, all at once ---------------------------------
# lint-fast is pure Go tooling and go-test-touched is `go test`: nothing here
# writes build/ or generated/, so they are independent and run together.
#
# AND NOTHING THAT BUILDS THE CORPUS MAY JOIN THEM. go-test-touched runs in the
# HOSTED RUNNER'S environment on purpose (see SCHEMA_REQUIRE_CORPUS above), and
# the corpus-reading suites decide to skip on whether build/fixedform-corpus
# EXISTS. Today block-gate — the only make before this job — does not build it,
# so those suites skip here exactly as they skip on the runner. Start the shared
# build beside this group and the corpus appears mid-run, the suites stop
# skipping, and the gate reddens one step early on #949's row. So the shared
# build is its own barrier, below.
#
# AND THE THREE GO STEPS STAY IN A LINE, MEASURED, NOT ASSUMED. `go vet ./...`,
# `go test ./internal/ci/` and `go test -short ./...` are independent CHECKS but
# not independent WORK: all three compile the same module graph through the one
# build cache, and on a fresh clone that cache is cold. Run at once they each
# pay the cold compile and fight for it — measured on this tip: 2.0 + 4.3 + 49.7
# serial became 93.8 s of wall, SLOWER than the line they replaced. Run in a
# line the first one warms the cache for the next two, which is why go-vet is
# two seconds. So only the three steps that compile NOTHING (gofmt, go mod tidy
# -diff, bash -n) run beside them.
export PACKAGES="$PLAN_PACKAGES" BROAD="$PLAN_BROAD"
pstep lint-fast/gofmt               lint_gofmt
pstep lint-fast/go-mod-tidy         go mod tidy -diff
pstep lint-fast/bench-scripts-parse bash -n bench/run.sh
step  lint-fast/go-vet              go vet ./...            || { pwait 'lint-fast, the three that compile nothing'; finish red }
step  lint-fast/ci-lints-itself     go test ./internal/ci/  || { pwait 'lint-fast, the three that compile nothing'; finish red }
if [[ -n $PLAN_PACKAGES ]]; then
  step go-test-touched go_test_touched || { pwait 'lint-fast, the three that compile nothing'; finish red }
else
  ROWS+=( "$(printf '%-34s | %-4s | %6.1f' go-test-touched skip 0)" )
  print -- "  go-test-touched ... skipped (plan selected no packages)"
fi
pwait 'lint-fast, the three that compile nothing' || finish red

# =================================================== job: leg-gate =======
# One matrix row per selected leg. The case statement is the workflow's, target
# lists unchanged; the toolchain addresses are the Studio's (see the header).
leg_gate() { (
  local leg=$1
  case "$leg" in
    cpp)    make tables-fixedform ;;
    go)     make tables-go-fixed-form tables-go-versioning ;;
    c)      make tables-c-fixedform tables-c-versioning ;;
    rust)   make tables-rust-fixedform tables-rust-versioning RUSTUP_BIN="$RUSTUP_BIN_LOCAL" ;;
    js)     make tables-js-fixed-form tables-js-versioning NODE="$NODE" ;;
    cs)     make tables-cs-leg tables-cs-versioning ;;
    java)   make tables-java-fixedform tables-java-versioning JAVA="$JAVA" JAVAC="$JAVAC" ;;
    dart)   make tables-dart-fixed-form tables-dart-versioning DART="$DART" ;;
    elixir) make tables-elixir-fixed-form tables-elixir-versioning \
              ELIXIR_BIN="$(command -v elixir)" ERL_BIN="$(dirname "$(command -v erl)")" ;;
    *)      echo "::error::no fixed-form gate is registered for leg '$leg' — add it here and in ci-full.yml"; exit 1 ;;
  esac
) }

[[ -n $CHILD_REQUIRE_CORPUS ]] && export SCHEMA_REQUIRE_CORPUS="$CHILD_REQUIRE_CORPUS"

# ---- THE SHARED BUILD, ONCE, BEFORE ANY LEG RUNS -------------------------
# The legs are independent JOBS but not independent BUILDS: they share
# prerequisites, and two makes building one file at the same time is a race that
# reddens a leg for no reason. So every prerequisite more than one leg reaches
# for is built here first, in ONE make (which orders them among themselves), and
# the legs below then find them up to date and build only their own:
#
#   bin/schema                          every leg's generator
#   build/tables-generated{,-c,-cs}/.stamp  cpp/c/cs, and block-gate's prereqs
#   generated/bench/paired/cpp/.stamp   js's corpus, java's corpus,
#                                       build/schema_test_bench_paired (go),
#                                       build/fixedform-bench-corpus (dart)
#   build/fixedform-corpus/.stamp       THE C++ REFERENCE'S BYTE ORACLE, which
#                                       rust, js, java, dart, elixir and every
#                                       leg's versioning half read — and whose
#                                       own recipe starts `rm -rf`, so a leg
#                                       rebuilding it under another leg's reader
#                                       is the worst race in this file
#   build/fixedform-bench-corpus/.stamp dart, and js through the paired stamp
#   build/schema_test_bench_paired      go's fixed-form gate
#
# This is not a looser gate: these are the same targets the legs would have
# built, built by the same rules, in the same tree.
typeset -a SHARED_TARGETS
SHARED_TARGETS=(
  bin/schema
  build/tables-generated/.stamp
  build/tables-generated-c/.stamp
  build/tables-generated-cs/.stamp
  generated/bench/paired/cpp/.stamp
  build/schema_test_bench_paired
  build/fixedform-corpus/.stamp
  build/fixedform-bench-corpus/.stamp
)
shared_build() { make -j"$NCPU" $SHARED_TARGETS }
step shared-build shared_build || finish red

# =================================================== job: leg-gate =======
# ONE GROUP, EVERY LEG AT ONCE — the hosted lane's matrix, which is parallel,
# and Glenn's rule: independent tests run in parallel. block-gate joins them:
# its three prerequisites are in the shared build above, so its recipe races
# nothing. Each leg keeps its own log, its own seconds and its own first failing
# line, and a red leg does not stop the others (pwait is `make -k`'s promise at
# the step level).
pstep block-gate make tables-block-zero-cost
if (( ${#PLAN_LEGS} == 0 )); then
  ROWS+=( "$(printf '%-34s | %-4s | %6.1f' leg-gate skip 0)" )
  print -- "  leg-gate ... skipped (plan selected no legs)"
else
  for leg in $PLAN_LEGS; do
    pstep "leg-gate/$leg" leg_gate "$leg"
  done
fi
pwait 'block-gate + leg-gate' || finish red

finish ok
