#!/usr/bin/env bash
# rebase-loop.sh with a fake gh and a fake launcher: one card per DIRTY head; same head never twice; a new DIRTY head gets a new card.
set -u; T=$(mktemp -d /tmp/rebasetest.XXXXXX); mkdir -p $T/bin $T/q; pass=0; fail=0
chk() { if [ "$2" = "$3" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "  FAIL: $1 (want [$2] got [$3])"; fi; }
printf '#!/usr/bin/env bash\necho "$4" >> %s/launched.txt; echo "space $4 attempt=1 wall=1s NATIVE OK"\n' $T > $T/bin/flash-native-bench.sh; chmod +x $T/bin/flash-native-bench.sh
mkgh() { printf '#!/usr/bin/env bash\ncase "$1 $2" in "pr list") printf "%s";; esac\n' "$1" > $T/bin/gh; chmod +x $T/bin/gh; }
run() { PATH=$T/bin:$PATH REBASE_Q=$T/q REBASE_BIN=$T/bin REBASE_TSV=$T/dirty.tsv REBASE_SLEEP=0 "$HOME/rowan-working/bin/rebase-loop.sh" 1 >/dev/null 2>&1; sleep 1; wc -l < $T/launched.txt 2>/dev/null | tr -d ' ' || echo 0; }
mkgh '977\trowan/a\taaaaaaaaaaaa\ttitle a\n1226\trowan/b\tbbbbbbbbbbbb\ttitle b\n'
chk "two dirty PRs: two cards" 2 "$(run)"
chk "same heads again: no new cards" 2 "$(run)"
mkgh '977\trowan/a\tcccccccccccc\ttitle a\n1226\trowan/b\tbbbbbbbbbbbb\ttitle b\n'
chk "977 rebased once and DIRTY again at a new head: one new card" 3 "$(run)"
chk "marker holds the new head" cccccccccccc "$(cat $T/q/pr-977)"
mkgh ''
chk "nothing dirty: nothing cut" 3 "$(run)"
touch $T/q/pr-555; mkgh '555\trowan/old\tdddddddddddd\told bare marker\n'
chk "an old bare-flag marker does not block forever" 4 "$(run)"
echo "RESULT pass=$pass fail=$fail"; rm -rf -- "$T"; exit $(( fail > 0 ))
