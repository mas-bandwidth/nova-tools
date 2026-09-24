#!/bin/sh
# Compile nova-work and its acceptance files into ASDF's configured output cache without
# running the suite. Prewarm prepares FASLs here; test-lisp owns executing the tests.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
exec sbcl --non-interactive \
  --eval "(require :asdf)" \
  --eval "(push #p\"${here}/\" asdf:*central-registry*)" \
  --eval "(handler-bind ((style-warning #'muffle-warning)) (asdf:compile-system :nova-work/tests))"
