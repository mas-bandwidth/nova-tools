#!/usr/bin/env sh
# preflight_test.sh: class test for tools/preflight.sh (#2498 Item S4).
#
# No bats, no external harness: plain sh, run directly, matching the shape of
# roadmap-parity_test.sh and nova-work-parallel-suites_test.sh in this directory.
#
# THE CLASS: CAN A CARD PREFLIGHT FALSELY CLEAR A DEFECT OR SILENTLY SKIP GATES?
# A preflight target that misses unformatted files, ignores vet errors, skips
# test failures, or fails to propagate arguments would be worse than useless:
# it would give false confidence before submission and lead to gate reds.
#
# Test cases:
#   1. Script exists and is executable.
#   2. --help prints usage and exits 0.
#   3. Gofmt check catches unformatted files and exits non-zero.
#   4. Go vet failure exits non-zero.
#   5. Unit test failure exits non-zero.
#   6. --lint-only / --no-test skips unit tests.
#   7. Package arguments are forwarded to vet and test.
#   8. -run / --run pattern is forwarded to go test.
#   9. NOVA_TEST_NO_HOST is exported.
#  10. Real end-to-end run against an existing clean package succeeds.
set -u

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
SCRIPT="$SCRIPT_DIR/preflight.sh"
TMP=$(mktemp -d) || exit 1
trap 'rm -rf "$TMP"' EXIT

fails=0
n=0
ok()  { n=$((n+1)); printf 'ok %s - %s\n' "$n" "$1"; }
bad() { n=$((n+1)); printf 'not ok %s - %s: %s\n' "$n" "$1" "$2"; fails=1; }

# 1. Script existence and executable permission
if [ -x "$SCRIPT" ]; then
  ok "preflight.sh exists and is executable"
else
  bad "preflight.sh exists and is executable" "script not found or not executable at $SCRIPT"
fi

# 2. --help flag
OUT=$("$SCRIPT" --help 2>&1)
RC=$?
if [ "$RC" = 0 ] && echo "$OUT" | grep -qi "usage"; then
  ok "--help prints usage and exits 0"
else
  bad "--help prints usage and exits 0" "exit=$RC out=$OUT"
fi

# Helper to create fake toolchains in $TMP/bin
setup_fakes() {
  mkdir -p "$TMP/bin"
  cat > "$TMP/bin/gofmt" <<'EOF'
#!/bin/sh
if [ "${FAKE_GOFMT_FAIL:-0}" = "1" ]; then
  echo "unformatted.go"
  exit 0
fi
exit 0
EOF
  chmod +x "$TMP/bin/gofmt"

  cat > "$TMP/bin/go" <<EOF
#!/bin/sh
cmd="\$1"
shift
case "\$cmd" in
  vet)
    if [ "\${FAKE_GO_VET_FAIL:-0}" = "1" ]; then
      echo "vet: fake error" >&2
      exit 1
    fi
    echo "vet-args: \$*" >> "$TMP/go_calls.log"
    exit 0
    ;;
  test)
    if [ "\${FAKE_GO_TEST_FAIL:-0}" = "1" ]; then
      echo "test: fake failure" >&2
      exit 1
    fi
    echo "test-args: \$*" >> "$TMP/go_calls.log"
    echo "test-host-guard: \${NOVA_TEST_NO_HOST:-0}" >> "$TMP/go_calls.log"
    exit 0
    ;;
  *)
    exit 0
    ;;
esac
EOF
  chmod +x "$TMP/bin/go"
}

setup_fakes

# 3. Gofmt failure detection
OUT=$(FAKE_GOFMT_FAIL=1 GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" --lint-only 2>&1)
RC=$?
if [ "$RC" != 0 ] && echo "$OUT" | grep -qi "gofmt"; then
  ok "gofmt check catches unformatted files and exits non-zero"
else
  bad "gofmt check catches unformatted files and exits non-zero" "exit=$RC out=$OUT"
fi

# 4. Go vet failure detection
OUT=$(FAKE_GO_VET_FAIL=1 GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" --lint-only 2>&1)
RC=$?
if [ "$RC" != 0 ] && echo "$OUT" | grep -qi "vet"; then
  ok "go vet failure exits non-zero"
else
  bad "go vet failure exits non-zero" "exit=$RC out=$OUT"
fi

# 5. Unit test failure detection
OUT=$(FAKE_GO_TEST_FAIL=1 GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" 2>&1)
RC=$?
if [ "$RC" != 0 ] && echo "$OUT" | grep -qi "test"; then
  ok "unit test failure exits non-zero"
else
  bad "unit test failure exits non-zero" "exit=$RC out=$OUT"
fi

# 6. --lint-only skips unit tests even if tests would fail
OUT=$(FAKE_GO_TEST_FAIL=1 GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" --lint-only 2>&1)
RC=$?
if [ "$RC" = 0 ] && echo "$OUT" | grep -qi "skipped"; then
  ok "--lint-only skips unit tests and succeeds"
else
  bad "--lint-only skips unit tests and succeeds" "exit=$RC out=$OUT"
fi

# 7. Package arguments forwarding
rm -f "$TMP/go_calls.log"
OUT=$(GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" ./internal/swarm ./cmd/nova-swarm 2>&1)
RC=$?
if [ "$RC" = 0 ] && grep -q "vet-args: ./internal/swarm ./cmd/nova-swarm" "$TMP/go_calls.log" && \
   grep -q "test-args: -count=1 ./internal/swarm ./cmd/nova-swarm" "$TMP/go_calls.log"; then
  ok "package arguments forwarded to vet and test"
else
  bad "package arguments forwarded to vet and test" "exit=$RC log=$(cat "$TMP/go_calls.log" 2>/dev/null)"
fi

# 8. -run regex filter forwarding
rm -f "$TMP/go_calls.log"
OUT=$(GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" -run TestSpecific ./internal/swarm 2>&1)
RC=$?
if [ "$RC" = 0 ] && grep -q -- "-run TestSpecific" "$TMP/go_calls.log"; then
  ok "-run pattern forwarded to go test"
else
  bad "-run pattern forwarded to go test" "exit=$RC log=$(cat "$TMP/go_calls.log" 2>/dev/null)"
fi

# 9. NOVA_TEST_NO_HOST host guard exported
rm -f "$TMP/go_calls.log"
OUT=$(GOFMT="$TMP/bin/gofmt" GO="$TMP/bin/go" "$SCRIPT" ./internal/swarm 2>&1)
RC=$?
if [ "$RC" = 0 ] && grep -q "test-host-guard: 1" "$TMP/go_calls.log"; then
  ok "NOVA_TEST_NO_HOST host guard exported"
else
  bad "NOVA_TEST_NO_HOST host guard exported" "exit=$RC log=$(cat "$TMP/go_calls.log" 2>/dev/null)"
fi

# 10. End-to-end run on an actual repository package
OUT=$("$SCRIPT" -run TestCLIDocSynopsisNamesTheFlagsTheBinaryHas ./internal/docs 2>&1)
RC=$?
if [ "$RC" = 0 ] && echo "$OUT" | grep -q "ALL CHECKS PASSED"; then
  ok "real end-to-end preflight passes on clean package"
else
  bad "real end-to-end preflight passes on clean package" "exit=$RC out=$OUT"
fi

if [ "$fails" != 0 ]; then
  echo "FAILED: some preflight tests failed" >&2
  exit 1
fi
echo "ALL $n preflight tests passed."
exit 0
