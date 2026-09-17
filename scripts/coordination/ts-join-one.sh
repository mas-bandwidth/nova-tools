#!/usr/bin/env bash
# ts-join-one.sh <ssh-host> <tailscale-binary-path>: join one machine; runs under nova-secrets exec. Never prints key material.
set -uo pipefail; h=$1; ts=$2
api() { printf 'user = "%s:"\n' "$TAILSCALE_API_KEY" | curl -sS -K - "$@"; }
AK=$(api -X POST -H 'Content-Type: application/json' https://api.tailscale.com/api/v2/tailnet/-/keys -d '{"capabilities":{"devices":{"create":{"reusable":false,"ephemeral":false,"preauthorized":true}}},"expirySeconds":600,"description":"fleet join '"$h"'"}' | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d.get("key",""), end=""); sys.stderr.write("mint: "+("ok" if d.get("key") else "FAIL "+str(d)[:120])+"\n")')
[ -n "$AK" ] || exit 1
printf '%s' "$AK" | ssh -o BatchMode=yes -o ConnectTimeout=8 "$h" "sudo -n $ts up --auth-key=file:/dev/stdin --hostname=$h --accept-dns=false 2>&1 | tail -1; echo \"$h ip=\$($ts ip -4 2>/dev/null | head -1)\""
unset AK
id=$(api https://api.tailscale.com/api/v2/tailnet/-/devices | python3 -c 'import sys,json
for d in json.load(sys.stdin).get("devices",[]):
    if d.get("hostname")=="'"$h"'": print(d["id"])' | head -1)
[ -n "$id" ] && api -X POST -H 'Content-Type: application/json' "https://api.tailscale.com/api/v2/device/$id/key" -d '{"keyExpiryDisabled":true}' -o /dev/null -w "$h expiry-disabled http=%{http_code}\n"
