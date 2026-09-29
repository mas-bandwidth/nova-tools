#!/usr/bin/env bash
# seed-work-test.sh: helper script for work-test seed fixtures, raw capture verification,
# and dry-run import verification (ideas#824, stella-f2f5ede0dd21).
#
# Usage:
#   scripts/seed-work-test.sh [--verify] [--dry-run] [--repo owner/name]
#
# Flags:
#   --verify   Verify raw capture byte hashes and run dry-run import tests (default)
#   --dry-run  Execute dry-run verification against seeded fixtures without remote network calls
#   --repo     Target repository (defaults to mas-bandwidth/work-test)
#   -h, --help Show this help text

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
TARGET_REPO="mas-bandwidth/work-test"
MODE="verify"

while [ $# -gt 0 ]; do
  case "$1" in
    --verify)
      MODE="verify"
      shift
      ;;
    --dry-run)
      MODE="dry-run"
      shift
      ;;
    --repo)
      TARGET_REPO="$2"
      shift 2
      ;;
    -h|--help)
      cat <<EOF
Usage: scripts/seed-work-test.sh [options]

Options:
  --verify   Verify byte-exact SHA-256 hashes of raw captures and run dry-run import tests (default)
  --dry-run  Execute dry-run verification against seeded fixtures without remote network calls
  --repo     Target repository (default: mas-bandwidth/work-test)
  -h, --help Print this help message
EOF
      exit 0
      ;;
    *)
      printf "Unknown option: %s\n" "$1" >&2
      exit 1
      ;;
  esac
done

printf "==> Work-test seed helper for %s (mode: %s)\n" "$TARGET_REPO" "$MODE"

# 1. Verify Raw Captures and Hashes
CAPTURES_DIR="$REPO_ROOT/internal/work/testdata/raw_captures"
if [ ! -d "$CAPTURES_DIR" ]; then
  printf "ERROR: raw captures directory not found at %s\n" "$CAPTURES_DIR" >&2
  exit 1
fi

printf "==> Verifying raw capture hashes in %s...\n" "$CAPTURES_DIR"
cd "$CAPTURES_DIR"
shasum -a 256 -c <<'EOF'
11307d78edf54b88d394ea5c8c428d4507c0a7b9274c5ee642e604807c84d827  issues.json
b3ac2c6407b04fe38b5158fd0f5914794bf0f12fc7a7822d68dd9eafcb3e481d  comments-2.json
8a9bd824af57f0c57bbef0e38326fa24f2e9b7e6927f5f296f0c44686b50535c  comments-7.json
EOF

printf "==> Raw capture hashes verified byte-exact.\n"

# 2. Run Dry-Run Import Verification Test Suite
printf "==> Running dry-run import verification suite (internal/work)...\n"
cd "$REPO_ROOT"
go test -v -count=1 ./internal/work/...

printf "==> All seed fixtures and dry-run import tests verified successfully.\n"
