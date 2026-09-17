#!/usr/bin/env bash
# Hostile-input test of safe_rm inside a fake HOME; a canary outside the roots must survive. Works on macOS and Linux.
set -u; T=$(mktemp -d /tmp/safermtest.XXXXXX); H=$T/home; mkdir -p "$H/rowan-working/a/b" "$H/rowan-swarm-root/s" "$H/canary/keep" "$H/rowan-working-evil/x"; echo precious > "$H/canary/keep/file"
ln -s "$H/canary" "$H/rowan-working/link-out"; mkdir -p "$H/rowan-working/deep"; ln -s "$H/canary/keep" "$H/rowan-working/deep/inner-link"
. "$HOME/rowan-working/bin/safe-rm.sh"; pass=0; fail=0
chk() { if [ "$2" = "$3" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "  FAIL: $1 (want $2 got $3)"; fi; }
canary() { [ -f "$H/canary/keep/file" ] && echo alive || echo DEAD; }
go() { ( HOME="$H"; cd "$H" && safe_rm "$@" ) >/dev/null 2>&1; }
for p in "" "/" "$H" "$H/rowan-working" "$H/rowan-swarm-root" "$H/canary" "$H/rowan-working/../canary" "$H/rowan-working/a/../../canary" "$H/rowan-working/link-out" "$H/rowan-working/link-out/keep" "$H/rowan-working-evil" "$H/rowan-working-evil/x" "../canary" "canary" "$H/rowan-working/a/b/../../.." ; do go "$p"; chk "canary after safe_rm [$p]" alive "$(canary)"; done
chk "root rowan-working survives" yes "$([ -d "$H/rowan-working" ] && echo yes || echo no)"; chk "root swarm-root survives" yes "$([ -d "$H/rowan-swarm-root" ] && echo yes || echo no)"; chk "sibling with same prefix survives" yes "$([ -d "$H/rowan-working-evil/x" ] && echo yes || echo no)"
go "$H/rowan-working/deep"; chk "dir containing a symlink removed" gone "$([ -e "$H/rowan-working/deep" ] && echo there || echo gone)"; chk "symlink target survives that" alive "$(canary)"
go "$H/rowan-working/a"; chk "normal delete works" gone "$([ -e "$H/rowan-working/a" ] && echo there || echo gone)"
( cd "$H/rowan-working" && HOME="$H" safe_rm "../rowan-swarm-root/s" ) >/dev/null 2>&1; chk "relative path inside roots (allowed or refused, never outside)" alive "$(canary)"
for bad in "" "/" "rel/home"; do ( HOME="$bad"; safe_rm "$H/rowan-working" ) >/dev/null 2>&1; chk "bad HOME [$bad] deletes nothing" yes "$([ -d "$H/rowan-working" ] && echo yes || echo no)"; done
chk "log written" yes "$([ -s "$H/hygiene.log" ] && echo yes || echo no)"
echo "RESULT pass=$pass fail=$fail"; rm -rf -- "$T"
