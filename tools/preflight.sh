#!/usr/bin/env bash
# tools/preflight.sh: preflight check: gofmt + go vet + go test -count=1.
#
# Part of Swarm Cards v2 (#2498 Item S4).
# One standard preflight entry: runs gofmt check, go vet, and targeted unit tests,
# catching defects before submitting a card or opening a PR.
#
# Usage:
#   ./tools/preflight.sh [flags] [packages...]
#
# Flags:
#   -h, --help       Show usage and exit
#   -run <pattern>   Filter tests by regexp
#   --run <pattern>  Alias for -run
#
# Environment variables:
#   GO               Go toolchain command (default: go)
#   GOFMT            gofmt command (default: gofmt)
#   MAKE             make command for the test step (default: make)
#   PKGS             Package set when none given on CLI (default: ./cmd/... ./internal/...)
#   RUN              Test regex filter if not given via -run
#   NOVA_TEST_NO_HOST Safe test seam guard (default: 1)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT" || exit 1

GO="${GO:-go}"
GOFMT="${GOFMT:-gofmt}"
MAKE="${MAKE:-make}"
export NOVA_TEST_NO_HOST="${NOVA_TEST_NO_HOST:-1}"

RUN_PATTERN=""
declare -a TARGET_PKGS=()

while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help)
      echo "Usage: $0 [flags] [packages...]"
      echo ""
      echo "Runs preflight: gofmt check, go vet, and targeted unit tests."
      echo ""
      echo "Flags:"
      echo "  -h, --help        Show this help and exit"
      echo "  -run <pattern>    Filter unit tests by regex"
      echo "  --run <pattern>   Alias for -run"
      echo ""
      echo "Environment variables:"
      echo "  GO                Go command (default: go)"
      echo "  GOFMT             gofmt command (default: gofmt)"
      echo "  MAKE              make command for the test step (default: make)"
      echo "  PKGS              Packages to test when no arguments provided"
      echo "  RUN               Test regex filter"
      echo "  NOVA_TEST_NO_HOST Safe test seam guard (default: 1)"
      exit 0
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

echo "=== [3/3] Preflight: targeted unit tests ($PKGS) ==="
declare -a test_args=()
if [ -n "$RUN_PATTERN" ]; then
  test_args+=(RUN="$RUN_PATTERN")
elif [ -n "${RUN:-}" ]; then
  test_args+=(RUN="$RUN")
fi
# GO is forwarded so the Makefile's test-full runs the same toolchain vet did.
# The ${arr[@]+...} form keeps an empty array safe under set -u on bash 3.2.
"$MAKE" test-full GO="$GO" PKGS="$PKGS" ${test_args[@]+"${test_args[@]}"}
echo "unit tests: OK"

echo "=== Preflight: ALL CHECKS PASSED ==="
exit 0
