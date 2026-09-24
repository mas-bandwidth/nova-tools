#!/usr/bin/env sh
# preflight_test.sh: class test for tools/preflight.sh (#2498 Item S4).
#
# No bats, no external harness: plain sh, run directly, matching the shape of
# roadmap-parity_test.sh in this directory.
#
# THE CLASS: CAN PREFLIGHT FALSELY CLEAR A DEFECT OR DROP AN ARGUMENT ON THE
# WAY THROUGH MAKE? The test step hands off to `make test-full`, so each case
# checks what actually reaches the toolchain after that handoff:
#   1. --help prints usage and exits 0.
#   2. unformatted files fail the run (gofmt step).
#   3. a go vet failure fails the run.
#   4. a go test failure behind `make test-full` fails the run (real make).
#   5. a failing make fails the run (fake make: the exit status propagates).
#   6. package arguments reach go vet and, through make, go test.
#   7. -run / --run and RUN= reach go test through make as -run <pattern>.
#   8. PKGS= is the package set when no package arguments are given.
#   9. NOVA_TEST_NO_HOST=1 reaches go test.
#  10. the make handoff is exactly `test-full GO=... PKGS=... [RUN=...]`.
#  11. a real end-to-end run on a clean package passes.
set -u

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
SCRIPT="$SCRIPT_DIR/preflight.sh"
TMP=$(mktemp -d) || exit 1
trap 'rm -rf "$TMP"' EXIT

fails=0
n=0
ok()  { n=$((n+1)); printf 'ok %s - %s\n' "$n" "$1"; }
bad() { n=$((n+1)); printf 'not ok %s - %s: %s\n' "$n" "$1" "$2"; fails=1; }

LOG="$TMP/calls.log"
mkdir -p "$TMP/bin"

cat > "$TMP/bin/gofmt" <<'EOF'
#!/bin/sh
[ "${FAKE_GOFMT_FAIL:-0}" = 1 ] && echo "unformatted.go"
exit 0
EOF

cat > "$TMP/bin/go" <<EOF
#!/bin/sh
cmd="\$1"; shift
case "\$cmd" in
  vet)
    echo "vet: \$*" >> "$LOG"
    [ "\${FAKE_GO_VET_FAIL:-0}" = 1 ] && { echo "vet: fake error" >&2; exit 1; }
    ;;
  test)
    echo "test: \$*" >> "$LOG"
    echo "host-guard: \${NOVA_TEST_NO_HOST:-unset}" >> "$LOG"
    [ "\${FAKE_GO_TEST_FAIL:-0}" = 1 ] && { echo "test: fake failure" >&2; exit 1; }
    ;;
esac
exit 0
EOF

cat > "$TMP/bin/make" <<EOF
#!/bin/sh
echo "make: \$*" >> "$LOG"
exit "\${FAKE_MAKE_RC:-0}"
EOF
chmod +x "$TMP/bin/gofmt" "$TMP/bin/go" "$TMP/bin/make"

