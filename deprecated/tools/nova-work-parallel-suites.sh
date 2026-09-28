#!/usr/bin/env bash
# nova-work-parallel-suites.sh: run N lisp/nova-work acceptance suites AT ONCE on
# one host and refuse unless every one of them is green with the same tests.
#
# This is the failing-first proof for nova-tools#1699. Until that fix, every temp
# path the suite made was named from `(get-universal-time)` plus a counter that
# starts at zero in every image, under a directory name fixed in the source, and
# the AF_UNIX fixtures were named from `(random ...)` -- which SBCL saves into
# its core, so a fresh image repeats. Two suites that share a host therefore
# built the SAME paths: one found the destination already there, or the journal's
# lock held by the other, or had its live socket directory deleted underneath it.
# Measured: three reds on PR #1682, a red ci-ok on PR #1692 on runner
# air-nova-2, and 12-17 manufactured failures with four suites parallel on hulk,
# where the same suites one at a time were green.
#
# So this script is red on dev and green on the fix, and it stays in the tree
# because the property it asserts -- the suite is safe to run beside itself -- is
# the one CI silently depends on and no single-suite run can observe. CI runners
# share hosts (the Air runs two, the Studio several, superman ten).
#
# WHAT IT ASSERTS, for N concurrent suites:
#   1. every suite exits 0;
#   2. every suite prints `fail=0`;
#   3. every suite registers the IDENTICAL SET of test names.
# (3) is not redundant. A suite that dies early, or that loads fewer slices, can
# reach `fail=0` with fewer tests: green by absence. The set, not the total, is
# what says the same work ran -- the same reason nova-tools#1699's sibling rule
# for carried approvals records the test-name SET and not a count.
#
# It also reports every distinct FAIL line it saw, so a red here is a finding
# with its own text and never something to rerun.
#
# ONE suite per bench is the standing rule while #1699 is open; this script is
# the exception that proves it, so run it on a QUIET bench with nothing else on
# it (check `pgrep sbcl` first) and give it the bench alone.
#
# Usage: tools/nova-work-parallel-suites.sh [N] [OUTDIR]
#   N       how many suites to run at once (default 4)
#   OUTDIR  where the logs go (default: a fresh mktemp -d, removed on success)
#
# IT RUNS ONE SUITE ALONE FIRST, and refuses if that is not green. Two reasons,
# both necessary:
#   - the contrast IS the finding. "Four at once are red, one alone is green" is
#     what makes a collision a collision; four reds with no baseline is just a
#     broken tree or a broken bench, and #1699 has already cost people that
#     confusion once.
#   - it warms the ASDF fasl cache. Four cold SBCL images compiling the same
#     system into the same cache at the same moment is a DIFFERENT race, and one
#     that would show up here as noise and get blamed on temp paths.
#
# NOVA_WORK_SUITE overrides the command each worker runs. It exists so
# nova-work-parallel-suites_test.sh can drive this script's own logic with a
# fake suite instead of SBCL; nothing else should set it.
# NOVA_WORK_PARALLEL_BASELINE=0 skips the alone-first run; only that test sets
# it, to reach the concurrent logic without paying for a baseline every case.
set -u

cd "$(dirname "$0")/.." || exit 1
repo=$(pwd)

n=${1:-4}
outdir=${2:-}
suite=${NOVA_WORK_SUITE:-$repo/lisp/nova-work/run-tests.sh}

case "$n" in
  ''|*[!0-9]*) echo "PARALLEL-SUITES FAIL reason=bad-n n=$n" >&2; exit 2 ;;
esac
[ "$n" -ge 2 ] || { echo "PARALLEL-SUITES FAIL reason=n-must-be-at-least-2 n=$n" >&2; exit 2; }

keep=1
if [ -z "$outdir" ]; then
  outdir=$(mktemp -d) || exit 2
  keep=0
fi
mkdir -p "$outdir" || exit 2

echo "PARALLEL-SUITES START n=$n host=$(hostname) suite=$suite outdir=$outdir"
echo "PARALLEL-SUITES HEAD $(git -C "$repo" rev-parse HEAD 2>/dev/null || echo unknown)"

status=OK
reasons=""
refuse() { status=FAIL; reasons="$reasons $1"; }

# names_of <log> <out>: the SET of test names the suite registered.
# awk, not sed: a name is the second field of `TEST <name> PASS spec=...`, and
# BSD sed has no `\|` alternation, so a sed that worked on the Linux benches
# would quietly extract nothing on the darwin ones -- every names file empty,
# every set trivially equal, and this check green by doing nothing at all.
names_of() {
  awk '$1 == "TEST" && ($3 == "PASS" || $3 == "FAIL") { print $2 }' "$1" \
    | LC_ALL=C sort -u > "$2"
}

