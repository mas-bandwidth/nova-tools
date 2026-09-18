#!/bin/bash
# with-secrets.sh <cmd> [args...]: run a command with the swarm seat's keys injected by nova-secrets
# (sealed in the store, delivered at use, never a file on disk). Glenn 2026-09-16: no copying
# secrets between machines; these keys are for swarms, not people.
SOPS_BIN=$(command -v sops || true); [ -n "$SOPS_BIN" ] || for c in /opt/homebrew/bin/sops "$HOME/.local/bin/sops" /usr/local/bin/sops; do [ -x "$c" ] && { SOPS_BIN=$c; break; }; done; [ -n "$SOPS_BIN" ] || { echo "with-secrets: no sops found on PATH or in the usual places" >&2; exit 2; }
exec "$HOME/.local/bin/nova-secrets" exec --store "$HOME/rowan-working/secrets" --as studio \
  --key "$HOME/.config/nova-secrets/studio.key" --sops "$SOPS_BIN" \
  --only OPENCODE_API_KEY,DEEPSEEK_API_KEY,INCEPTION_API_KEY --require OPENCODE_API_KEY -- "$@"
