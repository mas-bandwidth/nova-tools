#!/usr/bin/env bash
# tools/bench-standard.sh — the acceptance WITNESS for a Linux bench
# (SPEC-FLEET-KUBE.md, "What bench-standard.sh stops doing"): it checks a
# bench against the standard and prints one DRIFT line per finding.
#
# THIS SCRIPT IS A WITNESS, NOT A PROVISIONER. It does not install packages,
# does not create users, does not write unit files, does not arm timers, does
# not install k3s, does not mount volumes, and does not authorize seat keys --
# all of that is Terraform's job (SPEC-FLEET-KUBE.md "A new bench is one apply")
# and a `DRIFT` line this script prints is the witness that the declaration and
# the host disagreed, not a ticket for this script to repair.
#
# Usage:
#
#   tools/bench-standard.sh
#   NOVA_GO=go1.26.5 NOVA_WANT=v1.2.3 tools/bench-standard.sh  # NOVA_GO overrides go.mod
#   tools/bench-standard.sh --apply   # kills stray runner listeners, nothing else
#
# The ONE AND ONLY mutation this script performs is `--apply` killing stray
# runner listeners: a process holding the runner's listener port that is not
# under the runner's systemd unit. No other action is taken on any path, with
# or without `--apply`. A script that wants to repair or provision is a
# different script.
#
# Exit 0 prints "STANDARD OK ..."; exit 1 prints "STANDARD DRIFT (see lines
# above)" after the DRIFT lines. Runner checks (1)-(2) run only on Linux.
set -uo pipefail

APPLY=0
for arg in "$@"; do
  case "$arg" in
    --apply) APPLY=1 ;;
    -h|--help) echo "usage: bench-standard.sh [--apply]"
    echo "  --apply   kills stray runner listeners (the script's only mutation); nothing more"
    exit 0 ;;
    *) echo "DRIFT unknown argument $arg" ; echo "STANDARD DRIFT (see lines above)"; exit 1 ;;
  esac
done

# The wanted Go is the tree's go.mod `go` line, not a patch copied here.
# $NOVA_GO stays an explicit override (a person gating an older tree on purpose).
if [ -z "${NOVA_GO:-}" ]; then
  _dir=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd) || _dir=""
  _mod=""
  if [ -n "$_dir" ] && [ -f "$_dir/../go.mod" ]; then
    _mod="$_dir/../go.mod"
  fi
  if [ -n "$_mod" ]; then
    _ver=$(awk '/^go / { print $2; exit }' "$_mod")
    if [ -n "$_ver" ]; then
      NOVA_GO="go${_ver}"
    fi
  fi
fi
NOVA_GO="${NOVA_GO:-}"
NOVA_WANT="${NOVA_WANT:-}"
NOVA_HARNESS="${NOVA_HARNESS:-}"
HOME_DIR="${HOME:-}"
DRIFTS=0
STRAY_PIDS=""

# Build the card environment before any tool resolution so the verdict
# does not depend on the caller's PATH (nova-tools#2052).
if [ -f "$HOME_DIR/sdk/env.sh" ]; then
  set +u
  . "$HOME_DIR/sdk/env.sh"
  set -u
fi

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
      # ps -eo pid=,args= lists "pid cmd...". Match the LISTENER binary under the
      # runner dir, not the dir string: run.sh and run-helper.sh also carry the dir,
      # and the awk of this very pipeline carries it in its own argv, so a bare dir
      # match counted 4 where the answer is 1 on every bench (antman, 2026-09-18).
      # ps is snapshotted before awk exists for the same reason.
      pstable="$(ps -eo pid=,args= 2>/dev/null)"
      pids="$(printf '%s\n' "$pstable" | awk -v bin="${d}bin/Runner.Listener" 'index($0, bin) {print $1}')"
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

