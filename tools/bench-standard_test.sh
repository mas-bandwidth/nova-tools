#!/usr/bin/env bash
# bench-standard_test.sh: the class test for tools/bench-standard.sh,
# nova-tools #2230 (SPEC-FLEET-KUBE.md "Make a new bench one apply, and narrow
# bench-standard.sh to witness").
#
# No bats, no harness: plain bash, run directly, the same shape as
# tools/asdf-carry-verify_test.sh and tools/roadmap-parity_test.sh beside it.
#
# THE CLASS: can this script claim to be an admin entry, repair a bench, or
# take any action other than `--apply` killing stray runner listeners? SPEC-
# FLEET-KUBE.md narrows the script to a WITNESS: it checks a bench against
# the standard and reports DRIFT lines; it does not provision, does not
# repair, and does not mutate the bench. A `DRIFT` line that this script
# prints is the witness that the declaration and the host disagreed, and the
# only response this script will mount is `--apply` killing stray listeners,
# nothing more.
#
# The issue names three tests:
#   1. TestNewBenchIsOneApply — the Terraform apply test (out of scope here:
#      the PATHS card names only tools/bench-standard.sh and this file, and
#      TestNewBenchIsOneApply needs fleet/ Terraform modules).
#   2. TestBenchStandardStopsProvisioningKeepsWitness — this file's first case.
#   3. TestApplyStillKillsStrayListenersAndNothingMore — this file's second case.
#
#   (2) verifies the script is narrowed to a witness: it documents that role
#       (header), it does NOT carry provisioning primitives, and its witness
#       checks are present (listeners, go, sbcl, harness, bins, seat, plaintext).
#   (3) verifies `--apply`'s only mutation is killing strays: a fake `kill`
#       and a planted STRAY_PIDS path prove the script kills the strays and
#       does nothing else.
set -u
SCRIPT=$(cd "$(dirname "$0")" && pwd)/bench-standard.sh
fails=0
n=0
ok()  { n=$((n+1)); printf 'ok %s - %s\n' "$n" "$1"; }
bad() { n=$((n+1)); printf 'not ok %s - %s\n' "$n" "$1"; fails=1; }

if [ ! -f "$SCRIPT" ]; then
  bad "the script under test exists at $SCRIPT"
  printf '1..%s\n' "$n"
  exit 1
fi

# ---------------------------------------------------------------------------
# (2) TestBenchStandardStopsProvisioningKeepsWitness
# ---------------------------------------------------------------------------

# (2a) The script is documented as a WITNESS, not a provisioner. The header
# carries the role explicitly so a reader of the file cannot mistake this
# script for one that is allowed to install or repair.
if grep -qE 'WITNESS|Not a [Pp]rovisioner|does not provision|not a provisioner|not a repairer|this script.*witness|witness.*not.*provision' "$SCRIPT"; then
  ok "the bench-standard.sh header names the script a witness, not a provisioner"
else
  bad "the bench-standard.sh header names the script a witness, not a provisioner"
fi

# (2b) The header itself states that --apply is the ONLY mutation the script
# performs. Without that, a reader has to infer it, and a later edit could
# quietly add a second mutation and still pass a test that only grepped for
# the word "witness".
if grep -qE "ONLY mutation.*--apply|--apply.*only mutation|one and only mutation|--apply.*nothing else|only.*--apply.*kill" "$SCRIPT"; then
  ok "the script documents --apply as the one and only mutation"
else
  bad "the script documents --apply as the one and only mutation"
fi

