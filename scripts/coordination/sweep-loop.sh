#!/usr/bin/env bash
# sweep-loop.sh [ticks]: every 15 min, for each open rowan/* PR from this session that is not DIRTY: enqueue when its head run is green;
# rerun its head run when it failed/cancelled and the queue holds five entries or fewer; skip PRs listed in /tmp/enqueue-skip; honour /tmp/enqueue-hold.
export GH_CONFIG_DIR=$HOME/.config/gh-rowan; R=mas-bandwidth/nova-tools
for tick in $(seq 1 ${1:-6}); do q=0; r=0; SKIP=""; SKIPF=${SWEEP_SKIP_FILE:-$HOME/rowan-working/queue/control/enqueue-skip}; HOLDF=${SWEEP_HOLD_FILE:-$HOME/rowan-working/queue/control/enqueue-hold}; [ -f "$SKIPF" ] && SKIP=$(grep -oE "[0-9]+" "$SKIPF" | tr "\n" " ") # numbers only, in any layout; never sourced (pit stop 2026-09-17: a bare number appended to the old sourced file silently un-skipped a poison PR)
  if [ -f "$HOLDF" ]; then echo "$(date -u +%H:%MZ) sweep tick $tick: HOLD $(cat "$HOLDF")"; sleep ${SWEEP_SLEEP:-900}; continue; fi
  inq=$(gh api graphql -f query='{ repository(owner:"mas-bandwidth", name:"nova-tools") { mergeQueue(branch:"dev") { entries(first:100) { nodes { pullRequest { number } } } } } }' -q '.data.repository.mergeQueue.entries.nodes[].pullRequest.number' 2>/dev/null | tr '\n' ' '); qn=$(echo $inq | wc -w | tr -d ' ')
  for row in $(gh pr list -R $R --state open --limit 400 --json number,headRefName,createdAt,mergeStateStatus -q '.[] | select((.headRefName|startswith("rowan/")) and (.createdAt > "2026-09-17T02:00:00Z") and (.mergeStateStatus!="DIRTY")) | "\(.number)=\(.headRefName)"'); do p=${row%%=*}; br=${row#*=}
    case " ${SKIP:-} " in *" $p "*) continue;; esac; case " $inq " in *" $p "*) continue;; esac
    st=$(gh run list -R $R --branch "$br" --workflow ci.yml --limit 1 --json databaseId,status,conclusion -q '.[0] | "\(.databaseId) \(.status) \(.conclusion)"' 2>/dev/null)
    case "$st" in *"completed success") gh pr merge $p -R $R >/dev/null 2>&1 && q=$((q+1));; *"completed failure"|*"completed cancelled") [ "$qn" -le 5 ] && gh run rerun ${st%% *} -R $R >/dev/null 2>&1 && r=$((r+1));; esac; done
  echo "$(date -u +%H:%MZ) sweep tick $tick: queued=$q reruns=$r inqueue=$qn"; sleep ${SWEEP_SLEEP:-900}; done