# (3a) THE TOOLCHAIN ROOTS the sandbox wall grants a card. This list and
# internal/swarm/toolchain.go are ONE list: internal/ci's class test fails when they drift
# apart, because a literal in two places is exactly how the standard and the wall came to
# contradict each other -- the standard put Go under ~/sdk, the wall named no toolchain root,
# and every Go card on hulk died on `go.mod requires go >= 1.26 (running go 1.22.2)`.
# The KIND of each grant lives in internal/swarm/toolchain.go and not here, because it is the
# wall's decision and not the bench's: ~/sdk is read AND execute (the card runs that go),
# ~/go/pkg/mod is read WITHOUT execute. A bench only has to HAVE them.
# NOVA_TOOLCHAIN_ROOTS BEGIN
NOVA_TOOLCHAIN_ROOTS="sdk go/pkg/mod"
# NOVA_TOOLCHAIN_ROOTS END
for tcroot in $NOVA_TOOLCHAIN_ROOTS; do
  if [ ! -d "$HOME_DIR/$tcroot" ]; then
    drift "toolchain root $HOME_DIR/$tcroot missing; the sandbox wall grants this path and a card's go lives under it"
  fi
done

# (3) go version, sbcl, harness.
if [ -z "$NOVA_GO" ]; then
  drift "go.mod go directive unread; set NOVA_GO or run from a nova-tools checkout"
elif command -v go >/dev/null 2>&1; then
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