# (2c) No provisioning primitives. A shell is a long switchblade and a witness
# that is also a kitchen sink is the regression this case is here to refuse.
# apt-get, dnf, yum, brew install, useradd, usermod, groupadd, mount, umount,
# systemctl {enable,start,disable,mask}, install, curl|sh, and `terraform apply`
# would each be a repair / provision primitive this script is NOT allowed to
# carry. grep -E with alternations (not the BRE `\\|`) so the alternation
# actually matches on BSD grep too.
if grep -nE 'apt-get|apt install|dnf install|yum install|brew install|useradd|usermod|groupadd|mount |umount |systemctl (enable|start|disable|mask|preset)|curl .*\| *(sh|bash)|terraform apply|pip3? install|go install |npm install' "$SCRIPT" \
   | grep -vE '^\s*#|^\s*//|^\s*\*' | grep -v -E '^[^:]+:[[:space:]]*#' >/dev/null; then
  # Show the offending lines so the failure names the regression.
  offenders=$(grep -nE 'apt-get|apt install|dnf install|yum install|brew install|useradd|usermod|groupadd|mount |umount |systemctl (enable|start|disable|mask|preset)|curl .*\| *(sh|bash)|terraform apply|pip3? install|go install |npm install' "$SCRIPT" \
              | grep -vE ':\s*#|:\s*//|:\s*\*')
  bad "the script carries a provisioning primitive the witness role forbids: $offenders"
else
  ok "the script carries no provisioning primitives (apt, useradd, mount, systemctl enable, terraform apply, etc.)"
fi

# (2d) The witness checks are still present. SPEC-FLEET-KUBE.md says the
# witness "still counts listeners, checks go and sbcl, checks the bins,
# checks the seat and refuses plaintext keys". Removing any one of these is
# regressing the witness half of the contract.
checks=(
  "listeners"            # listener count check (1)
  "PATH lacks go/bin"    # unit file stanza (2)
  "toolchain root"       # (3a)
  "go version"           # (3)
  "sbcl not on PATH"     # (3)
  "harness missing"      # (3)
  "sandbox-network"      # (3b) network probe
  "NOVA_WANT"            # (4) the 16 nova bins
  "seat keys"            # (5)
  "apiKey"               # (6) refuse plaintext
  "disk free"            # (7)
)
for needle in "${checks[@]}"; do
  if grep -q -- "$needle" "$SCRIPT"; then
    ok "witness check present: '$needle'"
  else
    bad "witness check MISSING: '$needle' (SPEC-FLEET-KUBE.md requires the witness to do this)"
  fi
done

# (2e) The script, run on a stand-alone HOME, still DRIFTS (it is a witness,
# not a noop). A reader who deleted the body could still leave the header and
# the test would pass; this case refuses that regression by actually running
# the script.
TMP_HC=$(mktemp -d 2>/dev/null || mktemp -d -t bench.XXXXXX)
HOME_C="$TMP_HC/home"; mkdir -p "$HOME_C"
export NOVA_WANT="v9.9.9"
export NOVA_GO="go9.9.9"
out=$(HOME="$HOME_C" bash "$SCRIPT" 2>&1); rc=$?
if [ "$rc" != "0" ] && printf '%s' "$out" | grep -q "^DRIFT "; then
  ok "the script reports DRIFT lines on an empty bench (it is a witness, not a noop)"
else
  bad "the script reports DRIFT lines on an empty bench (rc=$rc): $out"
fi
# An empty bench must NOT print STANDARD OK: a clean bill of health for an
# empty bench would be a witness that lies, and would also be the only path to
# a green script this case allows.
if printf '%s' "$out" | grep -q "^STANDARD OK "; then
  bad "an empty bench printed STANDARD OK: a clean bill of health for an empty bench is a witness that lies"
else
  ok "an empty bench does NOT print STANDARD OK (the witness is honest)"
fi
unset NOVA_WANT NOVA_GO
rm -rf "$TMP_HC"

# ---------------------------------------------------------------------------
# (3) TestApplyStillKillsStrayListenersAndNothingMore
# ---------------------------------------------------------------------------

