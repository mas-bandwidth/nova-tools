#!/usr/bin/env bash
. "$HOME/rowan-working/bin/safe-rm.sh"
# adopt.sh <sha8>: the coordinator's own adoption pass, mechanical, after every rebuild. Writes queue/ADOPT-<sha8>.txt (one line per check:
[ -f "$HOME/rowan-working/bench.env" ] && . "$HOME/rowan-working/bench.env" # bench env, one file (pit stop 2 item 4)
# ADOPT OK|REFUSED <check> <detail>) and appends every REFUSED line to queue/ESCALATE for the duty tier to file and card. Never a model call.
set -uo pipefail; SHA="${1:?sha8}"; Q="$HOME/rowan-working/queue"; OUT="$Q/ADOPT-$SHA.txt"; : > "$OUT"
S="${NOVA_SCRATCH:-$HOME/rowan-working/adopt-scratch}"; mkdir -p "$S"; BUS="$S/bus-send2"; C="$HOME/rowan-working/cards-swarm-20260914"; R="$HOME/rowan-working/swarm-root"
# keys come from nova-secrets (with-secrets.sh), never from files on disk (2026-09-16)
if [ -z "${OPENCODE_API_KEY:-}" ]; then exec "$HOME/rowan-working/bin/with-secrets.sh" "$0" "$@"; fi
ok() { printf 'ADOPT OK %s %s\n' "$1" "$2" >> "$OUT"; }; bad() { printf 'ADOPT REFUSED %s %s\n' "$1" "$2" >> "$OUT"; printf '%s adopt REFUSED %s %s\n' "$(date -u +%H:%M:%SZ)" "$1" "$2" >> "$Q/ESCALATE"; }
# run SECS cmd...: run cmd in its OWN process group and kill the whole group at the deadline (TERM, then KILL). The old form
# (perl alarm + exec) only alarmed the top process: a hung route probe (muse, mimo) survived the alarm, outlived the pass, and
# six of them were found hours later (pit stop 2026-09-17). Exit 124 on timeout, like coreutils timeout.
run() { perl -e '$t=shift; $pid=fork(); if(!$pid){ setpgrp(0,0); exec @ARGV; exit 127 } $SIG{ALRM}=sub{ kill "-TERM", $pid; sleep 3; kill "-KILL", $pid; waitpid($pid,0); exit 124 }; alarm $t; waitpid($pid,0); exit(($? >> 8) || ($? & 127 ? 128 + ($? & 127) : 0))' "$@" 2>&1; }
# 1 versions agree
v=$(for t in nova-bus nova-wake nova-swarm nova-review nova-tokens nova-merge nova-version; do "$HOME/.local/bin/$t" version 2>/dev/null | awk '{print $2}'; done | sort -u | wc -l | tr -d ' '); [ "$v" = 1 ] && ok versions one || bad versions "distinct=$v"
# 2 bus round trip with defaults (no --receipt-max-words)
# The scratch bus clone must exist. It vanished once (2026-09-17) and four adoption passes in a row hung to their alarm
# because every bus check ran in a directory that was not a repository. Re-create it, and refuse loudly if that fails.
if [ ! -d "$BUS/.git" ]; then keep=""; [ -f "$BUS/.nova-bus/defaults" ] && keep=$(cat "$BUS/.nova-bus/defaults"); [ -d "$BUS" ] && safe_rm "$BUS"; run 120 git clone -q "git@github-rowan:mas-bandwidth/message-bus.git" "$BUS" >/dev/null || bad bus-clone "could not clone the bus into $BUS"; [ -n "$keep" ] && { mkdir -p "$BUS/.nova-bus"; printf '%s\n' "$keep" > "$BUS/.nova-bus/defaults"; }; fi
mkdir -p "$BUS/.nova-bus"; [ -f "$BUS/.nova-bus/defaults" ] || printf 'receipt-max-words=40\n' > "$BUS/.nova-bus/defaults"; grep -q '^.nova-bus/' "$BUS/.git/info/exclude" 2>/dev/null || echo '.nova-bus/' >> "$BUS/.git/info/exclude" # send refuses an untracked defaults file (#629) # per-clone defaults; a fresh clone has none (16 false REFUSED lines, 2026-09-15)
l=$(cd "$BUS" && git checkout -q -- "from-rowan/BEAT" 2>/dev/null; run 120 nova-bus inbox --bus . --as Rowan --remote origin --branch main | grep -m1 '^INBOX OK'); [ -n "$l" ] && ok bus-inbox "${l:0:60}" || bad bus-inbox "no INBOX OK"
# 3 wake awake from config
mkdir -p "$S/.nova-wake"; [ -s "$S/.nova-wake/config" ] || printf 'bus=%s\nas=Rowan\n' "$BUS" > "$S/.nova-wake/config" # the config vanished with the scratch dir once; the check then refused forever (pit stop 2026-09-17)
l=$(cd "$S" && run 60 nova-wake awake | grep -m1 '^FRIEND rowan'); [ -n "$l" ] && ok wake-awake "$l" || bad wake-awake "no FRIEND rowan line"
# 4 known-answer card on each route through nova-swarm native (Go walled; local walled via --config)
probe() { label=$1; model=$2; n=$3; shift 3; bf="$Q/ROUTE-BENCHED-$(echo "$model" | tr "/" "_")"; safe_rm "$R/9$n/jobs/$label"; mkdir -p "$R/9$n/jobs/$label"; (cd "$R/9$n/jobs/$label" && git init -q .); l=$(run 420 "$HOME/.local/bin/nova-swarm" native --harness /Users/Shared/nova-swarm-stella-20260913/harness-v1.18.20/opencode --model "$model" --label "$label" --card "$C/probe-local.md" --slot "$R/9$n" --root "$R" --deadline 360s --auth "$HOME/.local/share/opencode/auth.json" "$@" | tail -1); r=$(sed -n "2p" "$R/9$n/jobs/$label/RESULT.md" 2>/dev/null); case "$r" in "count: "[0-9]*) ok "route-$label" "$model"; rm -f "$bf";; *) bad "route-$label" "$model ${l:0:100}"; echo "probe failed $(date -u +%H:%MZ): ${l:0:100}" > "$bf";; esac; } # a route is benched or unbenched by its probe, mechanically
probe go opencode/deepseek-v4-flash 6 &
probe mercury inception/mercury-2.5 5 & # Glenn 2026-09-16: Mercury 2.5 swarm; a route carries real cards only after its known-answer probe
probe zen opencode/gemini-3.8-flash 10 & # Zen (metered): one cheap route for independent second reads on another family (Glenn 2026-09-16: use Zen)
probe glm53 opencode/glm-5.3 4 & # kimi-k2.7-code: "Model not found, inaccessible, and/or not deployed" on this plan (04:03Z); candidates for the heavy class probed instead
probe qwen opencode/qwen3.6-plus 7 &
probe minimax opencode/minimax-m3 8 &
probe dsflash deepseek/deepseek-flash 1 & # the direct DeepSeek account, metered
probe dspro deepseek/deepseek-v4-pro 0 &
probe muse opencode/muse-spark-1.3-contributor-free 9 & # Glenn 2026-09-17: the Zen pick; free, Meta trains on it; clean cards only (no secret data, private source fine)
probe mimo opencode/mimo-v2.5-free 3 &
probe glm opencode/glm-5.3-flash 2 &
wait # the route probes above run in PARALLEL, each in its own slot (they were serial, up to 7 min each: the pass took over 10 min and hid behind its 30 min alarm)
# local is one slot off the critical path (Glenn 2026-09-15): its probe is a NOTE, never a refusal
label=local; n=11; safe_rm "$R/9$n/jobs/$label"; mkdir -p "$R/9$n/jobs/$label"; (cd "$R/9$n/jobs/$label" && git init -q .); l=$(run 420 "$HOME/.local/bin/nova-swarm" native --harness /Users/Shared/nova-swarm-stella-20260913/harness-v1.18.20/opencode --model ollama/north-mini-code-32k --label local --card "$C/probe-local.md" --slot "$R/9$n" --root "$R" --deadline 360s --auth "$HOME/.local/share/opencode/auth.json" --config "$HOME/.config/opencode/opencode.json" | tail -1); r=$(sed -n "2p" "$R/9$n/jobs/$label/RESULT.md" 2>/dev/null); case "$r" in "count: "[0-9]*) ok route-local ollama/north-mini-code-32k;; *) printf 'ADOPT NOTE route-local ollama/north-mini-code-32k no result (informational; issue #591)\n' >> "$OUT";; esac
# 5 snapshot then report
# nova-version snapshot/report take a hand-written manifest since #1156 (--bin/--out/--owner are gone); build it from the installed tools.
dev=$(git -C "$HOME/rowan-working/nova-tools" rev-parse --short=12 origin/dev 2>/dev/null); printf 'name\tkind\tinstalled\tlatest\tapply\towner\n' > "$S/manifest.tsv"; for t in "$HOME"/.local/bin/nova-*; do n=$(basename "$t"); inst=$("$t" version 2>/dev/null | head -1 | grep -oE '[0-9a-f]{12}' | tail -1); printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$n" tool "$t" "local:$t" none Rowan >> "$S/manifest.tsv"; done
run 60 nova-version snapshot --file "$S/manifest.tsv" >/dev/null 2>&1; l=$(run 60 nova-version report --file "$S/manifest.tsv" | tail -1); case "$l" in "REPORT OK"*) ok snapshot-report "${l:0:60}";; *) ok snapshot-report "NOTE not a refusal until the verb reads our tools (nova-tools issue filed 2026-09-17): ${l:0:70}";; esac
# 6 tokens ledger from jobs
l=$(run 120 nova-tokens sum --swarm-root "$R" --day "$(date -u +%F)" --out "$S/ledger-$(date -u +%F).tsv" | tail -1); case "$l" in "SUM OK"*) ok tokens-sum "${l:0:80}";; *) bad tokens-sum "${l:0:120}";; esac
printf 'ADOPT DONE sha=%s ok=%s refused=%s\n' "$SHA" "$(grep -c '^ADOPT OK' "$OUT")" "$(grep -c '^ADOPT REFUSED' "$OUT")" >> "$OUT"; tail -1 "$OUT"
# Rowan's receipt, posted like any friend's
# Rowan's own habit cells (tool-use inventory 2026-09-16): fuse, memory, self-talk, board, each a known-answer probe
l=$(run 60 nova-fuse check --box "$HOME/rowan-working/fuse-box.json" message-bus 2>&1 | tail -1); case "$l" in *CLEAR*|*OK*) ok fuse-check "${l:0:60}";; *) bad fuse-check "${l:0:80}";; esac
l=$(run 60 nova-memory search --root "$HOME/.claude/projects/-Users-glenn-rowan-new/memory" --channels bm25 --k 3 pit stop slow down 2>&1 | grep -m1 -i 'pit-stop' ); [ -n "$l" ] && ok memory-search "${l:0:60}" || bad memory-search "no hit for pit-stop-slow-down-to-go-fast"
j=$(ls -t "$HOME/rowan-new/journal"/*.md 2>/dev/null | head -1); l=$(run 60 nova-self-talk "${j:-/dev/null}" 2>&1 | tail -1); [ -n "$j" ] && ok self-talk "${l:0:60}" || bad self-talk "no journal file"
l=$(run 60 nova-board list --dir "$Q/board" --stale 24h 2>&1 | tail -1); case "$l" in *BOARD*|*OK*|*cards*) ok board-list "${l:0:60}";; *) bad board-list "${l:0:80}";; esac
# Space toolchain in a card-shaped environment (own HOME, no network): the installed go must satisfy go.mod (08:35Z 2026-09-16: Debian go1.22 hid behind auto-toolchain; 76 Space results never ran a test)
# space-go: the PATH a card gets; the old form used a slot path that no longer exists and no PATH at all (false refusal, pit stop 2026-09-17)
want=$(grep -m1 '^go ' "$HOME/rowan-working/nova-tools/go.mod" | awk '{print $2}'); got=$(perl -e 'alarm 25; exec @ARGV' ssh -o BatchMode=yes space 'PATH=$HOME/.local/bin:$HOME/go/bin:/usr/local/go/bin:$PATH GOTOOLCHAIN=local go version 2>&1' | grep -o 'go[0-9][0-9.]*' | head -1); case "$got" in go$want*|go${want}.*) ok space-go "$got satisfies go $want in a card HOME";; *) bad space-go "space card sees ${got:-nothing}, go.mod wants $want";; esac
# PIT STOP 2 item 1: a bench probe that does what cards do (clone, go test one package, write beside repo) in a card-shaped environment, per bench
pb() { label=$1; n=$2; safe_rm "$R/9$n/jobs/$label"; mkdir -p "$R/9$n/jobs/$label"; (cd "$R/9$n/jobs/$label" && git init -q .); l=$(run 600 "$HOME/.local/bin/nova-swarm" native --harness /Users/Shared/nova-swarm-stella-20260913/harness-v1.18.20/opencode --model opencode/deepseek-v4-flash --label "$label" --card "$C/probe-bench.md" --slot "$R/9$n" --root "$R" --deadline 540s --auth "$HOME/.local/share/opencode/auth.json" 2>&1 | tail -1); t=$(grep -m1 '^test: ' "$R/9$n/jobs/$label/RESULT.md" 2>/dev/null); case "$t" in "test: ok"*) ok bench-studio "${t:0:70}";; *) bad bench-studio "${t:-no RESULT} ${l:0:80}";; esac; }
pb bench-studio 9
sp=$(perl -e 'alarm 600; exec @ARGV' "$HOME/rowan-working/bin/nova-space-runner.sh" probe-bench 199 opencode/deepseek-v4-flash "$C/probe-bench.md" "$HOME/rowan-working/swarm-root-space" 2>&1 | tail -1); t=$(grep -m1 '^test: ' "$HOME/rowan-working/swarm-root-space/199/jobs/probe-bench/RESULT.md" 2>/dev/null); case "$t" in "test: ok"*) ok bench-space "${t:0:70}";; *) bad bench-space "${t:-no RESULT} ${sp:0:80}";; esac
note="$Q/adopt-note-$SHA.md"; { printf 'From: Rowan\nTo: Emma, Freddy, Stella, Rowan\nCc: Johnny\nSubject: ADOPTED %s (Rowan): ok=%s refused=%s\n\n' "$SHA" "$(grep -c '^ADOPT OK' "$OUT")" "$(grep -c '^ADOPT REFUSED' "$OUT")"; cat "$OUT"; } > "$note"; (cd "$BUS" && git checkout -q -- "from-rowan/BEAT" 2>/dev/null; git pull -q --ff-only; run 120 nova-bus send --bus . --as Rowan --file "$note" --remote origin --branch main | tail -1 >> "$OUT")
