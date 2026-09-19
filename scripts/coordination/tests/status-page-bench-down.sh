#!/usr/bin/env bash
# status-page.sh with one bench simulated down and publishing intercepted: it must finish quickly, say DOWN for that bench,
# keep the other rows real, and never touch the live page or the real metrics file.
set -u; T=$(mktemp -d /tmp/statustest.XXXXXX); mkdir -p $T/bin $T/pub; pass=0; fail=0; real_ssh=$(command -v ssh)
chk() { if [ "$2" = "$3" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "  FAIL: $1 (want [$2] got [$3])"; fi; }
printf '#!/usr/bin/env bash\nfor a in "$@"; do [ "$a" = vision ] && { sleep 5; exit 255; }; done\nexec %s "$@"\n' "$real_ssh" > $T/bin/ssh
printf '#!/usr/bin/env bash\nsrc=""; for a in "$@"; do case "$a" in -*|space:*) ;; *) src="$a";; esac; done; cp "$src" %s/pub/ 2>/dev/null; exit 0\n' "$T" > $T/bin/scp
chmod +x $T/bin/ssh $T/bin/scp; cp "$HOME/rowan-working/queue/metrics.tsv" $T/metrics.tsv; before=$(wc -l < "$HOME/rowan-working/queue/metrics.tsv")
s=$(date +%s); PATH=$T/bin:$PATH STATUS_METRICS=$T/metrics.tsv "$HOME/rowan-working/bin/status-page.sh" >/dev/null 2>&1; wall=$(( $(date +%s) - s ))
page=$(ls -t $T/pub | grep -v metrics | head -1); txt=$(sed 's/<[^>]*>/ /g' "$T/pub/$page" | tr -s ' ')
chk "finishes inside 60 s with a bench down" yes "$([ $wall -lt 60 ] && echo yes || echo "no:${wall}s")"
chk "down bench says DOWN" yes "$(echo "$txt" | grep -q 'vision DOWN' && echo yes || echo no)"
chk "other benches still have real core counts" yes "$(echo "$txt" | grep -qE 'hulk [0-9]+ 64 ' && echo yes || echo no)"
# The live one-minute loop may add its own real row while this runs, so look for the TEST's row (the down bench reads 0 GB), not a line count.
chk "the test row (a 0 GB bench) never reaches the real metrics file" 0 "$(tail -5 "$HOME/rowan-working/queue/metrics.tsv" | awk -F'\t' '{print $NF}' | grep -cE '(^|,)0(,|$)')"
chk "test metrics got the row" yes "$([ $(wc -l < $T/metrics.tsv) -gt $before ] && echo yes || echo no)"
echo "RESULT pass=$pass fail=$fail wall=${wall}s"; rm -rf -- "$T"; exit $(( fail > 0 ))
