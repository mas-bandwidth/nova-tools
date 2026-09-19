#!/usr/bin/env bash
# bench-hygiene.sh: mechanical clean-as-we-work on a bench, as verbs (Glenn 2026-09-17: "It shouldn't be
# able to delete arbitrary directories. It should just be verbs").
#   bench-hygiene run [--dry-run]     the timer's verb: reap dead slots, delete read jobs, drop the build cache when low; one HYGIENE line
#   bench-hygiene reap <slot>         delete <slot>/data <slot>/tmp and each job's scratch
#   bench-hygiene delete-job <slot> <job>   delete <root>/<slot>/jobs/<job> whole (only when .harvested or unread for 6 h)
#   bench-hygiene delete-slot <slot>  delete an empty slot
#   bench-hygiene drop-cache          delete $HOME/.cache/go-build
#   bench-hygiene log [n]             the last n lines of the per-bench action log
# Every deletion is one line in $HOME/hygiene.log: <utc> <verb> <path>. Safety by construction: a path is
# never built from user text; it is the join of one of two literal roots under $HOME, a slot name and a
# job name that match [A-Za-z0-9._-]+, resolved, checked for symlinks, and checked to sit strictly below
# its root. `run` also reaps DEAD jobs: a job dir with no RESULT.md whose harness-output.log is older
# than 30 minutes and that no live process names as cwd or in its command line (an outage killed it).
set -u; set -o pipefail
LOG=$HOME/hygiene.log; DRY=0; case "${2:-}${3:-}${4:-}" in *--dry-run*) DRY=1;; esac; [ "${1:-}" = run ] && [ "${2:-}" = --dry-run ] && DRY=1
ROOT1=$HOME/rowan-swarm-root; ROOT2=$HOME/rowan-working/tmp
name_ok() { case "$1" in ''|.|..|*/*|-*) return 1;; esac; printf %s "$1" | grep -qE '^[A-Za-z0-9._-]+$'; }
log() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*" >> "$LOG"; }
refuse() { log "REFUSE $1 ($2)"; printf 'REFUSED %s (%s)\n' "$1" "$2" >&2; return 2; }
# PORTABLE PRIMITIVES. The bench is Linux, the class test runs where the developer is, and
# `realpath -e`, `stat -c` and `df -BG` are GNU spellings a BSD userland refuses outright.
# A refusing helper made the whole script a no-op off Linux, which is a test that proves
# nothing. resolve() is realpath -e for a directory (cd -P) and, for a file, its parent
# resolved with the name appended, so the caller's own symlink test still decides.
resolve() { local p=$1 d b
  [ -e "$p" ] || return 1
  if [ -d "$p" ]; then ( cd -P -- "$p" 2>/dev/null && pwd -P ) || return 1
  else d=$(dirname -- "$p"); b=$(basename -- "$p"); d=$( cd -P -- "$d" 2>/dev/null && pwd -P ) || return 1; printf '%s/%s' "$d" "$b"; fi; }
mtime() { stat -c %Y -- "$1" 2>/dev/null || stat -f %m -- "$1" 2>/dev/null; }
under_root() { local p r; r=$(resolve "$2") || return 1; p=$(resolve "$1") || return 1; [ -L "$1" ] && return 1; case "$p" in "$r"/*) ;; *) return 1;; esac; [ "$p" != "$r" ]; }
slot_path() { name_ok "$1" || return 1; local r; for r in "$ROOT1" "$ROOT2"; do [ -d "$r/$1" ] && under_root "$r/$1" "$r" && { printf %s "$r/$1"; return 0; }; done; return 1; }
path_live() { local p=$1 pid cwd; pgrep -f -- "$p" >/dev/null 2>&1 && return 0; for pid in /proc/[0-9]*; do cwd=$(readlink -- "$pid/cwd" 2>/dev/null) || continue; case "$cwd" in "$p"|"$p"/*) return 0;; esac; done; return 1; }
remove() { local verb=$1 p=$2 root; case "$p" in "$ROOT1"/*) root=$ROOT1;; "$ROOT2"/*) root=$ROOT2;; "$HOME/.cache/go-build") root=$HOME/.cache;; *) refuse "$verb $p: outside the roots" "delete only paths under $ROOT1, $ROOT2, or the go build cache"; return 2;; esac
  under_root "$p" "$root" || { refuse "$verb $p: not strictly below $root, or a symlink" "pass a real, non-symlink path inside the root"; return 2; }
  if [ $DRY = 1 ]; then echo "WOULD $verb $p"; else log "$verb $p"; chmod -R u+w -- "$p" 2>/dev/null; rm -rf -- "$p" 2>>"$LOG"; fi; }
v_reap() { local slot=${1:-} p d; name_ok "$slot" || { refuse "reap: bad slot name '$slot'" "use a slot matching [A-Za-z0-9._-]+"; return 2; }; p=$(slot_path "$slot") || { refuse "reap: no such slot '$slot'" "create the slot under $ROOT1 or $ROOT2"; return 2; }
  for d in "$p/data" "$p/tmp"; do [ -e "$d" ] && remove reap "$d"; done
  for d in "$p"/jobs/*/scratch "$p"/jobs/*/.nova-sandbox-tmp "$p"/jobs/*/repo/scratch; do { [ -e "$d" ] || [ -L "$d" ]; } && remove reap "$d"; done; return 0; }
