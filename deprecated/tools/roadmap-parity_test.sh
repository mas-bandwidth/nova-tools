#!/usr/bin/env sh
# roadmap-parity_test.sh: the class test for tools/roadmap-parity.sh.
#
# No bats, no harness: plain sh, run directly. Each case builds a throwaway
# ROADMAP/sexp pair under mktemp -d and runs the script against it.
#
# The class is: CAN THE PAGE STATE A NUMBER ITS OWN DATA DOES NOT SUPPORT.
# A roadmap that shows per-feature acceptance progress is worth more than one
# that hides real progress behind 0%, but only if the shown numbers cannot drift
# from the ticked criteria underneath them. So:
#   (a) the old shape -- no items column -- still passes, and still fails on a
#       feature/item/paren mismatch;
#   (b) an items cell that does not match the ticked boxes in its own detail
#       block fails, in either direction;
#   (c) a cell wider than its total fails;
#   (d) a feature marked verified with unticked criteria fails;
#   (e) the column is all-or-nothing: one feature row without a cell fails;
#   (f) the column totals must reconcile with the sexp's :current-acceptance-items;
#   (g) a fully consistent page with the column passes.
set -u
SCRIPT=$(cd "$(dirname "$0")" && pwd)/roadmap-parity.sh
TMP=$(mktemp -d) || exit 1
trap 'rm -rf "$TMP"' EXIT
fails=0
n=0
ok()  { n=$((n+1)); printf 'ok %s - %s\n' "$n" "$1"; }
bad() { n=$((n+1)); printf 'not ok %s - %s\n' "$n" "$1"; fails=1; }

# sexp <path> <features> <items>
sexp() {
  printf '(  :schema "t"\n  :current-features %s\n  :current-acceptance-items %s\n  :events ())\n' "$2" "$3" > "$1"
}

run() { # run <md> <sexp> -> sets OUT, RC
  OUT=$("$SCRIPT" "$1" "$2" 2>&1); RC=$?
}

expect_ok()   { run "$1" "$2"; if [ "$RC" = 0 ]; then ok "$3"; else bad "$3 [$OUT]"; fi; }
expect_fail() { run "$1" "$2"; if [ "$RC" != 0 ]; then ok "$3"; else bad "$3 [$OUT]"; fi; }

# (a) the old shape, no items column
cat > "$TMP/a.md" <<'MD'
| Feature | Verified |
|---|:---:|
| E01-F01 — one | ❌ |
| E01-F02 — two | ❌ |

**E01-F01 — one**

- [ ] alpha
- [ ] beta

**E01-F02 — two**

- [ ] gamma
MD
sexp "$TMP/a.sexp" 2 3
expect_ok "$TMP/a.md" "$TMP/a.sexp" "(a) legacy shape without the items column passes"

sexp "$TMP/a-bad.sexp" 2 9
expect_fail "$TMP/a.md" "$TMP/a-bad.sexp" "(a) legacy shape still fails on an item-count mismatch"

sexp "$TMP/a-badf.sexp" 7 3
expect_fail "$TMP/a.md" "$TMP/a-badf.sexp" "(a) legacy shape still fails on a feature-count mismatch"

printf '(  :current-features 2\n  :current-acceptance-items 3\n' > "$TMP/a-paren.sexp"
expect_fail "$TMP/a.md" "$TMP/a-paren.sexp" "(a) unbalanced parens in the sexp fail"

# (g) a consistent page WITH the column
cat > "$TMP/g.md" <<'MD'
| Feature | Items | Verified |
|---|:---:|:---:|
| E01-F01 — one | 1/2 | ❌ |
| E01-F02 — two | 1/1 | ✅ |

**E01-F01 — one**

- [x] alpha
- [ ] beta

**E01-F02 — two**

- [x] gamma
MD
sexp "$TMP/g.sexp" 2 3
expect_ok "$TMP/g.md" "$TMP/g.sexp" "(g) consistent items column passes"

# (b) cell disagrees with the ticked boxes, both directions
sed 's|1/2|2/2|' "$TMP/g.md" > "$TMP/b1.md"
expect_fail "$TMP/b1.md" "$TMP/g.sexp" "(b) a cell claiming more ticked than the block has fails"
sed 's|1/2|0/2|' "$TMP/g.md" > "$TMP/b2.md"
expect_fail "$TMP/b2.md" "$TMP/g.sexp" "(b) a cell claiming fewer ticked than the block has fails"
sed 's|1/2|1/5|' "$TMP/g.md" > "$TMP/b3.md"
expect_fail "$TMP/b3.md" "$TMP/g.sexp" "(b) a cell whose total is not the block's item count fails"

# (c) verified wider than total
sed 's|1/1|3/1|' "$TMP/g.md" > "$TMP/c.md"
expect_fail "$TMP/c.md" "$TMP/g.sexp" "(c) a cell with verified greater than total fails"

# (d) a verified mark over unticked criteria
sed 's#| E01-F01 — one | 1/2 | ❌ |#| E01-F01 — one | 1/2 | ✅ |#' "$TMP/g.md" > "$TMP/d.md"
expect_fail "$TMP/d.md" "$TMP/g.sexp" "(d) a feature marked verified with an unticked criterion fails"

# (e) the column is all-or-nothing
sed 's#| E01-F02 — two | 1/1 | ✅ |#| E01-F02 — two | ✅ |#' "$TMP/g.md" > "$TMP/e.md"
expect_fail "$TMP/e.md" "$TMP/g.sexp" "(e) one feature row without an items cell fails"

# (f) the column totals must reconcile with the sexp
sexp "$TMP/f.sexp" 2 4
expect_fail "$TMP/g.md" "$TMP/f.sexp" "(f) items-column total that disagrees with the sexp fails"

printf '1..%s\n' "$n"
[ "$fails" = 0 ] || exit 1
exit 0
