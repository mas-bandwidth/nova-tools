#!/bin/sh
# nova-hook-pending: print the pending bus lines for this name (a HOOK
# CHALLENGE among them) so the model sees them inside its turn. It only
# reads: it never proves, sends, acks or writes. Only the model answers a
# challenge, from inside its own turn, with nova-hook prove.
cat >/dev/null 2>/dev/null || true

bus=${NOVA_BUS_BIN:-}
if [ -z "$bus" ]; then
    if command -v nova-bus >/dev/null 2>&1; then
        bus=nova-bus
    elif [ -x "$HOME/.local/bin/nova-bus" ]; then
        bus=$HOME/.local/bin/nova-bus
    else
        exit 0
    fi
fi
name=${NOVA_HOOK_AS:-aurora}
redis=${NOVA_BUS_REDIS:-127.0.0.1:6381}

out=$("$bus" peek --as "$name" --redis "$redis" 2>/dev/null) || exit 0
printf '%s\n' "$out" | grep 'PEEK MESSAGE' || true
exit 0
