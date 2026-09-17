#!/usr/bin/env bash
. "$HOME/rowan-working/bin/safe-rm.sh"
# bench-standard.sh: assert the bench standard (memory bench-provisioning-standard) and fix what it can.
# One FIX/DRIFT line per item, one STANDARD OK|DRIFT line per bench. The shape of nova-work `fleet survey --enforce`.
# Runs ON the Linux bench it checks, with no arguments: `ssh <bench> bash -s < bench-standard.sh` (no -n: ssh -n swallows the script).
# Pit stop 2026-09-17: run on the Studio with a bench name as an argument, it ignored the argument, started checking the Studio, and
# hung; it can kill "stray" runner listeners, which on the Studio are all launchd's. So: refuse anything but Linux, refuse any argument.
[ "$(uname -s)" = Linux ] || { echo "STANDARD REFUSED: this script checks the Linux bench it runs on; run: ssh <bench> bash -s < $0"; exit 2; }
[ $# -eq 0 ] || { echo "STANDARD REFUSED: takes no arguments (got: $*); run it ON the bench: ssh <bench> bash -s < bench-standard.sh"; exit 2; }
export PATH=$HOME/.local/bin:$HOME/go/bin:/usr/local/go/bin:$PATH; drift=0; host=$(hostname | cut -d. -f1)
say() { echo "$host: $*"; }
descendants() { local set="$1" gen more k; for gen in 1 2 3 4; do more=""; for k in $set; do more="$more $(ps -eo pid,ppid | awk -v P="$k" '$2==P {print $1}' | tr '\n' ' ')"; done; set="$set $more"; done; echo "$set"; }
# 1. exactly one listener per runner dir, and it belongs to its unit (user or system); Linux benches only (the Studio runs no runner units)
[ "$(uname)" = Linux ] && for d in $HOME/runner-nova-tools-*/; do d=${d%/}; i=${d##*-}
  pids=$(ps -eo pid,args | awk -v b="$d/bin/Runner.Listener" '$2==b && $3=="run" {print $1}')
  unit_pid=""; for scope in --user --system; do m=$(systemctl $scope show -p MainPID --value nova-runner-$i.service 2>/dev/null); [ -n "$m" ] && [ "$m" != 0 ] && unit_pid="$m" && break; done
  keep=""; [ -n "$unit_pid" ] && keep=$(descendants "$unit_pid")
  for p in $pids; do case " $keep " in *" $p "*) ;; *) kill "$p" 2>/dev/null; say "FIX runner-$i: killed stray listener $p (not under its unit)"; drift=1;; esac; done
  [ -n "$unit_pid" ] || { say "DRIFT runner-$i: no unit running"; drift=1; }
done
# 2. every unit carries PATH and KillMode=control-group
for u in $HOME/.config/systemd/user/nova-runner-*.service /etc/systemd/system/nova-runner-*.service; do [ -f "$u" ] || continue
  grep -q '^Environment=PATH=' "$u" || { say "DRIFT $(basename "$u"): no PATH"; drift=1; }
  grep -q '^KillMode=control-group' "$u" || { say "DRIFT $(basename "$u"): KillMode"; drift=1; }
