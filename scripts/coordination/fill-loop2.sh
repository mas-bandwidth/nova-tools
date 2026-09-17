#!/usr/bin/env bash
# fill-loop2.sh [ticks] [cap]: as fill-loop.sh, but cards are DEALT across the benches (one each in turn, up to each
# bench's allowance) instead of draining ready into the first bench; v1 put 34 cards on hulk while vision sat at load 0.4.
# Per-bench per-tick cap defaults to 60 (was 30, set for the old 10 Mbps uplink; 40 Mbps since 2026-09-17).
# Test seams (defaults are production): FILL_BIN (launcher dir), FILL_Q (queue dir), FILL_CAPS ("hulk=2 vision=1 space=0": skip ssh),
# FILL_SLEEP (seconds between ticks), FILL_LAUNCHER (launcher file name). tests/fill-loop-deal.sh drives them.
B=${FILL_BIN:-$HOME/rowan-working/bin}; QD=${FILL_Q:-$HOME/rowan-working/queue}; READY=$QD/ready; LAUNCHED=$QD/launched; mkdir -p "$READY" "$LAUNCHED"; CAP=${2:-60}; LAUNCHER=${FILL_LAUNCHER:-flash-native-bench.sh}
cap() { if [ -n "${FILL_CAPS:-}" ]; then for kv in $FILL_CAPS; do [ "${kv%%=*}" = "$1" ] && { echo "${kv#*=}"; return; }; done; echo 0; return; fi; ssh -n -o BatchMode=yes -o ConnectTimeout=8 "$1" 'c=$(nproc); l=$(cut -d. -f1 /proc/loadavg); f=$(df -BG "$HOME" | awk '"'"'NR==2{gsub("G","",$4); print $4}'"'"'); m=$(awk '"'"'/MemAvailable/{printf "%d", $2/1048576}'"'"' /proc/meminfo); a1=$(( c*3/2 - l - c/8 )); a2=$(( (f-25)/2 )); a3=$(( m/2 )); a=$a1; [ $a2 -lt $a ] && a=$a2; [ $a3 -lt $a ] && a=$a3; echo "$a"' 2>/dev/null; }
for tick in $(seq 1 ${1:-12}); do
  ah=$(cap hulk); av=$(cap vision); as=$(cap space); ah=${ah:-0}; av=${av:-0}; as=${as:-0}
  [ "$ah" -gt $CAP ] && ah=$CAP; [ "$av" -gt $CAP ] && av=$CAP; [ "$as" -gt $CAP ] && as=$CAP; [ "$ah" -lt 0 ] && ah=0; [ "$av" -lt 0 ] && av=0; [ "$as" -lt 0 ] && as=0
  nh=0; nv=0; ns=0; progress=1
  while [ $progress = 1 ]; do progress=0
    for b in vision hulk space; do
      case $b in hulk) a=$ah;; vision) a=$av;; space) a=$as;; esac
      [ "$a" -gt 0 ] || continue
      f=$(ls "$READY"/card-*.md 2>/dev/null | head -1); [ -n "$f" ] || break 2
      l=$(basename "$f" .md); mv "$f" "$LAUNCHED/"
      ( "$B/$LAUNCHER" "$b" "swarm-$b" "$LAUNCHED/$(basename "$f")" "$l" 2400 2>&1 | grep -E 'attempt=|REFUSED' | cut -c1-120 >> "$QD/fill.log" ) &
      case $b in hulk) ah=$((ah-1)); nh=$((nh+1));; vision) av=$((av-1)); nv=$((nv+1));; space) as=$((as-1)); ns=$((ns+1));; esac
      progress=1; sleep 1
    done
  done
  echo "$(date -u +%H:%MZ) FILL tick $tick: hulk:launched=$nh vision:launched=$nv space:launched=$ns ready=$(ls "$READY"/card-*.md 2>/dev/null | wc -l | tr -d ' ')"; sleep ${FILL_SLEEP:-300}
done; wait
