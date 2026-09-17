# safe-rm.sh: source this; safe_rm PATH... removes only paths strictly below $HOME/rowan-working or $HOME/rowan-swarm-root, never a symlink, never a root, and logs each one.
safe_rm() { local p r ok rp rr; [ -n "${HOME:-}" ] && [ "${HOME#/}" != "$HOME" ] && [ "$(printf %s "$HOME" | tr -cd / | wc -c)" -ge 2 ] || { echo "safe_rm REFUSE: bad HOME" >&2; return 2; }
  for p in "$@"; do ok=0; [ -n "$p" ] && [ ! -L "$p" ] && [ -e "$p" ] || continue; rp=$(realpath -- "$p" 2>/dev/null) || continue
    for r in "$HOME/rowan-working" "$HOME/rowan-swarm-root"; do rr=$(realpath -- "$r" 2>/dev/null) || continue; case "$rp" in "$rr"/*) ok=1;; esac; done
    [ $ok = 1 ] || { echo "safe_rm REFUSE: $p (outside the roots)" >&2; continue; }; printf '%s rm %s\n' "$(date -u +%FT%TZ)" "$rp" >> "$HOME/hygiene.log"; rm -rf -- "$rp"; done; }