done
# 3. toolchains, harness, bins
free=$(df -BG "$HOME" 2>/dev/null | awk 'NR==2{gsub("G","",$4); print $4}'); [ "${free:-0}" -ge 15 ] || { say "DRIFT free space ${free}G under HOME (want >= 15G); largest: $(du -xs $HOME/*/ 2>/dev/null | sort -rn | head -2 | awk '{printf "%s=%dM ", $2, $1/1024}')"; drift=1; }
command -v sqlite3 >/dev/null 2>&1 || { say "DRIFT no sqlite3 on PATH (usage rows read opencode.db through it)"; drift=1; }
gv=$(go version 2>/dev/null | cut -d' ' -f3); [ "$gv" = go1.26.5 ] || { say "DRIFT go=$gv want go1.26.5"; drift=1; }
NOVA_WANT=${NOVA_WANT:-97ada57b91fd}; nv=$(nova-swarm version 2>/dev/null | head -1); case "$nv" in *"$NOVA_WANT"*) ;; *) say "DRIFT nova bins $nv want $NOVA_WANT"; drift=1;; esac
for b in $(ls ~/.local/bin 2>/dev/null | grep "^nova-"); do v=$($HOME/.local/bin/$b version 2>/dev/null | head -1); case "$v" in *"$NOVA_WANT"*) ;; *) say "DRIFT $b at ${v:-?} want $NOVA_WANT"; drift=1;; esac; done
command -v sbcl >/dev/null || { say "DRIFT no sbcl"; drift=1; }
[ -x "$HOME/nova-bench/harness-v1.18.20/opencode" ] || { say "DRIFT no harness"; drift=1; }
nb=$(ls "$HOME"/.local/bin/nova-* 2>/dev/null | wc -l); [ "$nb" -ge 16 ] || { say "DRIFT nova bins=$nb"; drift=1; }
# 3b. the real sandbox can resolve and reach the provider catalog (#880 item 19; tonight a host probe passed while every card died inside the wall)
if [ "$(uname)" = Linux ] && [ -x "$HOME/.local/bin/nova-sandbox" ]; then t=$(mktemp -d "$HOME/nova-bench/netprobe.XXXX"); mkdir -p "$t/home"
  code=$(HOME="$t/home" "$HOME/.local/bin/nova-sandbox" --read "$HOME/nova-bench" --write "$t" --cwd "$t" -- curl -s -o /dev/null -w '%{http_code}' https://models.opencode.ai/api.json 2>/dev/null | tail -c 3)
  [ -n "$t" ] && [ -d "$t" ] && case "$t" in /tmp/*|/private/tmp/*|/var/*) rm -rf -- "$t";; esac; [ "$code" = 200 ] || { say "DRIFT sandbox-network: curl inside nova-sandbox got http=${code:-none} (want 200)"; drift=1; }
fi
# 3c. the toolchain the cards need is readable INSIDE the sandbox (2026-09-17: the Go SDK lived at ~/sdk, outside the nova-bench read root; cards on vision could not run go test)
if [ "$(uname)" = Linux ] && [ -x "$HOME/.local/bin/nova-sandbox" ]; then t=$(mktemp -d "$HOME/nova-bench/goprobe.XXXX"); mkdir -p "$t/home"
  gv2=$(HOME="$t/home" "$HOME/.local/bin/nova-sandbox" --read "$HOME/nova-bench" --write "$t" --cwd "$t" -- "$HOME/go/bin/go" version 2>/dev/null | tail -1); rm -rf "$t"
  case "$gv2" in *go1.26.5*) ;; *) say "DRIFT sandbox-go: go inside nova-sandbox says [${gv2:-nothing}] (the SDK must live under ~/nova-bench)"; drift=1;; esac
fi
# 4. secrets: a seat that opens; no plaintext key file; no literal key in a harness config
K=$(ls "$HOME"/.config/nova-secrets/swarm-*.key 2>/dev/null | head -1); s=$(basename "${K:-none}" .key)
if [ -n "$K" ] && [ -d "$HOME/nova-bench/secrets" ]; then (cd "$HOME/nova-bench/secrets" && git pull -q --ff-only 2>/dev/null; nova-secrets check --store . --as "$s" --key "$K" --sops "$HOME/.local/bin/sops" 2>&1 | grep -q 'CHECK OK') || { say "DRIFT seat $s: check fails"; drift=1; }; else say "DRIFT no seat key"; drift=1; fi
for f in "$HOME/.local/share/opencode/auth.json" "$HOME/.config/deepseek/env" "$HOME/.config/freddy/env"; do [ -f "$f" ] && { say "DRIFT plaintext key file $f"; drift=1; }; done
for f in "$HOME"/.config/opencode/*.json; do [ -f "$f" ] && grep -qE 'apiKey": *"sk-' "$f" && { say "DRIFT literal key in $f"; drift=1; }; done
if [ "$drift" = 0 ]; then say "STANDARD OK listeners=$(ps -eo args | awk '$1 ~ /\/bin\/Runner\.Listener$/ && $2=="run"' | wc -l) go=$gv seat=$s"; else say "STANDARD DRIFT (see lines above)"; fi
# disk budget (Glenn 2026-09-17: benches and the AIs on them must not use too much disk; encode what we learn in the setup tool)
[ -d "$HOME/rowan-working/tmp/cache/go-mod" ] && [ -d "$HOME/rowan-working/tmp/cache/go-build" ] || { mkdir -p "$HOME/rowan-working/tmp/cache/go-mod" "$HOME/rowan-working/tmp/cache/go-build" && say "FIX shared Go caches created under rowan-working/tmp/cache (nova-swarm native points cards at them)"; }
systemctl is-active nova-hygiene.timer >/dev/null 2>&1 || { say "DRIFT hygiene timer not active (install: bench-hygiene.sh + nova-hygiene.timer every 10 min)"; drift=1; }
big=0; for s in "$HOME"/rowan-working/tmp/*/ "$HOME"/rowan-swarm-root/*/; do [ -d "$s" ] || continue; g=$(du -xsBG "$s" 2>/dev/null | awk '{gsub("G","",$1); print $1}'); [ "${g:-0}" -gt 1 ] && big=$((big+1)); done; [ $big = 0 ] || { say "DRIFT $big slot(s) over 1 GB (a card's slot holds harness state only; Go caches are shared per bench)"; drift=1; }
free_g=$(df -BG "$HOME" | awk 'NR==2{gsub("G","",$4); print $4}'); [ "$free_g" -ge 25 ] || { say "DRIFT free space ${free_g}G under 25G (launch refuses below it)"; drift=1; }
# mixed stamps (Johnny's row, 2026-09-17 17:15Z: ~/go/bin shadowed the rebuilt ~/.local/bin/nova-swarm for 25 minutes and cards ran without the shared caches): every nova-* in ~/go/bin must be the same stamp as ~/.local/bin, or be removed
for t in $(ls "$HOME/.local/bin" 2>/dev/null | grep '^nova-'); do a=$("$HOME/.local/bin/$t" version 2>/dev/null | head -1); g=$("$HOME/go/bin/$t" version 2>/dev/null | head -1); [ -z "$g" ] && continue; [ "$a" = "$g" ] || { cp -f "$HOME/.local/bin/$t" "$HOME/go/bin/$t" && say "FIX $t in go/bin was $(echo $g | cut -c1-40), now the .local/bin stamp"; }; done
