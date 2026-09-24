#!/usr/bin/env bash
# rowan-tools/bin since 2026-09-22 (Glenn: "Move the surviving bash into rowan-tools bin/ now."); $HOME/rowan-working/bin/sprint-table-redis is a symlink to this file; replaced by nova-tools #2561 (fleet state and the table as verbs).
# sprint-table-redis [interval]: Glenn's sprint table (host | queue | working | done | ok | fail | ok%) read from the fleet
# Redis, where every bench pushes its own row once a second (bin/bench-row). Zero ssh. A bench whose row expired is absent.
# Runs on the coordinator under its seat (viewer would do; the bench user has read on bench:* too).
#
# Friend rows (2026-09-22, Glenn 4:57 PM then 6:05 PM: "the whole table should grab from the recent data
# on redis"): a second block, same idea as the bench rows -- every cell is a Redis GET/EXISTS on the 1 s
# render, no gh, no cache file, no background refresh. friend-row writes each friend's row as ONE hash
# friend:<name> (at, up, ready, working, done; no TTL) every second, and the table shows its age
# (rt#229). A missing key prints "-", never a blank cell and never a crash.
#
# ROBUSTNESS (2026-09-22 7:07 PM, Glenn: "Please make the sprint table robust. I rely on it for
# visibility."); controls: sprint-table-control (live) and sprint-table-friends-control (fixtures).
#  (1) Edit-immune: the inner stage runs from a private copy of this script that unlinks itself
#      (bash reads scripts incrementally; an in-place edit corrupted running loops that day).
#  (2) One instance: a lock dir (~/nova-bench/run/sprint-table-redis.lockd, mkdir test-and-set,
#      pid inside, a dead or foreign holder taken over). A second copy -- by hand or a kickstart
#      race -- prints REFUSED and exits 3 at once; a loop whose lock no longer names it exits. Each
#      render goes to its own $OUT.<pid>.new then mv (atomic): two writers sharing one .new was a
#      way to publish a half-written, blank table.
#  (3) Nothing expires (friend-row, rt#229): the row's `up` and its age decide up/down/stale, the counts stay.
#  (4) Stale marker: under the friends block, `stale: ...` when the xy key is missing (sprint-xy has
#      not written for 180 s) or when Redis did not answer (the last good rows are shown, never blank).
#      A friend row's own age is in its status cell (#3299: no friend-beat stale line).
#  (5) Fast: one MGET for every friend cell, the xy line and the beat stamps; --scan with COUNT
#      1000 (the default COUNT 10 took 3.2 s over ~270 keys, measured 23:20Z); every HGETALL in one
#      redis-cli. The render had grown to ~8 s (25 connects at ~300 ms); now ~1 s.
# The bench block no longer eval's HGETALL output into shell variables: a field missing on one
# bench used to print the previous bench's value.
#
# BLOCKED | READY (nova-tools #3219; Glenn 2026-09-23 1:58 PM ET: "update the sprint table with
# `blocked: x` directly under SPRINT TABLE"; 1:40 PM: "there is no reason to maintain blocked
# per-row"): line 2 is the ONE global count, ZCARD q:blocked (friend-queue block/unblock/blocked
# write it), "blocked: ?" when Redis did not answer, never a carried number. The per-row count
# column that was "queue" is headed "ready": the same per-friend / per-bench queues, renamed.
set -u; IV=${1:-1}; NB=$HOME/nova-bench; OUT=${SPRINT_TABLE_OUT:-$HOME/rowan-working/tmp/session-0919b/SPRINT-TABLE.txt}
R=${NOVA_REDIS_HOST:-100.115.99.19}; P=${NOVA_REDIS_PORT:-6380}
if [ "${STR_INNER:-}" != 1 ]; then
  # key filenames are plain slugs, never globs or newlines; ls|grep matches this repo's style elsewhere (e.g. bin/bench-row)
  # shellcheck disable=SC2010
  seat=$(ls "$HOME/.config/nova-secrets/" | grep -E '^(swarm-.*|air)\.key$' | head -1 | sed 's/\.key$//')
  SOPS=$HOME/.local/bin/sops; [ -x "$SOPS" ] || SOPS=/opt/homebrew/bin/sops
  exec "$HOME/.local/bin/nova-secrets" exec --store "$NB/secrets" --as "$seat" --key "$HOME/.config/nova-secrets/$seat.key" --sops "$SOPS" \
    --only NOVA_REDIS_BENCH_PASSWORD --require NOVA_REDIS_BENCH_PASSWORD -- env STR_INNER=1 "$0" "$IV"
