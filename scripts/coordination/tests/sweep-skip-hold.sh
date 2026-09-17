#!/usr/bin/env bash
# sweep-loop.sh with a fake gh: the skip list (any layout), the hold flag, and "already in queue" must each keep a PR out.
set -u; T=$(mktemp -d /tmp/sweeptest.XXXXXX); mkdir -p $T/bin; pass=0; fail=0
chk() { if [ "$2" = "$3" ]; then pass=$((pass+1)); else fail=$((fail+1)); echo "  FAIL: $1 (want [$2] got [$3])"; fi; }
cat > $T/bin/gh <<EOG
#!/usr/bin/env bash
case "\$1 \$2" in
  "api graphql") echo 103;;
  "pr list") printf '101=rowan/a\n102=rowan/b\n103=rowan/c\n104=rowan/d\n';;
  "run list") case "\$*" in *rowan/d*) echo "777 completed failure";; *) echo "555 completed success";; esac;;
  "pr merge") echo "\$3" >> $T/merged.txt;;
  "run rerun") echo "\$3" >> $T/reruns.txt;;
esac
EOG
chmod +x $T/bin/gh
run() { rm -f $T/merged.txt $T/reruns.txt; PATH=$T/bin:$PATH SWEEP_SKIP_FILE=$T/skip SWEEP_HOLD_FILE=$T/hold SWEEP_SLEEP=0 "$HOME/rowan-working/bin/sweep-loop.sh" 1 >/dev/null 2>&1; echo $(sort -n $T/merged.txt 2>/dev/null | tr '\n' ' '); }
rm -f $T/skip $T/hold; chk "no skip file: 101 and 102 enqueued, 103 already queued, 104 red" "101 102" "$(run)"
printf 'SKIP="102 900"\n' > $T/skip; chk "old variable layout honoured" "101" "$(run)"
printf 'SKIP="900"\n102\n' > $T/skip; chk "bare number appended (the layout that broke tonight)" "101" "$(run)"
printf '# poison tonight\n102  # redeclared type\n101\n' > $T/skip; chk "comments and one per line" "" "$(run)"
printf 'rm -rf /tmp/SHOULD-NOT-RUN; touch %s/EXECUTED\n102\n' $T > $T/skip; run >/dev/null; chk "skip file is never executed" no "$([ -e $T/EXECUTED ] && echo yes || echo no)"
rm -f $T/skip; echo "pit stop" > $T/hold; chk "hold flag: nothing enqueued" "" "$(run)"
rm -f $T/hold; run >/dev/null; chk "red PR with a deep queue is not rerun (queue depth 1 <= 5 so it is)" "777" "$(cat $T/reruns.txt 2>/dev/null | tr '\n' ' ' | sed 's/ $//')"
echo "RESULT pass=$pass fail=$fail"; rm -rf -- "$T"
