#!/usr/bin/env bash
# asdf-carry-verify.sh: decide, by measurement, whether an approval at head A
# carries to the rebased head B when the ONLY thing the rebase changed is the
# component list of lisp/nova-work/nova-work.asd.
#
# WHY THIS EXISTS. Eleven approved nova-work PRs collide pairwise on that one
# file. Each landing moves the line, so each survivor must be rebased, and a
# rebase voids the approve: eleven PRs is up to fifty-five re-reads of a line
# that says `(:file "tests/...")`. The structural fix (explicit prelude plus
# sorted discovery) is being built; until it lands, a narrow mechanical carry
# rule can spare those re-reads -- but only if a machine, not a person's memory
# and not a pasted success line, establishes that the rebase really did change
# nothing else.
#
# THE RULE IS NOT IN FORCE. Stella gave a CONDITIONAL YES to the rule as logic
# (stella-abebd6464110); Emma has not yet said whether her approve carries, and
# it is her approve. This script is the instrument. Running it decides nothing
# by itself, creates no vote, releases no other holder, and waives no security
# or merge-history gate.
#
# WHAT IT ESTABLISHES, all of it itself, from the object database and the bench:
#
#   1  PAIRING     git range-diff pairs every commit of A with one of B; none is
#                  dropped, added, squashed or split.
#   2  MESSAGES    each pair's commit message is byte-identical, and each pair
#                  touches the same set of paths.
#   3  COMPONENTS  against its own base, B adds exactly the components A added,
#                  removes none of its base's, and reorders none of them.
#   4  ORDER       every component present in both keeps its relative order, so
#                  a src file cannot drift past a neighbour it depends on.
#   5  DUPLICATES  no NEW duplicate component. One already identical in A and in
#                  B's base is reported and allowed: nova-tools#1989's repeated
#                  slice registrations pre-date every one of these PRs, and
#                  refusing them would refuse every carry for a fault none of
#                  them introduced.
#   6  BLOBS       every path A touched other than the .asd is BYTE-IDENTICAL in
#                  B, and B touched nothing else.
#   7  TESTS       a fresh isolated load and the suite at baseA, A, baseB and B,
#                  each ALONE, recording the exact registered test-name SET and
#                  every test's outcome. A's set is preserved in B; anything B
#                  gained is exactly what dev gained between the two bases;
#                  no new duplicate registration; no outcome changes.
#   8  CI          ci-ok is green at the EXACT head B, not at a branch name.
#
# Stella's first correction is why 7 is written the way it is: `total >= A` is
# insufficient, because a suite can reach a higher total having lost a test and
# gained two. Totals are a receipt; the SET and the per-test outcomes are the
# measurement.
#
# OUTPUT. One verdict file: `CARRY OK` or `CARRY REFUSED reason=...`, with every
# measurement, the pinned shas of A, B and both bases, and the range-diff text
# verbatim. It is meant to be pasted whole into a batch body and re-run by a
# reviewer against the same four shas to get the same answer.
#
# COST AND BENCH. Stage 7 runs FOUR full suites, one after another, each in its
# own worktree with its own ASDF cache. They must run alone: while
# nova-tools#1699 was open, two suites sharing a host manufactured failures, and
# a verifier that measured those would refuse honest carries. Give it a quiet
# bench and check `pgrep sbcl` first.
#
# Usage:
#   tools/asdf-carry-verify.sh --a SHA --base-a SHA --b SHA --base-b SHA \
#       [--repo DIR] [--out FILE] [--pr N] [--no-suite] [--no-ci]
#
# --no-suite and --no-ci do not weaken the verdict: they FORCE
# `CARRY REFUSED`, and exist so the structural stages can be run cheaply on a
# busy bench while the suite is queued for a quiet one.
#
# ASDF_CARRY_SUITE, ASDF_CARRY_CI: command overrides used by
# asdf-carry-verify_test.sh to drive this script's logic without SBCL or the
# network. Nothing else should set them.
set -u

ASD=lisp/nova-work/nova-work.asd

A=; BASE_A=; B=; BASE_B=; REPO=; OUT=; PR=; DO_SUITE=1; DO_CI=1
while [ $# -gt 0 ]; do
  case "$1" in
    --a) A=$2; shift 2 ;;
    --base-a) BASE_A=$2; shift 2 ;;
    --b) B=$2; shift 2 ;;
    --base-b) BASE_B=$2; shift 2 ;;
    --repo) REPO=$2; shift 2 ;;
    --out) OUT=$2; shift 2 ;;
    --pr) PR=$2; shift 2 ;;
    --no-suite) DO_SUITE=0; shift ;;
    --no-ci) DO_CI=0; shift ;;
    -h|--help) sed -n '2,70p' "$0"; exit 0 ;;
    *) echo "asdf-carry-verify: unknown argument $1" >&2; exit 2 ;;
  esac