# pf <args...>: run preflight with the fake toolchain and the REAL make, so the
# Makefile's test-full recipe is part of what is tested. Sets OUT and RC.
pf() {
  rm -f "$LOG"
  OUT=$(GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" "$@" 2>&1); RC=$?
}
# pfm <args...>: same, with the fake make recording the handoff.
pfm() {
  rm -f "$LOG"
  OUT=$(MAKE="$TMP/bin/make" GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" "$@" 2>&1); RC=$?
}
logged() { grep -qF -- "$1" "$LOG" 2>/dev/null; }
showlog() { cat "$LOG" 2>/dev/null; }

# 1
OUT=$("$SCRIPT" --help 2>&1); RC=$?
if [ "$RC" = 0 ] && echo "$OUT" | grep -qi usage; then ok "--help prints usage and exits 0"
else bad "--help prints usage and exits 0" "rc=$RC out=$OUT"; fi

# 2
rm -f "$LOG"
OUT=$(FAKE_GOFMT_FAIL=1 GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" ./internal/x 2>&1); RC=$?
if [ "$RC" != 0 ] && echo "$OUT" | grep -q unformatted.go && ! logged "test:"; then
  ok "unformatted files fail the run before tests"
else bad "unformatted files fail the run before tests" "rc=$RC out=$OUT log=$(showlog)"; fi

# 3
rm -f "$LOG"
OUT=$(FAKE_GO_VET_FAIL=1 GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" ./internal/x 2>&1); RC=$?
if [ "$RC" != 0 ] && ! logged "test:"; then ok "go vet failure fails the run before tests"
else bad "go vet failure fails the run before tests" "rc=$RC log=$(showlog)"; fi

# 4
rm -f "$LOG"
OUT=$(FAKE_GO_TEST_FAIL=1 GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" ./internal/x 2>&1); RC=$?
if [ "$RC" != 0 ] && logged "test:" && ! echo "$OUT" | grep -q "ALL CHECKS PASSED"; then
  ok "go test failure behind make test-full fails the run"
else bad "go test failure behind make test-full fails the run" "rc=$RC out=$OUT"; fi

# 5
rm -f "$LOG"
OUT=$(FAKE_MAKE_RC=2 MAKE="$TMP/bin/make" GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" ./internal/x 2>&1); RC=$?
if [ "$RC" != 0 ] && logged "make: test-full" && ! echo "$OUT" | grep -q "ALL CHECKS PASSED"; then
  ok "a failing make fails the run"
else bad "a failing make fails the run" "rc=$RC out=$OUT"; fi

# 6
pf ./internal/swarm ./cmd/nova-swarm
if [ "$RC" = 0 ] && logged "vet: ./internal/swarm ./cmd/nova-swarm" \
   && logged "test: -count=1 ./internal/swarm ./cmd/nova-swarm"; then
  ok "package arguments reach go vet and go test through make"
else bad "package arguments reach go vet and go test through make" "rc=$RC out=$OUT log=$(showlog)"; fi

# 7
pf -run TestSpecific ./internal/swarm; r1=$RC; l1=$(showlog)
logged "test: -count=1 -run TestSpecific ./internal/swarm"; g1=$?
pf --run 'TestA|TestB' ./internal/swarm; r2=$RC; l2=$(showlog)
logged "test: -count=1 -run TestA|TestB ./internal/swarm"; g2=$?
rm -f "$LOG"
OUT=$(RUN=TestEnv GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" ./internal/swarm 2>&1); r3=$?
logged "test: -count=1 -run TestEnv ./internal/swarm"; g3=$?
if [ "$r1$g1$r2$g2$r3$g3" = 000000 ]; then ok "-run, --run and RUN= reach go test through make"
else bad "-run, --run and RUN= reach go test through make" "rc=$r1/$r2/$r3 log1=$l1 log2=$l2 log3=$(showlog)"; fi

# 8
rm -f "$LOG"
OUT=$(PKGS=./internal/fromenv GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" 2>&1); RC=$?
if [ "$RC" = 0 ] && logged "vet: ./internal/fromenv" && logged "test: -count=1 ./internal/fromenv"; then
  ok "PKGS= is the package set when no arguments are given"
else bad "PKGS= is the package set when no arguments are given" "rc=$RC log=$(showlog)"; fi

# 9
rm -f "$LOG"
OUT=$(env -u NOVA_TEST_NO_HOST GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" ./internal/x 2>&1); RC=$?
if [ "$RC" = 0 ] && logged "host-guard: 1"; then ok "NOVA_TEST_NO_HOST=1 reaches go test"
else bad "NOVA_TEST_NO_HOST=1 reaches go test" "rc=$RC log=$(showlog)"; fi

# 10
pfm -run TestX ./internal/a ./internal/b; r1=$RC; l1=$(showlog)
logged "make: test-full GO=$TMP/bin/go PKGS=./internal/a ./internal/b RUN=TestX"; g1=$?
pfm ./internal/a; r2=$RC
logged "make: test-full GO=$TMP/bin/go PKGS=./internal/a"; g2=$?
if [ "$r1$g1$r2$g2" = 0000 ] && ! grep -q "RUN=" "$LOG"; then
  ok "make handoff is test-full GO= PKGS= [RUN=]"
else bad "make handoff is test-full GO= PKGS= [RUN=]" "rc=$r1/$r2 log1=$l1 log2=$(showlog)"; fi

# 11
OUT=$("$SCRIPT" -run TestCLIDocSynopsisNamesTheFlagsTheBinaryHas ./internal/docs 2>&1); RC=$?
if [ "$RC" = 0 ] && echo "$OUT" | grep -q "ALL CHECKS PASSED"; then
  ok "real end-to-end preflight passes on a clean package"
else bad "real end-to-end preflight passes on a clean package" "rc=$RC out=$OUT"; fi

if [ "$fails" != 0 ]; then
  echo "FAILED: some preflight tests failed" >&2
  exit 1
fi
echo "ALL $n preflight tests passed."
exit 0
