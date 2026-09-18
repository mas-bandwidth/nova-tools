#!/usr/bin/env bash
# Refusal cases for the bench launchers: each must refuse BEFORE any ssh, exit non-zero, and leave nothing on the bench.
set -u; pass=0; fail=0; printf 'RESULT: x\n' > /tmp/tiny-card.md
chk() { if [ "$2" = "$3" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "  FAIL: $1 (want $2 got $3)"; fi; }
for L in flash-native-bench.sh muse-native-bench.sh; do S=$HOME/rowan-working/bin/$L
  for label in '../../evil; touch /tmp/PWNED-by-label' 'a b' '' '..' '-x' 'x$(id)' 'x`id`' 'a/b'; do out=$(cd / && perl -e 'alarm 30; exec @ARGV' "$S" space swarm-space /tmp/tiny-card.md "$label" 60 2>&1); rc=$?; chk "$L label [$label] refused" 2 "$rc"; done
  out=$(cd / && "$S" 'space; id' swarm-space /tmp/tiny-card.md card-x 60 2>&1); chk "$L hostile bench refused" 2 "$?"
  out=$(cd / && "$S" space 'swarm space' /tmp/tiny-card.md card-x 60 2>&1); chk "$L hostile seat refused" 2 "$?"
  out=$(cd / && "$S" space swarm-space /tmp/tiny-card.md card-x '60; id' 2>&1); chk "$L hostile deadline refused" 2 "$?"
  out=$(cd / && "$S" space swarm-space /tmp/no-such-card.md card-x 60 2>&1); chk "$L missing card refused" 2 "$?"; echo "$out" | grep -q 'not readable' || { fail=$((fail+1)); echo "  FAIL: missing-card message: $out"; }
  out=$(cd / && perl -e 'alarm 40; exec @ARGV' "$S" nosuchbench swarm-x /tmp/tiny-card.md card-x 60 2>&1); chk "$L unreachable bench exit" 3 "$?"; echo "$out" | grep -q UNREACHABLE || { fail=$((fail+1)); echo "  FAIL: unreachable message: $out"; }
done
art=$(ssh -n space 'ls -d /tmp/PWNED-by-label ~/rowan-working/evil ~/rowan-working/tmp/evil 2>/dev/null | wc -l'); chk "no artefacts on the bench" 0 "$(echo $art)"
echo "RESULT pass=$pass fail=$fail"; exit $(( fail > 0 ))
