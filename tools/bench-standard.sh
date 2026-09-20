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
# ~/go/pkg/mod is read WITHOUT execute, /tmp/.dotnet is the one root granted --write. A bench
# only has to HAVE them -- and, for the writable one, have it under the right mode and owner.
# A name beginning with "/" is an ABSOLUTE root and is that path; any other name is joined to
# the bench home, exactly as the wall joins it.
# NOVA_TOOLCHAIN_ROOTS BEGIN
NOVA_TOOLCHAIN_ROOTS="sdk go/pkg/mod /tmp/.dotnet"
# NOVA_TOOLCHAIN_ROOTS END
for tcroot in $NOVA_TOOLCHAIN_ROOTS; do
  case "$tcroot" in
    /*) tcpath="$tcroot" ;;
    *)  tcpath="$HOME_DIR/$tcroot" ;;
  esac
  if [ ! -d "$tcpath" ]; then
    case "$tcroot" in
      /tmp/.dotnet)
        # PROVISIONING, and it is not once: /tmp is cleared on a reboot, so a bench that has
        # rebooted has lost this directory and every cs card on it dies until it is back.
        # Nothing in the runner creates it -- a runner racing for a name in /tmp would grant
        # --write on whatever that name resolved to -- so it is provisioned, and on a linux
        # bench that means a systemd-tmpfiles line so the reboot puts it back by itself:
        #   printf 'd /tmp/.dotnet 1777 %s %s -\nd /tmp/.dotnet/shm 1777 %s %s -\n' ... \
        #     | sudo tee /etc/tmpfiles.d/nova-dotnet.conf && sudo systemd-tmpfiles --create
        drift "toolchain root $tcpath missing; the .NET named-mutex root is hard-coded to /tmp (TMPDIR is ignored) and the wall grants it --write, so without it every dotnet build and dotnet test on this bench dies in restore on 'NuGet-Migrations'. /tmp is cleared on a reboot: provision it, do not create it by hand once -- mkdir -p /tmp/.dotnet/shm && chmod 1777 /tmp/.dotnet /tmp/.dotnet/shm, and an /etc/tmpfiles.d entry (linux) or a boot script (macOS) so a reboot puts it back"
        ;;
      *)
        drift "toolchain root $tcpath missing; the sandbox wall grants this path and a card's toolchain lives under it"
        ;;
    esac
    continue
  fi
done
# /tmp/.dotnet IS THE ONE WRITABLE ROOT, so it is the one whose MODE AND OWNER are part of
# the standard and not just its existence. The .NET runtime's named-mutex root is hard-coded
# to /tmp (TMPDIR is ignored -- measured by strace on hulk, 2026-09-20), so every card on the
# bench shares this one directory and a cs card cannot restore without --write on it:
#   error MSB4018: ... : 'NuGet-Migrations'. One or more system calls failed:
#   open("/tmp/.dotnet/shm", 0x80000, 0x0) == -1; errno == EACCES
# 1777 is PREFERRED over 0777 and both pass: the sticky bit stops any other UNIX user on the
# machine removing another's entries, and .NET 10 tolerates it and does not rewrite it
# (measured on hulk and batman). It does NOT separate one card from another -- every card
# runs as the same bench user -- which is written out at the row in internal/swarm/toolchain.go.
# /tmp is cleared on a reboot, so this is a PROVISIONED directory: see fleet provisioning.
if [ -d /tmp/.dotnet ]; then
  dmode="$(ls -ld /tmp/.dotnet | cut -c1-10)"
  downer="$(ls -ld /tmp/.dotnet | awk '{print $3}')"
  if [ "$downer" != "$(id -un)" ]; then
    drift "toolchain root /tmp/.dotnet is owned by $downer, not $(id -un); the wall grants it --write to every card on this bench, so it belongs to the bench user"
  fi
  case "$dmode" in
    drwxrwxrwt) ;;
    drwxrwxrwx) echo "NOTE /tmp/.dotnet is 0777; 1777 (sticky) is the provisioned mode and .NET tolerates it: chmod 1777 /tmp/.dotnet /tmp/.dotnet/shm" ;;
    *) drift "toolchain root /tmp/.dotnet is [$dmode] want drwxrwxrwt (1777, preferred) or drwxrwxrwx (0777); the .NET named-mutex root is hard-coded to /tmp, TMPDIR is ignored, and .NET rebuilds any level whose mode it dislikes with mkdtemp(\"/tmp/.dotnet.XXXXXX\") in a /tmp a card cannot write" ;;
  esac
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