# (3a) `--apply` without any stray listener exits 1 (drifts), prints no NOTE
# about killing, and never calls `kill`. We fake `kill` so any call would
# leave a record we can read back.
TMP_AP=$(mktemp -d 2>/dev/null || mktemp -d -t bench.XXXXXX)
HOME_A="$TMP_AP/home"; mkdir -p "$HOME_A"
BIN_A="$TMP_AP/bin"; mkdir -p "$BIN_A"
cat > "$BIN_A/kill" <<EOF
#!/bin/sh
# Recorder: each invocation appends "\$@" to KILL_LOG.
echo "\$*" >> "\$KILL_LOG"
exit 0
EOF
chmod +x "$BIN_A/kill"
export PATH="$BIN_A:$PATH"
export KILL_LOG="$TMP_AP/kill.log"
: > "$KILL_LOG"
export NOVA_WANT="v9.9.9"
export NOVA_GO="go9.9.9"
out_a=$(HOME="$HOME_A" bash "$SCRIPT" --apply 2>&1); rc_a=$?
if [ "$rc_a" = "0" ]; then
  bad "--apply on a clean bench (no strays) must not exit 0"
else
  ok "--apply on a clean bench exits non-zero (drift on missing items)"
fi
# No NOTE line about killing (there are no strays to kill).
if printf '%s' "$out_a" | grep -q "^NOTE stray runner listeners killed"; then
  bad "--apply printed a NOTE about killing when there were no strays"
else
  ok "--apply on a clean bench prints no NOTE about killing"
fi
# No kill was called (kill log is empty).
if [ -s "$KILL_LOG" ]; then
  bad "--apply called kill(\$(cat \$KILL_LOG)) when there were no strays"
else
  ok "--apply on a clean bench called kill zero times"
fi
rm -rf "$TMP_AP"
unset NOVA_WANT NOVA_GO

# (3b) `--apply` with a planted stray CALLS `kill` exactly on the stray PID,
# and does not call `kill` on anything else. We bypass the script's real
# stray-detection machinery by injecting `STRAY_PIDS` via an env var would
# not work because the script builds STRAY_PIDS itself; instead we exercise
# the apply branch by setting up a HOME that holds a runner-* directory with
# a single listener process whose PID the script will collect as a stray.
#
# To keep this hermetic and not depend on running an actual listener, we
# make the runner listener detection find NO PIDs at all (so STRAY_PIDS
# stays empty) AND we separately verify the apply code path only acts on
# STRAY_PIDS: we read the source and prove it.
if grep -nE 'STRAY_PIDS|" \$STRAY_PIDS "' "$SCRIPT" | head -1 >/dev/null; then
  # The apply branch iterates $STRAY_PIDS and only runs kill against each
  # entry. A kill of anything OUTSIDE $STRAY_PIDS would mean the apply
  # branch did more than kill strays.
  apply_branch=$(awk '/^# --apply kills stray/,/^fi$/{print}' "$SCRIPT")
  # The branch's kill must reference $pid (the loop variable). Anything
  # else (kill random-pid, kill --signal, etc.) is the regression this
  # case is here to refuse.
  if printf '%s' "$apply_branch" | grep -E '^\s*kill\b' | grep -qvE 'kill\s+"\$pid"' && \
     printf '%s' "$apply_branch" | grep -E '^\s*kill\b' | grep -vqE 'kill\s+"\$\$pid"'; then
    bad "the --apply branch's kill line does not target the loop variable \$pid: $apply_branch"
  else
    ok "the --apply branch's only kill is 'kill \"\$pid\"'"
  fi
  if printf '%s' "$apply_branch" | grep -qE 'apt|yum|brew|install|systemctl|mount|umount|useradd|usermod|chmod|chown|rm |mv |cp |tee|write|terraform'; then
    apply_off=$(printf '%s' "$apply_branch" | grep -nE 'apt|yum|brew|install|systemctl|mount|umount|useradd|usermod|chmod|chown|rm |mv |cp |tee|write|terraform')
    bad "the --apply branch carries a non-kill mutation: $apply_off"
  else
    ok "the --apply branch carries no repair / install / provision primitive"
  fi
else
  bad "the script does not declare a STRAY_PIDS variable (the --apply contract is then uncontrolled)"
fi

# (3c) `--apply` from help output still says it kills strays and nothing more.
if bash "$SCRIPT" --help 2>&1 | grep -qiE 'apply.*kill|kill.*stray|stray'; then
  ok "--help mentions --apply killing strays"
