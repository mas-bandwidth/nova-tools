#!/usr/bin/env bash
# bench-hygiene.sh: mechanical clean-as-we-work on a bench, as verbs (Glenn 2026-09-17: "It shouldn't be able to delete arbitrary directories. It should just be verbs").
#   bench-hygiene run [--dry-run]     the timer's verb: reap dead slots, delete read jobs, drop the build cache when low; one HYGIENE line
#   bench-hygiene reap <slot>         delete <slot>/data <slot>/tmp and each job's scratch (slot = a dir name directly under a root)
#   bench-hygiene delete-job <slot> <job>   delete <root>/<slot>/jobs/<job> whole (only when .harvested or unread for 6 h)
#   bench-hygiene delete-slot <slot>  delete an empty slot
#   bench-hygiene drop-cache          delete $HOME/.cache/go-build
#   bench-hygiene log [n]             the last n lines of the per-bench action log
# Every deletion is one line in $HOME/hygiene.log: <utc> <verb> <path>. Safety by construction: a path is never built from user text;
# it is the join of one of two literal roots under $HOME, a slot name and a job name that match [A-Za-z0-9._-]+, resolved, checked for symlinks,
# and checked to sit strictly below its root. Nothing else can be removed by this script.
set -u; set -o pipefail
[ "$(uname -s)" = Linux ] || { echo "HYGIENE REFUSED: this script is for the Linux benches (GNU stat, df -BG, du -BG, /proc); on $(uname -s) it would abort half way (Emma, #1263)"; exit 2; }
LOG=$HOME/hygiene.log; DRY=0; case "${2:-}${3:-}${4:-}" in *--dry-run*) DRY=1;; esac; [ "${1:-}" = run ] && [ "${2:-}" = --dry-run ] && DRY=1
[ -n "${HOME:-}" ] && [ "${HOME#/}" != "$HOME" ] && [ "$(printf %s "$HOME" | tr -cd / | wc -c)" -ge 2 ] || { echo "REFUSE: HOME is not an absolute path with at least two components: '${HOME:-}'"; exit 2; }
ROOT1=$HOME/rowan-swarm-root; ROOT2=$HOME/rowan-working/tmp
name_ok() { case "$1" in ''|.|..|*/*|-*) return 1;; esac; printf %s "$1" | grep -qE '^[A-Za-z0-9._-]+$'; }
log() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >> "$LOG"; }
# under_root PATH ROOT: PATH resolves (no symlink at any component), is strictly below ROOT, and ROOT itself resolves to a dir
under_root() { local p r; r=$(realpath -e -- "$2" 2>/dev/null) || return 1; p=$(realpath -e -- "$1" 2>/dev/null) || return 1; [ -L "$1" ] && return 1; case "$p" in "$r"/*) ;; *) return 1;; esac; [ "$p" != "$r" ]; }
# slot_path SLOT -> prints <root>/<slot> for the root that has it, or fails
slot_path() { name_ok "$1" || return 1; local r; for r in "$ROOT1" "$ROOT2"; do [ -d "$r/$1" ] && under_root "$r/$1" "$r" && { printf %s "$r/$1"; return 0; }; done; return 1; }
remove() { # remove VERB PATH: the only rm in this file
  local verb=$1 p=$2 root; case "$p" in "$ROOT1"/*) root=$ROOT1;; "$ROOT2"/*) root=$ROOT2;; "$HOME/.cache/go-build") root=$HOME/.cache;; *) log "REFUSE $verb $p (outside the roots)"; return 1;; esac
  under_root "$p" "$root" || { log "REFUSE $verb $p (not strictly below $root, or a symlink)"; return 1; }
  if [ $DRY = 1 ]; then echo "WOULD $verb $p"; else log "$verb $p"; chmod -R u+w -- "$p" 2>/dev/null; rm -rf -- "$p" 2>>"$LOG"; fi; }
v_reap() { local s; s=$(slot_path "$1") || { echo "REAP REFUSED: not a slot: $1"; return 2; }; local j; for j in "$s"/jobs/*/; do j=${j%/}; [ -d "$j" ] || continue; name_ok "$(basename "$j")" || continue; [ -d "$j/scratch" ] && remove reap "$j/scratch"; [ -d "$j/.nova-sandbox-tmp" ] && remove reap "$j/.nova-sandbox-tmp"; [ -d "$j/repo/scratch" ] && remove reap "$j/repo/scratch"; done; [ -d "$s/data" ] && remove reap "$s/data"; [ -d "$s/tmp" ] && remove reap "$s/tmp"; return 0; }
v_delete_job() { local s; s=$(slot_path "$1") || { echo "DELETE-JOB REFUSED: not a slot: $1"; return 2; }; name_ok "$2" || { echo "DELETE-JOB REFUSED: bad job name"; return 2; }; [ -d "$s/jobs/$2" ] || { echo "DELETE-JOB REFUSED: no such job"; return 2; }; remove delete-job "$s/jobs/$2"; }
v_delete_slot() { local s; s=$(slot_path "$1") || { echo "DELETE-SLOT REFUSED: not a slot: $1"; return 2; }; [ -z "$(ls -A "$s/jobs" 2>/dev/null)" ] || { echo "DELETE-SLOT REFUSED: jobs remain in $1"; return 2; }; remove delete-slot "$s"; }
v_drop_cache() { [ -d "$HOME/.cache/go-build" ] && remove drop-cache "$HOME/.cache/go-build"; return 0; }
live_slot() { # a slot is live when any job's harness log is under 15 min old without RESULT.md, or any process names the slot in its cmdline or cwd
  local s=$1 j m now; now=$(date +%s); for j in "$s"/jobs/*/; do j=${j%/}; [ -f "$j/harness-output.log" ] || continue; m=$(stat -c %Y "$j/harness-output.log"); [ ! -f "$j/RESULT.md" ] && [ $(( (now - m) / 60 )) -lt 15 ] && return 0; done
  pgrep -f -- "$s" >/dev/null 2>&1 && return 0; local pid; for pid in $(ls /proc 2>/dev/null | grep -E '^[0-9]+$'); do case "$(readlink /proc/$pid/cwd 2>/dev/null)" in "$s"*) return 0;; esac; done; return 1; }
newest_log() { local s=$1 j m n=0; for j in "$s"/jobs/*/; do j=${j%/}; [ -f "$j/harness-output.log" ] && { m=$(stat -c %Y "$j/harness-output.log"); [ $m -gt $n ] && n=$m; }; done; echo $n; }
v_run() { local before slots=0 reaped=0 jobs=0 dropped=0 cache=kept r s j now newest; before=$(df -h "$HOME" | awk 'NR==2{print $4}'); now=$(date +%s)
  for r in "$ROOT1" "$ROOT2"; do [ -d "$r" ] || continue; for s in "$r"/*/; do s=${s%/}; [ -d "$s" ] || continue; name_ok "$(basename "$s")" || continue; slots=$((slots+1)); live_slot "$s" && continue
      { [ -d "$s/data" ] || [ -d "$s/tmp" ]; } && { v_reap "$(basename "$s")" >/dev/null && reaped=$((reaped+1)); }
      newest=$(newest_log "$s"); for j in "$s"/jobs/*/; do j=${j%/}; [ -d "$j" ] || continue; if [ -f "$j/.harvested" ] || { [ "$newest" -gt 0 ] && [ $(( (now - newest) / 3600 )) -ge 6 ]; }; then v_delete_job "$(basename "$s")" "$(basename "$j")" >/dev/null && jobs=$((jobs+1)); fi; done
      [ -z "$(ls -A "$s/jobs" 2>/dev/null)" ] && [ ! -d "$s/data" ] && { v_delete_slot "$(basename "$s")" >/dev/null && dropped=$((dropped+1)); }
    done; done
  local free_g size_g; free_g=$(df -BG "$HOME" | awk 'NR==2{gsub("G","",$4); print $4}'); size_g=$(du -sBG "$HOME/.cache/go-build" 2>/dev/null | awk '{gsub("G","",$1); print $1}'); size_g=${size_g:-0}
  if [ "$free_g" -lt 25 ] || [ "$size_g" -gt 20 ]; then v_drop_cache && cache=dropped; fi
  local line="HYGIENE $(hostname -s) slots=$slots reaped=$reaped jobs-deleted=$jobs slots-deleted=$dropped cache=$cache(${size_g}G) free $before -> $(df -h "$HOME" | awk 'NR==2{print $4}')"; [ $DRY = 1 ] || log "$line"; echo "$line"; }
case "${1:-}" in run) v_run;; reap) v_reap "${2:-}";; delete-job) v_delete_job "${2:-}" "${3:-}";; delete-slot) v_delete_slot "${2:-}";; drop-cache) v_drop_cache;; log) tail -n "${2:-20}" "$LOG" 2>/dev/null;; *) sed -n '2,9p' "$0"; exit 2;; esac
