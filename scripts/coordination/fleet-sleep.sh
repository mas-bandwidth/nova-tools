#!/usr/bin/env bash
# fleet-sleep.sh <mac-bench>...: let a Mac bench sleep after 30 idle minutes (fleet-wake.sh wakes it). Refuses while its runners are busy.
set -u; [ $# -gt 0 ] || { echo "usage: fleet-sleep.sh <batman|superman>..."; exit 2; }; rc=0
for h in "$@"; do case "$h" in batman|superman) ;; *) echo "SLEEP REFUSED: unknown bench '$h'"; rc=1; continue;; esac
  busy=$(GH_CONFIG_DIR=$HOME/.config/gh-rowan gh api repos/mas-bandwidth/nova-tools/actions/runners --paginate -q "[.runners[]|select((.name|startswith(\"$h\")) and .busy)]|length" 2>/dev/null || echo unknown)
  [ "$busy" = 0 ] || { echo "SLEEP REFUSED $h: runners busy=$busy"; rc=1; continue; }
  ssh -n -o BatchMode=yes -o ConnectTimeout=6 "$h" 'sudo -n pmset -a sleep 30 displaysleep 1 womp 1 >/dev/null 2>&1 && echo ok' 2>/dev/null | grep -q ok && echo "SLEEP $h will sleep after 30 idle minutes" || { echo "SLEEP FAIL $h"; rc=1; }
done; exit $rc
