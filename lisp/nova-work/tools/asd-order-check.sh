#!/bin/bash
# asd-order-check.sh --- prove that the order of the discovered tests does not
# matter, and that the system loads out of a source archive with no Git.
#
# nova-work.asd discovers its test components from the directory and sorts them
# (nova-tools#1947). ASDF's `:components` order IS the load order, so replacing
# a hand-written order with a sorted one is only safe if the order after the
# prelude carries no meaning. That is a claim about the tree, and a claim about
# the tree is measured, not asserted.
#
# What this does, from a clean checkout of the tree it is run in:
#
#   orders  three throwaway copies whose nova-work.asd has the tests written out
#           EXPLICITLY -- current (the pre-#1947 hand-maintained order), sorted,
#           and reverse-sorted -- each run in a FRESH SBCL image with its own
#           ASDF cache. It then compares the totals, the SET of test names, each
#           name's multiplicity, and every case's PASS/FAIL outcome across the
#           three. Any difference is a hidden dependency between test files and
#           the sorted idiom is not safe until it is fixed or moved into the
#           prelude. tests/asd-discovery.lisp is dropped from these three copies
#           -- it asserts that the components are the sorted directory, which
#           this mode has deliberately undone -- so `orders` measures the
#           acceptance cases and `live` measures the shipped discovery.
#   archive `git archive` of HEAD into a temp directory -- no .git, no index, no
#           working tree -- then loads and runs the suite there, because
#           discovery reads the filesystem and a release tarball is a filesystem
#           with no Git in it.
#   live    the discovery the .asd actually performs, printed, so the sorted
#           order above can be compared with what ships.
#
# Usage:  lisp/nova-work/tools/asd-order-check.sh [orders|archive|live|all]
# Exit 0 only when every comparison holds. Needs sbcl and git; run it on a quiet
# host, because the suite is not parallel-safe with another copy of itself
# (nova-tools#1699).
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
lisp=$(cd "$here/.." && pwd)
root=$(cd "$lisp/../.." && pwd)
what=${1:-all}
work=$(mktemp -d "${TMPDIR:-/tmp}/asd-order-XXXXXX")
trap 'rm -rf "$work"' EXIT
echo "asd-order-check.sh: work=$work root=$root"

# rewrite <asd> <cur|srt|rev> -- replace the tests system's :components with an
# EXPLICIT list: the prelude first, then the rest in the named order.
rewrite() {
  local asd=$1 mode=$2 start comp all rest ordered
  start=$(grep -n 'defsystem "nova-work/tests"' "$asd" | head -1 | cut -d: -f1)
  comp=$(awk -v s="$start" 'NR>=s && /:components/ {print NR; exit}' "$asd")
  head -n $((comp - 1)) "$asd" > "$asd.new"
  all=$(ls "$(dirname "$asd")/tests" | grep '\.lisp$' | sed 's/\.lisp$//' | sed 's|^|tests/|')
  # tests/asd-discovery is dropped from the throwaway copies, and its file with
  # it. This mode deliberately UNDOES the discovery, writing an explicit list in
  # an order that is not the sorted one, and that file's whole job is to assert
  # that the components ARE the sorted directory -- it would fail here for the
  # right reason and drown the signal. What the shipped .asd discovers is the
  # `live` mode's business; what this mode asks is whether the ACCEPTANCE cases
  # care about the order they are loaded in.
  rest=$(printf '%s\n' "$all" \
    | grep -v -x -e 'tests/harness' -e 'tests/acceptance' -e 'tests/asd-discovery')
  rm -f "$(dirname "$asd")/tests/asd-discovery.lisp"
  case "$mode" in
    # The hand-maintained order this file had at dev@1fbb5e20, before #1947,
    # kept in tools/asd-order-legacy.txt so the comparison stays reproducible
    # after the hand list is gone. Any test file added since is appended,
    # sorted, so the mode does not rot as tests land.
    cur) ordered=$( { grep -x -F -f <(printf '%s\n' "$rest") "$here/asd-order-legacy.txt" || true
                      printf '%s\n' "$rest" | grep -v -x -F -f "$here/asd-order-legacy.txt" | LC_ALL=C sort || true
                    } ) ;;
    srt) ordered=$(printf '%s\n' "$rest" | LC_ALL=C sort) ;;
    rev) ordered=$(printf '%s\n' "$rest" | LC_ALL=C sort -r) ;;
    *) echo "rewrite: bad mode $mode" >&2; return 2 ;;
  esac
  {
    printf '  :components ((:file "tests/harness")\n'
    printf '               (:file "tests/acceptance")\n'
    printf '%s\n' "$ordered" | sed 's|.*|               (:file "&")|'
  } > "$asd.body"
  sed -i.bak '$ s/)$/)))/' "$asd.body" && rm -f "$asd.body.bak"
  cat "$asd.body" >> "$asd.new"
  mv "$asd.new" "$asd"
  rm -f "$asd.body"
}

