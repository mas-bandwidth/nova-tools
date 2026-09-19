#!/usr/bin/env sh
# bench-hygiene_test.sh: the class test for scripts/bench-hygiene.sh's reaper (issue #1499).
#
# No bats, no make: the repo has no Makefile and no test harness for this script, so this is
# plain sh, run directly. It builds a throwaway bench under mktemp -d and drives the script
# with HOME pointed at that fixture -- NEVER at the real home, which is the root the script
# deletes under.
#
# The class is: WHAT MAY THIS TOOL DELETE. Two benches lost a certify tree, corpus and all,
# to a reaper that deleted on SHAPE (any directory under the swarm root that was not a slot)
# and judged a live card dead by SILENCE (a quiet 15 minutes took its HOME and TMPDIR out
# from under it). So:
#   (a) a bare directory under either swarm root survives a run, whatever its age;
#   (b) a job with a live lease is never touched, even when it has been quiet for hours,
#       and neither are its slot's data/ (the card's HOME) and tmp/ (its TMPDIR);
#   (c) an unleased job newer than 6 h survives; one older than 6 h goes;
#   (d) an unrecognised or freshly emptied slot is not deleted; an empty slot quiet for
#       6 h is;
#   (e) the go build cache is never dropped while any lease is live.
# The second pass proves the lease is not a permanent shield: with its pid gone and its
# heartbeat stale, the same job and the same cache go.
set -u
SCRIPT=$(cd "$(dirname "$0")" && pwd)/bench-hygiene.sh
TMP=$(mktemp -d) || exit 1
SLEEP_PID=
fails=0
n=0
ok()  { n=$((n+1)); printf 'ok %s - %s\n' "$n" "$1"; }
bad() { n=$((n+1)); printf 'not ok %s - %s\n' "$n" "$1"; fails=1; }
is()  { if [ "$1" = yes ]; then ok "$2"; else bad "$2 [$3]"; fi; }
gone() { if [ -e "$1" ]; then bad "$2 [$1 is still there]"; else ok "$2"; fi; }
kept() { if [ -e "$1" ]; then ok "$2"; else bad "$2 [$1 was deleted]"; fi; }
cleanup() { [ -n "$SLEEP_PID" ] && kill "$SLEEP_PID" 2>/dev/null; rm -rf "$TMP"; }
trap cleanup EXIT INT TERM

# age <seconds-ago> <path>...: portable utimes. `touch -d '6 hours ago'` is GNU only.
age() { secs=$1; shift; perl -e 'my $t = time - shift(@ARGV); utime $t, $t, @ARGV or die "utime: $!"' "$secs" "$@"; }

R1=$TMP/rowan-swarm-root
R2=$TMP/rowan-working/tmp
H8=28800   # 8 hours: past the 6 h the reaper needs
H2=7200    # 2 hours: a job that is merely quiet

# (a) two bare directories, one under each root: somebody's work, not a slot.
BARE1=$R1/certify-tree
BARE2=$R2/toolchains
mkdir -p "$BARE1/corpus" "$BARE2/corpus"
printf 'the corpus\n' > "$BARE1/corpus/data.txt"
printf 'the report\n' > "$BARE2/REPORT.md"

# (b) a leased slot: one job quiet for 8 hours, holding a lease with a live pid and a fresh
# heartbeat, beside the card's HOME and TMPDIR.
LEASED=$R1/slot-leased
LJOB=$LEASED/jobs/job-quiet
mkdir -p "$LJOB" "$LEASED/data" "$LEASED/tmp/job-quiet"
printf 'a long model call\n' > "$LJOB/harness-output.log"

# (c) an unleased young job and an unleased old one, in their own slots.
YOUNG=$R1/slot-young
mkdir -p "$YOUNG/jobs/job-young"
printf 'working\n' > "$YOUNG/jobs/job-young/harness-output.log"
OLD=$R1/slot-old
mkdir -p "$OLD/jobs/job-old"
printf 'crashed\n' > "$OLD/jobs/job-old/harness-output.log"

# a harvested job goes whatever its age: it has been read.
HARV=$R2/slot-harvested
mkdir -p "$HARV/jobs/job-read"
printf 'done\n' > "$HARV/jobs/job-read/harness-output.log"
: > "$HARV/jobs/job-read/RESULT.md"
: > "$HARV/jobs/job-read/.harvested"

# (d) an empty slot quiet for 8 hours, and one made this second.
STALE=$R1/slot-stale
mkdir -p "$STALE/jobs"

# (e) the build cache.
CACHE=$TMP/.cache/go-build
mkdir -p "$CACHE/trim.txt.d"
printf 'x\n' > "$CACHE/trim.txt.d/x"

# Age everything from the leaves up: writing a child touches its parent.
age $H8 "$BARE1/corpus/data.txt" "$BARE1/corpus" "$BARE1" \
        "$BARE2/REPORT.md" "$BARE2/corpus" "$BARE2" \
        "$LJOB/harness-output.log" "$LJOB" "$LEASED/jobs" "$LEASED/data" \
        "$LEASED/tmp/job-quiet" "$LEASED/tmp" "$LEASED" \
        "$OLD/jobs/job-old/harness-output.log" "$OLD/jobs/job-old" "$OLD/jobs" "$OLD" \
        "$HARV/jobs/job-read/harness-output.log" "$HARV/jobs/job-read" "$HARV/jobs" "$HARV" \
        "$STALE/jobs" "$STALE"
