#!/usr/bin/env sh
# asdf-carry-verify_test.sh: the class test for tools/asdf-carry-verify.sh.
#
# Plain sh, run directly, the same shape as roadmap-parity_test.sh beside it.
# Each case builds a THROWAWAY GIT REPOSITORY under mktemp -d with four real
# commits -- baseA, A, baseB, B -- and runs the real verifier over them. The
# suite and the ci-ok lookup are injected with ASDF_CARRY_SUITE and
# ASDF_CARRY_CI, so no case needs SBCL or the network; everything else the
# verifier does (range-diff, blob comparison, component parsing, message
# comparison) is the real thing operating on real objects.
#
# THE CLASS: can this verifier say CARRY OK about a rebase that changed
# something other than the .asd component list? A false OK carries an approval
# onto code nobody read, which is the one outcome worse than making Emma re-read
# eleven PRs. So every case below is one way to be falsely green, and each is a
# negative control the rule names:
#
#   (1) the happy path: a pure .asd-only rebase -> CARRY OK
#   (2) a component REMOVED in B
#   (3) a component REORDERED in B
#   (4) a NEW duplicate component in B
#   (5) a SECOND FILE changed in B
#   (6) a CHANGED commit message
#   (7) a test name DROPPED at B
#   (8) a test outcome FLIPPED at B
#   (9) ci-ok not green at the exact head B
#  (10) commits that do not pair (B squashed two into one)
#  (11) nova-tools#1989's tolerance: a duplicate already identical in A and in
#       B's base is REPORTED and allowed, and does not become a refusal
#  (12) --no-suite and --no-ci refuse rather than quietly passing
#  (13) the verdict file carries the pinned shas and the range-diff verbatim
set -u
SCRIPT=$(cd "$(dirname "$0")" && pwd)/asdf-carry-verify.sh
TMP=$(mktemp -d) || exit 1
trap 'rm -rf "$TMP"' EXIT
fails=0
n=0
ok()  { n=$((n+1)); printf 'ok %s - %s\n' "$n" "$1"; }
bad() { n=$((n+1)); printf 'not ok %s - %s\n' "$n" "$1"; fails=1; }

# A fake suite. It runs inside a worktree, reads that worktree's HEAD, and
# prints one TEST line per `tests/...` component the .asd names -- so the test
# names follow the component list, and "dev gained a test" is expressed by dev's
# commit adding one. $TMP/sabotage-<sha> overrides the output for one head,
# which is how a dropped name and a flipped outcome are injected without
# changing any blob (changing a blob would trip a different check and prove
# nothing about this one).
cat > "$TMP/fake-suite.sh" <<'EOF'
#!/bin/sh
sha=$(git rev-parse HEAD)
if [ -f "$SABOTAGE_DIR/sabotage-$sha" ]; then
  cat "$SABOTAGE_DIR/sabotage-$sha"
  exit "$(cat "$SABOTAGE_DIR/rc-$sha" 2>/dev/null || echo 0)"
