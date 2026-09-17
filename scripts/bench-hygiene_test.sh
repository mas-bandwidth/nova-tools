#!/usr/bin/env sh
# bench-hygiene_test.sh: a plain sh test for scripts/bench-hygiene.sh's DEAD-job reap.
# No bats, no make: the repo has no Makefile and no test harness for this script.
# Builds a throwaway bench under mktemp -d, fakes a live job with a real long-sleeping
# background process whose cwd is the job dir, a finished-and-harvested job, and a DEAD
# one (old harness-output.log, no RESULT.md, no process), then asserts that a dry run
# proposes only the DEAD job (tagged DEAD) and a real run removes only it.
set -u
SCRIPT=$(cd "$(dirname "$0")" && pwd)/bench-hygiene.sh
TMP=$(mktemp -d) || exit 1
SLEEP_PID=
fails=0
ok()  { printf 'ok %s - %s\n' "$1" "$2"; }
bad() { printf 'not ok %s - %s\n' "$1" "$2"; fails=1; }
cleanup() {
  [ -n "$SLEEP_PID" ] && kill "$SLEEP_PID" 2>/dev/null
  rm -rf "$TMP"
}
trap cleanup EXIT INT TERM

SLOT=$TMP/rowan-working/tmp/slot-aaa
JOBS=$SLOT/jobs
LIVE=$JOBS/job-live
HARV=$JOBS/job-harvested
DEAD=$JOBS/job-dead
mkdir -p "$LIVE" "$HARV" "$DEAD"

printf 'still running\n' > "$LIVE/harness-output.log"
printf 'done\n' > "$HARV/harness-output.log"
: > "$HARV/RESULT.md"
: > "$HARV/.harvested"
printf 'crashed\n' > "$DEAD/harness-output.log"
touch -d '45 minutes ago' "$HARV/harness-output.log" "$DEAD/harness-output.log"

( cd "$LIVE" && exec sleep 600 ) &
SLEEP_PID=$!
sleep 1

DRY=$(HOME="$TMP" "$SCRIPT" run --dry-run 2>&1)

n=$(printf '%s\n' "$DRY" | grep -c '^WOULD ')
if [ "$n" -eq 1 ] && printf '%s\n' "$DRY" | grep -qF "WOULD DEAD $DEAD"; then
  ok 1 "the dry run proposes exactly the DEAD job, tagged DEAD"
else
  bad 1 "expected one 'WOULD DEAD $DEAD' line, got $n WOULD lines: $DRY"
fi
if printf '%s\n' "$DRY" | grep -qF "$LIVE"; then
  bad 2 "the live job was proposed for deletion"
else
  ok 2 "the live job is left alone in the dry run"
fi
if printf '%s\n' "$DRY" | grep -q 'dead=1'; then
  ok 3 "the summary counts dead=1"
else
  bad 3 "the summary does not count dead=1: $DRY"
fi

REAL=$(HOME="$TMP" "$SCRIPT" run 2>&1)
if [ ! -e "$DEAD" ]; then
  ok 4 "the real run removed the DEAD job"
else
  bad 4 "the DEAD job survived the real run: $REAL"
fi
if [ -d "$LIVE" ]; then
  ok 5 "the real run left the live job in place"
else
  bad 5 "the real run removed the live job: $REAL"
fi
if [ -d "$HARV" ]; then
  ok 6 "the real run left the harvested job in place"
else
  bad 6 "the real run removed the harvested job: $REAL"
fi
if grep -q "DEAD $DEAD" "$TMP/hygiene.log" 2>/dev/null; then
  ok 7 "hygiene.log records the reap as DEAD"
else
  bad 7 "hygiene.log has no DEAD line for the reaped job"
fi

exit "$fails"