v_delete_job() { local slot=${1:-} job=${2:-} p j now m; name_ok "$slot" || { refuse "delete-job: bad slot name '$slot'" "use a slot matching [A-Za-z0-9._-]+"; return 2; }; name_ok "$job" || { refuse "delete-job: bad job name '$job'" "use a job matching [A-Za-z0-9._-]+"; return 2; }
  p=$(slot_path "$slot") || { refuse "delete-job: no such slot '$slot'" "create the slot under $ROOT1 or $ROOT2"; return 2; }; j=$p/jobs/$job; [ -d "$j" ] || { refuse "delete-job: no such job '$job' in $slot" "check the job label under $p/jobs"; return 2; }
  if [ ! -f "$j/.harvested" ]; then now=$(date +%s); m=$(mtime "$j/harness-output.log") || { refuse "delete-job: $job is unharvested and has no log" "wait for .harvested, or for its log to age 6 h"; return 2; }; [ $(( (now - m) / 3600 )) -ge 6 ] || { refuse "delete-job: $job is not harvested and was read less than 6 h ago" "wait for .harvested, or for its log to age 6 h"; return 2; }; fi
  remove delete-job "$j"; }
v_delete_slot() { local slot=${1:-} p; name_ok "$slot" || { refuse "delete-slot: bad slot name '$slot'" "use a slot matching [A-Za-z0-9._-]+"; return 2; }; p=$(slot_path "$slot") || { refuse "delete-slot: no such slot '$slot'" "create the slot under $ROOT1 or $ROOT2"; return 2; }
  [ -z "$(ls -A "$p/jobs" 2>/dev/null)" ] || { refuse "delete-slot: $slot still has jobs" "delete its jobs first with delete-job"; return 2; }
  remove delete-slot "$p"; }
