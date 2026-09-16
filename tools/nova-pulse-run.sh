#!/usr/bin/env bash
# nova-pulse-run.sh [hours] — the bench's loop, as one command.
#
# This is the switch that retires bin/pulse-loop.sh: the same tick (gate, harvest, sweep,
# reap, refill, launch, one WIDTH line) held by `nova-pulse run`, with the model taken out
# of it. Nothing here is policy — the slots, the headroom, the tick and the refill cadence
# are <queue>/pulse.toml, re-read every tick, so a change needs no restart and a restart
# loses no counter.
#
#   tools/nova-pulse-run.sh        # eight hours on this bench
#   tools/nova-pulse-run.sh 1      # one hour
#   PULSE_QUEUE=... PULSE_ROOTS=... tools/nova-pulse-run.sh
#
# One loop per bench: a second one on the same queue would launch a card twice.
set -uo pipefail

HOURS="${1:-8}"
QUEUE="${PULSE_QUEUE:-$HOME/rowan-working/queue}"
ROOTS="${PULSE_ROOTS:-$HOME/rowan-working/swarm-root,$HOME/rowan-working/swarm-root-space}"
REPO="${PULSE_REPO:-mas-bandwidth/nova-tools}"
BRANCH="${PULSE_BRANCH:-dev}"
TICK="${PULSE_TICK:-60}"
DEADLINE="${PULSE_DEADLINE:-1500}"
export GH_CONFIG_DIR="${GH_CONFIG_DIR:-$HOME/.config/gh-rowan}"

command -v nova-pulse >/dev/null || { echo "REFUSED nova-pulse is not on PATH (go install ./cmd/nova-pulse)" >&2; exit 2; }
[ -d "$QUEUE" ] || { echo "REFUSED the queue $QUEUE is not a directory (pass PULSE_QUEUE)" >&2; exit 2; }

PIDF="$QUEUE/pulse-run.pid"
if [ -f "$PIDF" ] && kill -0 "$(cat "$PIDF")" 2>/dev/null; then
  echo "REFUSED nova-pulse run is already holding this queue, pid=$(cat "$PIDF") (one loop per bench)" >&2
  exit 1
fi
echo $$ > "$PIDF"
trap 'rm -f "$PIDF"' EXIT

exec nova-pulse run \
  --queue "$QUEUE" \
  --roots "$ROOTS" \
  --repo "$REPO" \
  --branch "$BRANCH" \
  --hours "$HOURS" \
  --tick "$TICK" \
  --deadline "$DEADLINE"
