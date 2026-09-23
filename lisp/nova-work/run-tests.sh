#!/bin/sh
# Run the nova-work slice-1 acceptance cases under SBCL, non-interactively.
#   run-tests.sh                        every case
#   run-tests.sh --suite NAME           one Preservation and recovery suite
#   run-tests.sh --lane per-change|nightly   every suite of that CI lane
# (suites, owners, lanes and cases: *suite-registry* in src/control.lisp,
# SPEC-WORK.md:7064-7107, :7235-7240)
# Exit 0 when every selected case passes, 1 when any fails, 2 on a bad
# argument or unknown suite/lane, 3 for a suite with no case yet (owed).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
usage() {
  echo "usage: run-tests.sh [--suite NAME | --lane per-change|nightly]" >&2
  exit 2
}
form="(nova-work/tests:main)"
if [ $# -gt 0 ]; then
  [ $# -eq 2 ] || usage
  case "$1" in --suite|--lane) ;; *) usage ;; esac
  case "$2" in ''|*[!a-z-]*) usage ;; esac
  form="(nova-work/tests:main :${1#--} \"$2\")"
fi
exec sbcl --non-interactive \
  --eval "(require :asdf)" \
  --eval "(push #p\"${here}/\" asdf:*central-registry*)" \
  --eval "(handler-bind ((warning #'muffle-warning)) (asdf:load-system :nova-work/tests))" \
  --eval "$form"
