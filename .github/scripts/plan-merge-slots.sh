#!/usr/bin/env bash
# plan-merge-slots.sh <base> <head>: print 1, 3 or 6, the shard slots a merge group needs.
# 6: go.mod/go.sum moved, or a changed package is over 40 s in testdata/ci/package-sizes.tsv.
# 1: the changed packages sum under 20 s (a package missing from the table counts 10 s), or no Go changed.
# 3: everything else. The test step caps each package's shard count at this number.
set -euo pipefail
base=$1; head=$2
mod=$(awk '$1=="module"{print $2; exit}' go.mod)
changed=$(git diff --name-only "$base" "$head")
if printf '%s\n' "$changed" | grep -q -E '^(go\.mod|go\.sum)$'; then echo 6; exit 0; fi
dirs=$(printf '%s\n' "$changed" | grep -E '\.go$' | xargs -r -n1 dirname | sort -u || true)
[ -n "$dirs" ] || { echo 1; exit 0; }
sum=0; big=0
for d in $dirs; do
  [ "$d" = "." ] && pkg="$mod" || pkg="$mod/$d"
  secs=$(awk -F '\t' -v p="$pkg" '$1 == p { print $2; exit }' testdata/ci/package-sizes.tsv)
  [ -n "$secs" ] || secs=10
  if awk -v s="$secs" 'BEGIN { exit !(s > 40) }'; then big=1; fi
  sum=$(awk -v a="$sum" -v b="$secs" 'BEGIN { printf "%.1f", a + b }')
done
if [ "$big" = 1 ]; then echo 6; elif awk -v s="$sum" 'BEGIN { exit !(s < 20) }'; then echo 1; else echo 3; fi
