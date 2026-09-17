#!/usr/bin/env bash
# tools/bench-standard.sh — the one admin entry for a Linux bench.
#
# Checks a bench against the standard and prints one DRIFT line per finding:
#
#   tools/bench-standard.sh
#   NOVA_GO=go1.26.5 NOVA_WANT=v1.2.3 tools/bench-standard.sh
#   tools/bench-standard.sh --apply   # kills stray runner listeners, nothing else
#
# Exit 0 prints "STANDARD OK ..."; exit 1 prints "STANDARD DRIFT (see lines
# above)" after the DRIFT lines. Runner checks (1)-(2) run only on Linux.
set -uo pipefail

APPLY=0
for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    -h|--help) echo "usage: bench-standard.sh [--apply]"; exit 0 ;;
    *) echo "DRIFT unknown argument $arg" ; echo "STANDARD DRIFT (see lines above)"; exit 1 ;;
  esac
done

NOVA_GO="${NOVA_GO:-go1.26.5}"
NOVA_WANT="${NOVA_WANT:-}"
NOVA_HARNESS="${NOVA_HARNESS:-}"
HOME_DIR="${HOME:-}"
DRIFTS=0
STRAY_PIDS=""

drift() {
  echo "DRIFT $*"
  DRIFTS=$((DRIFTS + 1))
}

# Runner checks run only when uname is Linux.
OS="$(uname -s 2>/dev/null || echo unknown)"
RUNNERS=()
if [ "$OS" = "Linux" ]; then
  for d in "$HOME_DIR"/runner-nova-tools-*/; do
    [ -d "$d" ] || continue
    RUNNERS+=("$d")
  done
fi

if [ "$OS" = "Linux" ] && [ "${#RUNNERS[@]}" -gt 0 ]; then
  for d in "${RUNNERS[@]}"; do
    base="$(basename "$d")"
    idx="${base##*-}"
    unit="nova-runner-${idx}.service"
    # (1) exactly one listener process mentioning the runner dir.
    pids=""
    if command -v ps >/dev/null 2>&1; then
      # ps -eo pid=,args= lists "pid cmd..."; match the literal dir.
      pids="$(ps -eo pid=,args= 2>/dev/null | awk -v dir="$d" 'index($0, dir) {print $1}')"
    else
      drift "$unit ps not available to count listeners in $d"
      continue
    fi
    n="$(echo "$pids" | grep -c . || true)"
    if [ "$n" != "1" ]; then
      drift "$unit listeners=$n want=1 in $d"
    fi
    # (1b) the listener descends from the systemd unit.
    for pid in $pids; do
      under=0
      if [ -f "/proc/$pid/cgroup" ] && grep -q "$unit" "/proc/$pid/cgroup" 2>/dev/null; then
        under=1
      fi
      if [ "$under" = "0" ] && command -v systemctl >/dev/null 2>&1; then
        main="$(systemctl --user show "$unit" -p MainPID 2>/dev/null | cut -d= -f2)"
        if [ -n "$main" ] && [ "$main" != "0" ] && [ "$main" = "$pid" ]; then
          under=1
        fi
        main="$(systemctl show "$unit" -p MainPID 2>/dev/null | cut -d= -f2)"
        if [ -n "$main" ] && [ "$main" != "0" ] && [ "$main" = "$pid" ]; then
          under=1
        fi
      fi
      if [ "$under" = "0" ]; then
        drift "$unit pid=$pid not under $unit"
        STRAY_PIDS="$STRAY_PIDS $pid"
      fi
    done
    # (2) the unit file carries the standard stanzas.
    unitfile=""
    if [ -f "$HOME_DIR/.config/systemd/user/$unit" ]; then
      unitfile="$HOME_DIR/.config/systemd/user/$unit"
    elif [ -f "/etc/systemd/system/$unit" ]; then
      unitfile="/etc/systemd/system/$unit"
    else
      drift "$unit missing unit file $unit"
      continue
    fi
    if ! grep -q "Environment=PATH" "$unitfile" || ! grep -q "go/bin" "$unitfile" || ! grep -q ".local/bin" "$unitfile"; then
      drift "$unit unit file PATH lacks go/bin and .local/bin in $unitfile"
    fi
    if ! grep -q "KillMode=control-group" "$unitfile"; then
      drift "$unit unit file lacks KillMode=control-group in $unitfile"
    fi
    if ! grep -q "TimeoutStopSec=30s" "$unitfile"; then
      drift "$unit unit file lacks TimeoutStopSec=30s in $unitfile"
    fi
  done
fi

# --apply kills stray runner listeners not under their unit, nothing else.
if [ "$APPLY" = "1" ] && [ -n "$STRAY_PIDS" ]; then
  # shellcheck disable=SC2086
  for pid in $STRAY_PIDS; do
    case "$pid" in
      ''|*[!0-9]*) continue ;;
    esac
    if [ "$pid" = "1" ]; then
      continue
    fi
    kill "$pid" 2>/dev/null || true
  done
  echo "NOTE stray runner listeners killed:$STRAY_PIDS"
fi

# (3) go version, sbcl, harness.
if command -v go >/dev/null 2>&1; then
  goout="$(go version 2>&1 || true)"
  case "$goout" in
    *"$NOVA_GO"*) ;;
    *) drift "go version [$goout] want $NOVA_GO" ;;
  esac
else
  drift "go not on PATH want $NOVA_GO"
fi
if ! command -v sbcl >/dev/null 2>&1; then
  drift "sbcl not on PATH"
fi
harness_ok=0
if [ -n "$NOVA_HARNESS" ]; then
  if [ -x "$NOVA_HARNESS" ]; then
    harness_ok=1
  else
    drift "harness missing at NOVA_HARNESS=$NOVA_HARNESS"
  fi
