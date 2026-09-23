#!/usr/bin/env sh
# nova-work-parallel-suites_test.sh: the class test for
# tools/nova-work-parallel-suites.sh.
#
# No bats, no harness: plain sh, run directly, the same shape as
# roadmap-parity_test.sh beside it. Each case drives the real script with a FAKE
# suite through NOVA_WORK_SUITE, so the cases are fast, deterministic and need no
# SBCL -- which matters, because the thing under test is a script whose real
# input takes four concurrent Lisp suites and a quiet bench.
#
# THE CLASS: can this script report OK when the suites did not actually all do
# the same, complete work? That is the only way it could be worse than useless --
# a green proof for nova-tools#1699 that a collision could still slip past. So
# each case below is one way to be falsely green:
#   (a) all suites green with identical test-name sets -> OK (the fix's shape);
#   (b) one suite exits non-zero -> refused;
#   (c) one suite prints fail=N -> refused, and the FAIL lines are reported;
#   (d) one suite registers FEWER tests but still says fail=0 -> refused
#       (green by absence: this is why the set is compared and not the total);
#   (e) one suite registers a DIFFERENT test name for the same count -> refused;
#   (f) a suite that prints no summary line at all -> refused;
#   (g) a suite that registers no tests at all -> refused;
#   (h) the suites really do run concurrently, not one after another -- a
#       sequential runner is exactly the case that was always green and would
#       make this whole proof vacuous;
#   (i) N below 2 is refused rather than silently proving nothing;
#   (j) the alone-first baseline: a suite that is red ALONE is refused as a
#       broken bench or tree and NOT reported as a concurrency finding, and a
#       green baseline becomes the reference the concurrent sets are compared
#       against -- so all N losing the SAME test is still caught.
#
# Cases (a)-(i) set NOVA_WORK_PARALLEL_BASELINE=0: they are about the concurrent
# logic, and paying for a baseline run in each would only slow them down. (j) is
# where the baseline itself is exercised.
set -u
SCRIPT=$(cd "$(dirname "$0")" && pwd)/nova-work-parallel-suites.sh
TMP=$(mktemp -d) || exit 1
trap 'rm -rf "$TMP"' EXIT
fails=0
n=0
ok()  { n=$((n+1)); printf 'ok %s - %s\n' "$n" "$1"; }
bad() { n=$((n+1)); printf 'not ok %s - %s\n' "$n" "$1"; fails=1; }

# mk_suite <path> <body...> writes an executable stand-in for run-tests.sh.
# Each invocation claims its own ordinal in $c, so a case can make the third
# suite behave differently from the first two. The claim is a mkdir, which is
# atomic on every filesystem here and needs no flock -- macOS ships none, and a
# missing flock would not fail loudly, it would just hand two concurrent suites
# the same ordinal and make these cases intermittent.
mk_suite() {
  path=$1; shift
  cat > "$path" <<EOF
#!/bin/sh
c=1
while ! mkdir "$TMP/ord/\$c" 2>/dev/null; do c=\$((c+1)); done
$*
EOF
  chmod +x "$path"
}

reset() { rm -rf "$TMP/ord" "$TMP/order"; mkdir -p "$TMP/ord"; }

# A green suite: three tests, all PASS, fail=0.
GREEN='
printf "TEST alpha PASS spec=s:1 e\n"
printf "TEST beta PASS spec=s:2 e\n"
printf "TEST gamma PASS spec=s:3 e\n"
printf "NOVA-WORK SLICE1 total=3 pass=3 fail=0\n"
exit 0'

run() { OUT=$("$SCRIPT" "$1" "$TMP/out.$$" 2>&1); RC=$?; rm -rf "$TMP/out.$$"; }

expect_ok()   { run "$1"; if [ "$RC" = 0 ]; then ok "$2"; else bad "$2 [$OUT]"; fi; }
expect_fail() { run "$1"; if [ "$RC" != 0 ]; then ok "$2"; else bad "$2 [$OUT]"; fi; }

export NOVA_WORK_SUITE="$TMP/suite.sh"
export NOVA_WORK_PARALLEL_BASELINE=0