run_one() {
  local dir=$1 tag=$2 rc=0
  rm -rf "$work/cache-$tag"; mkdir -p "$work/cache-$tag"
  XDG_CACHE_HOME="$work/cache-$tag" "$dir/lisp/nova-work/run-tests.sh" > "$work/$tag.out" 2>&1 || rc=$?
  { grep -E '^TEST ' "$work/$tag.out" || true; } | awk '{print $2, $3}' | LC_ALL=C sort > "$work/$tag.outcomes"
  awk '{print $1}' "$work/$tag.outcomes" | LC_ALL=C sort -u > "$work/$tag.names"
  printf '%s: rc=%s %s\n' "$tag" "$rc" "$(grep -E 'NOVA-WORK SLICE1' "$work/$tag.out" || echo 'NO SUMMARY LINE')"
  return $rc
}

fail=0

if [ "$what" = orders ] || [ "$what" = all ]; then
  echo "--- orders: current, sorted, reverse, each in a fresh image ---"
  for m in cur srt rev; do
    git -C "$root" archive HEAD | (mkdir -p "$work/$m" && tar -x -C "$work/$m")
    rewrite "$work/$m/lisp/nova-work/nova-work.asd" "$m"
    run_one "$work/$m" "$m" || { echo "ORDER $m: the suite exited non-zero"; fail=1; }
  done
  for m in srt rev; do
    if diff -u "$work/cur.names" "$work/$m.names" > "$work/$m.names.diff"; then
      echo "names: cur == $m"
    else
      echo "NAMES DIFFER cur vs $m:"; cat "$work/$m.names.diff"; fail=1
    fi
    if diff -u "$work/cur.outcomes" "$work/$m.outcomes" > "$work/$m.outcomes.diff"; then
      echo "outcomes: cur == $m"
    else
      echo "OUTCOMES DIFFER cur vs $m:"; cat "$work/$m.outcomes.diff"; fail=1
    fi
  done
fi

if [ "$what" = archive ] || [ "$what" = all ]; then
  echo "--- archive: git archive HEAD, no .git, load and run there ---"
  mkdir -p "$work/arch"
  git -C "$root" archive HEAD | tar -x -C "$work/arch"
  if [ -d "$work/arch/.git" ]; then
    echo "the archive carries a .git; this is not the test it claims"; fail=1
  fi
  run_one "$work/arch" arch || { echo "ARCHIVE: the suite exited non-zero"; fail=1; }
fi

if [ "$what" = live ] || [ "$what" = all ]; then
  echo "--- live: what the shipped .asd discovers ---"
  sbcl --non-interactive \
    --eval '(require :asdf)' \
    --eval "(push #p\"${lisp}/\" asdf:*central-registry*)" \
    --eval '(asdf:find-system :nova-work/tests)' \
    --eval '(format t "PRELUDE ~{~A ~}~%" nova-work-asdf:+test-prelude+)' \
    --eval '(format t "DISCOVERED ~D~%" (length (nova-work-asdf:discovered-test-names)))' \
    --eval '(dolist (c (asdf:component-children (asdf:find-system "nova-work/tests"))) (format t "COMPONENT ~A~%" (asdf:component-name c)))' \
    > "$work/live.out" 2>&1 || { echo "LIVE: sbcl exited non-zero"; cat "$work/live.out"; fail=1; }
  grep -E '^(PRELUDE|DISCOVERED)' "$work/live.out" || true
  # Parity in the shell, from the directory, with no lisp in the loop: the
  # components ASDF registered must be the prelude followed by every other
  # regular tests/*.lisp, sorted. (tests/asd-discovery.lisp proves the same
  # thing inside the suite; this is the same claim from outside it.)
  grep '^COMPONENT ' "$work/live.out" | sed 's/^COMPONENT //' > "$work/live.components"
  {
    printf 'tests/harness\ntests/acceptance\n'
    ls "$lisp/tests" | grep '\.lisp$' | sed 's/\.lisp$//; s|^|tests/|' \
      | grep -v -x -e 'tests/harness' -e 'tests/acceptance' | LC_ALL=C sort
  } > "$work/live.expected"
  if diff -u "$work/live.expected" "$work/live.components" > "$work/live.diff"; then
    echo "components: the shipped discovery == the prelude then the sorted tests directory"
  else
    echo "THE SHIPPED DISCOVERY IS NOT THE TESTS DIRECTORY:"; cat "$work/live.diff"; fail=1
  fi
fi

if [ "$fail" = 0 ]; then echo "asd-order-check.sh: OK"; else echo "asd-order-check.sh: FAILED"; fi
exit "$fail"