done
for v in A BASE_A B BASE_B; do
  eval "x=\$$v"
  [ -n "$x" ] || { echo "asdf-carry-verify: --$(echo "$v" | tr 'A-Z_' 'a-z-') is required" >&2; exit 2; }
done

REPO=${REPO:-$(cd "$(dirname "$0")/.." && pwd)}
git -C "$REPO" rev-parse --git-dir >/dev/null 2>&1 || {
  echo "asdf-carry-verify: $REPO is not a git repository" >&2; exit 2; }

WORK=$(mktemp -d) || exit 2
trap 'rm -rf "$WORK"; for w in "$WORK"/wt-*; do git -C "$REPO" worktree remove --force "$w" 2>/dev/null; done; git -C "$REPO" worktree prune 2>/dev/null' EXIT

OUT=${OUT:-$WORK/verdict.txt}
: > "$OUT"
say() { printf '%s\n' "$*" | tee -a "$OUT"; }

REFUSED=""
refuse() { REFUSED="$REFUSED $1"; say "CHECK $2 REFUSED $3"; }
pass()   { say "CHECK $1 OK $2"; }
note()   { say "NOTE $*"; }

git_() { git -C "$REPO" "$@"; }

resolve() {
  git_ rev-parse --verify --quiet "$1^{commit}" || {
    echo "asdf-carry-verify: cannot resolve $1 in $REPO" >&2; exit 2; }
}
A=$(resolve "$A"); BASE_A=$(resolve "$BASE_A")
B=$(resolve "$B"); BASE_B=$(resolve "$BASE_B")

say "CARRY-VERIFY v1  nova-tools ASDF-only approval carry"
say "generated $(date -u '+%Y-%m-%dT%H:%M:%SZ') on $(hostname) by $(basename "$0")"
say "rule: rowan-857404b75477, as corrected by stella-abebd6464110 (CONDITIONAL YES)"
say "the rule is not in force until Emma says her approve carries; this file decides nothing"
[ -n "$PR" ] && say "pr $PR"
say "pinned:"
say "  A       $A"
say "  baseA   $BASE_A"
say "  B       $B"
say "  baseB   $BASE_B"
say "  asd     $ASD"
say "  repo    $REPO"
say ""

# --- helpers -------------------------------------------------------------

# components_of <ref>: the (:file "...") component names, in file order.
components_of() {
  git_ show "$1:$ASD" 2>/dev/null \
    | grep -o '(:file "[^"]*")' \
    | sed -e 's/^(:file "//' -e 's/")$//'
}

# blob_of <ref> <path>: the blob sha, or ABSENT.
blob_of() { git_ rev-parse --verify --quiet "$1:$2" || echo ABSENT; }

# dups_of <file>: `name count` for every line appearing more than once.
dups_of() { LC_ALL=C sort "$1" | uniq -c | awk '$1 > 1 { print $2" "$1 }' | LC_ALL=C sort; }

# only_in <a> <b>: lines in a that are not in b (set semantics).
only_in() { LC_ALL=C comm -23 <(LC_ALL=C sort -u "$1") <(LC_ALL=C sort -u "$2"); }

# --- 1  PAIRING ----------------------------------------------------------

# --creation-factor: range-diff's default (60) decides a commit is NEW rather
# than a rebase of an old one when its patch has drifted far enough. A one-line
# .asd change is a large fraction of a small commit's patch, so the default
# refuses to pair exactly the commits this rule is about. Pairing is not where
# the safety lives -- the commit count, the byte-identical messages and the
# per-pair path sets below each independently forbid a squash or a split -- so a
# permissive factor costs nothing and stops the receipt being noise.
git_ range-diff --no-color --creation-factor=95 "$BASE_A..$A" "$BASE_B..$B" > "$WORK/range-diff.txt" 2>"$WORK/range-diff.err"
if [ ! -s "$WORK/range-diff.txt" ]; then
  refuse range-diff-empty 1-PAIRING "git range-diff produced nothing: $(head -2 "$WORK/range-diff.err" | tr '\n' ' ')"