# (a) four green suites with identical sets -> OK
reset; mk_suite "$TMP/suite.sh" "$GREEN"
expect_ok 4 "four identical green suites pass"
case "$OUT" in
  *"identical-test-name-sets ref=suite-1 tests=3"*) ok "the OK line names the test count and its reference" ;;
  *) bad "the OK line names the test count and its reference [$OUT]" ;;
esac

# (b) the third suite exits non-zero
reset; mk_suite "$TMP/suite.sh" "
if [ \"\$c\" = 3 ]; then printf 'NOVA-WORK SLICE1 total=3 pass=3 fail=0\n'; exit 7; fi
$GREEN"
expect_fail 4 "a suite that exits non-zero is refused"
case "$OUT" in *exit-7*) ok "the refusal names the exit code" ;; *) bad "the refusal names the exit code [$OUT]" ;; esac

# (c) the third suite reports a real failure
reset; mk_suite "$TMP/suite.sh" "
if [ \"\$c\" = 3 ]; then
  printf 'TEST alpha PASS spec=s:1 e\n'
  printf 'TEST beta PASS spec=s:2 e\n'
  printf 'TEST gamma FAIL spec=s:3 e: journal /tmp/x.journal is held by another process\n'
  printf 'NOVA-WORK SLICE1 total=3 pass=2 fail=1\n'
  exit 1
fi
$GREEN"
expect_fail 4 "a suite with fail=1 is refused"
case "$OUT" in
  *"is held by another process"*) ok "the failing line itself is reported, not just a count" ;;
  *) bad "the failing line itself is reported, not just a count [$OUT]" ;;
esac

# (d) GREEN BY ABSENCE: fewer tests, still fail=0. The whole reason the set is
# compared rather than the total.
reset; mk_suite "$TMP/suite.sh" "
if [ \"\$c\" = 2 ]; then
  printf 'TEST alpha PASS spec=s:1 e\n'
  printf 'TEST beta PASS spec=s:2 e\n'
  printf 'NOVA-WORK SLICE1 total=2 pass=2 fail=0\n'
  exit 0
fi
$GREEN"
expect_fail 4 "a suite that is green with FEWER tests is refused"
case "$OUT" in
  *test-name-set-differs*) ok "the refusal names the test-name set, not the total" ;;
  *) bad "the refusal names the test-name set, not the total [$OUT]" ;;
esac

# (c2) a PASSING case whose expected= string quotes an output grammar
# containing the word FAIL must not be listed as a failure. The suite has two
# such cases ("SESSION FAIL session=<path>...", "NOTES FAIL at exit 2..."), and
# a substring match printed both of them under a FAILURES heading on a run that
# was entirely green.
reset; mk_suite "$TMP/suite.sh" "
printf 'TEST alpha PASS spec=s:1 refused with SESSION FAIL session=<path> naming the verb\n'
printf 'TEST beta PASS spec=s:2 e\n'
printf 'TEST gamma PASS spec=s:3 e\n'
printf 'NOVA-WORK SLICE1 total=3 pass=3 fail=0\n'
exit 0"
run 4
if [ "$RC" = 0 ]; then ok "a green run whose expected= text quotes FAIL still passes"; else bad "a green run whose expected= text quotes FAIL still passes [$OUT]"; fi
case "$OUT" in
  *FAILURES*) bad "a green run must print no FAILURES heading [$OUT]" ;;
  *) ok "a green run prints no FAILURES heading" ;;
esac

# (e) same count, different name
reset; mk_suite "$TMP/suite.sh" "
if [ \"\$c\" = 2 ]; then
  printf 'TEST alpha PASS spec=s:1 e\n'
  printf 'TEST beta PASS spec=s:2 e\n'
  printf 'TEST delta PASS spec=s:3 e\n'
  printf 'NOVA-WORK SLICE1 total=3 pass=3 fail=0\n'
  exit 0
fi
$GREEN"
expect_fail 4 "a suite with the same COUNT but a different name is refused"

# (f) no summary line
reset; mk_suite "$TMP/suite.sh" "
if [ \"\$c\" = 4 ]; then printf 'TEST alpha PASS spec=s:1 e\n'; exit 0; fi
$GREEN"
expect_fail 4 "a suite that prints no summary line is refused"

# (g) no tests registered at all, everywhere
reset; mk_suite "$TMP/suite.sh" "
printf 'NOVA-WORK SLICE1 total=0 pass=0 fail=0\n'
exit 0"
expect_fail 4 "suites that register no tests at all are refused"
case "$OUT" in
  *registered-no-tests*) ok "the refusal says no tests were registered" ;;
  *) bad "the refusal says no tests were registered [$OUT]" ;;
