#!/usr/bin/env bash
# Hostile-input test of bench-hygiene.sh inside a fake HOME. A canary outside the roots must survive every case.
set -u; H=$(mktemp -d /tmp/hygtest.XXXXXX)/home; mkdir -p "$H"; S=${HYGIENE_SCRIPT:-$HOME/.local/bin/bench-hygiene.sh} # HYGIENE_SCRIPT=<path> tests a copy in a worktree (Emma, #1263)
R1=$H/rowan-swarm-root; R2=$H/rowan-working/tmp; mkdir -p "$R1" "$R2" "$H/canary/keep" "$H/.cache/go-build/x"; echo precious > "$H/canary/keep/file"
mk() { mkdir -p "$1/jobs/$2"; echo log > "$1/jobs/$2/harness-output.log"; }
mk "$R2/slot-ok" job1; echo "RESULT: x" > "$R2/slot-ok/jobs/job1/RESULT.md"; touch "$R2/slot-ok/jobs/job1/.harvested"; mkdir -p "$R2/slot-ok/data"
mk "$R2/slot-unread" jobX                                   # not harvested, fresh: must stay
ln -s "$H/canary" "$R2/slot-symlink"                        # slot that is a symlink to outside
mk "$R2/slot-symjob" real; ln -s "$H/canary/keep" "$R2/slot-symjob/jobs/evil"   # job that is a symlink to outside
mkdir -p "$R2/slot with space/jobs/j"; mkdir -p "$R2/-dash/jobs/j"
run() { HOME="$H" bash "$S" "$@" 2>&1 | head -3 | cut -c1-140; }
pass=0; fail=0; chk() { if [ "$2" = "$3" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "  FAIL: $1 (want $2 got $3)"; fi; }
canary() { [ -f "$H/canary/keep/file" ] && echo alive || echo DEAD; }
echo "-- hostile slot names"; for n in "" "." ".." "/" "a/b" "../canary" "slot with space" "-dash" "slot-symlink" "$H/canary" "slot-ok/../../canary"; do out=$(run reap "$n"); chk "reap [$n] refused" yes "$(echo "$out" | grep -qi 'refus\|usage\|not a slot' && echo yes || echo no:"$out")"; chk "canary after reap [$n]" alive "$(canary)"; done
echo "-- hostile job names"; for j in "" ".." "../.." "evil" "a/b" "../../canary"; do out=$(run delete-job slot-symjob "$j"); chk "delete-job [$j] leaves canary" alive "$(canary)"; done
chk "symlink job target intact" yes "$([ -d "$H/canary/keep" ] && echo yes || echo no)"
echo "-- delete-slot with jobs remaining"; out=$(run delete-slot slot-unread); chk "delete-slot refused" yes "$(echo "$out" | grep -qi refus && echo yes || echo "no:$out")"
echo "-- dry run changes nothing"; before=$(find "$H" | wc -l); run run --dry-run >/dev/null; chk "dry-run file count" "$before" "$(find "$H" | wc -l)"
echo "-- real run"; out=$(run run); echo "  $out"; chk "harvested job deleted" gone "$([ -d "$R2/slot-ok/jobs/job1" ] && echo there || echo gone)"; chk "unread fresh job kept" there "$([ -d "$R2/slot-unread/jobs/jobX" ] && echo there || echo gone)"; chk "canary after run" alive "$(canary)"; chk "symlink slot untouched" yes "$([ -L "$R2/slot-symlink" ] && echo yes || echo no)"
echo "-- bad HOME"; for bad in "" "/" "relative/home" "/onlyone"; do out=$(HOME="$bad" bash "$S" run --dry-run 2>&1 | head -1); chk "HOME=[$bad] refused" yes "$(echo "$out" | grep -q REFUSE && echo yes || echo "no:$out")"; done
echo "-- log written"; chk "hygiene.log has lines" yes "$([ -s "$H/hygiene.log" ] && echo yes || echo no)"; echo "  refusals logged: $(grep -c REFUSE "$H/hygiene.log")"
echo "RESULT pass=$pass fail=$fail"; chmod -R u+w "$(dirname "$H")"; rm -rf -- "$(dirname "$H")"; exit $(( fail > 0 ))
