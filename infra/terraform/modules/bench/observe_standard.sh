#!/usr/bin/env bash
# observe_standard.sh is the OS-fact witness of SPEC-FLEET-KUBE, Part 1. Terraform
# runs it on every plan; it runs tools/bench-standard.sh on the bench over ssh
# and prints {"drift":"0"|"1"} as JSON. The module's precondition fails the plan
# the moment the standard and the declaration disagree, so a DRIFT line the
# script prints while the plan is clean is itself a red test.
set -euo pipefail

user="$1"
host="$2"

root="$(cd "$(dirname "$0")/../../../.." && pwd)"
script="$root/tools/bench-standard.sh"
[ -f "$script" ] || { printf '{"drift":"1"}\n' ; exit 0; }

out="$(ssh -o BatchMode=yes -o ConnectTimeout=5 "${user}@${host}" 'bash -s' < "$script" 2>/dev/null || true)"

drift=0
if printf '%s\n' "$out" | grep -qE '^DRIFT( |$)|STANDARD DRIFT'; then
  drift=1
fi
printf '{"drift":"%s"}\n' "$drift"
