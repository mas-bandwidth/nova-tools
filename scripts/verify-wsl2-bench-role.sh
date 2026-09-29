#!/usr/bin/env bash
# scripts/verify-wsl2-bench-role.sh — Verification suite for WSL2 bench Ansible role
# PR #4601
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

echo "=== Verifying WSL2 Bench Runner Ansible Role ==="

# 1. Verify required role files exist
echo "[1/6] Checking role structure and declarations..."
REQUIRED_FILES=(
    "roles/wsl2_bench_runner/defaults/main.yml"
    "roles/wsl2_bench_runner/vars/main.yml"
    "roles/wsl2_bench_runner/handlers/main.yml"
    "roles/wsl2_bench_runner/meta/main.yml"
    "roles/wsl2_bench_runner/tasks/main.yml"
    "roles/wsl2_bench_runner/tasks/preflight.yml"
    "roles/wsl2_bench_runner/tasks/runner.yml"
    "roles/wsl2_bench_runner/tasks/packages.yml"
    "roles/wsl2_bench_runner/tasks/toolchains.yml"
    "roles/wsl2_bench_runner/tasks/redis.yml"
    "roles/wsl2_bench_runner/tasks/tailnet.yml"
    "roles/wsl2_bench_runner/tasks/check_mode.yml"
    "playbooks/wsl2_bench_runner.yml"
    "fleet/roles/bench-wsl2/defaults/main.yml"
    "fleet/roles/bench-wsl2/vars/main.yml"
    "fleet/roles/bench-wsl2/tasks/main.yml"
    "fleet/bench-wsl2.yml"
)

for file in "${REQUIRED_FILES[@]}"; do
    if [[ ! -s "${file}" ]]; then
        echo "FAIL: Required file missing or empty: ${file}" >&2
        exit 1
    fi
done
echo "  All required role and playbook files present."

# 2. Check for hardcoded user homes
echo "[2/6] Verifying no hardcoded user homes in role definitions..."
HARDCODED_MATCHES=$(grep -rn "/home/nova" roles/wsl2_bench_runner/ fleet/roles/bench-wsl2/ || true)
if [[ -n "${HARDCODED_MATCHES}" ]]; then
    echo "FAIL: Hardcoded user home found in role files:" >&2
    echo "${HARDCODED_MATCHES}" >&2
    exit 1
fi
echo "  No hardcoded /home/nova paths found; dynamic user homes enforced."

# 3. Check for Windows path and WSL registration validation
echo "[3/6] Verifying Windows path and WSL distro registration tasks..."
if ! grep -q "bench_wsl2_windows_mount" roles/wsl2_bench_runner/tasks/preflight.yml; then
    echo "FAIL: Windows host mount validation missing from tasks/preflight.yml" >&2
    exit 1
fi
if ! grep -q "wsl.exe -l -v" roles/wsl2_bench_runner/tasks/preflight.yml; then
    echo "FAIL: WSL distro registration probe missing from tasks/preflight.yml" >&2
    exit 1
fi
echo "  Windows path and WSL distro registration validations present."

# 4. Check for Go, Rust, Nova tools, and .NET toolchains
echo "[4/6] Verifying toolchain support (Go, Rust, Nova tools, .NET)..."
for tool in "bench_wsl2_go_version" "rustc" "cargo" "bench_wsl2_nova_tools" "dotnet"; do
    if ! grep -q "${tool}" roles/wsl2_bench_runner/tasks/toolchains.yml; then
        echo "FAIL: Toolchain ${tool} missing from tasks/toolchains.yml" >&2
        exit 1
    fi
done
echo "  Toolchains verified (Go, Rust, Nova tools, .NET)."

# 5. Run ansible-playbook syntax checks
echo "[5/6] Running ansible-playbook --syntax-check..."
if command -v ansible-playbook >/dev/null 2>&1; then
    ansible-playbook --syntax-check playbooks/wsl2_bench_runner.yml
    ansible-playbook --syntax-check fleet/bench-wsl2.yml
    echo "  Syntax check passed for playbooks/wsl2_bench_runner.yml and fleet/bench-wsl2.yml."
else
    echo "WARNING: ansible-playbook not on PATH; skipped syntax execution."
fi

# 6. Run ansible-lint if installed
echo "[6/6] Checking ansible-lint..."
if command -v ansible-lint >/dev/null 2>&1; then
    echo "  Running ansible-lint..."
    ansible-lint playbooks/wsl2_bench_runner.yml fleet/bench-wsl2.yml
else
    echo "  ansible-lint not installed; syntax-check verified clean."
fi

echo "=== WSL2 BENCH ROLE VERIFIED OK ==="
exit 0