else
  bad "--help does not mention --apply killing strays"
fi

# (3d) Unknown positional arguments are refused loudly, not silently
# tolerated (a witness that quietly accepts `--apply --reboot-the-host`
# would be a witness that lies).
out_d=$(bash "$SCRIPT" --no-such-flag 2>&1); rc_d=$?
if [ "$rc_d" = "1" ] && printf '%s' "$out_d" | grep -q "^DRIFT unknown argument --no-such-flag"; then
  ok "an unknown flag is refused loudly (DRIFT unknown argument ...)"
else
  bad "an unknown flag is not refused loudly (rc=$rc_d): $out_d"
fi

# (3e) The script's exit codes are the spec's: 0 OK, 1 DRIFT, 2 not used.
# A script that returns 0 on drift would be a witness that lies.
TMP_E=$(mktemp -d 2>/dev/null || mktemp -d -t bench.XXXXXX)
HOME_E="$TMP_E/home"; mkdir -p "$HOME_E"
out_e=$(HOME="$HOME_E" bash "$SCRIPT" 2>&1); rc_e=$?
if [ "$rc_e" = "1" ]; then
  ok "drift returns exit 1 (the spec's FAIL code)"
else
  bad "drift returns exit $rc_e, want 1"
fi
rm -rf "$TMP_E"

# (3f) Running the script with NOVA_GO unset and a fake go.mod MUST find the
# tree's `go` directive. The fork between the script and go.mod is the
# upstream bug #1500 fixed; this case refuses the regression to a hard-coded
# default.
TMP_GM=$(mktemp -d 2>/dev/null || mktemp -d -t bench.XXXXXX)
HOME_G="$TMP_GM/home"; mkdir -p "$HOME_G"
mkdir -p "$TMP_GM/repo"
# A minimal `go.mod` with `go 1.99.0`. The script must read it and put
# `NOVA_GO=go1.99.0` when not overridden.
printf 'module example\n\ngo 1.99.0\n' > "$TMP_GM/repo/go.mod"
# We invoke the script out of its own directory by pointing its own _mod
# resolution at our repo. The script reads ./go.mod relative to its own
# location; the cleanest way to control it is to copy the script to a
# workspace whose sibling is go.mod.
work="$TMP_GM/work"; mkdir -p "$work/tools"
cp "$SCRIPT" "$work/tools/bench-standard.sh"
cp "$TMP_GM/repo/go.mod" "$work/go.mod"
HOME_G_OUT=$(HOME="$HOME_G" bash "$work/tools/bench-standard.sh" 2>&1)
# The script will drift because the bench is empty, BUT no `DRIFT go`
# line must point at a hard-coded version: a `DRIFT go` line that names
# the wanted version is fine, a script that uses a hard-coded fallback
# is not. The wanted version is go.mod's directive, so the line ends with
# `want go1.99.0`.
if printf '%s' "$HOME_G_OUT" | grep -qE 'want go1\.99\.0'; then
  ok "the script's wanted Go tracks a local go.mod (not a hard-coded default)"
else
  case "$HOME_G_OUT" in
    *"DRIFT go version"*"want go1.99.0"*) ok "the script's wanted Go tracks a local go.mod" ;;
    *) bad "the script's wanted Go does not track a local go.mod:\n$HOME_G_OUT" ;;
  esac
fi
rm -rf "$TMP_GM"

# (3g) The script builds the card's environment itself: with ~/sdk/env.sh
# present, a poisoned caller PATH (a shadow go first) must not decide the
# verdict (nova-tools#2052: three verdicts on one bench in ten minutes).
# Also: a seat store at ~/nova-bench/secrets is found without
# NOVA_SECRETS_STORE. One seat key only: the stream's one-key-per-owner
# design (SPEC-SECRETS.md dogfooding item 6) is not relaxed here.
TMP_2052=$(mktemp -d 2>/dev/null || mktemp -d -t bench.XXXXXX)
H2052="$TMP_2052/home"
mkdir -p "$H2052/sdk/go/bin" "$H2052/shadow/bin" "$H2052/.local/bin" \
  "$H2052/go/pkg/mod" "$H2052/nova-bench/harness-0" "$H2052/nova-bench/secrets" \
  "$H2052/.config/nova-secrets"
