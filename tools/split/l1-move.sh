#!/usr/bin/env bash
# tools/split/l1-move.sh: split L1 (nova-sprint v1.2.3), as a script so it re-runs on any dev head.
#
# Every package under internal/ that nova-sprint needs moves to pkg/, unchanged: same package
# name, same files, same code, so the nova-sprint module can import it. go.mod is untouched.
#
#   1. the set: go list -deps -test (under every build tag the tree uses) over nova-sprint's own
#      packages, kept to internal/, minus nova-sprint's own paths. Written, with each package's
#      importers, to tools/split/l1-packages.txt.
#   2. the move: git mv of each package's files. A subdirectory that is not a package of its own
#      (testdata, an embedded tree) moves with it; a nested package moves only if it is in the set.
#      A CI ledger that mirrors a package path (internal/ci/testdata/<ledger>/internal/<x>/...)
#      follows its package.
#   3. the import rewrite: in every tracked .go file, the quoted import path
#      "<module>/internal/<x>" becomes "<module>/pkg/<x>" for each <x> in the set.
#   4. the path rewrite: every other reference to a moved package's path, in every tracked text
#      file (the class-test ledgers and allowlists, test strings, comments, docs), internal/<x>
#      becomes pkg/<x>, and a Go join "internal", "<x>" becomes "pkg", "<x>". A path is matched
#      whole against the tree's packages, longest first, so internal/<x>/<y> is left alone when
#      <y> is a package that stays. Not rewritten: TLA+ models and configs anywhere (their run records
#      fingerprint the files), the records (CHANGELOG.md, docs/RELEASE-NOTES-*, docs/audit,
#      docs/ratings, docs/dogfood), diff and patch files, the diffcheck
#      fixtures (hypothetical trees), and this directory.
#   5. the roots: the walkers, test selection and preflight that name cmd/ and internal/ as the
#      roots of this repository's code name pkg/ beside internal/ (a fixed list of spellings,
#      each counted), so every rule and every CI selection still covers the moved code.
#   6. gofmt on the Go files that changed (it re-sorts an import block; nothing else).
#
# Run at the repository root on a clean tree; it needs go and perl. It does not commit.
#   tools/split/l1-move.sh            all six steps
#   tools/split/l1-move.sh --list     step 1 only
# Refuses (exit 1) when a package outside nova-sprint imports nova-sprint's code in its own code
# (split L2 not landed), unless L1_ALLOW_REVERSE=1, which only reports them.
#
# The check a reader runs: on the PR's base, run this script and commit; the tree must equal the
# PR's generated commit (git diff --stat <that commit> <the reader's commit> prints nothing).
set -euo pipefail

LIST_ONLY=0
[ "${1:-}" = "--list" ] && LIST_ONLY=1

# nova-sprint's own paths: they leave with nova-sprint and never move to pkg/. The same list as
# internal/ci's sprintOnlyPaths.
SPRINT_PATHS="cmd/nova-sprint cmd/nova-card cmd/nova-work internal/sprint internal/sprintdash internal/card internal/cardgen internal/workfile internal/workgh internal/worklang tools/sprintsize"
TAGS=functional,slow,perf,race

[ -f go.mod ] || { echo "l1-move: run at the repository root" >&2; exit 1; }
if [ "$LIST_ONLY" = 0 ] && [ -n "$(git status --porcelain --untracked-files=no)" ]; then
  echo "l1-move: the tree has changes; run on a clean checkout" >&2; exit 1
fi
[ -e pkg ] && [ "$LIST_ONLY" = 0 ] && { echo "l1-move: pkg/ exists already; L1 has been applied" >&2; exit 1; }
MOD=$(go list -m)
OUT=tools/split/l1-packages.txt
W=$(mktemp -d); trap 'rm -r "$W"' EXIT

issprint() { # reads repo-relative paths, prints those that are nova-sprint's
  local re; re=$(echo "$SPRINT_PATHS" | tr ' ' '\n' | sed 's/[.]/\\./g' | paste -sd'|' -)
  grep -E "^($re)(/|$)" || true
}

# ---- 1. the set
go list -e -tags "$TAGS" -f '{{.ImportPath}}{{"\t"}}{{join .Imports " "}}{{"\t"}}{{join .TestImports " "}} {{join .XTestImports " "}}' ./... |
  sed "s#$MOD/##g" > "$W/imports.tsv"
cut -f1 "$W/imports.tsv" | sort > "$W/all.txt"
issprint < "$W/all.txt" > "$W/sprint.txt"

