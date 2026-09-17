#!/usr/bin/env bash
# bench-hygiene.sh: mechanical clean-as-we-work on a bench (Glenn 2026-09-17: a job works in working/tmp/<guid>; when done it is read, then deleted).
# Runs from a systemd timer every 10 min; needs nothing from the Studio. One line per run:
# HYGIENE <host> slots=<n> reaped=<n> jobs-deleted=<n> slots-deleted=<n> cache=<kept|dropped>(<size>) free <a> -> <b>
set -u; export PATH=$HOME/go/bin:/usr/local/go/bin:$HOME/nova-bench/sdk/go1.26.5/bin:$PATH
ROOTS="$HOME/rowan-swarm-root $HOME/rowan-working/tmp"; now=$(date +%s); before=$(df -h "$HOME" | awk 'NR==2{print $4}')
slots=0; reaped=0; jobs=0; dropped=0
for s in $(for r in $ROOTS; do ls -d "$r"/*/ 2>/dev/null; done); do s=${s%/}; [ -d "$s" ] || continue; slots=$((slots+1)); live=0; newest=0
  for j in "$s"/jobs/*/; do j=${j%/}; [ -d "$j" ] || continue
    if [ -f "$j/harness-output.log" ]; then m=$(stat -c %Y "$j/harness-output.log"); [ $m -gt $newest ] && newest=$m; [ ! -f "$j/RESULT.md" ] && [ $(( (now - m) / 60 )) -lt 15 ] && live=1; fi
  done
  [ $live = 1 ] && continue
  # finished or dead: the slot's data home, tmp and scratch go at once
  { [ -d "$s/data" ] || [ -d "$s/tmp" ]; } && { rm -rf "$s/data" "$s/tmp" "$s"/jobs/*/scratch "$s"/jobs/*/.nova-sandbox-tmp "$s"/jobs/*/repo/scratch 2>/dev/null; reaped=$((reaped+1)); }
  # read, then delete: a harvested job goes whole; a finished job nobody read goes after six hours; an empty slot goes
  for j in "$s"/jobs/*/; do j=${j%/}; [ -d "$j" ] || continue
    if [ -f "$j/.harvested" ] || { [ $newest -gt 0 ] && [ $(( (now - newest) / 3600 )) -ge 6 ]; }; then rm -rf "$j"; jobs=$((jobs+1)); fi
  done
  if [ -z "$(ls -A "$s/jobs" 2>/dev/null)" ]; then rm -rf "$s"; dropped=$((dropped+1)); fi
done
# runner scratch older than a day; the checked-out trees stay (the runner reuses them)
for w in "$HOME"/runner-nova-tools-*/_work/_temp; do [ -d "$w" ] && find "$w" -mindepth 1 -maxdepth 1 -mmin +1440 -exec rm -rf {} + 2>/dev/null; done
# the shared Go build cache grows ~30 GB a day under the runners: drop it below 25 GB free or above 20 GB in size
cache=kept; free_g=$(df -BG "$HOME" | awk 'NR==2{gsub("G","",$4); print $4}'); size_g=$(du -sBG "$HOME/.cache/go-build" 2>/dev/null | awk '{gsub("G","",$1); print $1}'); size_g=${size_g:-0}
if [ "$free_g" -lt 25 ] || [ "$size_g" -gt 20 ]; then rm -rf "$HOME/.cache/go-build"; cache=dropped; fi
echo "HYGIENE $(hostname -s) slots=$slots reaped=$reaped jobs-deleted=$jobs slots-deleted=$dropped cache=$cache(${size_g}G) free $before -> $(df -h "$HOME" | awk 'NR==2{print $4}')"