else
  # A summary line's marker is `=` identical, `!` differs, `<` only in the first
  # range, `>` only in the second. `<` or `>` means a commit was dropped, added,
  # squashed or split, which is not a rebase this rule speaks about.
  unpaired=$(grep -c -E '^ *[0-9-]+: *[0-9a-f]+ *[<>] ' "$WORK/range-diff.txt" || true)
  if [ "$unpaired" != 0 ]; then
    refuse commits-do-not-pair 1-PAIRING "$unpaired commit(s) appear on only one side of the range-diff"
    grep -E '^ *[0-9-]+: *[0-9a-f]+ *[<>] ' "$WORK/range-diff.txt" | sed 's/^/  /' | tee -a "$OUT"
  else
    pass 1-PAIRING "every commit of A pairs with a commit of B"
  fi
fi

git_ rev-list --reverse "$BASE_A..$A" > "$WORK/commits-a.txt"
git_ rev-list --reverse "$BASE_B..$B" > "$WORK/commits-b.txt"
na=$(wc -l < "$WORK/commits-a.txt" | tr -d ' ')
nb=$(wc -l < "$WORK/commits-b.txt" | tr -d ' ')
say "commits: A has $na, B has $nb"
if [ "$na" != "$nb" ]; then
  refuse commit-count-differs 1-PAIRING "A has $na commits, B has $nb"
elif [ "$na" = 0 ]; then
  refuse no-commits 1-PAIRING "A adds no commits over its base; there is nothing to carry"
fi

# --- 2  MESSAGES and per-commit paths ------------------------------------

if [ "$na" = "$nb" ] && [ "$na" != 0 ]; then
  bad_msg=0; bad_paths=0
  i=1
  while [ "$i" -le "$na" ]; do
    ca=$(sed -n "${i}p" "$WORK/commits-a.txt")
    cb=$(sed -n "${i}p" "$WORK/commits-b.txt")
    git_ log -1 --format=%B "$ca" > "$WORK/msg-a"
    git_ log -1 --format=%B "$cb" > "$WORK/msg-b"
    if ! cmp -s "$WORK/msg-a" "$WORK/msg-b"; then
      bad_msg=$((bad_msg+1))
      say "  message differs at pair $i: $ca vs $cb"
      diff -u "$WORK/msg-a" "$WORK/msg-b" | sed -n '3,12p' | sed 's/^/    /' | tee -a "$OUT"
    fi
    git_ show --name-only --format= "$ca" | LC_ALL=C sort -u > "$WORK/paths-a"
    git_ show --name-only --format= "$cb" | LC_ALL=C sort -u > "$WORK/paths-b"
    if ! cmp -s "$WORK/paths-a" "$WORK/paths-b"; then
      bad_paths=$((bad_paths+1))
      say "  changed paths differ at pair $i: $ca vs $cb"
      diff -u "$WORK/paths-a" "$WORK/paths-b" | sed -n '3,12p' | sed 's/^/    /' | tee -a "$OUT"
    fi
    i=$((i+1))
  done
  [ "$bad_msg" = 0 ] && pass 2-MESSAGES "all $na commit messages byte-identical" \
    || refuse commit-message-differs 2-MESSAGES "$bad_msg of $na messages differ"
  [ "$bad_paths" = 0 ] && pass 2-PATHS "each pair touches the same set of paths" \
    || refuse commit-paths-differ 2-PATHS "$bad_paths of $na pairs touch different paths"
fi

# --- 3  COMPONENTS -------------------------------------------------------

components_of "$BASE_A" > "$WORK/c-basea"; components_of "$A" > "$WORK/c-a"
components_of "$BASE_B" > "$WORK/c-baseb"; components_of "$B" > "$WORK/c-b"
for f in c-basea c-a c-baseb c-b; do
  [ -s "$WORK/$f" ] || { echo "asdf-carry-verify: $ASD names no component at one of the four shas ($f)" >&2; exit 2; }
done

only_in "$WORK/c-a" "$WORK/c-basea" > "$WORK/added-a"
only_in "$WORK/c-b" "$WORK/c-baseb" > "$WORK/added-b"
say "components added by A over baseA: $(wc -l < "$WORK/added-a" | tr -d ' ')"
sed 's/^/  + /' "$WORK/added-a" | tee -a "$OUT"
say "components added by B over baseB: $(wc -l < "$WORK/added-b" | tr -d ' ')"
sed 's/^/  + /' "$WORK/added-b" | tee -a "$OUT"

