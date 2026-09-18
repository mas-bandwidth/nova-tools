#!/usr/bin/env bash
# observe.sh is the read-each-plan witness of SPEC-FLEET-KUBE, Part 1. Terraform's
# `data "external" "file_state"` runs it on every plan; it ssh's to the bench and
# prints the observed sha256 of one managed file as JSON. The external data
# source contract is JSON on stdout with string values, so a failure that is not
# a hash is exit non-zero rather than a clean reading.
set -euo pipefail

user="$1"
host="$2"
path="$3"

hash="$(ssh -o BatchMode=yes -o ConnectTimeout=5 "${user}@${host}" "sha256sum ${path}")"
hash="${hash%% *}"
printf '{"sha256":"%s"}\n' "$hash"
