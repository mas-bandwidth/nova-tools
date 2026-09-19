#!/usr/bin/env bash
# The dealing fill loop with a fake launcher and fixed allowances: cards must be dealt round-robin within each bench's allowance.
set -u; T=$(mktemp -d /tmp/filltest.XXXXXX); mkdir -p $T/bin $T/q/ready; pass=0; fail=0
chk() { if [ "$2" = "$3" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "  FAIL: $1 (want $2 got $3)"; fi; }
cat > $T/bin/fake-launcher.sh <<'EOL'
#!/usr/bin/env bash
echo "$1 $4" >> "$(dirname "$0")/../launches.txt"; echo "$1 $4 attempt=1 wall=1s NATIVE OK"
EOL
chmod +x $T/bin/fake-launcher.sh
run() { FILL_BIN=$T/bin FILL_Q=$T/q FILL_LAUNCHER=fake-launcher.sh FILL_SLEEP=0 FILL_CAPS="$1" "$HOME/rowan-working/bin/fill-loop2.sh" 1 "${2:-60}" 2>&1 | tail -1; }
seed() { rm -f $T/launches.txt $T/q/ready/* $T/q/launched/* 2>/dev/null; i=0; while [ $i -lt $1 ]; do i=$((i+1)); echo "RESULT: c$i" > $T/q/ready/card-$(printf %04d $i).md; done; }
count() { grep -c "^$1 " $T/launches.txt 2>/dev/null || true; }
seed 9; out=$(run "hulk=10 vision=10 space=10"); chk "9 cards, equal room: hulk" 3 "$(count hulk)"; chk "vision" 3 "$(count vision)"; chk "space" 3 "$(count space)"; chk "ready drained" 0 "$(ls $T/q/ready | wc -l | tr -d ' ')"; chk "launched holds 9" 9 "$(ls $T/q/launched | wc -l | tr -d ' ')"
seed 9; out=$(run "hulk=1 vision=0 space=10"); chk "allowance respected: hulk" 1 "$(count hulk)"; chk "vision at zero gets none" 0 "$(count vision)"; chk "space takes the rest" 8 "$(count space)"
seed 9; out=$(run "hulk=2 vision=2 space=2"); chk "over-subscribed: only 6 launched" 6 "$(wc -l < $T/launches.txt | tr -d ' ')"; chk "3 stay ready" 3 "$(ls $T/q/ready | wc -l | tr -d ' ')"
seed 0; out=$(run "hulk=5 vision=5 space=5"); chk "empty ready: no launches" 0 "$( [ -f $T/launches.txt ] && wc -l < $T/launches.txt | tr -d ' ' || echo 0)"; echo "$out" | grep -q 'ready=0' || { fail=$((fail+1)); echo "  FAIL: FILL line: $out"; }
seed 9; out=$(run "hulk=-3 vision=abc space=2"); chk "negative and junk allowances treated as zero" 2 "$(wc -l < $T/launches.txt | tr -d ' ')"
seed 200; out=$(run "hulk=500 vision=500 space=500" 60); chk "per-bench cap 60 holds" 60 "$(count hulk)"; chk "total 180" 180 "$(wc -l < $T/launches.txt | tr -d ' ')"
seed 4; out=$(run "hulk=9 vision=9 space=9"); dup=$(awk '{print $2}' $T/launches.txt | sort | uniq -d | wc -l | tr -d ' '); chk "no card launched twice" 0 "$dup"
echo "RESULT pass=$pass fail=$fail"; rm -rf -- "$T"; exit $(( fail > 0 ))
