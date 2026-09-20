#!/usr/bin/env bash
# Roadmap parity check: ROADMAP.md features/items vs docs/roadmaps/nova-work.sexp.
#
# The page states progress. This check is what stops the page stating a number the
# page's own data does not support.
#
# Convention:
#   md features  = count of table rows matching '^| E[0-9]*-F[0-9]* '
#   md items     = count of acceptance checkboxes '^- [ ]' or '^- [x]'
#   sexp features = value of ':current-features'
#   sexp items    = value of ':current-acceptance-items'
#   parens        = open/close paren counts in the sexp (must balance)
#
# Per-feature acceptance column (issue: honest partial progress). When a feature row
# carries an items cell of the form 'N/M', then for EVERY feature row:
#   - the cell must be present and well formed, N <= M;
#   - M must equal the number of '- [ ]'/'- [x]' lines in that feature's detail block;
#   - N must equal the number of '- [x]' lines in that block;
#   - a feature marked verified must have N == M.
# So a green cell can never be wider than its ticked criteria.
#
# Usage: roadmap-parity.sh [ROADMAP.md] [nova-work.sexp]
set -u
cd "$(dirname "$0")/.." || exit 1

md=${1:-ROADMAP.md}
sexp=${2:-docs/roadmaps/nova-work.sexp}

md_features=$(grep -c '^| E[0-9]*-F[0-9]* ' "$md")
md_items=$(grep -c '^- \[ \]\|^- \[x\]' "$md")

sexp_features=$(grep -o ':current-features [0-9]*' "$sexp" | grep -o '[0-9]*' | tail -n1)
sexp_items=$(grep -o ':current-acceptance-items [0-9]*' "$sexp" | grep -o '[0-9]*' | tail -n1)

open=$(grep -o '(' "$sexp" | wc -l | tr -d ' ')
close=$(grep -o ')' "$sexp" | wc -l | tr -d ' ')

status=OK
[ "$md_features" = "$sexp_features" ] || status=FAIL
[ "$md_items" = "$sexp_items" ] || status=FAIL
[ "$open" = "$close" ] || status=FAIL

# Per-feature acceptance column.
items_report=$(awk '
  # Feature table row: | E01-F01 — title | 2/3 | ❌ |
  /^\| E[0-9]+-F[0-9]+ / {
    split($0, c, "|")
    id = c[2]; sub(/^ */, "", id); sub(/ .*/, "", id)
    ncell = 0
    for (i = 3; i <= 5; i++) if (c[i] != "") ncell++
    cell = c[3]; gsub(/ /, "", cell)
    verdict = c[4]; gsub(/ /, "", verdict)
    if (cell ~ /^[0-9]+\/[0-9]+$/) {
      cols++
      split(cell, nm, "/")
      claim_n[id] = nm[1]; claim_m[id] = nm[2]
    } else {
      # no items cell on this row: verdict lives in c[3]
      verdict = cell
      plain++
    }
    tab_verdict[id] = verdict
    rows++
    next
  }
  # Detail block: **E01-F01 — title**
  /^\*\*E[0-9]+-F[0-9]+ / {
    blk = $0; sub(/^\*\*/, "", blk); sub(/ .*/, "", blk)
    next
  }
  /^- \[x\]/ { if (blk != "") { real_n[blk]++; real_m[blk]++ } next }
  /^- \[ \]/ { if (blk != "") { real_m[blk]++ } next }
  END {
    if (cols == 0) { print "NONE " rows; exit 0 }
    bad = 0
    if (plain > 0) { printf "MISSING-CELL rows=%d\n", plain; bad = 1 }
    sn = 0; sm = 0
    for (id in claim_m) {
      if (claim_n[id] + 0 > claim_m[id] + 0) { printf "OVER %s %s/%s\n", id, claim_n[id], claim_m[id]; bad = 1 }
      if (claim_m[id] + 0 != real_m[id] + 0) { printf "TOTAL %s cell=%s block=%d\n", id, claim_m[id], real_m[id] + 0; bad = 1 }
      if (claim_n[id] + 0 != real_n[id] + 0) { printf "TICKED %s cell=%s block=%d\n", id, claim_n[id], real_n[id] + 0; bad = 1 }
      if (index(tab_verdict[id], "✅") > 0 && claim_n[id] + 0 != claim_m[id] + 0) { printf "GREEN-PARTIAL %s %s/%s\n", id, claim_n[id], claim_m[id]; bad = 1 }
      sn += claim_n[id]; sm += claim_m[id]
    }
    printf "SUMS %d %d %d %d\n", sn, sm, rows, bad
  }
' "$md")

items_line=""
case "$items_report" in
  NONE*) items_line="items-column=absent" ;;
  *)
    sums=$(printf '%s\n' "$items_report" | grep '^SUMS ')
    faults=$(printf '%s\n' "$items_report" | grep -v '^SUMS ' | tr '\n' ' ')
    set -- $sums
    sn=${2:-0}; sm=${3:-0}; bad=${5:-1}
    ticked=$(grep -c '^- \[x\]' "$md")
    items_line="items-column verified=$sn total=$sm ticked=$ticked"
    [ "$sm" = "$sexp_items" ] || { status=FAIL; items_line="$items_line TOTAL-MISMATCH(sexp=$sexp_items)"; }
    [ "$sn" = "$ticked" ] || { status=FAIL; items_line="$items_line TICKED-MISMATCH"; }
    [ "$bad" = "0" ] || { status=FAIL; items_line="$items_line FAULTS: $faults"; }
    ;;
esac

# Per-criterion record (the sexp is primary; the page is a view). For every feature, the sequence of
# '- [x]' / '- [ ]' lines in the page's detail block must equal, in order, the sequence of :criteria
# rows' :state in the sexp (verified = x; unverified and unmet = blank), and each by-feature row's
# :verified must equal its count of verified criteria. A tick the data does not hold fails the build.
md_seq=$(awk '/^\*\*E[0-9]+-F[0-9]+ — /{f=substr($1,3)} /^- \[x\]/{if(f)s[f]=s[f]"x"} /^- \[ \]/{if(f)s[f]=s[f]"."} END{for(k in s)print k" "s[k]}' "$md" | sort)
sx_seq=$(awk '{ if (match($0,/\(:feature "E[0-9]+-F[0-9]+"/)) { f=substr($0,RSTART+11,RLENGTH-12); if (match($0,/:verified [0-9]+/)) want[f]=substr($0,RSTART+10,RLENGTH-10) }
               if (f && match($0,/:id "E[0-9]+-F[0-9]+-[0-9]+" :state "[a-z]+"/)) { st=$0; sub(/.*:state "/,"",st); sub(/".*/,"",st); if(st=="verified"){s[f]=s[f]"x";n[f]++} else s[f]=s[f]"." } }
          END{for(k in s){ if(n[k]+0!=want[k]+0) print k" COUNT-MISMATCH verified="want[k]" criteria="n[k]+0; else print k" "s[k]}}' "$sexp" | sort)
crit_line="criteria-records=$(grep -c ':id "E[0-9]*-F[0-9]*-[0-9]*" :state' "$sexp")"
if [ "$md_seq" != "$sx_seq" ]; then status=FAIL; crit_line="$crit_line CRITERIA-MISMATCH: $(diff <(echo "$md_seq") <(echo "$sx_seq") | grep '^[<>]' | head -4 | tr '\n' ' ')"; fi

echo "PARITY features md=$md_features sexp=$sexp_features items md=$md_items sexp=$sexp_items parens=$open/$close $items_line $crit_line $status"
[ "$status" = "OK" ]