if cmp -s <(LC_ALL=C sort -u "$WORK/added-a") <(LC_ALL=C sort -u "$WORK/added-b"); then
  pass 3-ADDED "B adds exactly the components A added"
else
  refuse added-components-differ 3-ADDED "the component sets the two heads add are not the same"
  diff -u <(LC_ALL=C sort -u "$WORK/added-a") <(LC_ALL=C sort -u "$WORK/added-b") \
    | sed -n '3,20p' | sed 's/^/  /' | tee -a "$OUT"
fi

only_in "$WORK/c-baseb" "$WORK/c-b" > "$WORK/removed-b"
if [ -s "$WORK/removed-b" ]; then
  refuse components-removed 3-REMOVED "B removes $(wc -l < "$WORK/removed-b" | tr -d ' ') component(s) its base had"
  sed 's/^/  - /' "$WORK/removed-b" | tee -a "$OUT"
else
  pass 3-REMOVED "B removes none of its base's components"
fi

# --- 4  ORDER ------------------------------------------------------------

# Every component present in BOTH heads must appear in the same relative order.
# That is "reorders none of dev's existing lines" and "a src component keeps its
# semantic relative order and neighbour constraints" in one measurement: if no
# pair of components swaps, nothing can have drifted past a neighbour.
LC_ALL=C comm -12 <(LC_ALL=C sort -u "$WORK/c-a") <(LC_ALL=C sort -u "$WORK/c-b") > "$WORK/c-common"
grep -Fxf "$WORK/c-common" "$WORK/c-a" > "$WORK/order-a" || true
grep -Fxf "$WORK/c-common" "$WORK/c-b" > "$WORK/order-b" || true
if cmp -s "$WORK/order-a" "$WORK/order-b"; then
  pass 4-ORDER "all $(wc -l < "$WORK/c-common" | tr -d ' ') shared components keep their relative order"
else
  refuse components-reordered 4-ORDER "the shared components appear in a different order in B"
  diff -u "$WORK/order-a" "$WORK/order-b" | sed -n '3,30p' | sed 's/^/  /' | tee -a "$OUT"
fi

# --- 5  DUPLICATES -------------------------------------------------------

dups_of "$WORK/c-a" > "$WORK/dup-a"; dups_of "$WORK/c-b" > "$WORK/dup-b"
dups_of "$WORK/c-baseb" > "$WORK/dup-baseb"
newdup=0
while read -r name count; do
  [ -n "$name" ] || continue
  # Tolerated only when the SAME name appears the SAME number of times in A and
  # in B's base: then it pre-dates this PR and this rebase both (nova-tools#1989).
  if grep -qxF "$name $count" "$WORK/dup-a" && grep -qxF "$name $count" "$WORK/dup-baseb"; then
    note "pre-existing duplicate component, identical in A and in baseB, allowed: $name x$count (nova-tools#1989)"
  else
    newdup=$((newdup+1))
    say "  NEW duplicate component in B: $name x$count"
  fi
done < "$WORK/dup-b"
[ "$newdup" = 0 ] && pass 5-DUPLICATES "B introduces no new duplicate component" \
  || refuse new-duplicate-component 5-DUPLICATES "$newdup new duplicate component(s) in B"

# --- 6  BLOBS ------------------------------------------------------------

git_ diff --name-only "$BASE_A" "$A" | LC_ALL=C sort -u > "$WORK/touched-a"
git_ diff --name-only "$BASE_B" "$B" | LC_ALL=C sort -u > "$WORK/touched-b"
say "paths A touched: $(wc -l < "$WORK/touched-a" | tr -d ' ') ; paths B touched: $(wc -l < "$WORK/touched-b" | tr -d ' ')"

differing=0
while read -r p; do
  [ -n "$p" ] || continue
  [ "$p" = "$ASD" ] && continue
  ba=$(blob_of "$A" "$p"); bb=$(blob_of "$B" "$p")
  if [ "$ba" != "$bb" ]; then
    differing=$((differing+1))
    say "  blob differs: $p  A=$ba  B=$bb"
  fi
done < "$WORK/touched-a"

extra=0
while read -r p; do
  [ -n "$p" ] || continue
  [ "$p" = "$ASD" ] && continue
  if ! grep -qxF "$p" "$WORK/touched-a"; then
    extra=$((extra+1))
    say "  B touches a path A did not: $p"
  fi