go list -e -deps -test -tags "$TAGS" -f '{{.ImportPath}}' $(sed "s#^#$MOD/#" "$W/sprint.txt") |
  sed -e 's/ \[.*\]$//' -e '/\.test$/d' -e '/_test$/d' | { grep "^$MOD/internal/" || true; } | sed "s#^$MOD/##" |
  sort -u | { grep -vxF -f "$W/sprint.txt" || true; } > "$W/set.txt"
[ -s "$W/set.txt" ] || { echo "l1-move: empty set" >&2; exit 1; }
N=$(wc -l < "$W/set.txt" | tr -d ' ')

awk -F'\t' '{ n=split($2" "$3, a, " "); for (i=1;i<=n;i++) if (a[i]!="") print a[i]"\t"$1 }' "$W/imports.tsv" | sort -u > "$W/edges.tsv"
{ grep -vxF -f "$W/sprint.txt" "$W/all.txt" || true; } > "$W/nonsprint.txt"
awk -F'\t' '{ n=split($2, a, " "); for (i=1;i<=n;i++) print $1"\t"a[i] }' "$W/imports.tsv" |
  awk -F'\t' 'NR==FNR { s[$1]=1; next } ($1 in s) { print }' "$W/nonsprint.txt" - |
  awk -F'\t' 'NR==FNR { s[$1]=1; next } ($2 in s) { print $1" -> "$2 }' "$W/sprint.txt" - > "$W/reverse.txt"

{
  echo "# split L1: the packages nova-sprint needs, internal/<x> -> pkg/<x>. Generated by tools/split/l1-move.sh;"
  echo "# do not edit. nova-sprint's own paths (not moved): $SPRINT_PATHS"
  echo "# columns: package (before the move) <TAB> direct (yes: nova-sprint's own code or tests import it;"
  echo "#          no: only through another package of this list) <TAB> importers (every package of this"
  echo "#          module that imports it, in code or tests, by their paths before the move)"
  echo "# count: $N"
  while read -r p; do
    imps=$(awk -F'\t' -v p="$p" '$1==p {print $2}' "$W/edges.tsv")
    direct=no
    if [ -n "$(printf '%s\n' "$imps" | issprint)" ]; then direct=yes; fi
    printf '%s\t%s\t%s\n' "$p" "$direct" "$(printf '%s\n' "$imps" | paste -sd' ' -)"
  done < "$W/set.txt"
  if [ -s "$W/reverse.txt" ]; then
    echo "# REVERSE IMPORTS (code outside nova-sprint importing it; split L2 removes these):"
    sed 's/^/# /' "$W/reverse.txt"
  fi
} > "$W/list.txt"
mkdir -p tools/split; cp "$W/list.txt" "$OUT"
echo "l1-move: $N packages; list in $OUT"

if [ -s "$W/reverse.txt" ]; then
  echo "l1-move: code outside nova-sprint imports nova-sprint's code (split L2 not landed):" >&2
  cat "$W/reverse.txt" >&2
  [ "${L1_ALLOW_REVERSE:-0}" = 1 ] || exit 1
fi
[ "$LIST_ONLY" = 1 ] && exit 0

# the tree's package directories under internal/, before the move (step 2's nested-package test,
# step 4's longest-first match)
git ls-files '*.go' | { grep -v '/testdata/' || true; } | xargs -n1 dirname | sort -u > "$W/pkgdirs.txt"
{ grep '^internal/' "$W/pkgdirs.txt" || true; } | sed 's#^internal/##' > "$W/inpkgs.txt"
sed 's#^internal/##' "$W/set.txt" > "$W/moved.txt"

