#!/bin/sh
# Run the nova-work slice-1 acceptance cases under SBCL, non-interactively.
#   run-tests.sh                        every case
#   run-tests.sh --suite NAME           one Preservation and recovery suite
#   run-tests.sh --lane per-change|nightly   every suite of that CI lane
# (suites, owners, lanes and cases: *suite-registry* in src/control.lisp,
# SPEC-WORK.md:7064-7107, :7235-7240)
# Exit 0 when every selected case passes, 1 when any fails, 2 on a bad
# argument or unknown suite/lane, 3 for a suite with no case yet (owed). A
# failure outranks owed: a lane with a failing case exits 1 even when it also
# owes a suite, so exit 3 always means "every selected case passed".
#
# WHAT IS MUFFLED, AND WHY NOT MORE (nova-tools #1612). This loaded under
# `(handler-bind ((warning #'muffle-warning)) ...)` -- EVERY warning, blanket --
# and that one form was hiding a build that did not build: `(asdf:load-system
# :nova-work)` FAILED at exit 1 on three duplicate definitions in one file
# (`machine` twice in src/fleet.lisp, `session-identity-line` twice in
# src/transport.lisp, a `request-bundle` constructor clobbered between
# src/operations.lisp and src/transport.lisp) and on a docstring whose
# unescaped `""` made it two docstrings. The suite printed
# `total=327 pass=327 fail=0` over all of it.
#
# Only STYLE-WARNING is muffled now. This system is `:serial t` and full of
# forward references -- src/kernel.lisp calls verbs defined in files after it --
# so undefined-function style-warnings are the normal shape of a correct load
# and there are ~83 of them. A full WARNING is not: it is a duplicate
# definition, a clobbered constructor or a type conflict, and SBCL turns it
# into a COMPILE-FILE-ERROR that ends this script at a non-zero exit. That is
# the check, and it lives here because this is the only gate the kernel has.
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
  --eval "(handler-bind ((style-warning #'muffle-warning)) (asdf:load-system :nova-work/tests))" \
  --eval "$form"
