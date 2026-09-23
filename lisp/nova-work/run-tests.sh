#!/bin/sh
# Run the nova-work slice-1 acceptance cases under SBCL, non-interactively.
# Exit 0 when every case passes, 1 when any case fails.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
exec sbcl --non-interactive \
  --eval "(require :asdf)" \
  --eval "(push #p\"${here}/\" asdf:*central-registry*)" \
  --eval "(handler-bind ((warning #'muffle-warning)) (asdf:load-system :nova-work/tests))" \
  --eval "(nova-work/tests:main)"