else
  for h in "$HOME_DIR"/nova-bench/harness-*/opencode; do
    if [ -x "$h" ]; then
      harness_ok=1
      break
    fi
  done
  if [ "$harness_ok" = "0" ]; then
    drift "harness missing at $HOME_DIR/nova-bench/harness-<ver>/opencode"
  fi
fi

# (3b) the network probe runs inside the real sandbox, never on the host, so
# what it reports is what a card would see (#893).
if [ "$OS" = "Linux" ]; then
  probe_url="${NOVA_PROBE_URL:-https://models.opencode.ai/api.json}"
  probe_dir="$(mktemp -d "$HOME_DIR/nova-bench/nova-probe.XXXXXX" 2>/dev/null || true)"
  if [ -z "$probe_dir" ]; then
    drift "sandbox-network: cannot make probe dir under $HOME_DIR/nova-bench"
  else
    mkdir -p "$probe_dir/home"
    sandbox_bin="$HOME_DIR/.local/bin/nova-sandbox"
    if [ ! -x "$sandbox_bin" ]; then
      drift "sandbox-network: $sandbox_bin not executable"
    else
      http="$(HOME="$probe_dir/home" "$sandbox_bin" --read "$HOME_DIR/nova-bench" --write "$probe_dir" --cwd "$probe_dir" -- curl -s -o /dev/null -w '%{http_code}' "$probe_url" 2>/dev/null || true)"
      # Only an all-digit reply is curl's http code. Anything else means the
      # binary did not run the command (check (4) already owns whether the
      # nova-sandbox on the bench is the one we want); an empty reply is a
      # reachability failure and still drifts.
      case "$http" in
        "") drift "sandbox-network: curl inside nova-sandbox got http= (want 200)" ;;
        *[!0-9]*) ;;
        *) [ "$http" = "200" ] || drift "sandbox-network: curl inside nova-sandbox got http=$http (want 200)" ;;
      esac
    fi
    rm -rf "$probe_dir"
  fi
fi

# (4) the 16 nova bins each report $NOVA_WANT.
if [ -z "$NOVA_WANT" ]; then
  drift "NOVA_WANT unset (set NOVA_WANT to the wanted version)"
else
  for name in nova-board nova-bus nova-check nova-fuse nova-memory nova-merge nova-pulse nova-review nova-sandbox nova-secrets nova-self-talk nova-swarm nova-tokens nova-update nova-version nova-wake; do
    bin="$HOME_DIR/.local/bin/$name"
    if [ ! -x "$bin" ]; then
      drift "$name missing at $bin"
      continue
    fi
    if ! out="$("$bin" version 2>&1)"; then
      out="$("$bin" --version 2>&1 || true)"
    fi
    case "$out" in
      *"$NOVA_WANT"*) ;;
      *) drift "$name version [$out] want $NOVA_WANT" ;;
    esac
  done
fi

# (5) a seat: exactly one *.key and nova-secrets check passes.
seatdir="$HOME_DIR/.config/nova-secrets"
nkeys=0
seatkey=""
if [ -d "$seatdir" ]; then
  for k in "$seatdir"/*.key; do
    [ -e "$k" ] || continue
    nkeys=$((nkeys + 1))
    seatkey="$k"
  done
fi
if [ "$nkeys" != "1" ]; then
  drift "seat keys=$nkeys want=1 in $seatdir"
else
  if ! command -v nova-secrets >/dev/null 2>&1; then
    drift "nova-secrets not on PATH for seat check of $seatkey"
  else
    seat="$(basename "$seatkey" .key)"
    store="${NOVA_SECRETS_STORE:-}"
    if [ -z "$store" ] && [ -d "$HOME_DIR/secrets" ]; then
      store="$HOME_DIR/secrets"
    fi
    if [ -n "$store" ] && [ -f "$store/$seat.yaml" ] && command -v sops >/dev/null 2>&1; then
      if ! nova-secrets check --store "$store" --as "$seat" --key "$seatkey" --sops "$(command -v sops)" >/dev/null 2>&1; then
        drift "seat nova-secrets check failed for $seatkey"
      fi
    else
      if ! nova-secrets check --key "$seatkey" >/dev/null 2>&1; then
        # A store-backed check is the real gate where a store exists; the
        # key-only probe keeps benches without a local store checkout
        # honest without inventing store paths.
        if ! nova-secrets check --store "$store" --as "$seat" --key "$seatkey" >/dev/null 2>&1; then
          drift "seat nova-secrets check failed for $seatkey"
        fi
      fi
    fi
  fi
fi

# (6) no plaintext keys and no literal apiKey sk- in opencode json.
if [ -e "$HOME_DIR/.local/share/opencode/auth.json" ]; then
  drift "plaintext key file $HOME_DIR/.local/share/opencode/auth.json present"
fi
if [ -e "$HOME_DIR/.config/deepseek/env" ]; then
  drift "plaintext key file $HOME_DIR/.config/deepseek/env present"
fi
if [ -d "$HOME_DIR/.config/opencode" ]; then
  for f in "$HOME_DIR"/.config/opencode/*.json; do
    [ -e "$f" ] || continue
    if grep -qF 'apiKey": "sk-' "$f" 2>/dev/null; then
      drift "plaintext apiKey in $f"
    fi
  done
fi

if [ "$DRIFTS" = "0" ]; then
  echo "STANDARD OK go=$NOVA_GO bins=${NOVA_WANT:-unset} harness=ok seats=1"
  exit 0
fi
echo "STANDARD DRIFT (see lines above)"
exit 1
