#!/usr/bin/env bash
# adopt.sh's run() must kill the whole process group at the deadline and pass exit codes through.
set -u; pass=0; fail=0; chk() { if [ "$2" = "$3" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "  FAIL: $1 (want [$2] got [$3])"; fi; }
eval "$(sed -n '/^run() {/p' "$HOME/rowan-working/bin/adopt.sh")"
s=$(date +%s); run 3 sh -c 'sleep 299 & exec sleep 299' >/dev/null; rc=$?; w=$(( $(date +%s) - s ))
chk "timeout exit code" 124 "$rc"; chk "returns near the deadline" yes "$([ $w -le 8 ] && echo yes || echo "no:${w}s")"; sleep 1; chk "no survivors in the group" 0 "$(pgrep -f "sleep 299" | wc -l | tr -d ' ')"
run 5 sh -c 'exit 7' >/dev/null; chk "exit code passes through" 7 "$?"; chk "stdout passes through" hello "$(run 5 sh -c 'echo hello')"
echo "RESULT pass=$pass fail=$fail"; exit $(( fail > 0 ))
