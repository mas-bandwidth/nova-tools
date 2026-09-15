#!/usr/bin/env bash
# Roadmap parity check: ROADMAP.md features/items vs docs/roadmaps/nova-work.sexp.
# Convention:
#   md features  = count of table rows matching '^| E[0-9]*-F[0-9]* '
#   md items     = count of acceptance checkboxes '^- [ ]' or '^- [x]'
#   sexp features = value of ':current-features'
#   sexp items   = value of ':current-acceptance-items'
#   parens       = open/close paren counts in the sexp (must balance)
set -u
cd "$(dirname "$0")/.." || exit 1

md_features=$(grep -c '^| E[0-9]*-F[0-9]* ' ROADMAP.md)
md_items=$(grep -c '^- \[ \]\|^- \[x\]' ROADMAP.md)

sexp_features=$(grep -o ':current-features [0-9]*' docs/roadmaps/nova-work.sexp | grep -o '[0-9]*' | tail -n1)
sexp_items=$(grep -o ':current-acceptance-items [0-9]*' docs/roadmaps/nova-work.sexp | grep -o '[0-9]*' | tail -n1)

open=$(grep -o '(' docs/roadmaps/nova-work.sexp | wc -l | tr -d ' ')
close=$(grep -o ')' docs/roadmaps/nova-work.sexp | wc -l | tr -d ' ')

status=OK
[ "$md_features" = "$sexp_features" ] || status=FAIL
[ "$md_items" = "$sexp_items" ] || status=FAIL
[ "$open" = "$close" ] || status=FAIL

echo "PARITY features md=$md_features sexp=$sexp_features items md=$md_items sexp=$sexp_items parens=$open/$close $status"
[ "$status" = "OK" ]