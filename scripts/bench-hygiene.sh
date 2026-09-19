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
# its root.
#
# WHAT MAY BE DELETED, after the two certify trees this script ate on antman and space (issue #1499):
#   * A SLOT IS A SHAPE THE LAUNCHER MAKES: <slot>/jobs. A directory under a root without one is
#     somebody's work -- a certify tree, a toolchain, a clone -- and is skipped entirely, at any age.
#   * A LIVE THING IS NEVER JUDGED DEAD BY QUIET. Liveness is the launcher's own lease, <job>/.lease,
#     written by `nova-swarm native` with its pid and heartbeated while the child runs. A leased job,
#     and the slot around it, is never touched, however long it has been silent: a card in a long model
#     call or a long compile says nothing for an hour and is working. pgrep and a process cwd are a
#     SECOND REASON TO KEEP, never a reason to delete.
#   * AGE IS REQUIRED BEFORE ANY DELETION. An unleased job is deleted when it is harvested, or when
#     nothing in it has changed for 6 h (DEAD when it left no RESULT.md); an emptied slot when it too
#     has been quiet 6 h.
#   * THE BUILD CACHE IS NEVER DROPPED WHILE ANY LEASE IS LIVE: that is a build cut out from under a
#     running card.
# The bench may set HYGIENE_MIN_FREE_G (default 25), HYGIENE_MAX_CACHE_G (20) and
# HYGIENE_LEASE_STALE_MIN (10, how old a heartbeat may be before the lease stops counting).
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
LEASE_STALE_MIN=${HYGIENE_LEASE_STALE_MIN:-10}; MIN_FREE_G=${HYGIENE_MIN_FREE_G:-25}; MAX_CACHE_G=${HYGIENE_MAX_CACHE_G:-20}; MIN_AGE_H=6
pid_alive() { case "$1" in ''|*[!0-9]*) return 1;; esac; kill -0 "$1" 2>/dev/null; }
# THE LEASE IS THE ONLY WORD ON LIVENESS. <job>/.lease carries `pid=<the launcher's pid>` and its
# mtime is the heartbeat. Live when the pid is alive, or when the heartbeat is younger than
# LEASE_STALE_MIN minutes (a bench whose pids the reaper cannot see still has a heartbeat).
job_leased() { local j=$1 f pid m now; f=$j/.lease; [ -f "$f" ] || return 1
  pid=$(sed -n 's/^pid=\([0-9][0-9]*\).*/\1/p' "$f" 2>/dev/null | head -1)
  pid_alive "$pid" && return 0
  m=$(mtime "$f"); [ -n "$m" ] || return 1; now=$(date +%s); [ $(( (now - m) / 60 )) -lt "$LEASE_STALE_MIN" ]; }