done < "$WORK/touched-b"

if [ "$differing" = 0 ] && [ "$extra" = 0 ]; then
  pass 6-BLOBS "every path A touched other than $ASD is byte-identical in B, and B touched nothing else"
else
  [ "$differing" = 0 ] || refuse other-blob-differs 6-BLOBS "$differing path(s) A touched are not byte-identical in B"
  [ "$extra" = 0 ] || refuse b-touches-extra-path 6-BLOBS "B touches $extra path(s) A did not"
fi

# --- 7  TESTS ------------------------------------------------------------

# run_suite <ref> <tag>: a fresh worktree, its own ASDF cache, the suite alone.
# Writes "<name> <PASS|FAIL>" lines to $WORK/tests-<tag>.
run_suite() {
  ref=$1; tag=$2
  wt="$WORK/wt-$tag"
  git_ worktree add --detach -q "$wt" "$ref" 2>"$WORK/wt-$tag.err" || {
    say "  worktree for $tag ($ref) failed: $(head -2 "$WORK/wt-$tag.err" | tr '\n' ' ')"; return 1; }
  # A cache of its own, so this measurement never reads another head's fasls and
  # never has to touch the account's shared one.
  ( cd "$wt" && XDG_CACHE_HOME="$WORK/cache-$tag" \
      ${ASDF_CARRY_SUITE:-lisp/nova-work/run-tests.sh} ) > "$WORK/suite-$tag.log" 2>&1
  echo $? > "$WORK/suite-$tag.rc"
  awk '$1 == "TEST" && ($3 == "PASS" || $3 == "FAIL") { print $2" "$3 }' \
    "$WORK/suite-$tag.log" | LC_ALL=C sort > "$WORK/tests-$tag"
  awk '{print $1}' "$WORK/tests-$tag" | LC_ALL=C sort > "$WORK/names-$tag"
  say "  suite at $tag ($ref): rc=$(cat "$WORK/suite-$tag.rc") $(grep -m1 '^NOVA-WORK SLICE1 ' "$WORK/suite-$tag.log" || echo 'no summary line') names=$(LC_ALL=C sort -u "$WORK/names-$tag" | wc -l | tr -d ' ')"
}

if [ "$DO_SUITE" = 0 ]; then
  refuse suite-not-run 7-TESTS "--no-suite was given: the structural stages above stand on their own, but a carry needs the suite"