# (3c) THE TOOLCHAIN MUST BE RUNNABLE INSIDE THE WALL, not merely on PATH.
# `command -v sbcl` answers about the bench user's own shell. A card runs behind the
# sandbox wall, whose linux read roots are the system table of
# internal/sandbox/wrap_linux.go plus the toolchain roots of internal/swarm/toolchain.go.
# Of the toolchain roots only `sdk` carries EXECUTE (`go/pkg/mod` is read WITHOUT execute),
# so the roots that can run a tool are the system table plus `$HOME/sdk`. An sbcl at
# $HOME/.local/bin/sbcl is on PATH and is `Permission denied` inside the wall, which is why
# every lisp card was forced onto the one bench whose sbcl is /usr/bin/sbcl -- measured
# 2026-09-19: E09-G1 on vision 1036 s against 248-393 s for the same class on space, and the
# r1785 worker on mini fetched an SBCL 2.4.0 of its own into $TMPDIR before it could run a
# test.
for tool in go sbcl; do
  p="$(command -v "$tool" 2>/dev/null || true)"
  [ -n "$p" ] || continue          # absent is the check above's DRIFT, not this one's
  rp="$(readlink -f "$p" 2>/dev/null || echo "$p")"
  granted=0
  for root in /usr /bin /sbin /lib /lib64 /opt "$HOME_DIR/sdk"; do
    rroot="$(readlink -f "$root" 2>/dev/null || echo "$root")"
    [ -n "$rroot" ] || continue
    case "$rp" in "$rroot"/*) granted=1 ;; esac
  done
  if [ "$granted" != "1" ]; then
    drift "$tool on PATH is $p -> $rp, under NO read root the sandbox wall grants (the system roots, and \$HOME/sdk from internal/swarm/toolchain.go): a card cannot EXECUTE it inside the wall. Install it under $HOME_DIR/sdk/$tool-<ver>/ and point the PATH entry there"
  fi
done
# (3d) THE SBCL PIN, not only its presence (nova-tools#2053): space ran SBCL 2.6.0.debian
# from /usr/bin while the fleet pins $NOVA_SBCL under ~/sdk, and a presence check printed
# PINNED for it. The version is `sbcl --version`'s second word, compared whole (2.5.80 is
# not 2.5.8); the path is the resolved one, compared against the resolved ~/sdk (macOS
# temp and home dirs sit behind /var -> /private/var). An absent sbcl is (3)'s DRIFT.
NOVA_SBCL="${NOVA_SBCL:-2.5.8}"
sdk_real="$(readlink -f "$HOME_DIR/sdk" 2>/dev/null || echo "$HOME_DIR/sdk")"
if command -v sbcl >/dev/null 2>&1; then
  sbcl_out="$(sbcl --version 2>&1 | head -1 || true)"
  sbcl_ver="$(printf '%s\n' "$sbcl_out" | awk '{print $2}')"
  if [ "$sbcl_ver" != "$NOVA_SBCL" ]; then
    drift "sbcl version [$sbcl_out] want $NOVA_SBCL (NOVA_SBCL)"
  fi
  sbcl_p="$(command -v sbcl)"
  sbcl_rp="$(readlink -f "$sbcl_p" 2>/dev/null || echo "$sbcl_p")"
  case "$sbcl_rp" in
    "$sdk_real"/*) ;;
    *) drift "sbcl at $sbcl_p -> $sbcl_rp not under $HOME_DIR/sdk (want $HOME_DIR/sdk/sbcl-$NOVA_SBCL/)" ;;
  esac
fi

# (3e) THE PRO RUNG (nova-tools#2053): pro loops existed on five linux benches only; the
# Macs were flash-only and 528 of 998 slots sat idle in a pro wave. A bench has the rung
# when NOVA_PRO_RUNG names an executable or ~/nova-bench/rungs/pro or ~/nova-bench/pro exists.
NOVA_PRO_RUNG="${NOVA_PRO_RUNG:-}"
pro_rung=0
if [ -n "$NOVA_PRO_RUNG" ] && [ -x "$NOVA_PRO_RUNG" ]; then
  pro_rung=1
else
  for rp in "$HOME_DIR/nova-bench/rungs/pro" "$HOME_DIR/nova-bench/pro"; do
    if [ -d "$rp" ]; then pro_rung=1; break; fi
  done
fi
if [ "$pro_rung" = "0" ]; then
  drift "pro rung missing (no executable NOVA_PRO_RUNG, no $HOME_DIR/nova-bench/rungs/pro, no $HOME_DIR/nova-bench/pro)"
fi

# (3f) SQLITE3 UNDER ~/sdk (nova-tools#2053): space resolved /usr/bin/sqlite3 while the
# other benches carry ~/sdk/sqlite3-<ver>, so one card saw two sqlite3s by bench.
if ! command -v sqlite3 >/dev/null 2>&1; then
  drift "sqlite3 not on PATH (want $HOME_DIR/sdk/sqlite3-<ver>/bin/sqlite3)"
else
  sq_p="$(command -v sqlite3)"
  sq_rp="$(readlink -f "$sq_p" 2>/dev/null || echo "$sq_p")"
  case "$sq_rp" in
    "$sdk_real"/*) ;;
    *) drift "sqlite3 at $sq_p -> $sq_rp not under $HOME_DIR/sdk (want $HOME_DIR/sdk/sqlite3-<ver>/bin/sqlite3)" ;;
  esac
fi

# (3g) THE SLOT SHARE IS DECLARED (nova-tools#2053): hulk 110/125, vision 104/121, space
# 192/125, hetzner 64/61, superman 54/64, batman 24/32, the Studio 450/512, and no formula
# recorded anywhere. A bench declares its share as a positive whole number of slots.
NOVA_SLOT_SHARE="${NOVA_SLOT_SHARE:-}"
case "$NOVA_SLOT_SHARE" in
  "") drift "NOVA_SLOT_SHARE unset (declare the bench's slot share, a positive whole number of slots)" ;;
  *[!0-9]*|0|0*) drift "NOVA_SLOT_SHARE=$NOVA_SLOT_SHARE is not a positive whole number of slots" ;;
esac

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

# (3c) harness canary: try to start the harness inside the sandbox wall.
# A harness that cannot start inside the wall means the bench is unfit for
# cards -- every card would fail at startup (#2388).
if [ "$harness_ok" = "1" ] && [ "$OS" = "Linux" ]; then
  _hbin=""
  if [ -n "$NOVA_HARNESS" ]; then
    _hbin="$NOVA_HARNESS"
  else
    for _h in "$HOME_DIR"/nova-bench/harness-*/opencode; do
      [ -x "$_h" ] || continue
      _hbin="$_h"
      break
    done
  fi
  if [ -n "$_hbin" ]; then
    _sbin="$HOME_DIR/.local/bin/nova-sandbox"
    if [ -x "$_sbin" ]; then
      _cdir="$(mktemp -d "$HOME_DIR/nova-bench/nova-canary.XXXXXX" 2>/dev/null || true)"
      if [ -n "$_cdir" ]; then
        mkdir -p "$_cdir/home"
        if ! HOME="$_cdir/home" "$_sbin" --read "$HOME_DIR/nova-bench" --write "$_cdir" --cwd "$_cdir" -- "$_hbin" --help >/dev/null 2>&1; then
          drift "harness cannot start inside the sandbox wall; $_hbin --help failed under nova-sandbox"
        fi
        rm -rf "$_cdir"
      fi
    fi
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
# Exactly one seat key per owner prefix (SPEC-SECRETS.md dogfooding item 6):
# the owner is the key name before its first "-" (rowan-claude and
# rowan-codex are both owner rowan). Two keys for one owner is a lost key
# still trusted or an undeclared grant, named by owner. Portable to bash 3.2
# (no associative arrays).
if [ "$nkeys" -gt 1 ]; then
  owners=""
  for k in "$seatdir"/*.key; do
    [ -e "$k" ] || continue
    kn="$(basename "$k" .key)"
    owners="$owners${kn%%-*}
"
  done
  # Heredoc, not a pipe, so drift() counts in this shell.
  while read -r n owner; do
    [ -n "$owner" ] || continue
    if [ "$n" -gt 1 ]; then
      drift "seat owner=$owner keys=$n want=1 in $seatdir"
    fi
  done <<OWNERS
$(printf '%s' "$owners" | sort | uniq -c)
OWNERS
fi
if [ "$nkeys" != "1" ]; then
  drift "seat keys=$nkeys want=1 in $seatdir"
else
  if ! command -v nova-secrets >/dev/null 2>&1; then
    drift "nova-secrets not on PATH for seat check of $seatkey"
  else
    seat="$(basename "$seatkey" .key)"
    store="${NOVA_SECRETS_STORE:-}"
    if [ -z "$store" ] && [ -d "$HOME_DIR/nova-bench/secrets" ]; then
      store="$HOME_DIR/nova-bench/secrets"
    fi
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

# (7) DISK HEADROOM, and where the space went (issue #1048, item 3).
#
# A Go card costs 5-7 GB of module cache, build cache and scratch in its own slot, against
# 330 MB for a Lisp card. hulk and vision filled to 100% under 120 cards and their runners
# died; one bench reached 0 free with 53 GB of slot data homes and had to be reaped by hand.
# BOTH LAUNCHERS REFUSE A BENCH UNDER 25 GB FREE -- it is a term of the capacity formula,
# `allowed = min(cores*1.5 - load - 8, (free_gb - 25)/2, memfree_gb/2)` at
# cmd/nova-pulse/fill.go -- so a bench below the floor is a bench nothing will be launched
# onto, which is not a conforming bench however clean the rest of this script finds it.
#
# AND IT NAMES THE THREE LARGEST DIRECTORIES, because "out of disk" is otherwise a sentence
# somebody has to go and investigate by hand at whatever hour it is. The `du` runs only on
# the failing path: a walk of a whole bench home is not something a green run should pay.
# The floor and the probe are `nova-pulse fleet standard`'s, not a second spelling of them:
# --min-free defaults to 25 there (cmd/nova-pulse/fleet_verbs.go) and its `disk-free` check
# reads exactly this line (internal/pulse/fleetstandard.go). `df -Pk` is POSIX and answers
# the same on a GNU and a BSD userland, where `df -BG` is GNU-only and a BSD refuses it
# outright -- which matters because the bench is Linux and this check's test runs wherever
# the developer is. The remote witness and the on-host one must agree, and the only way to
# be sure of that is for them to be one line.
NOVA_MIN_FREE_G="${NOVA_MIN_FREE_G:-25}"
free_g="$(df -Pk "$HOME_DIR" 2>/dev/null | awk 'NR==2{printf "%d", $4/1048576}')"
case "${free_g:-}" in
  ''|*[!0-9]*) free_g="" ;;
esac
if [ -z "$free_g" ]; then
  drift "disk free unknown: df answered nothing readable for $HOME_DIR; a bench whose space cannot be read is not known to be conforming"
elif [ "$free_g" -lt "$NOVA_MIN_FREE_G" ]; then
  largest="$(du -sk "$HOME_DIR"/* 2>/dev/null | sort -rn | head -3 | awk '{
      k = $1; $1 = ""; sub(/^[ \t]+/, "", $0)
      v = k; unit = "K"
      if (k >= 1048576) { v = int(k / 1048576); unit = "G" }
      else if (k >= 1024) { v = int(k / 1024); unit = "M" }
      printf "%s%s %d%s", sep, $0, v, unit; sep = "; "
    }')"
  [ -n "$largest" ] || largest="nothing readable under $HOME_DIR"
  drift "disk free=${free_g}G want>=${NOVA_MIN_FREE_G}G (both launchers refuse below it); largest under $HOME_DIR: $largest"
fi

if [ "$DRIFTS" = "0" ]; then
  echo "STANDARD OK go=$NOVA_GO bins=${NOVA_WANT:-unset} harness=ok seats=1 free=${free_g}G"
  exit 0
fi
echo "STANDARD DRIFT (see lines above)"
exit 1