slot_leased() { local s=$1 j; for j in "$s"/jobs/*/; do j=${j%/}; [ -d "$j" ] || continue; job_leased "$j" && return 0; done; return 1; }
any_leased() { local r s; for r in "$ROOT1" "$ROOT2"; do for s in "$r"/*/; do s=${s%/}; [ -d "$s" ] || continue; slot_leased "$s" && return 0; done; done; return 1; }
# A SLOT IS WHAT THE LAUNCHER MAKES: <slot>/jobs, made before the child starts. Nothing else is one.
slot_shaped() { [ -d "$1/jobs" ] && [ ! -L "$1/jobs" ]; }
# newest_mtime: the newest mtime of a directory and its direct entries -- where a job's own files
# (harness-output.log, RESULT.md, .lease) and a slot's own (jobs, data, tmp, native.log) sit.
newest_mtime() { local p=$1 best e m; best=$(mtime "$p"); best=${best:-0}
  for e in "$p"/* "$p"/.[!.]*; do [ -e "$e" ] || continue; m=$(mtime "$e"); [ -n "$m" ] || continue; [ "$m" -gt "$best" ] && best=$m; done
  printf %s "$best"; }
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
  slot_shaped "$p" || { refuse "delete-slot: $slot is not a slot (no jobs directory)" "this verb deletes slots; move or remove other directories yourself"; return 2; }
  [ -z "$(ls -A "$p/jobs" 2>/dev/null)" ] || { refuse "delete-slot: $slot still has jobs" "delete its jobs first with delete-job"; return 2; }
  remove delete-slot "$p"; }
v_drop_cache() { [ -e "$HOME/.cache/go-build" ] || { refuse "drop-cache: no $HOME/.cache/go-build" "there is nothing to drop"; return 2; }; remove drop-cache "$HOME/.cache/go-build"; }
v_log() { local n=${1:-20}; case "$n" in ''|*[!0-9]*) refuse "log: bad count '$n'" "pass a non-negative line count"; return 2;; esac; tail -n "$n" -- "$LOG" 2>/dev/null; }
v_run() {
  local slots=0 reaped=0 jobs=0 dropped=0 dead=0 cache=kept
  local now before free_g size_g r s j m live slot_age w
  now=$(date +%s); before=$(df -h "$HOME" 2>/dev/null | awk 'NR==2{print $4}')
  for r in "$ROOT1" "$ROOT2"; do
    for s in "$r"/*/; do
      s=${s%/}; [ -d "$s" ] || continue
      # RULE 5, ASKED FIRST: what this tool does not recognise as a slot, it does not touch --
      # not its jobs, not its scratch, not the directory. This is the certify trees' rule.
      slot_shaped "$s" || continue
      slots=$((slots+1))
      # The slot's age is read BEFORE anything under it is deleted: deleting a job touches jobs/,
      # and an age read afterwards would say "just now" of a slot that has been idle for days.
      slot_age=$(( (now - $(newest_mtime "$s")) / 3600 ))
      # RULE 3, PER JOB. A leased job is live and is never touched, however quiet. A process that
      # names it or sits in it keeps it too. What is left is deleted only when harvested, or when
      # NOTHING in it has changed for MIN_AGE_H hours -- DEAD when it left no RESULT.md.
      for j in "$s"/jobs/*/; do
        j=${j%/}; [ -d "$j" ] || continue
        job_leased "$j" && continue
        path_live "$j" && continue
        if [ -f "$j/.harvested" ]; then remove delete-job "$j" && jobs=$((jobs+1)); continue; fi
        m=$(newest_mtime "$j"); [ "${m:-0}" -gt 0 ] || continue
        [ $(( (now - m) / 3600 )) -ge "$MIN_AGE_H" ] || continue
        if [ -f "$j/RESULT.md" ]; then remove delete-job "$j" && jobs=$((jobs+1)); else remove DEAD "$j" && dead=$((dead+1)); fi
      done
      # The slot's own scratch -- <slot>/data is a card's HOME and <slot>/tmp its TMPDIR -- goes only
      # when nothing in the slot is live at all.
      live=0; slot_leased "$s" && live=1
      [ $live = 0 ] && path_live "$s" && live=1
      [ $live = 1 ] && continue
      if [ -d "$s/data" ] || [ -d "$s/tmp" ]; then
        for j in "$s/data" "$s/tmp"; do [ -e "$j" ] && remove reap "$j"; done
        for j in "$s"/jobs/*/scratch "$s"/jobs/*/.nova-sandbox-tmp "$s"/jobs/*/repo/scratch; do { [ -e "$j" ] || [ -L "$j" ]; } && remove reap "$j"; done
        reaped=$((reaped+1))
      fi
      # RULE 5: a recognised slot, emptied of jobs, and quiet for MIN_AGE_H hours.
      if [ -z "$(ls -A "$s/jobs" 2>/dev/null)" ] && [ "$slot_age" -ge "$MIN_AGE_H" ]; then
        remove delete-slot "$s" && dropped=$((dropped+1))
      fi
    done
  done
  for w in "$HOME"/runner-nova-tools-*/_work/_temp; do [ -d "$w" ] || continue; find "$w" -mindepth 1 -maxdepth 1 -mmin +1440 -exec rm -rf {} + 2>/dev/null; done
  free_g=$(df -BG "$HOME" 2>/dev/null | awk 'NR==2{gsub("G","",$4); print $4}')
  [ -n "${free_g:-}" ] || free_g=$(df -g "$HOME" 2>/dev/null | awk 'NR==2{print $4}')
  size_g=$(du -sBG "$HOME/.cache/go-build" 2>/dev/null | awk '{gsub("G","",$1); print $1}')
  [ -n "${size_g:-}" ] || size_g=$(du -sg "$HOME/.cache/go-build" 2>/dev/null | awk '{print $1}'); size_g=${size_g:-0}
  if [ "${free_g:-999}" -lt "$MIN_FREE_G" ] || [ "$size_g" -gt "$MAX_CACHE_G" ]; then
    # RULE 6. Dropping the cache under a running card is that card's build cut out from under it:
    # while any lease is live the cache stays, and the line says why.
    if any_leased; then cache=kept-lease
    else [ -e "$HOME/.cache/go-build" ] && remove drop-cache "$HOME/.cache/go-build"; cache=dropped; fi
  fi
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
