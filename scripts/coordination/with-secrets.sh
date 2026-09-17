#!/bin/bash
# with-secrets.sh <cmd> [args...]: run a command with the swarm seat's keys injected by nova-secrets
# (sealed in the store, delivered at use, never a file on disk). Glenn 2026-09-16: no copying
# secrets between machines; these keys are for swarms, not people.
exec "$HOME/.local/bin/nova-secrets" exec --store "$HOME/rowan-working/secrets" --as studio \
  --key "$HOME/.config/nova-secrets/studio.key" --sops /opt/homebrew/bin/sops \
  --only OPENCODE_API_KEY,DEEPSEEK_API_KEY,INCEPTION_API_KEY --require OPENCODE_API_KEY -- "$@"