# fails_in <log...>: the FAIL lines. A line is a failure when its THIRD FIELD is
# FAIL -- not when the line contains "FAIL", because a PASSING case's expected=
# string quotes output grammars like `SESSION FAIL session=<path> ...`, and a
# substring match printed two passes under a FAILURES heading.
fails_in() { awk '$1 == "TEST" && $3 == "FAIL"' "$@"; }

# ONE suite alone first: the contrast is the finding, and it warms the fasl
# cache so four cold images are not racing the compiler as well.
if [ "${NOVA_WORK_PARALLEL_BASELINE:-1}" = 1 ]; then
  "$suite" >"$outdir/alone.log" 2>&1
  arc=$?
  aline=$(grep -m1 '^NOVA-WORK SLICE1 ' "$outdir/alone.log" 2>/dev/null || true)
  names_of "$outdir/alone.log" "$outdir/names-alone.txt"
  echo "PARALLEL-SUITES ALONE rc=$arc summary=${aline:-none} tests=$(wc -l < "$outdir/names-alone.txt" | tr -d ' ')"
  if [ "$arc" != 0 ] || [ "${aline##* }" != "fail=0" ]; then
    echo "PARALLEL-SUITES FAIL reason=baseline-not-green-alone logs=$outdir"
    echo "PARALLEL-SUITES NOTE one suite alone is RED, so this bench or this tree is broken and no statement about concurrency can be made from it. That is a different finding from nova-tools#1699 and must be read as one."
    fails_in "$outdir/alone.log" | head -40
    exit 1
  fi
fi

# Launch all N at once. They must overlap: a sequential run is exactly the case
# that was always green and is not what this proves.
pids=""
i=1
while [ "$i" -le "$n" ]; do
  ( "$suite" >"$outdir/suite-$i.log" 2>&1; echo $? >"$outdir/suite-$i.rc" ) &
  pids="$pids $!"
  i=$((i+1))
done
for p in $pids; do wait "$p"; done

# (1) and (2): every suite exited 0 and printed fail=0.
i=1
while [ "$i" -le "$n" ]; do
  rc=$(cat "$outdir/suite-$i.rc" 2>/dev/null || echo missing)
  line=$(grep -m1 '^NOVA-WORK SLICE1 ' "$outdir/suite-$i.log" 2>/dev/null || true)
  echo "PARALLEL-SUITES SUITE i=$i rc=$rc summary=${line:-none}"
  [ "$rc" = 0 ] || refuse "suite-$i-exit-$rc"
  case "$line" in
    *' fail=0') : ;;
    '') refuse "suite-$i-no-summary-line" ;;
    *)  refuse "suite-$i-${line##* }" ;;
  esac
  i=$((i+1))
done

# (3) the registered test-name SET is identical across all N.
i=1
while [ "$i" -le "$n" ]; do
  names_of "$outdir/suite-$i.log" "$outdir/names-$i.txt"
  echo "PARALLEL-SUITES NAMES i=$i count=$(wc -l < "$outdir/names-$i.txt" | tr -d ' ')"
  i=$((i+1))
done
# The reference is the ALONE run when there was one. Comparing the concurrent
# suites only to each other would be green if all N lost the same test -- which
# is exactly what a collision that hits every worker looks like.
ref="$outdir/names-1.txt"; refname="suite-1"
if [ -s "$outdir/names-alone.txt" ]; then
  ref="$outdir/names-alone.txt"; refname="the-alone-run"
fi
if [ ! -s "$ref" ]; then
  refuse "${refname}-registered-no-tests"
fi
i=1
while [ "$i" -le "$n" ]; do
  if ! diff -u "$ref" "$outdir/names-$i.txt" > "$outdir/names-diff-$i.txt"; then
    refuse "suite-$i-test-name-set-differs-from-$refname"
    echo "PARALLEL-SUITES NAME-SET-DIFF i=$i vs=$refname"
    sed -n '3,$p' "$outdir/names-diff-$i.txt" | grep '^[+-]' | head -40
  fi
  i=$((i+1))
done

# Every distinct FAIL line seen, so a red is read and not rerun.
fails=$(fails_in "$outdir"/suite-*.log 2>/dev/null | LC_ALL=C sort -u || true)
if [ -n "$fails" ]; then
  echo "PARALLEL-SUITES FAILURES (distinct across all $n suites):"
  printf '%s\n' "$fails"
fi

if [ "$status" = OK ]; then
  echo "PARALLEL-SUITES OK n=$n every-suite-green identical-test-name-sets ref=$refname tests=$(wc -l < "$ref" | tr -d ' ')"
  [ "$keep" = 1 ] || rm -rf "$outdir"
  exit 0
fi

echo "PARALLEL-SUITES FAIL n=$n reason=$(echo "$reasons" | sed 's/^ //; s/ /,/g') logs=$outdir"
echo "PARALLEL-SUITES NOTE a failure here is a temp-path collision between suites (nova-tools#1699), not a rerunnable flake: the same suites one at a time are green."
exit 1