v_drop_cache() { [ -e "$HOME/.cache/go-build" ] || { refuse "drop-cache: no $HOME/.cache/go-build" "there is nothing to drop"; return 2; }; remove drop-cache "$HOME/.cache/go-build"; }
v_log() { local n=${1:-20}; case "$n" in ''|*[!0-9]*) refuse "log: bad count '$n'" "pass a non-negative line count"; return 2;; esac; tail -n "$n" -- "$LOG" 2>/dev/null; }
v_run() {
  local slots=0 reaped=0 jobs=0 dropped=0 dead=0 cache=kept
  local now before free_g size_g r s j m live newest_j pid cwd w
  now=$(date +%s); before=$(df -h "$HOME" 2>/dev/null | awk 'NR==2{print $4}')
  for r in "$ROOT1" "$ROOT2"; do
    for s in "$r"/*/; do
      s=${s%/}; [ -d "$s" ] || continue; slots=$((slots+1))
      # DEAD: no RESULT.md, a harness-output.log older than 30 min, and no process naming its path.
      # Scanned before the slot liveness test so a dead job beside a live one is still reaped.
      for j in "$s"/jobs/*/; do
        j=${j%/}; [ -d "$j" ] || continue
        [ -f "$j/RESULT.md" ] && continue
        [ -f "$j/harness-output.log" ] || continue
        m=$(mtime "$j/harness-output.log"); [ -n "$m" ] || continue
        [ $(( (now - m) / 60 )) -ge 30 ] || continue
        path_live "$j" && continue
        remove DEAD "$j" && dead=$((dead+1))
      done
      live=0; newest_j=0
      for j in "$s"/jobs/*/; do
        j=${j%/}; [ -d "$j" ] || continue
        if [ -f "$j/harness-output.log" ]; then m=$(mtime "$j/harness-output.log"); [ -n "$m" ] || m=0; [ "$m" -gt "$newest_j" ] && newest_j=$m; [ ! -f "$j/RESULT.md" ] && [ $(( (now - m) / 60 )) -lt 15 ] && live=1; fi
        pgrep -f -- "$j" >/dev/null 2>&1 && live=1
      done
      [ $live = 0 ] && for pid in /proc/[0-9]*; do cwd=$(readlink -- "$pid/cwd" 2>/dev/null) || continue; case "$cwd" in "$s"|"$s"/*) live=1; break;; esac; done
      [ $live = 1 ] && continue
      if [ -d "$s/data" ] || [ -d "$s/tmp" ]; then
        for j in "$s/data" "$s/tmp"; do [ -e "$j" ] && remove reap "$j"; done
        for j in "$s"/jobs/*/scratch "$s"/jobs/*/.nova-sandbox-tmp "$s"/jobs/*/repo/scratch; do { [ -e "$j" ] || [ -L "$j" ]; } && remove reap "$j"; done
        reaped=$((reaped+1))
      fi
      for j in "$s"/jobs/*/; do
        j=${j%/}; [ -d "$j" ] || continue
        if [ -f "$j/.harvested" ] || { [ "$newest_j" -gt 0 ] && [ $(( (now - newest_j) / 3600 )) -ge 6 ]; }; then remove delete-job "$j" && jobs=$((jobs+1)); fi
      done
      if [ -z "$(ls -A "$s/jobs" 2>/dev/null)" ]; then remove delete-slot "$s" && dropped=$((dropped+1)); fi
    done
  done
  for w in "$HOME"/runner-nova-tools-*/_work/_temp; do [ -d "$w" ] || continue; find "$w" -mindepth 1 -maxdepth 1 -mmin +1440 -exec rm -rf {} + 2>/dev/null; done
  free_g=$(df -BG "$HOME" 2>/dev/null | awk 'NR==2{gsub("G","",$4); print $4}')
  [ -n "${free_g:-}" ] || free_g=$(df -g "$HOME" 2>/dev/null | awk 'NR==2{print $4}')
  size_g=$(du -sBG "$HOME/.cache/go-build" 2>/dev/null | awk '{gsub("G","",$1); print $1}')
  [ -n "${size_g:-}" ] || size_g=$(du -sg "$HOME/.cache/go-build" 2>/dev/null | awk '{print $1}'); size_g=${size_g:-0}
  if [ "${free_g:-999}" -lt 25 ] || [ "$size_g" -gt 20 ]; then [ -e "$HOME/.cache/go-build" ] && remove drop-cache "$HOME/.cache/go-build"; cache=dropped; fi
  echo "HYGIENE $(hostname -s) slots=$slots reaped=$reaped jobs-deleted=$jobs slots-deleted=$dropped dead=$dead cache=$cache(${size_g}G) free $before -> $(df -h "$HOME" 2>/dev/null | awk 'NR==2{print $4}')"
}
case "${1:-}" in
  run)         v_run ;;
  reap)        v_reap "${2:-}" ;;
  delete-job)  v_delete_job "${2:-}" "${3:-}" ;;
  delete-slot) v_delete_slot "${2:-}" ;;
  drop-cache)  v_drop_cache ;;
  log)         v_log "${2:-}" ;;
  *) printf 'REFUSED bench-hygiene: unknown verb %s (use run, reap, delete-job, delete-slot, drop-cache, or log)\n' "${1:-}" >&2; exit 2 ;;
esac
