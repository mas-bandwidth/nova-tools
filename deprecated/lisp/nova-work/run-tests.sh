#!/bin/sh
# Run the nova-work slice-1 acceptance cases under SBCL, non-interactively.
# Exit 0 when every case passes, 1 when any case fails.
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
exec sbcl --non-interactive \
  --eval "(require :asdf)" \
  --eval "(push #p\"${here}/\" asdf:*central-registry*)" \
  --eval "(handler-bind ((style-warning #'muffle-warning)) (asdf:load-system :nova-work/tests))" \
  --eval "(nova-work/tests:main)"