fi
p=0
for c in $(grep -o '(:file "[^"]*")' lisp/nova-work/nova-work.asd | sed -e 's/^(:file "//' -e 's/")$//'); do
  case "$c" in
    tests/*) printf 'TEST %s PASS spec=s:1 e\n' "${c#tests/}"; p=$((p+1)) ;;
  esac
done
printf 'NOVA-WORK SLICE1 total=%s pass=%s fail=0\n' "$p" "$p"
EOF
chmod +x "$TMP/fake-suite.sh"
export SABOTAGE_DIR="$TMP"
export ASDF_CARRY_SUITE="$TMP/fake-suite.sh"

cat > "$TMP/ci-green.sh" <<'EOF'
#!/bin/sh
echo "ci-ok status=completed conclusion=success at $1"
EOF
cat > "$TMP/ci-red.sh" <<'EOF'
#!/bin/sh
echo "ci-ok status=completed conclusion=failure at $1"
exit 1
EOF
chmod +x "$TMP/ci-green.sh" "$TMP/ci-red.sh"
export ASDF_CARRY_CI="$TMP/ci-green.sh"

# asd <path> <component...> writes a nova-work.asd naming those components.
asd() {
  out=$1; shift
  { printf '(defsystem "nova-work"\n  :serial t\n  :components ('
    first=1
    for c in "$@"; do
      [ "$first" = 1 ] && first=0 || printf '\n               '
      printf '(:file "%s")' "$c"
    done
    printf '))\n'
  } > "$out"
}

# build <dir> — the four-commit fixture. Extra behaviour comes from the
# variables the caller sets before calling: B_COMPONENTS, B_EXTRA_FILE,
# B_MESSAGE, B_SQUASH, BASE_DUP.
build() {
  R=$TMP/$1; rm -rf "$R"; mkdir -p "$R/lisp/nova-work" "$R/src"
  git -C "$R" init -q .
  git -C "$R" config user.email t@example.com
  git -C "$R" config user.name Test

  base_components="src/a src/b tests/t1"
  [ "${BASE_DUP:-0}" = 1 ] && base_components="src/a src/b src/b tests/t1"

  # baseA
  printf 'one\n' > "$R/src/a.lisp"; printf 'two\n' > "$R/src/b.lisp"
  asd "$R/lisp/nova-work/nova-work.asd" $base_components
  git -C "$R" add -A; git -C "$R" commit -qm "baseA"
  BASE_A=$(git -C "$R" rev-parse HEAD)

  # A = baseA + the PR's commit: one new test component and its file.
  printf 'the pr\n' > "$R/tests-t2.txt"
  asd "$R/lisp/nova-work/nova-work.asd" $base_components tests/t2
  git -C "$R" add -A; git -C "$R" commit -qm "the pr: add tests/t2"
  A=$(git -C "$R" rev-parse HEAD)

  # baseB = baseA + a dev landing that also touches the component list.
  git -C "$R" checkout -q -b devline "$BASE_A"
  printf 'dev landed\n' > "$R/tests-dev1.txt"
  asd "$R/lisp/nova-work/nova-work.asd" $base_components tests/dev1
  git -C "$R" add -A; git -C "$R" commit -qm "dev: add tests/dev1"
  BASE_B=$(git -C "$R" rev-parse HEAD)

  # B = the PR's commit rebased onto baseB.
  b_components=${B_COMPONENTS:-"$base_components tests/dev1 tests/t2"}
  printf 'the pr\n' > "$R/tests-t2.txt"
  asd "$R/lisp/nova-work/nova-work.asd" $b_components
  [ -n "${B_EXTRA_FILE:-}" ] && printf 'meddled\n' > "$R/$B_EXTRA_FILE"
  git -C "$R" add -A; git -C "$R" commit -qm "${B_MESSAGE:-the pr: add tests/t2}"
  B=$(git -C "$R" rev-parse HEAD)
}

run() {
  OUT=$("$SCRIPT" --repo "$R" --a "$A" --base-a "$BASE_A" --b "$B" --base-b "$BASE_B" \
        --out "$TMP/verdict.txt" "$@" 2>&1)
  RC=$?
}
reset_vars() { unset B_COMPONENTS B_EXTRA_FILE B_MESSAGE BASE_DUP; ASDF_CARRY_CI="$TMP/ci-green.sh"; rm -f "$TMP"/sabotage-* "$TMP"/rc-*; }

expect_ok() { if [ "$RC" = 0 ]; then ok "$1"; else bad "$1 [$OUT]"; fi; }
expect_refused() { # expect_refused <reason-ere> <label>
  # grep -E, not -q with a BRE `\|`: BSD grep's basic-regex alternation is not
  # portable, and a pattern that silently never matched would turn every one of
  # these negative controls into a case that passes by accident.
  if [ "$RC" = 1 ] && printf '%s' "$OUT" | grep -qE "CARRY REFUSED.*($1)"; then ok "$2"
  else bad "$2 (rc=$RC, wanted refusal naming $1) [$OUT]"; fi
}

# (1) the happy path
reset_vars; build r1; run
expect_ok "a pure .asd-only rebase carries"
case "$OUT" in *"CARRY OK"*) ok "the verdict says CARRY OK" ;; *) bad "the verdict says CARRY OK [$OUT]" ;; esac
case "$OUT" in
  *"DOES NOT MEAN"*) ok "the OK verdict says what it does NOT mean" ;;
  *) bad "the OK verdict says what it does NOT mean [$OUT]" ;;
esac

# (13) the verdict file is the artefact: pinned shas and the range-diff verbatim
V=$TMP/verdict.txt
if grep -q "  A       $A" "$V" && grep -q "  baseA   $BASE_A" "$V" \
   && grep -q "  B       $B" "$V" && grep -q "  baseB   $BASE_B" "$V"; then
  ok "the verdict file pins all four shas"
else bad "the verdict file pins all four shas"; fi
if grep -q -- "--- begin range-diff ---" "$V" && grep -q -- "--- end range-diff ---" "$V" \
   && grep -qE '^ *1: *[0-9a-f]+ ' "$V"; then
  ok "the verdict file carries the range-diff verbatim"
else bad "the verdict file carries the range-diff verbatim [$(cat "$V")]"; fi

# (2) a component removed in B
reset_vars; B_COMPONENTS="src/a tests/t1 tests/dev1 tests/t2"; build r2; run
expect_refused "components-removed" "a component REMOVED in B is refused"

# (3) a component reordered in B
reset_vars; B_COMPONENTS="src/b src/a tests/t1 tests/dev1 tests/t2"; build r3; run
expect_refused "components-reordered" "a component REORDERED in B is refused"

# (4) a new duplicate component in B
reset_vars; B_COMPONENTS="src/a src/b tests/t1 tests/dev1 tests/t2 tests/t2"; build r4; run
expect_refused "new-duplicate-component" "a NEW duplicate component in B is refused"

# (5) a second file changed in B
reset_vars; B_EXTRA_FILE="src/a.lisp"; build r5; run
expect_refused "other-blob-differs|commit-paths-differ" "a SECOND FILE changed in B is refused"

# (6) a changed commit message
reset_vars; B_MESSAGE="the pr: add tests/t2 (rebased)"; build r6; run
expect_refused "commit-message-differs" "a CHANGED commit message is refused"

# (7) a test name dropped at B
reset_vars; build r7
{ printf 'TEST t1 PASS spec=s:1 e\n'; printf 'TEST dev1 PASS spec=s:1 e\n'
  printf 'NOVA-WORK SLICE1 total=2 pass=2 fail=0\n'; } > "$TMP/sabotage-$B"
run
expect_refused "test-names-lost" "a test name DROPPED at B is refused"

# (8) a test outcome flipped at B
reset_vars; build r8
{ printf 'TEST t1 FAIL spec=s:1 e: it broke\n'; printf 'TEST dev1 PASS spec=s:1 e\n'
  printf 'TEST t2 PASS spec=s:1 e\n'
  printf 'NOVA-WORK SLICE1 total=3 pass=2 fail=1\n'; } > "$TMP/sabotage-$B"
echo 1 > "$TMP/rc-$B"
run
expect_refused "outcome-changed" "a test outcome FLIPPED at B is refused"

# (9) ci-ok not green at the exact head B
reset_vars; build r9; ASDF_CARRY_CI="$TMP/ci-red.sh"; run
expect_refused "ci-not-green-at-b" "ci-ok not green at the exact head B is refused"

# (10) commits that do not pair
reset_vars; build r10
# Squash B's single commit together with dev's, so B has one commit where the
# pairing expects one-to-one against A's one over a DIFFERENT base.
git -C "$R" checkout -q -B squashed "$BASE_A"
git -C "$R" merge -q --squash devline 2>/dev/null || true
git -C "$R" checkout -q "$B" -- .
git -C "$R" add -A; git -C "$R" commit -qm "dev: add tests/dev1"
B=$(git -C "$R" rev-parse HEAD); BASE_B=$BASE_A
run
if [ "$RC" = 1 ]; then ok "a rebase whose commits do not pair one-to-one is refused"
else bad "a rebase whose commits do not pair one-to-one is refused [$OUT]"; fi

# (11) nova-tools#1989: a duplicate identical in A and in B's base is allowed
reset_vars; BASE_DUP=1; build r11; run
expect_ok "a duplicate already identical in A and in baseB does not refuse the carry"
case "$OUT" in
  *"pre-existing duplicate component"*"src/b"*) ok "the pre-existing duplicate is REPORTED, not silently ignored" ;;
  *) bad "the pre-existing duplicate is REPORTED, not silently ignored [$OUT]" ;;
esac
# and the same tolerance must NOT extend to a new one on top of it
reset_vars; BASE_DUP=1; B_COMPONENTS="src/a src/b src/b tests/t1 tests/dev1 tests/t2 tests/t2"; build r11b; run
expect_refused "new-duplicate-component" "a NEW duplicate on top of a tolerated one is still refused"

# (12) the escape hatches refuse rather than quietly passing
reset_vars; build r12; run --no-suite
expect_refused "suite-not-run" "--no-suite refuses rather than passing on the structural checks alone"
run --no-ci
expect_refused "ci-not-checked" "--no-ci refuses rather than passing"

printf '1..%s\n' "$n"
[ "$fails" = 0 ] || { printf 'FAILED\n'; exit 1; }
printf 'PASSED\n'
