#!/usr/bin/env bash
# tools/bench-standard_test.sh — verifies bench-standard.sh builds the card
# environment itself and its verdict is deterministic regardless of the
# caller's shell (nova-tools#2052).
set -euo pipefail

MOCK_HOME=$(mktemp -d)
trap 'rm -rf "$MOCK_HOME"' EXIT

# ------------------------------------------------------------------
# 1. Mock SDK env.sh — the card's environment.
# ------------------------------------------------------------------
mkdir -p "$MOCK_HOME/sdk"
cat > "$MOCK_HOME/sdk/env.sh" <<'ENVEOF'
export PATH="$HOME/sdk/go/bin:$HOME/sdk/bin:$HOME/.local/bin:$HOME/go/bin:/usr/local/bin:/usr/bin:/bin"
ENVEOF

# ------------------------------------------------------------------
# 2. Correct go at SDK path (reports go.mod's version: 1.26.6).
# ------------------------------------------------------------------
mkdir -p "$MOCK_HOME/sdk/go/bin"
cat > "$MOCK_HOME/sdk/go/bin/go" <<'GOEOF'
#!/usr/bin/env bash
echo "go version go1.26.6 linux/amd64"
exit 0
GOEOF
chmod +x "$MOCK_HOME/sdk/go/bin/go"

# ------------------------------------------------------------------
# 3. WRONG go at a shadow path — if bench-standard.sh uses caller's
#    PATH this one wins and the version check drifts.
# ------------------------------------------------------------------
mkdir -p "$MOCK_HOME/shadow/bin"
cat > "$MOCK_HOME/shadow/bin/go" <<'GOEOF'
#!/usr/bin/env bash
echo "go version go1.20.0 linux/amd64"
exit 0
GOEOF
chmod +x "$MOCK_HOME/shadow/bin/go"

# ------------------------------------------------------------------
# 4. Mock binaries for tools checked with `command -v`.
# ------------------------------------------------------------------
mkdir -p "$MOCK_HOME/.local/bin"
for tool in sbcl nova-secrets sops; do
  cat > "$MOCK_HOME/.local/bin/$tool" <<'TOOLEOF'
#!/usr/bin/env bash
exit 0
TOOLEOF
  chmod +x "$MOCK_HOME/.local/bin/$tool"
done

# ------------------------------------------------------------------
# 5. The 16 nova bins — each must report $NOVA_WANT when asked.
# ------------------------------------------------------------------
NOVA_WANT="v1.0.0"
for name in nova-board nova-bus nova-check nova-fuse nova-memory nova-merge nova-pulse nova-review nova-sandbox nova-secrets nova-self-talk nova-swarm nova-tokens nova-update nova-version nova-wake; do
  cat > "$MOCK_HOME/.local/bin/$name" <<BINEOF
#!/usr/bin/env bash
echo "${NOVA_WANT}"
exit 0
BINEOF
  chmod +x "$MOCK_HOME/.local/bin/$name"
done

# ------------------------------------------------------------------
# 6. Toolchain roots that the sandbox wall grants.
# ------------------------------------------------------------------
mkdir -p "$MOCK_HOME/sdk"
mkdir -p "$MOCK_HOME/go/pkg/mod"

# ------------------------------------------------------------------
# 7. Harness.
# ------------------------------------------------------------------
mkdir -p "$MOCK_HOME/nova-bench/harness-0"
cat > "$MOCK_HOME/nova-bench/harness-0/opencode" <<'EOF'
#!/usr/bin/env bash
exit 0
EOF
chmod +x "$MOCK_HOME/nova-bench/harness-0/opencode"

# ------------------------------------------------------------------
# 8. Secrets store at ~/nova-bench/secrets (the bench location).
# ------------------------------------------------------------------
mkdir -p "$MOCK_HOME/nova-bench/secrets"
echo "bench: test-bench" > "$MOCK_HOME/nova-bench/secrets/test-bench.yaml"

# ------------------------------------------------------------------
# 9. Two seat keys — benches hold a bench key AND Stella's key.
# ------------------------------------------------------------------
mkdir -p "$MOCK_HOME/.config/nova-secrets"
touch "$MOCK_HOME/.config/nova-secrets/test-bench.key"
touch "$MOCK_HOME/.config/nova-secrets/stella-test-bench.key"

# ------------------------------------------------------------------
# 10. Run bench-standard.sh with a poisoned PATH (shadow go first).
# ------------------------------------------------------------------
SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd)"
REPO_DIR="$SCRIPT_DIR/.."

out="$(HOME="$MOCK_HOME" \
  PATH="$MOCK_HOME/shadow/bin:$MOCK_HOME/bin:/usr/bin:/bin" \
  NOVA_WANT="$NOVA_WANT" \
  bash "$SCRIPT_DIR/bench-standard.sh" 2>&1 || true)"

# ------------------------------------------------------------------
# 11. The check: if bench-standard.sh builds its own environment,
#     the shadow go is never used, so go1.20.0 must NOT appear.
#     Also check that multiple keys don't trigger keys!=1 drift.
# ------------------------------------------------------------------
FAIL=0
if echo "$out" | grep -q "go version.*go1\.20"; then
  echo "FAIL: bench-standard.sh used wrong go from caller PATH (nova-tools#2052)"
  FAIL=1
fi

if echo "$out" | grep -q "seat keys=2 want=1"; then
  echo "FAIL: bench-standard.sh rejected multiple seat keys (nova-tools#2052)"
  FAIL=1
fi

if [ "$FAIL" = "0" ]; then
  echo "PASS: bench-standard.sh builds card environment, ignores poisoned PATH, handles multiple keys"
  exit 0
fi

echo ""
echo "--- bench-standard.sh output ---"
echo "$out"
exit 1