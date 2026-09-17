#!/usr/bin/env bash
# nova-log.sh [--since <minutes>] [--grep <regex>] [--bench <name>...]: one stream, one line per mechanical event, across the fleet and this window.
# Sources: each bench's ~/hygiene.log and ~/mirror.log (the timers), ~/rowan-working/queue/fill.log (the fill loop), the Studio loops' outputs
# (harvest, sweep, rebase, upgrade) in the session's tasks dir, and the upgrade loop's log. Prefix per line: <bench> <source>. Read-only.
since=60; pat='.'; benches="hulk vision space mini"; while [ $# -gt 0 ]; do case "$1" in --since) since=$2; shift 2;; --grep) pat=$2; shift 2;; --bench) benches=$2; shift 2;; *) shift;; esac; done
cut_ts=$(date -u -v-${since}M +%Y-%m-%dT%H:%M 2>/dev/null || date -u -d "-${since} min" +%Y-%m-%dT%H:%M)
for b in $benches; do ssh -n -o BatchMode=yes -o ConnectTimeout=5 $b "for f in hygiene.log mirror.log; do [ -f \$HOME/\$f ] && awk -v c='$cut_ts' -v b='$b' -v s=\${f%.log} '\$1 >= c {print b, s, \$0}' \$HOME/\$f; done" 2>/dev/null; done
awk -v b=studio '{print b, "fill", $0}' "$HOME/rowan-working/queue/fill.log" 2>/dev/null | tail -200
T=$(ls -d /private/tmp/claude-501/-Users-glenn-rowan-new/*/tasks 2>/dev/null | head -1); [ -n "$T" ] && grep -hE '^(HARVEST|REAP|HYGIENE|NO-COMMIT|[0-9]{2}:[0-9]{2}Z (sweep|rebase|FILL|tick))' "$T"/*.output 2>/dev/null | tail -300 | awk '{print "studio loop", $0}'
awk '{print "studio upgrade", $0}' "$HOME/rowan-working/upgrade-loop/upgrade.log" 2>/dev/null | tail -30
