#!/bin/sh
# Run the nova-work acceptance suite under SBCL, non-interactively, on the CI
# runners. Exit 0 when every case passes, 1 when any case fails.
#
# The system is loaded from the checkout with the ASDF that SBCL bundles — no
# quicklisp, no network — so this works on the self-hosted runners as-is: SBCL
# 2.6.8 on the macOS studio runners and 2.2.9 on the Linux space runners, and
# nothing newer than 2.2.9 is used here. The 120 s budget is the job's
# timeout-minutes in .github/workflows/ci.yml, not a GNU timeout(1) wrapper
# (not installed by default on macOS).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
lisp="$root/lisp/nova-work"

[ -d "$lisp" ] || { echo "lisp-test.sh: lisp/nova-work not found in the checkout" >&2; exit 1; }

# Each run gets its own short TMPDIR. The suite keys its journals, exports and
# socket bases off the ambient TMPDIR and clears stale directories first, so two
# runs on one host (concurrent merge groups, 2026-09-18) wrecked each other's
# state: "held by another process", "destination exists", MKDIR ENOENT. Short,
# because an AF_UNIX path is bounded and the suite only uses TMPDIR directly
# when it is under 80 bytes. Removed on exit, whatever the verdict.
TMPDIR="${LISP_TEST_TMPROOT:-/tmp}/nw-$$"
mkdir -p "$TMPDIR" || { echo "lisp-test.sh: cannot create $TMPDIR" >&2; exit 1; }
export TMPDIR
trap 'rm -rf "$TMPDIR"' EXIT
echo "lisp-test.sh: TMPDIR=$TMPDIR" >&2

sbcl --no-sysinit --no-userinit --non-interactive \
  --eval "(require :asdf)" \
  --eval "(push #p\"${lisp}/\" asdf:*central-registry*)" \
  --eval "(handler-bind ((warning #'muffle-warning)) (asdf:load-system :nova-work/tests))" \
  --eval "(nova-work/tests:main)"