esac

# (h) the suites overlap in time. A sequential runner would make every case
# above pass while proving nothing about concurrency, which is the entire point.
reset; mk_suite "$TMP/suite.sh" "
printf 'start\n' >> \"$TMP/order\"
sleep 1
printf 'end\n' >> \"$TMP/order\"
$GREEN"
run 4
if [ "$RC" = 0 ] && [ "$(head -4 "$TMP/order" | LC_ALL=C sort -u | tr -d '\n')" = "start" ]; then
  ok "all four suites start before any finishes (they really overlap)"
else
  bad "all four suites start before any finishes [order=$(tr '\n' ',' < "$TMP/order")] [$OUT]"
fi

# (i) N below 2 proves nothing and is refused rather than passing vacuously
reset; mk_suite "$TMP/suite.sh" "$GREEN"
expect_fail 1 "N=1 is refused: one suite cannot collide with itself"
run xyz; if [ "$RC" != 0 ]; then ok "a non-numeric N is refused"; else bad "a non-numeric N is refused"; fi

# (j) the alone-first baseline.
NOVA_WORK_PARALLEL_BASELINE=1

# A tree that is red even alone is a broken bench or a broken tree, and saying
# "concurrency" about it would be the exact confusion #1699 already cost once.
reset; mk_suite "$TMP/suite.sh" "
printf 'TEST alpha FAIL spec=s:1 e: the kernel does not load\n'
printf 'NOVA-WORK SLICE1 total=1 pass=0 fail=1\n'
exit 1"
expect_fail 4 "a suite that is red ALONE is refused"
case "$OUT" in
  *baseline-not-green-alone*) ok "a red baseline is named as a broken bench or tree, not as a collision" ;;
  *) bad "a red baseline is named as a broken bench or tree, not as a collision [$OUT]" ;;
esac
case "$OUT" in
  *"PARALLEL-SUITES SUITE i="*) bad "a red baseline must stop before the concurrent run [$OUT]" ;;
  *) ok "a red baseline stops before the concurrent run is even started" ;;
esac

# A green baseline is the reference. ALL FOUR concurrent suites losing the SAME
# test is the case that comparing them only to each other would call green.
reset; mk_suite "$TMP/suite.sh" "
if [ \"\$c\" = 1 ]; then
  printf 'TEST alpha PASS spec=s:1 e\n'
  printf 'TEST beta PASS spec=s:2 e\n'
  printf 'TEST gamma PASS spec=s:3 e\n'
  printf 'NOVA-WORK SLICE1 total=3 pass=3 fail=0\n'
  exit 0
fi
printf 'TEST alpha PASS spec=s:1 e\n'
printf 'TEST beta PASS spec=s:2 e\n'
printf 'NOVA-WORK SLICE1 total=2 pass=2 fail=0\n'
exit 0"
expect_fail 4 "all four concurrent suites losing the SAME test is refused against the alone run"
case "$OUT" in
  *differs-from-the-alone-run*) ok "the refusal names the alone run as the reference" ;;
  *) bad "the refusal names the alone run as the reference [$OUT]" ;;
esac

# And the whole thing green, baseline included, is the shape the fix produces.
reset; mk_suite "$TMP/suite.sh" "$GREEN"
expect_ok 4 "a green baseline plus four matching concurrent suites passes"
case "$OUT" in
  *"ref=the-alone-run tests=3"*) ok "the OK line says what it compared against" ;;
  *) bad "the OK line says what it compared against [$OUT]" ;;
esac

printf '1..%s\n' "$n"
[ "$fails" = 0 ] || { printf 'FAILED\n'; exit 1; }
printf 'PASSED\n'