# ---- 2. the move
while read -r p; do
  [ -d "$p" ] || { echo "l1-move: $p is not a directory" >&2; exit 1; }
  dst=pkg/${p#internal/}
  mkdir -p "$dst"
  for e in "$p"/* "$p"/.[!.]*; do
    [ -e "$e" ] || continue
    if [ -d "$e" ] && grep -qE "^$(printf '%s' "$e" | sed 's/[.]/\\./g')(/|$)" "$W/pkgdirs.txt"; then
      continue # a package of its own: it moves only if it is in the set
    fi
    [ -n "$(git ls-files -- "$e")" ] || continue
    git mv "$e" "$dst/"
  done
done < "$W/set.txt"

# the path rewrite as one perl program, for file contents (step 4) and for ledger file names
cat > "$W/rw.pl" <<'PERL'
BEGIN {
  open my $fa, '<', $ENV{L1_INPKGS} or die; my @all = map { chomp; $_ } <$fa>;
  open my $m, '<', $ENV{L1_MOVED} or die; %mv = map { chomp; ($_, 1) } <$m>;
  $re = join '|', map { quotemeta } sort { length($b) <=> length($a) || $a cmp $b } @all;
  $one = join '|', map { quotemeta } grep { !m{/} } sort { length($b) <=> length($a) } keys %mv;
}
s{(?<![A-Za-z0-9_.\-])internal/($re)(?![A-Za-z0-9_\-])}{$mv{$1} ? "pkg/$1" : "internal/$1"}ge;
s{"internal", "($one)"}{"pkg", "$1"}g;
PERL
export L1_INPKGS="$W/inpkgs.txt" L1_MOVED="$W/moved.txt"

# a CI ledger mirroring a package path follows it
git ls-files -- 'internal/ci/testdata/*' | while read -r f; do
  g=$(printf '%s\n' "$f" | perl -p "$W/rw.pl")
  [ "$g" = "$f" ] && continue
  mkdir -p "$(dirname "$g")"; git mv "$f" "$g"
done

# ---- 3. the import rewrite
ALT=$(sed 's/[.]/\\./g' "$W/moved.txt" | sort -r | paste -sd'|' -)
QMOD=$(printf '%s' "$MOD" | sed 's/[.]/\\./g')
git grep -lE "\"$QMOD/internal/($ALT)\"" -- '*.go' > "$W/imports-rewritten.txt" || true
if [ -s "$W/imports-rewritten.txt" ]; then
  xargs perl -pi -e 's{"'"$QMOD"'/internal/('"$ALT"')"}{"'"$MOD"'/pkg/$1"}g' < "$W/imports-rewritten.txt"; fi

# ---- 4. the path rewrite
git grep -I -lE "internal/($ALT)|\"internal\", \"" -- . ':!*.tla' ':!*.cfg' ':!CHANGELOG.md' ':!docs/RELEASE-NOTES-*' ':!docs/audit' ':!docs/ratings' ':!docs/dogfood' \
  ':!*.diff' ':!*.patch' ':!pkg/diffcheck/*_test.go' ':!pkg/diffcheck/testdata' ':!tools/split' > "$W/paths-rewritten.txt" || true
if [ -s "$W/paths-rewritten.txt" ]; then xargs perl -pi "$W/rw.pl" < "$W/paths-rewritten.txt"; fi

# ---- 5. the roots: each spelling of "the repository's code is cmd/ and internal/" gains pkg/
cat > "$W/roots.pl" <<'PERL'
s{"cmd", "internal"(?=[,\}])}{"cmd", "internal", "pkg"}g;
s{"internal", "cmd"(?=[,\}])}{"internal", "pkg", "cmd"}g;
s{GoFilesUnder\((true|false), "internal"\)}{GoFilesUnder($1, "internal", "pkg")}g;
s{"\./cmd/\.\.\.", "\./internal/\.\.\."}{"./cmd/...", "./internal/...", "./pkg/..."}g;
s{\./cmd/\.\.\. \./internal/\.\.\.}{./cmd/... ./internal/... ./pkg/...}g;
s{"cmd/\*\.go", "internal/\*\.go"}{"cmd/*.go", "internal/*.go", "pkg/*.go"}g;
s{"cmd/", "internal/"}{"cmd/", "internal/", "pkg/"}g;
s{strings\.HasPrefix\(pkg, "internal/"\) \|\|}{strings.HasPrefix(pkg, "internal/") || strings.HasPrefix(pkg, "pkg/") ||}g;
PERL
git grep -lE '"cmd", "internal"|"internal", "cmd"|GoFilesUnder\((true|false), "internal"\)|\./cmd/\.\.\.,? "?\./internal/\.\.\.|"cmd/\*\.go", "internal/\*\.go"|"cmd/", "internal/"|HasPrefix\(pkg, "internal/"\) \|\|' \
  -- '*.go' ':!*/testdata/*' ':!pkg/diffcheck/*_test.go' > "$W/roots-rewritten.txt" || true
if [ -s "$W/roots-rewritten.txt" ]; then xargs perl -pi "$W/roots.pl" < "$W/roots-rewritten.txt"; fi

# ---- 6. gofmt on the Go files that changed, outside testdata (fixtures are bytes, not code)
git diff --name-only -- '*.go' | { grep -v '/testdata/' || true; } > "$W/fmt.txt"
if [ -s "$W/fmt.txt" ]; then xargs gofmt -w < "$W/fmt.txt"; fi

echo "l1-move: moved $N packages; import rewrite in $(wc -l < "$W/imports-rewritten.txt" | tr -d ' ') files," \
  "path rewrite in $(wc -l < "$W/paths-rewritten.txt" | tr -d ' ') files, roots in $(wc -l < "$W/roots-rewritten.txt" | tr -d ' ') files"