else
  say "running four suites, one at a time (this needs a quiet bench):"
  ok_all=1
  for pair in "$BASE_A basea" "$A a" "$BASE_B baseb" "$B b"; do
    set -- $pair
    run_suite "$1" "$2" || ok_all=0
  done
  if [ "$ok_all" = 0 ]; then
    refuse suite-did-not-run 7-TESTS "a worktree or a suite could not be started; nothing is measured"
  else
    # (a) A's set is preserved in B.
    only_in "$WORK/names-a" "$WORK/names-b" > "$WORK/lost"
    if [ -s "$WORK/lost" ]; then
      refuse test-names-lost 7-TESTS "$(wc -l < "$WORK/lost" | tr -d ' ') test name(s) registered at A are gone at B"
      sed 's/^/  - /' "$WORK/lost" | tee -a "$OUT"
    else
      pass 7-PRESERVED "every test name registered at A is registered at B"
    fi

    # (b) anything B gained is exactly what dev gained between the bases.
    only_in "$WORK/names-b" "$WORK/names-a" > "$WORK/gained-b"
    only_in "$WORK/names-baseb" "$WORK/names-basea" > "$WORK/gained-dev"
    if cmp -s <(LC_ALL=C sort -u "$WORK/gained-b") <(LC_ALL=C sort -u "$WORK/gained-dev"); then
      pass 7-ADDITIONS "the $(wc -l < "$WORK/gained-b" | tr -d ' ') test name(s) B gained are exactly the ones dev gained between the bases"
    else
      refuse unexplained-test-names 7-TESTS "B's new test names are not the ones dev gained between baseA and baseB"
      diff -u <(LC_ALL=C sort -u "$WORK/gained-dev") <(LC_ALL=C sort -u "$WORK/gained-b") \
        | sed -n '3,30p' | sed 's/^/  /' | tee -a "$OUT"
    fi

    # (c) no NEW duplicate registration. nova-tools#1989's repeated slice
    # registrations are already there in A and in dev, and refusing them would
    # refuse every carry for a fault no PR here introduced.
    dups_of "$WORK/names-a" > "$WORK/tdup-a"
    dups_of "$WORK/names-b" > "$WORK/tdup-b"
    dups_of "$WORK/names-baseb" > "$WORK/tdup-baseb"
    tnew=0
    while read -r name count; do
      [ -n "$name" ] || continue
      if grep -qxF "$name $count" "$WORK/tdup-a" && grep -qxF "$name $count" "$WORK/tdup-baseb"; then
        note "pre-existing duplicate test registration, identical in A and in baseB, allowed: $name x$count (nova-tools#1989)"
      else
        tnew=$((tnew+1))
        say "  NEW duplicate test registration in B: $name x$count"
      fi
    done < "$WORK/tdup-b"
    [ "$tnew" = 0 ] && pass 7-DUPLICATES "B introduces no new duplicate test registration" \
      || refuse new-duplicate-test 7-TESTS "$tnew new duplicate test registration(s) at B"

    # (d) no outcome changes on the tests both heads registered.
    LC_ALL=C sort -u "$WORK/tests-a" > "$WORK/o-a"; LC_ALL=C sort -u "$WORK/tests-b" > "$WORK/o-b"
    changed=0
    while read -r name outcome; do
      [ -n "$name" ] || continue
      grep -qxF "$name" "$WORK/names-b" || continue
      if ! grep -qxF "$name $outcome" "$WORK/o-b"; then
        changed=$((changed+1))
        say "  outcome changed: $name was $outcome at A, is $(grep -m1 "^$name " "$WORK/o-b" | awk '{print $2}') at B"
      fi
    done < "$WORK/o-a"
    [ "$changed" = 0 ] && pass 7-OUTCOMES "no test changed outcome between A and B" \
      || refuse outcome-changed 7-TESTS "$changed test(s) changed outcome between A and B"

    # (e) and B itself must be green. A carry onto a red head is not a carry.
    bline=$(grep -m1 '^NOVA-WORK SLICE1 ' "$WORK/suite-b.log" || true)
    if [ "$(cat "$WORK/suite-b.rc")" = 0 ] && [ "${bline##* }" = "fail=0" ]; then
      pass 7-GREEN "the suite at B is green alone: $bline"
    else
      refuse suite-red-at-b 7-TESTS "the suite at B is not green alone: rc=$(cat "$WORK/suite-b.rc") ${bline:-no summary line}"
      awk '$1 == "TEST" && $3 == "FAIL"' "$WORK/suite-b.log" | head -20 | sed 's/^/  /' | tee -a "$OUT"
    fi
  fi
fi

# --- 8  CI ---------------------------------------------------------------

if [ "$DO_CI" = 0 ]; then
  refuse ci-not-checked 8-CI "--no-ci was given"
else
  ci_out=$( ${ASDF_CARRY_CI:-tools/asdf-carry-ci-ok.sh} "$B" 2>&1 )
  ci_rc=$?
  say "ci-ok at the exact head $B: rc=$ci_rc ${ci_out}"
  if [ "$ci_rc" = 0 ]; then
    pass 8-CI "ci-ok is green at the exact head B"
  else
    refuse ci-not-green-at-b 8-CI "ci-ok is not green at the exact head B: $ci_out"
  fi
fi

# --- range-diff, verbatim -------------------------------------------------

say ""
say "range-diff $BASE_A..$A  $BASE_B..$B"
say "--- begin range-diff ---"
cat "$WORK/range-diff.txt" >> "$OUT"; cat "$WORK/range-diff.txt"
say "--- end range-diff ---"
say ""

# --- verdict --------------------------------------------------------------

if [ -z "$REFUSED" ]; then
  say "CARRY OK  a=$A base-a=$BASE_A b=$B base-b=$BASE_B"
  say "MEANS: the rebase from A to B changed nothing but $ASD's component list, and the suite says the same tests ran with the same outcomes."
  say "DOES NOT MEAN: that anything is approved. Emma decides whether her approve carries; this creates no vote, releases no other holder, and waives no security or merge-history gate."
  [ -n "${OUT##$WORK/*}" ] && echo "verdict written to $OUT" >&2
  exit 0
fi
say "CARRY REFUSED reason=$(echo "$REFUSED" | sed 's/^ //; s/ /,/g')"
say "MEANS: an ordinary re-read. That is the rule's own answer to any failure, not a bug in this script."
[ -n "${OUT##$WORK/*}" ] && echo "verdict written to $OUT" >&2
exit 1