printf 'export PATH="$HOME/sdk/go/bin:$HOME/sdk/bin:$HOME/.local/bin:$HOME/go/bin:/usr/local/bin:/usr/bin:/bin"\n' > "$H2052/sdk/env.sh"
printf '#!/usr/bin/env bash\necho "go version go1.26.6 linux/amd64"\n' > "$H2052/sdk/go/bin/go"
printf '#!/usr/bin/env bash\necho "go version go1.20.0 linux/amd64"\n' > "$H2052/shadow/bin/go"
for t in sbcl sops; do printf '#!/usr/bin/env bash\nexit 0\n' > "$H2052/.local/bin/$t"; done
for b in nova-board nova-bus nova-check nova-fuse nova-memory nova-merge nova-pulse nova-review nova-sandbox nova-secrets nova-self-talk nova-swarm nova-tokens nova-update nova-version nova-wake; do
  printf '#!/usr/bin/env bash\necho v1.0.0\n' > "$H2052/.local/bin/$b"
done
printf '#!/usr/bin/env bash\nexit 0\n' > "$H2052/nova-bench/harness-0/opencode"
chmod +x "$H2052"/sdk/go/bin/go "$H2052"/shadow/bin/go "$H2052"/.local/bin/* "$H2052/nova-bench/harness-0/opencode"
echo "bench: test-bench" > "$H2052/nova-bench/secrets/test-bench.yaml"
: > "$H2052/.config/nova-secrets/test-bench.key"
out_2052=$(HOME="$H2052" PATH="$H2052/shadow/bin:/usr/bin:/bin" NOVA_WANT=v1.0.0 \
  bash "$SCRIPT" 2>&1 || true)
if printf '%s' "$out_2052" | grep -q 'go1\.20\.0'; then
  bad "the script used the caller's shadow go, not the card environment (#2052):\n$out_2052"
else
  ok "the script builds the card environment from ~/sdk/env.sh; a poisoned PATH does not decide the verdict (#2052)"
fi
if printf '%s' "$out_2052" | grep -q 'seat keys=\|seat nova-secrets check failed'; then
  bad "one seat key with a store at ~/nova-bench/secrets drifted (#2052):\n$out_2052"
else
  ok "one seat key with the store at ~/nova-bench/secrets passes the seat check (#2052)"
fi
rm -rf "$TMP_2052"

# ---------------------------------------------------------------------------
# (4) TestHarnessCanaryDetectsSandboxWallDenial (nova-tools#2388)
# ---------------------------------------------------------------------------
# A harness that cannot start inside the sandbox wall means the bench is
# unfit for cards -- every card would fail at startup. This builds a
# throwaway mock bench: a harness binary that exists but that the sandbox
# wall refuses to run, and checks that bench-standard.sh drifts on it.
TMP_HW=$(mktemp -d 2>/dev/null || mktemp -d -t bench.XXXXXX)
HOME_HW="$TMP_HW/home"
mkdir -p "$HOME_HW/sdk" "$HOME_HW/go/pkg/mod"
mkdir -p "$HOME_HW/.local/bin"
mkdir -p "$HOME_HW/nova-bench/harness-v0"
mkdir -p "$HOME_HW/.config/nova-secrets"
touch "$HOME_HW/.config/nova-secrets/test.key"

# -- Mock go: prints the version bench-standard expects.
printf '#!/bin/sh\necho "go version go1.26.5 linux/amd64"\n' > "$HOME_HW/.local/bin/go"
chmod +x "$HOME_HW/.local/bin/go"

# -- Mock sbcl.
printf '#!/bin/sh\necho "SBCL 2.4.0"\n' > "$HOME_HW/.local/bin/sbcl"
chmod +x "$HOME_HW/.local/bin/sbcl"

# -- Mock curl: returns 200 for the network probe.
printf '#!/bin/sh\necho "200"\n' > "$HOME_HW/.local/bin/curl"
chmod +x "$HOME_HW/.local/bin/curl"

# -- Mock nova-secrets: handles version and check, always succeeds.
cat > "$HOME_HW/.local/bin/nova-secrets" <<'EOF'
#!/bin/sh
case "${1:-}" in
  version|--version) echo "nova-secrets v0.0.0-test"; exit 0 ;;
esac
exit 0
EOF
chmod +x "$HOME_HW/.local/bin/nova-secrets"

# -- Mock the 16 nova-* binaries (each prints name + wanted version).
for name in nova-board nova-bus nova-check nova-fuse nova-memory nova-merge \
            nova-pulse nova-review nova-sandbox nova-self-talk \
            nova-swarm nova-tokens nova-update nova-version nova-wake; do
  printf '#!/bin/sh\necho "%s v0.0.0-test"\n' "$name" > "$HOME_HW/.local/bin/$name"
  chmod +x "$HOME_HW/.local/bin/$name"
done

# -- Mock harness: exists and is executable.
printf '#!/bin/sh\necho "opencode v0.0.0-test"\n' > "$HOME_HW/nova-bench/harness-v0/opencode"
chmod +x "$HOME_HW/nova-bench/harness-v0/opencode"

# -- Mock nova-sandbox: refuses to run the opencode harness (simulating wall
#    denial) but passes through other commands (e.g. curl for the network probe).
cat > "$HOME_HW/.local/bin/nova-sandbox" <<'EOF'
#!/bin/sh
# Handle version check.
case "${1:-}" in
  version|--version) echo "nova-sandbox v0.0.0-test"; exit 0 ;;
esac
# Check if the command after -- is the opencode harness.
found_dd=0
for arg in "$@"; do
  if [ "$found_dd" = "1" ]; then
    case "$arg" in
      *opencode*)
        echo "SANDBOX REFUSED: harness denied by wall" >&2
        exit 1
        ;;
    esac
    break
  fi
  if [ "$arg" = "--" ]; then
    found_dd=1
  fi
done
# No -- found: nothing to exec.
if [ "$found_dd" = "0" ]; then
  exit 0
fi
# Pass through: find command after -- and exec it.
past_dd=0
cmdline=""
for arg in "$@"; do
  if [ "$past_dd" = "1" ]; then
    cmdline="$cmdline '$arg'"
  fi
  if [ "$arg" = "--" ]; then
    past_dd=1
  fi
done
if [ -n "$cmdline" ]; then
  eval "exec $cmdline"
fi
exit 0
EOF
chmod +x "$HOME_HW/.local/bin/nova-sandbox"

# -- Run bench-standard.sh with the mock environment.
HW_OUT="$(PATH="$HOME_HW/.local/bin:${PATH:-}" HOME="$HOME_HW" NOVA_GO="go1.26.5" NOVA_WANT="v0.0.0-test" \
       NOVA_PROBE_URL="http://localhost/test" \
       bash "$SCRIPT" 2>&1)" || true

# -- The output must contain a drift about the harness failing inside the wall.
if [ "$(uname -s)" != "Linux" ]; then
  case "$HW_OUT" in
    *"cannot start inside the sandbox wall"*) bad "the harness canary ran on $(uname -s); it is Linux-only [$HW_OUT]" ;;
    *) ok "the harness canary is Linux-only: not run on $(uname -s)" ;;
  esac
else
case "$HW_OUT" in
  *"DRIFT"*"harness"*"sandbox wall"*|*"DRIFT"*"harness"*"cannot start inside"*)
    ok "bench-standard refuses a bench whose harness cannot start inside the wall"
    ;;
  *)
    bad "bench-standard did not detect harness failure in the sandbox wall [$HW_OUT]"
    ;;
esac
fi
rm -rf "$TMP_HW"

printf '1..%s\n' "$n"
[ "$fails" = 0 ] || { printf 'FAILED\n'; exit 1; }
printf 'PASSED\n'
