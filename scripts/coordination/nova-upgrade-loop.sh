#!/usr/bin/env bash
# Standing loop (Glenn 2026-09-15): when nova-tools main moves in cmd/ or internal/, rebuild every tool here,
# post the "tools moved" note so every line rebuilds, receipts and dogfoods, and record it. Bounded: runs for
# $1 seconds (default 6h) polling every $2 seconds (default 600), then exits; re-arm it.
set -uo pipefail
DUR="${1:-21600}"; EVERY="${2:-600}"; ROOT="${NOVA_UPGRADE_ROOT:-$HOME/rowan-working/upgrade-loop}"; mkdir -p "$ROOT"
BUS="${NOVA_UPGRADE_BUS:?set NOVA_UPGRADE_BUS to a fresh bus clone}"; export GH_CONFIG_DIR="$HOME/.config/gh-rowan"
[ -d "$ROOT/nova-tools" ] || git clone -q https://github.com/mas-bandwidth/nova-tools.git "$ROOT/nova-tools"
LAST_FILE="$ROOT/last-sha"; last="$(cat "$LAST_FILE" 2>/dev/null || true)"; end=$(( $(date +%s) + DUR ))
while [ "$(date +%s)" -lt "$end" ]; do
  (cd "$HOME/rowan-working/mirror/nova-tools.git" 2>/dev/null && git remote update --prune >/dev/null 2>&1; cd "$HOME/rowan-working/mirror/nova.git" 2>/dev/null && git remote update --prune >/dev/null 2>&1) 2>/dev/null
  cd "$ROOT/nova-tools" && git fetch -q origin dev && now="$(git rev-parse FETCH_HEAD)"
  if [ -n "$now" ] && [ "$now" != "$last" ]; then
    changed="$(git diff --name-only "${last:-$now~1}" "$now" -- cmd internal go.mod 2>/dev/null | head -50)"
    if [ -n "$changed" ] || [ -z "$last" ]; then
      git checkout -q "$now"; built=""; failed=""
      for t in nova-bus nova-wake nova-swarm nova-review nova-tokens nova-sandbox nova-merge nova-board nova-update nova-version nova-check; do
        if go build -o "$HOME/.local/bin/$t" "./cmd/$t" 2>"$ROOT/build-$t.err"; then built="$built $t"; else failed="$failed $t"; fi
      done
      ver="$("$HOME/.local/bin/nova-swarm" version 2>/dev/null | cut -d' ' -f2)"
      printf 'UPGRADE %s sha=%s built=[%s] failed=[%s] changed=%s\n' "$(date -u +%FT%TZ)" "${now:0:8}" "${built# }" "${failed# }" "$(echo "$changed" | wc -l | tr -d ' ')" >> "$ROOT/upgrade.log"
      # Stella 2026-09-15: duplicate upgrade notices; at most one TOOLS MOVED note and one adoption pass per hour, rebuilds every poll
      lastnote=$(cat "$ROOT/last-note-epoch" 2>/dev/null || echo 0); if [ $(( $(date +%s) - lastnote )) -lt 3600 ]; then last="$now"; printf '%s\n' "$now" > "$LAST_FILE"; printf 'REBUILT %s sha=%s (note and adoption deferred to the hourly slot)\n' "$(date -u +%FT%TZ)" "${now:0:8}" >> "$ROOT/upgrade.log"; sleep "$EVERY"; continue; fi; date +%s > "$ROOT/last-note-epoch"
      note="$ROOT/note-$(date -u +%Y%m%dT%H%M%SZ).md"
      printf 'From: Rowan\nTo: Emma, Freddy, Stella, Rowan\nCc: Johnny\nSubject: TOOLS MOVED: nova-tools dev at %s (%s); rebuild, receipt, dogfood the changed tool\n\nAll,\n\nnova-tools main moved to %s (version %s). Files changed under cmd/ or internal/:\n%s\n\nThe standing rule (Glenn 2026-09-15): rebuild every tool from this sha into your bin; run the changed tool on real work today; file each rough edge on nova-tools in the dogfood shape (tool and command, verbatim output, expected, smallest fix, one card); reply with your receipt: the version lines and the edges filed. This note is written by machinery (bin/nova-upgrade-loop.sh on the Studio); rebuilt here: %s.\n\nRowan\n' "${now:0:8}" "$ver" "${now:0:8}" "$ver" "$(echo "$changed" | sed 's/^/- /' | head -30)" "${built# }" > "$note"
      ( cd "$BUS" && git pull -q --ff-only && nova-bus send --bus . --as Rowan --file "$note" --remote origin --branch main 2>&1 | tail -1 >> "$ROOT/upgrade.log" )
      ssh -o BatchMode=yes -o ConnectTimeout=10 space "export PATH=\$HOME/go/bin:/usr/local/go/bin:\$PATH; cd ~/nova-bench/nova-tools && git fetch -q origin dev && git checkout -q $now && for t in nova-swarm nova-sandbox nova-bus nova-wake nova-version nova-tokens nova-review nova-merge nova-check nova-update; do taskset -c 15 go build -o ~/nova-bench/bin/\$t ./cmd/\$t; done && ~/nova-bench/bin/nova-swarm version" 2>&1 | tail -1 | sed 's/^/SPACE BUILD /' >> "$ROOT/upgrade.log"
      perl -e 'alarm shift; exec @ARGV' 1800 "$HOME/rowan-working/bin/adopt.sh" "${now:0:8}" >> "$ROOT/upgrade.log" 2>&1 || echo "ADOPT TIMEOUT sha=${now:0:8}" >> "$ROOT/upgrade.log"
    fi
    last="$now"; printf '%s\n' "$now" > "$LAST_FILE"
  fi
  sleep "$EVERY"
done
printf 'LOOP END %s\n' "$(date -u +%FT%TZ)" >> "$ROOT/upgrade.log"
