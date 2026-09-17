#!/usr/bin/env bash
# with-jev.sh <cmd...>: run a command with JEV_API_KEY in its environment, by nova-secrets exec from the studio seat. Never printed, never on disk.
exec "$HOME/.local/bin/nova-secrets" exec --store "$HOME/rowan-working/secrets" --as studio \
  --key "$HOME/.config/nova-secrets/studio.key" --sops /opt/homebrew/bin/sops \
  --only JEV_API_KEY --require JEV_API_KEY -- "$@"
