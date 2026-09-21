#!/usr/bin/env bash
# tools/preflight.sh: fast lint, gofmt check, go vet, and targeted unit test suite.
#
# Part of Swarm Cards v2 (#2498 Item S4).
# One standard preflight entry: a card that runs preflight catches lint/fmt/test
# before submitting, preventing gate failures on dev.
#
# Usage:
#   ./tools/preflight.sh [flags] [packages...]
#
# Flags:
#   -h, --help       Show usage and exit
#   --lint-only      Run gofmt check and go vet; skip unit tests
#   --no-test        Alias for --lint-only
#   -run <pattern>   Filter tests by regexp (passed to go test)
#   --run <pattern>  Alias for -run
#
# Environment variables:
#   GO               Go toolchain command (default: go)
#   GOFMT            gofmt command (default: gofmt)
#   PKGS             Package set when none given on CLI (default: ./cmd/... ./internal/...)
#   RUN              Test regex filter if not given via -run
#   NOVA_TEST_NO_HOST Host seam safety guard (default: 1)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT" || exit 1

GO="${GO:-go}"
GOFMT="${GOFMT:-gofmt}"
export NOVA_TEST_NO_HOST="${NOVA_TEST_NO_HOST:-1}"

RUN_TESTS=true
RUN_PATTERN=""
declare -a TARGET_PKGS=()

while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help)
      echo "Usage: $0 [flags] [packages...]"
      echo ""
      echo "Runs fast lint (gofmt check, go vet) and targeted unit tests."
      echo ""
      echo "Flags:"
      echo "  -h, --help        Show this help and exit"
      echo "  --lint-only       Run gofmt and go vet only; skip unit tests"
      echo "  --no-test         Alias for --lint-only"
      echo "  -run <pattern>    Filter unit tests by regex (passed to go test)"
      echo "  --run <pattern>   Alias for -run"
      echo ""
      echo "Environment variables:"
      echo "  GO                Go command (default: go)"
      echo "  GOFMT             gofmt command (default: gofmt)"
      echo "  PKGS              Packages to test when no arguments provided"
      echo "  RUN               Test regex filter"
      echo "  NOVA_TEST_NO_HOST Safe test seam guard (default: 1)"
      exit 0
      ;;
    --lint-only|--no-test)
      RUN_TESTS=false
      shift
      ;;
    -run|--run)
      if [ $# -lt 2 ]; then
        echo "preflight: $1 requires a regex pattern argument" >&2
        exit 1
      fi
      RUN_PATTERN="$2"
      shift 2
      ;;
    *)
      TARGET_PKGS+=("$1")
      shift
      ;;
  esac
done

if [ ${#TARGET_PKGS[@]} -gt 0 ]; then
  PKGS="${TARGET_PKGS[*]}"
elif [ -z "${PKGS:-}" ]; then
  PKGS="./cmd/... ./internal/..."
fi

echo "=== [1/3] Preflight: gofmt check ==="
unformatted="$("$GOFMT" -l . 2>/dev/null || true)"
if [ -n "$unformatted" ]; then
  echo "preflight: gofmt check FAILED: unformatted files detected:" >&2
  echo "$unformatted" >&2
  exit 1
fi
echo "gofmt: OK"

echo "=== [2/3] Preflight: go vet ($PKGS) ==="
# shellcheck disable=SC2086
"$GO" vet $PKGS
echo "go vet: OK"

if [ "$RUN_TESTS" = true ]; then
  echo "=== [3/3] Preflight: targeted unit tests ($PKGS) ==="
  declare -a test_args=(-count=1)
  if [ -n "$RUN_PATTERN" ]; then
    test_args+=(-run "$RUN_PATTERN")
  elif [ -n "${RUN:-}" ]; then
    test_args+=(-run "$RUN")
  fi
  # shellcheck disable=SC2086
  "$GO" test "${test_args[@]}" $PKGS
  echo "unit tests: OK"
else
  echo "=== [3/3] Preflight: unit tests skipped (--lint-only) ==="
fi

echo "=== Preflight: ALL CHECKS PASSED ==="
exit 0