age $H2 "$YOUNG/jobs/job-young/harness-output.log" "$YOUNG/jobs/job-young" "$YOUNG/jobs" "$YOUNG"

# THE LEASE. A real live process stands in for the card's launcher; the file carries its pid
# and its mtime is the heartbeat, written just now. This is the shape nova-swarm native
# writes: <job>/.lease, pid= and a heartbeat while the child runs.
( exec sleep 600 ) &
SLEEP_PID=$!
printf 'pid=%s\nhost=test\nlabel=job-quiet\n' "$SLEEP_PID" > "$LJOB/.lease"

# An empty slot made LAST, so nothing has aged it: fresh, and not to be deleted.
FRESH=$R1/slot-fresh
mkdir -p "$FRESH/jobs"

# HYGIENE_MIN_FREE_G forces the low-disk branch, so rule 6 is exercised on a bench with
# room to spare: with a lease live, the cache stays.
DRY=$(HOME="$TMP" HYGIENE_MIN_FREE_G=999999 "$SCRIPT" run --dry-run 2>&1)
if printf '%s\n' "$DRY" | grep -qE "WOULD (delete-job|DEAD) $OLD/jobs/job-old\$"; then
  ok "the dry run proposes the 8 h unleased job"
else
  bad "the dry run does not propose $OLD/jobs/job-old [$DRY]"
fi
for p in "$BARE1" "$BARE2" "$LJOB" "$LEASED/data" "$LEASED/tmp" "$YOUNG/jobs/job-young" "$FRESH" "$CACHE"; do
  if printf '%s\n' "$DRY" | grep -qF " $p"; then
    bad "the dry run proposes $p, which must survive"
  else
    ok "the dry run leaves $p alone"
  fi
done
printf '%s\n' "$DRY" | sed 's/^/# /'
kept "$BARE1/corpus/data.txt" "the dry run changed nothing on disk"

REAL=$(HOME="$TMP" HYGIENE_MIN_FREE_G=999999 "$SCRIPT" run 2>&1)

# (a)
kept "$BARE1/corpus/data.txt" "a bare directory under $R1 survives a run"
kept "$BARE2/REPORT.md" "a bare directory under $R2 survives a run"
# (b)
kept "$LJOB/harness-output.log" "a leased job quiet for 8 h is never touched"
kept "$LEASED/data" "the leased card's HOME survives"
kept "$LEASED/tmp/job-quiet" "the leased card's TMPDIR survives"
kept "$LEASED" "the leased slot survives"
# (c)
kept "$YOUNG/jobs/job-young/harness-output.log" "an unleased job quiet 2 h survives"
gone "$OLD/jobs/job-old" "an unleased job quiet 8 h is deleted"
gone "$HARV/jobs/job-read" "a harvested job is deleted"
# (d)
kept "$FRESH" "a freshly emptied slot is not deleted"
gone "$STALE" "an empty slot quiet 8 h is deleted"
# (e)
kept "$CACHE/trim.txt.d/x" "the build cache is kept while a lease is live"

# Every deletion is one dated line: <utc> <verb> <path>.
LOG=$TMP/hygiene.log
if [ -s "$LOG" ] && ! grep -vqE '^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z (reap|DEAD|delete-job|delete-slot|drop-cache|REFUSE) ' "$LOG"; then
  ok "every hygiene.log line is <utc> <verb> <path>"
else
  bad "hygiene.log is missing or malformed [$(cat "$LOG" 2>&1)]"
fi
if grep -qE "^[0-9-]+T[0-9:]+Z (DEAD|delete-job) $OLD/jobs/job-old\$" "$LOG" 2>/dev/null; then
  ok "the deletion of the 8 h job is one dated line"
else
  bad "no dated line for $OLD/jobs/job-old [$(cat "$LOG" 2>&1)] [$REAL]"
fi

# THE LEASE IS NOT A PERMANENT SHIELD. The launcher is gone and the heartbeat is stale: the
# same job and the same cache go on the next pass.
kill "$SLEEP_PID" 2>/dev/null
wait "$SLEEP_PID" 2>/dev/null
SLEEP_PID=
age $H8 "$LJOB/.lease" "$LJOB/harness-output.log" "$LJOB" "$LEASED/jobs" "$LEASED"
SECOND=$(HOME="$TMP" HYGIENE_MIN_FREE_G=999999 "$SCRIPT" run 2>&1)
gone "$LJOB" "a job whose lease is dead and stale is deleted"
gone "$CACHE" "the build cache is dropped once no lease is live"
kept "$BARE1/corpus/data.txt" "the bare directory still survives the second pass"
printf '%s\n' "$SECOND" | sed 's/^/# /'

exit "$fails"
