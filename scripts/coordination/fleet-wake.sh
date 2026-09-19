#!/usr/bin/env bash
# fleet-wake.sh <mac-bench>...: wake a sleeping Mac bench by magic packet (sent from hulk, on the same LAN) and wait for ssh. Works from any directory.
# The Macs sleep after 30 idle minutes (Glenn 2026-09-17: 100 W each; fleet runs on solar) with Wake for network access on; a runner
# reconnects by itself after a wake. Exit 0 when every named bench answers ssh, 1 otherwise. Names and MACs live in the table below.
set -u; rc=0; mac_of() { case "$1" in batman) echo d0:81:7a:d8:3a:ec;; superman) echo d0:81:7a:da:72:ec;; esac; } # a case, not declare -A: the Studio and the Macs run bash 3.2
[ $# -gt 0 ] || { echo "usage: fleet-wake.sh <batman|superman>..."; exit 2; }
for h in "$@"; do m=$(mac_of "$h"); [ -n "$m" ] || { echo "WAKE REFUSED: unknown bench '$h'"; rc=1; continue; }
  # "ssh answers" is not "awake": a Mac in a maintenance dark-wake answers ssh while its runners are offline (2026-09-17). So the packet
  # is always sent (harmless when awake) and the test is what matters: the bench's runners online in GitHub.
  online() { GH_CONFIG_DIR=$HOME/.config/gh-rowan gh api repos/mas-bandwidth/nova-tools/actions/runners --paginate -q "[.runners[]|select(.name|startswith(\"$h\") and .status==\"online\")]|length" 2>/dev/null || echo 0; }
  if [ "$(online)" -gt 0 ]; then echo "WAKE $h already awake (runners online=$(online))"; continue; fi
  ssh -n -o BatchMode=yes hulk "python3 - '$m' <<'PY'
import socket,sys; mac=bytes.fromhex(sys.argv[1].replace(':','')); pkt=b'\xff'*6+mac*16
s=socket.socket(socket.AF_INET,socket.SOCK_DGRAM); s.setsockopt(socket.SOL_SOCKET,socket.SO_BROADCAST,1)
for _ in range(3): s.sendto(pkt,('255.255.255.255',9)); s.sendto(pkt,('192.168.1.255',9))
PY" 2>/dev/null; t0=$(date +%s); ok=0
  for i in $(seq 1 30); do sleep 3; ssh -n -o BatchMode=yes -o ConnectTimeout=4 "$h" true 2>/dev/null && { ok=1; break; }; done
  if [ $ok != 1 ]; then echo "WAKE FAIL $h no ssh after 90s"; rc=1; continue; fi
  # A magic packet only DARK-wakes a Mac (ssh answers, runners stay offline). A user-activity assertion from inside turns it into a
  # full wake; then hold it awake while it works (sleep 0). fleet-sleep.sh puts it back to sleep-on-idle when the queue is empty.
  ssh -n -o BatchMode=yes "$h" 'sudo -n pmset -a sleep 0 >/dev/null 2>&1; /usr/bin/caffeinate -u -t 5' 2>/dev/null
  n=0; for i in $(seq 1 60); do n=$(online); [ "$n" -gt 0 ] && break; sleep 5; done
  if [ "$n" -gt 0 ]; then echo "WAKE $h up after $(( $(date +%s) - t0 ))s, runners online=$n"; else echo "WAKE FAIL $h ssh answers but no runner online after 5 min"; rc=1; fi
done; exit $rc
