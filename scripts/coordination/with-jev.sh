#!/usr/bin/env bash
# with-jev.sh <cmd...>: run a command with JEV_API_KEY in its environment, by nova-secrets exec from the studio seat. Never printed, never on disk.
SOPS_BIN=$(command -v sops || true); [ -n "$SOPS_BIN" ] || for c in /opt/homebrew/bin/sops "$HOME/.local/bin/sops" /usr/local/bin/sops; do [ -x "$c" ] && { SOPS_BIN=$c; break; }; done; [ -n "$SOPS_BIN" ] || { echo "with-secrets: no sops found on PATH or in the usual places" >&2; exit 2; }
exec "$HOME/.local/bin/nova-secrets" exec --store "$HOME/rowan-working/secrets" --as studio \
  --key "$HOME/.config/nova-secrets/studio.key" --sops "$SOPS_BIN" \
  --only JEV_API_KEY --require JEV_API_KEY -- "$@"