fi
# The pipelined reader (bin/redis-pipe.bash) sits beside the REAL script: find it before (1) moves
# this script to a private copy (~/rowan-working/bin/sprint-table-redis is a symlink to this file).
if [ -z "${STR_LIB:-}" ]; then
  SELF=$0; while [ -L "$SELF" ]; do l=$(readlink "$SELF"); case "$l" in /*) SELF=$l ;; *) SELF=$(dirname "$SELF")/$l ;; esac; done
  STR_LIB="$(cd "$(dirname "$SELF")" && pwd)/redis-pipe.bash"; export STR_LIB
fi
# (1): run from a private copy (same pid, exec), which unlinks itself once bash has it open.
if [ "${SELFCOPY_PATH:-}" != "$0" ]; then
  _copy=$(mktemp "${TMPDIR:-/tmp}/sprint-table-redis.XXXXXX") && cp "$0" "$_copy" && exec env SELFCOPY_PATH="$_copy" "$BASH" "$_copy" "$@"
  echo "sprint-table-redis: WARN no private copy; an in-place edit of $0 can corrupt this loop" >&2
else
  rm -f "$0"
fi
unset SELFCOPY_PATH

# (2): single_instance <lockdir> <ps-match...> -- see friend-row (same function, no sourcing).
SI_LOCK=""
single_instance(){
  local d=$1 held cmd w live i; shift
  mkdir -p "${d%/*}" 2>/dev/null
  for i in 1 2 3 4 5; do
    if mkdir "$d" 2>/dev/null; then printf '%s\n' "$$" > "$d/pid"; SI_LOCK=$d; return 0; fi
    held=$(cat "$d/pid" 2>/dev/null)
    [ -n "$held" ] || { sleep 1; held=$(cat "$d/pid" 2>/dev/null); }   # a taker between its mkdir and its pid write
    [ "$held" = "$$" ] && { SI_LOCK=$d; return 0; }
    live=0
    if [ -n "$held" ] && kill -0 "$held" 2>/dev/null; then
      cmd=$(ps -o command= -p "$held" 2>/dev/null); live=1
      for w in "$@"; do case "$cmd" in *"$w"*) ;; *) live=0 ;; esac; done
    fi
    if [ "$live" = 1 ]; then
      printf '%s sprint-table-redis REFUSED: already running as pid %s (lock %s)\n' "$(date -u +%FT%TZ)" "$held" "$d" >&2; exit 3
    fi
    mv "$d" "$d.stale.$$" 2>/dev/null && rm -rf "$d.stale.$$"
  done
  printf '%s sprint-table-redis REFUSED: could not take %s\n' "$(date -u +%FT%TZ)" "$d" >&2; exit 3
}
single_still_mine(){ local h=""; IFS= read -r h < "$SI_LOCK/pid" 2>/dev/null; [ "$h" = "$$" ]; }
single_release(){ [ -n "$SI_LOCK" ] && single_still_mine && rm -rf "$SI_LOCK"; SI_LOCK=""; }
single_instance "${SPRINT_TABLE_RUN_DIR:-$NB/run}/sprint-table-redis.lockd" sprint-table-redis
trap 'single_release; rm -f "$OUT.$$.new" "$OUT.$$.bench" "$OUT.$$.blocked"' EXIT
trap 'exit 143' TERM; trap 'exit 130' INT; trap 'exit 129' HUP

export REDISCLI_AUTH="$NOVA_REDIS_BENCH_PASSWORD"; unset NOVA_REDIS_BENCH_PASSWORD
rc(){ redis-cli -h "$R" -p "$P" --user bench --no-auth-warning "$@"; }
# shellcheck disable=SC2034  # RP_NAME is read by bin/redis-pipe.bash
RP_NAME=sprint-table-redis
# shellcheck source=bin/redis-pipe.bash
. "$STR_LIB" || { echo "sprint-table-redis: no redis-pipe.bash at $STR_LIB" >&2; exit 2; }

FRIENDS="rowan johnny emma stella"   # the roster (bus-read/participants.json), minus rowan/glenn/alex
XYKEY="sprint:fixes-2026-09-22:xy"   # sprint-xy writes this (EX 180); SPRINT-XY.txt is the fallback only
XYFILE=$HOME/rowan-working/tmp/session-0919b/SPRINT-XY.txt
LANDEDKEY="sprint:fixes-2026-09-22:landed"   # sprint-landed writes this (EX 180); the line ABOVE xy
ROW_STALE_S=${ROW_STALE_S:-10}        # a friend row whose `at` is older than this prints `stale <age>s` (friend-row writes every 1 s)

# redis_or_dash <value>: a raw value mapped to "-" for empty or redis-cli's own "(nil)" text --
# the one rule every friend cell obeys, so a missing key is always "-", never blank.
redis_or_dash(){ local v=$1; case "$v" in ""|"(nil)") echo "-" ;; *) echo "$v" ;; esac }

# format_landed <raw>: sprint-landed's stored value, "<x>/<y> landed <z>% (<breakdown>) eta=<etastr>
# at=<UTC>", reshaped into the table's own display line, "landed: <x>/<y> <z>% -> <etastr>" (Glenn
# 2026-09-23: no per-repo breakdown on the table line -- that stays in the Redis value/log only --
# "landed: "/"sprint: " labels, and the ETA sprint-landed itself computed from its own ring of past
# readings). "landed: ?" (never a carried number, table-correct-end-to-end; SAME rule as the sprint
# line) when the key is missing/unparseable OR its own at= is older than 180s -- belt and suspenders
# alongside the key's EX 180: a reader between the last good write and the key's actual expiry must
# not show a number sprint-landed no longer stands behind. A missing eta= (an older value, or a
# parse miss) falls back to "?" for just that field, same token sprint-landed itself writes when its
# own ring can't rate a tick yet.
format_landed(){
  local raw=$1 parsed lxy lpct leta lat lepoch lage
  case "$raw" in ""|"-"|"(nil)") echo "landed: ?"; return ;; esac
  parsed=$(printf '%s' "$raw" | sed -n 's/^\([0-9][0-9]*\/[0-9][0-9]*\) landed \([0-9][0-9]*%\) (.*) eta=\([^ ]*\) at=\(.*\)$/\1\t\2\t\3\t\4/p')
  if [ -n "$parsed" ]; then
    IFS=$'\t' read -r lxy lpct leta lat <<<"$parsed"
  else
    # no "eta=" (a value from before that field existed): same shape minus that group, ETA -> ?
    parsed=$(printf '%s' "$raw" | sed -n 's/^\([0-9][0-9]*\/[0-9][0-9]*\) landed \([0-9][0-9]*%\) (.*) at=\(.*\)$/\1\t\2\t\3/p')
    [ -n "$parsed" ] || { echo "landed: ?"; return; }
    IFS=$'\t' read -r lxy lpct lat <<<"$parsed"
    leta="?"
  fi
  lepoch=$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$lat" +%s 2>/dev/null || echo 0)
  if [ "$lepoch" -gt 0 ]; then
    lage=$(( $(date +%s) - lepoch ))
    [ "$lage" -gt 180 ] && { echo "landed: ?"; return; }
  fi
  [ -n "$leta" ] || leta="?"
  printf 'landed: %s %s -> %s\n' "$lxy" "$lpct" "$leta"
}

# sprint_line <xy>: sprint-xy's own line with its "sprint: " label, falling back to SPRINT-XY.txt
# when the xy key itself is missing (unchanged fallback behavior from before the landed/sprint
# labels; just factored out so it prints once, at the top, below format_landed).
sprint_line(){
  local x=$1
  if [ "$x" != - ]; then printf 'sprint: %s\n' "$x"
  else printf 'sprint: %s\n' "$(head -1 "$XYFILE" 2>/dev/null || echo "SPRINT ? (sprint-xy has not written yet)")"; fi
}

# friend_rows: ONE pipelined batch (bin/redis-pipe.bash) for every friend row, every down flag, the
# xy line and the landed line. Each friend row is ONE hash friend:<name>, written whole by friend-row
# in one HSET with one `at` (rt#229, the atomic-row rule nova-tools #3281), read whole here with one
# HMGET, so a rendered row is always one moment's snapshot. No key expires (Glenn 2026-09-23 4:20 PM:
# "keys should not expire generally"): the row's AGE says how fresh it is. Prints, tab-separated,
#   <name> <ready> <working> <done> <status>   per friend; status is
#       up <age>s      the row is fresh (<= ROW_STALE_S) and the friend's presence is up
#       down <age>s    fresh, presence gone (the row's up=0)
#       stale <age>s   friend-row itself is silent: the LAST counts, with their age -- never "-/- down"
#       down           friend:<name>:down=<reason> is set (out of credits, Glenn 5:55 PM)
#       ?              no row at all (never written, or a pre-rt#229 string key): every count "-"
# The counts are always the row's most recent ones (Glenn 2026-09-23 2:50 PM, #3299): up/down is
# the status cell's job alone.
#   xy <line or ->
#   landed <raw value or ->
#   newest <UTC digits YYYYMMDDHHMMSS of the newest row `at`, or ->
# It runs inside $(...), a subshell of its own: a lost connection exits just that (fok != 0, the last
# good rows are shown), and its EXIT trap closes the connection. Returns 1 if Redis is unreachable.
friend_rows(){
  trap disconnect EXIT
  local n at up rdy wk dn down age d newest=0 out="" st
  connect || return 1
  for n in $FRIENDS; do q HMGET "friend:$n" at up ready working "done"; q GET "friend:$n:down"; done
  q MGET "$XYKEY" "$LANDEDKEY"
  go
  for n in $FRIENDS; do
    rd; at=""; up=""; rdy=""; wk=""; dn=""
    if [ "$RE" = 0 ] && [ "$RN" = 5 ]; then at=${RL[0]}; up=${RL[1]}; rdy=${RL[2]}; wk=${RL[3]}; dn=${RL[4]}; fi
    rd; down=""; [ "$RE" = 0 ] && down=${RL[0]:-}
    age=""; d=${at//[!0-9]/}; d=${d:0:14}
    if [ "${#d}" -eq 14 ]; then
      age=$(utc_digits_age "$d"); case "$age" in ''|*[!0-9-]*) age="" ;; esac
      [ "$d" -gt "$newest" ] && newest=$d
    fi
    [ -n "$age" ] && [ "$age" -lt 0 ] && age=0
    # Glenn 2026-09-23 3:30 PM ET: "just up is fine; less is more": up | down | stale, no ages.
    if [ -n "$down" ]; then st=down
    elif [ -z "$age" ]; then st="?"
    elif [ "$age" -gt "$ROW_STALE_S" ]; then st=stale
    elif [ "$up" != 1 ]; then st=down
    else st=up; fi
    out="$out$n	$(redis_or_dash "$rdy")	$(redis_or_dash "$wk")	$(redis_or_dash "$dn")	$st
"
  done
  rd; [ "$RE" = 0 ] && [ "$RN" = 2 ] || return 1
  st=$newest; [ "$st" = 0 ] && st=-
  printf '%sxy\t%s\nlanded\t%s\nnewest\t%s\n' "$out" "$(redis_or_dash "${RL[0]}")" "$(redis_or_dash "${RL[1]}")" "$st"
}

# resolve_queue <queue> <dealer_queue> <dealer_at> <now_epoch> -> the queue cell (bench-queue-column,
# 2026-09-22): card-dealer is the only process that can see a remote bench's real dealt-queue depth
# (it lives on the Studio filesystem, not the bench's own disk, so bench-row's own "queue" field is
# always 0 off the Studio); it writes that count into a SEPARATE field, dealer_queue, plus its own
# freshness stamp dealer_at (bench-row HSETs "queue" every second regardless, which would clobber a
# shared field, and the dealer sets no EXPIRE of its own on these two). Prefer dealer_queue over
# bench-row's "queue" ONLY while dealer_at is <=30s old, so a dead dealer never leaves a stale number
# on the table forever. This gates the QUEUE CELL only -- it has nothing to do with whether the row
# itself is shown (that is bench_rows' own host==key + beat-freshness check below). Extracted as its
# own function so bench-queue-control can source it verbatim.
resolve_queue(){
  local queue=$1 dq=$2 dat=$3 now=$4 depoch age
  [ -n "$dq" ] && [ -n "$dat" ] || { echo "$queue"; return 0; }
  depoch=$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$dat" +%s 2>/dev/null || echo 0)
  [ "$depoch" -gt 0 ] || { echo "$queue"; return 0; }
  age=$((now - depoch))
  if [ "$age" -ge 0 ] && [ "$age" -le 30 ]; then echo "$dq"; else echo "$queue"; fi
}

# bench_rows: one SCAN (COUNT 1000), then every HGETALL at once, one redis-cli each (sequential
# round trips over one connection took 1.56 s for 7 benches; parallel ~0.4 s). Prints
# "host queue working done ok fail load1 dealer_queue dealer_at at" (tab-separated) per bench; a
# missing count is 0 and a missing load is -, never the previous bench's value. A hash is a row only
# if its OWN host field equals the key it lives under (bench:pool and bench:stuck_done have none, an
# expired/partial hash may have a stray one) -- checked in-process, no extra round trip. Row
# INCLUSION never depends on dealer_at (that only gates the queue cell, in the caller's
# resolve_queue): the caller applies its own 60s freshness check on "at" (bench-row's beat) instead.
# bench:pool (card-dealer's undealt-pool size) is emitted as one "POOL\t<n>" pseudo-row.
bench_rows(){
  local keys k d i=0 n
  local -a keyarr
  keys=$(rc --scan --count 1000 --pattern 'bench:*' 2>/dev/null | grep -vx 'bench:stuck_done' | sort)
  [ -n "$keys" ] || return 0
  d=$(mktemp -d "${TMPDIR:-/tmp}/sprint-table-bench.XXXXXX") || return 0
  for k in $keys; do keyarr[i]=$k; rc HGETALL "$k" > "$d/$i" 2>/dev/null & i=$((i + 1)); done
  wait
  n=0
  while [ "$n" -lt "$i" ]; do
    k=${keyarr[n]}
    if [ "$k" = "bench:pool" ]; then
      awk '
        f == "" && /^[A-Z]+ / { exit }
        f == "" { if ($0 == "") next; f = $0; next }
        { v = $0; gsub(/[^A-Za-z0-9_.:T-]/, "", v); h[f] = v; f = "" }
        END { printf "POOL\t%s\t\t\t\t\t\t\t\t\n", (h["queue"] != "" ? h["queue"] : "0") }' "$d/$n"
    else
      awk -v key="${k#bench:}" '
        f == "" && /^[A-Z]+ / { err = 1; exit }
        f == "" { if ($0 == "") next; f = $0; next }
        { v = $0; gsub(/[^A-Za-z0-9_.:T-]/, "", v); h[f] = v; have = 1; f = "" }
        END {   # no `next` in END: BSD awk rejects it ("illegal ... next from END", 2026-09-22 23:53Z)
          if (!have || err || h["host"] != key) exit   # a hash without ITS OWN host field (expired, partial) is not a row
          printf "%s\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t%s\t%s\n", h["host"], h["queue"], h["working"], h["done"], h["ok"], h["fail"], (h["load1"] != "" ? h["load1"] : "-"), h["dealer_queue"], h["dealer_at"], h["at"]
        }' "$d/$n"
    fi
    n=$((n + 1))
  done
  rm -rf "$d"
}

# blocked_line: "blocked: <ZCARD q:blocked>" -- the one global blocked count (nova-tools #3219), or
# "blocked: ?" when the reply is not a number (no answer, an error reply): never a carried value.
blocked_line(){
  local n
  n=$(rc ZCARD q:blocked 2>/dev/null)
  case "$n" in ""|*[!0-9]*) echo "blocked: ?" ;; *) echo "blocked: $n" ;; esac
}

# utc_digits_age <YYYYMMDDHHMMSS> -> seconds since that UTC time
utc_digits_age(){ local s; s=$(date -j -u -f %Y%m%d%H%M%S "$1" +%s 2>/dev/null || date -u -d "${1:0:8} ${1:8:2}:${1:10:2}:${1:12:2}" +%s 2>/dev/null) || { echo "?"; return; }; echo $(( $(date +%s) - s )); }

rm -f "$OUT.new"   # the shared temp name before (2); each writer now has its own
LAST_FROWS=""; LAST_FOK=0
while :; do
  single_still_mine || { printf '%s sprint-table-redis: lock %s no longer names pid %s -- exiting\n' "$(date -u +%FT%TZ)" "$SI_LOCK" "$$" >&2; SI_LOCK=""; exit 3; }
  bench_rows > "$OUT.$$.bench" 2>/dev/null & bp=$!   # concurrently with the MGET below
  blocked_line > "$OUT.$$.blocked" 2>/dev/null & blp=$!
  frows=$(friend_rows); fok=$?
  wait "$bp" 2>/dev/null; wait "$blp" 2>/dev/null
  if [ "$fok" = 0 ]; then LAST_FROWS=$frows; LAST_FOK=$(date +%s); else frows=$LAST_FROWS; fi
  # Glenn 2026-09-23 9:45 AM: landed/sprint as lines 2 and 3, right under the title -- pulled out of
  # $frows here (rather than where the friend loop below re-derives xy/landed/newest for the stale
  # markers) so they print before the loop that builds the rest of the table even runs.
  top_xy=$(printf '%s\n' "$frows" | awk -F'\t' '$1=="xy"{print $2; exit}'); [ -n "$top_xy" ] || top_xy=-
  top_landed=$(printf '%s\n' "$frows" | awk -F'\t' '$1=="landed"{print $2; exit}'); [ -n "$top_landed" ] || top_landed=-
  { echo "SPRINT TABLE"
    top_blocked=$(head -1 "$OUT.$$.blocked" 2>/dev/null); echo "${top_blocked:-blocked: ?}"
    format_landed "$top_landed"
    sprint_line "$top_xy"
    echo
    # Glenn 2026-09-22 6:58 PM: "clear everything in the host table. It's not relevant to the current friend
    # sprint." The bench rows come back (HOST_ROWS=1) when a swarm sprint runs.
    # Glenn 7:00 PM: keep the fleet table; clear done/ok/fail (not this sprint's work) until a swarm sprint runs (HOST_COUNTS=1).
    if [ "${HOST_ROWS:-1}" = "1" ]; then
      printf '%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s\n' host ready working 'done' ok fail 'ok%' load
      echo "-----------+-------+---------+-------+-------+-------+------+-------"
      tq=0; tw=0; td=0; to=0; tf=0; POOL_N=""
      now_epoch=$(date -u +%s)
      while IFS=$'\t' read -r host queue working dn ok fail load1 dq da at; do
        [ -n "$host" ] || continue
        if [ "$host" = "POOL" ]; then POOL_N=$queue; continue; fi   # card-dealer's undealt-pool size, never a host row
        # 60s freshness on the bench's OWN beat ("at"): host==key alone is not enough -- a stale hash
        # left behind by a bench that stopped pushing must not print as a live row. This is separate
        # from resolve_queue's 30s dealer_at check, which only ever governs the queue cell's value.
        if [ -n "$at" ]; then
          at_epoch=$(date -u -j -f '%Y-%m-%dT%H:%M:%SZ' "$at" +%s 2>/dev/null || echo 0)
          if [ "$at_epoch" -gt 0 ]; then
            age=$((now_epoch - at_epoch))
            { [ "$age" -ge 0 ] && [ "$age" -le 60 ]; } || continue
          fi
        fi
        queue=$(resolve_queue "${queue:-0}" "${dq:-}" "${da:-}" "$now_epoch")
        pct=$([ "$dn" -gt 0 ] && echo $((100 * ok / dn)) || echo 0)
        if [ "${HOST_COUNTS:-0}" = "1" ]; then printf '%-10s | %5s | %7s | %5s | %5s | %5s | %3s%% | %6s\n' "$host" "$queue" "$working" "$dn" "$ok" "$fail" "$pct" "$load1"
        else printf '%-10s | %5s | %7s | %5s | %5s | %5s | %4s | %6s\n' "$host" "$queue" "$working" - - - - "$load1"; fi
        tq=$((tq + queue)); tw=$((tw + working)); td=$((td + dn)); to=$((to + ok)); tf=$((tf + fail))
      done < "$OUT.$$.bench"
      echo "-----------+-------+---------+-------+-------+-------+------+-------"
      if [ "${HOST_COUNTS:-0}" = "1" ]; then printf '%-10s | %5s | %7s | %5s | %5s | %5s | %3s%% |\n' total "$tq" "$tw" "$td" "$to" "$tf" "$([ $td -gt 0 ] && echo $((100 * to / td)) || echo 0)"
      else printf '%-10s | %5s | %7s | %5s | %5s | %5s | %4s |\n' total "$tq" "$tw" - - - -; fi
      case "${POOL_N:-}" in ""|"(nil)") : ;; *) printf 'pool: %s undealt\n' "$POOL_N" ;; esac
      echo
      echo
    fi
    printf '%-10s | %5s | %7s | %5s | %-10s\n' friend ready working 'done' status
    echo "-----------+-------+---------+-------+------------"
    fq=0; fw=0; fd=0; xy=-; newest=-
    while IFS=$'\t' read -r name q w d st; do
      case "$name" in
        xy) xy=$q; continue ;;
        landed) continue ;;   # printed at the top from $frows (format_landed); nothing to keep here
        newest) newest=$q; continue ;;
        "") continue ;;
      esac
      case "$q" in *[!0-9]*|"") ;; *) fq=$((fq + q)) ;; esac
      case "$w" in *[!0-9]*|"") ;; *) fw=$((fw + w)) ;; esac
      case "$d" in *[!0-9]*|"") ;; *) fd=$((fd + d)) ;; esac
      printf '%-10s | %5s | %7s | %5s | %-10s\n' "$name" "$q" "$w" "$d" "$st"
    done <<EOF_ROWS
$frows
EOF_ROWS
    [ -n "$frows" ] || for name in $FRIENDS; do printf '%-10s | %5s | %7s | %5s | %-10s\n' "$name" - - - '?'; done
    echo "-----------+-------+---------+-------+------------"
    printf '%-10s | %5s | %7s | %5s |\n' total "$fq" "$fw" "$fd"
    # (4) the stale marker: say so rather than show an old table as if it were live
    if [ "$fok" != 0 ]; then
      if [ "$LAST_FOK" -gt 0 ]; then echo "stale: $(( $(date +%s) - LAST_FOK ))s (Redis did not answer; rows are the last good read)"
      else echo "stale: never read (Redis did not answer since start)"; fi
    fi
    # Glenn 2026-09-23 2:50 PM (#3299): no "stale: <age>s (newest friend beat ...)" line under the
    # friends block: each row carries its own age in its status cell, and always its most recent counts.
    if [ "$xy" = - ]; then
      xm=$(stat -f %m "$XYFILE" 2>/dev/null || stat -c %Y "$XYFILE" 2>/dev/null)
      if [ -n "$xm" ]; then echo "stale: $(( $(date +%s) - xm ))s (xy key missing: sprint-xy has not written for >180s; the x/y above is SPRINT-XY.txt)"
      else echo "stale: xy key missing and no SPRINT-XY.txt (sprint-xy is not running)"; fi
    fi
  } > "$OUT.$$.new" && mv -f "$OUT.$$.new" "$OUT"
  sleep "$IV"
done
