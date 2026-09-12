#!/usr/bin/env bash
# nova-sandbox — darwin profile check.
#
# Fills profiles/darwin.sb.tmpl for a scratch write set, then runs, INSIDE the wall
# and by ABSOLUTE path, the things a swarm worker actually does on its first second:
# cd, mkdir -p, git init, git clone --shared, a config write under HOME, cat /etc/hosts,
# /bin/sh -c true, killing its own child, stdout to a pipe and to a file in the write
# set — and asserts that a write outside, a read of the named secret and a listing of
# an ancestor all FAIL, each with a control run OUTSIDE the wall so that a check cannot
# pass by being impossible.
#
# One line per check: CHECK OK name=... / CHECK FAIL name=...  Exit 1 on any FAIL.
# The count is the script's own: every expect_ok, expect_deny, control_ok and report
# below is one line, and no prose anywhere states a number that this file can outgrow.
# No /tmp: the scratch lives beside this script. Works from any cwd.

set -euo pipefail

SELF_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
TMPL="$SELF_DIR/darwin.sb.tmpl"
SCRATCH="$SELF_DIR/../.darwin-check-scratch.$$"

[[ "$(uname -s)" == "Darwin" ]] || { echo "CHECK FAIL name=platform (this script is darwin only)"; exit 1; }
[[ -f "$TMPL" ]] || { echo "CHECK FAIL name=template_present path=$TMPL"; exit 1; }

mkdir -p "$SCRATCH"
SCRATCH="$(cd -- "$SCRATCH" && pwd -P)"
NC_PIDS=()
cleanup() { [[ ${#NC_PIDS[@]} -gt 0 ]] && kill "${NC_PIDS[@]}" 2>/dev/null || true
           chmod -R u+w "$SCRATCH" 2>/dev/null || true; rm -rf "$SCRATCH"; }
trap cleanup EXIT

W="$SCRATCH/w"                  # the write set
REF="$SCRATCH/ref"              # the read set: a local repo to clone --shared
SECRET="$SCRATCH/secret/env"    # named secret, in NEITHER list
OUTSIDE="$SCRATCH/outside"      # write-outside target, in NEITHER list
HOME_DIR="$W/home"              # rule 9: HOME inside a --write
NTMP="$W/.nova-sandbox-tmp"     # rule 8
mkdir -p "$W" "$REF" "$HOME_DIR" "$NTMP" "$SCRATCH/secret" "$OUTSIDE"
printf 'not-a-real-key\n' > "$SECRET"

GIT="$(command -v git)"
"$GIT" -c init.defaultBranch=main init -q "$REF"
printf 'ref\n' > "$REF/file.txt"
"$GIT" -C "$REF" -c user.name=check -c user.email=check@example.com \
       -c commit.gpgsign=false add -A
"$GIT" -C "$REF" -c user.name=check -c user.email=check@example.com \
       -c commit.gpgsign=false commit -q -m seed

# ---- fill the template -------------------------------------------------------
PROFILE="$SCRATCH/p.sb"
ancestors_of() { local d; d="$(dirname -- "$1")"; while [[ "$d" != "/" ]]; do printf '%s\n' "$d"; d="$(dirname -- "$d")"; done; }

OPTROOTS=""
# the documented darwin optional roots, and nothing else: a check that grants a root
# the spec's table does not name is testing a broader policy than the document.
# (A caller whose git is the Xcode shim names /private/var/db with --read, not here.)
for r in /opt/homebrew /opt/local "$(dirname -- "$GIT")"; do
  # skip-if-absent: an absent root is never a refusal
  [[ -d "$r" ]] || continue
  case "$r" in /usr/*|/bin|/sbin|/System/*) continue;; esac
  case "$OPTROOTS" in *"\"$r\""*) continue;; esac
  OPTROOTS+="(allow file-read* (subpath \"$r\"))"$'\n'
done
ANCESTORS="$(
  { ancestors_of "$W"; ancestors_of "$REF"; } | sort -u |
  while IFS= read -r d; do printf '(allow file-read-metadata (literal "%s"))\n' "$d"; done
)"
READS='(allow file-read* (subpath (param "READ0")))'
WRITES='(allow file-read* file-write* (subpath (param "WRITE0")))
(allow network-outbound (subpath (param "WRITE0")))'
# IP only: (allow network*) would grant every unix-domain socket on the machine,
# the inherited SSH agent's among them.
# The mDNSResponder literal is the DNS grant: macOS resolves names over that unix
# socket, and IP-only outbound without it is a wall with no DNS (check dns_resolves).
NET='(allow network-outbound (remote ip) (literal "/private/var/run/mDNSResponder"))'

: > "$PROFILE"
while IFS= read -r line; do
  case "$line" in
    '@@OPTROOTS@@')  printf '%s' "$OPTROOTS" >> "$PROFILE" ;;
    '@@ANCESTORS@@') printf '%s\n' "$ANCESTORS" >> "$PROFILE" ;;
    '@@READS@@')     printf '%s\n' "$READS" >> "$PROFILE" ;;
    '@@WRITES@@')    printf '%s\n' "$WRITES" >> "$PROFILE" ;;
    '@@NET@@')       printf '%s\n' "$NET" >> "$PROFILE" ;;
    *)               printf '%s\n' "$line" >> "$PROFILE" ;;
  esac
done < "$TMPL"

# ---- the child environment (rule 9: the wrapper scrubs the agent) ------------
# The caller's environment as a swarm worker's launcher really holds it: an SSH agent
# socket and an agent-shaped name beside a variable that must survive. The wrapper builds
# the child environment from it by EXCLUSION, and the set is rule 9's EXACT one, by name:
# SSH_AUTH_SOCK, SSH_AGENT_*, GPG_AGENT_INFO, *_AGENT_PID, *_AGENT_INFO, *_AGENT_SOCK.
# It is NOT "every name containing AGENT" -- that width was measured to drop AI_AGENT and
# CLAUDE_AGENT_SDK_VERSION, which say what is RUNNING the job and address nothing.
# This filter is the SCRIPT's, so it can only agree with itself; the tool's own scrub is
# asserted in Go, in internal/sandbox/policy_test.go TestScrubSetIsExactlyTheSpecs and
# TestChildEnv, and end to end in cmd/nova-sandbox TestTheNoteNamesExactlyWhatWasDropped.
CALLER_AGENT_SOCK="$SCRATCH/secret/agent.sock"
CALLER_ENV=$(printf '%s\n' \
  "SSH_AUTH_SOCK=$CALLER_AGENT_SOCK" \
  "GPG_AGENT_INFO=$CALLER_AGENT_SOCK:1:1" \
  "NOVA_KEEP=kept")
CHILD_ENV=()
while IFS= read -r kv; do
  case "${kv%%=*}" in SSH_AUTH_SOCK|SSH_AGENT_*|GPG_AGENT_INFO|*_AGENT_PID|*_AGENT_INFO|*_AGENT_SOCK) continue;; esac
  CHILD_ENV+=("$kv")
done <<< "$CALLER_ENV"

# ---- the runner --------------------------------------------------------------
FAILED=0
report() { # report <ok|fail> <name> [detail]
  if [[ "$1" == ok ]]; then printf 'CHECK OK name=%s\n' "$2"
  else FAILED=1; printf 'CHECK FAIL name=%s %s\n' "$2" "${3:-}"; fi
}
walled() { # walled <sh-command> -> runs it inside the wall, stdout+stderr to caller
  # cwd is the write set (rule 13): a cwd outside every named path is denied to
  # getcwd(3) and every git command dies with
  # "shell-init: error retrieving current directory ... Operation not permitted".
  cd -- "$W" || return 1
  /usr/bin/env -i \
    HOME="$HOME_DIR" PATH="/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
    TMPDIR="$NTMP" TMP="$NTMP" TEMP="$NTMP" \
    "${CHILD_ENV[@]}" \
    /usr/bin/sandbox-exec -p "$(cat "$PROFILE")" \
      -D READ0="$REF" -D WRITE0="$W" -D HOME="$HOME_DIR" \
      -- /bin/sh -c "$1"
}
expect_ok() { local name="$1" cmd="$2" out rc; set +e; out="$(walled "$cmd" 2>&1)"; rc=$?; set -e
  if [[ $rc -eq 0 ]]; then report ok "$name"; else report fail "$name" "rc=$rc out=$(printf '%s' "$out" | head -1)"; fi; }
expect_deny() { local name="$1" cmd="$2" out rc; set +e; out="$(walled "$cmd" 2>&1)"; rc=$?; set -e
  if [[ $rc -ne 0 ]]; then report ok "$name"; else report fail "$name" "SUCCEEDED inside the wall"; fi; }
control_ok() { local name="$1"; shift; set +e; "$@" >/dev/null 2>&1; local rc=$?; set -e
  if [[ $rc -eq 0 ]]; then report ok "$name"; else report fail "$name" "rc=$rc (outside the wall)"; fi; }

# --- the worker's first second, by absolute path, inside the wall -------------
expect_ok cd_absolute        "cd '$W' && test \"\$(pwd)\" = '$W'"
expect_ok mkdir_p_absolute   "mkdir -p '$W/a/b/c' && test -d '$W/a/b/c'"
expect_ok git_init_absolute  "git init -q '$W/g' && test -d '$W/g/.git'"
expect_ok git_clone_shared   "git clone -q --shared '$REF' '$W/clone' && test -f '$W/clone/file.txt'"
# from inside the repo git init just made: this scratch lives inside a checkout, and
# git's upward discovery would otherwise stop at the outer .git, which the wall denies.
expect_ok home_config_write  "cd '$W/g' && git config --global user.name nova-check && test -f '$HOME_DIR/.gitconfig'"
expect_ok cat_etc_hosts      "cat /etc/hosts > /dev/null"
expect_ok sh_c_true          "/bin/sh -c true"
expect_ok child_kill         "sleep 5 & kill \$!"
expect_ok stdout_to_file     "echo nova > '$W/out.txt' && test \"\$(cat '$W/out.txt')\" = nova"

# stdout to a pipe the caller drains (rule 12)
set +e; PIPED="$(walled "echo nova-pipe" 2>/dev/null)"; PRC=$?; set -e
[[ $PRC -eq 0 && "$PIPED" == "nova-pipe" ]] && report ok stdout_to_pipe \
  || report fail stdout_to_pipe "rc=$PRC out=$PIPED"

# --- the three denials, each with a control outside the wall ------------------
expect_deny write_outside    ": > '$OUTSIDE/probe.$$'"
control_ok  write_outside_control /usr/bin/touch "$OUTSIDE/control.$$"
expect_deny read_secret      "cat '$SECRET' > /dev/null"
control_ok  read_secret_control /bin/cat "$SECRET"
expect_deny list_ancestor    "ls '$SCRATCH' > /dev/null"
control_ok  list_ancestor_control /bin/ls "$SCRATCH"

# --- #69: no socket outside the write set is reachable ------------------------
# The socket lives in the secret directory, in NEITHER list, standing in for the SSH
# agent's socket. Paths are RELATIVE on purpose: sun_path is 104 bytes and an absolute
# path under a deep scratch silently fails to bind, which would pass this check for
# the wrong reason. Two listeners, because `nc -lU` serves one connection and exits:
# the walled attempt must not consume the one the control needs.
SOCKDIR="$SCRATCH/secret"
listen() { # listen <dir> <relative-socket-name>
  { cd -- "$1" && exec /usr/bin/nc -lU "./$2"; } >/dev/null 2>&1 &
  NC_PIDS+=("$!")
  disown "$!" 2>/dev/null || true   # the shell must not print "Terminated" when cleanup kills it
}
wait_for_socket() { # wait_for_socket <path>  (bounded: 2 seconds, never unbounded)
  local i; for i in 1 2 3 4 5 6 7 8 9 10; do [[ -S "$1" ]] && return 0; /bin/sleep 0.2; done; return 1
}
listen "$SOCKDIR" agent.sock
listen "$SOCKDIR" agent-control.sock
if wait_for_socket "$SOCKDIR/agent.sock" && wait_for_socket "$SOCKDIR/agent-control.sock"; then
  expect_deny unix_socket_outside "nc -U ../secret/agent.sock < /dev/null"
  set +e
  ( cd -- "$SOCKDIR" && /usr/bin/nc -U ./agent-control.sock < /dev/null >/dev/null 2>&1 )
  CRC=$?
  set -e
  if [[ $CRC -eq 0 ]]; then report ok unix_socket_outside_control
  else report fail unix_socket_outside_control "rc=$CRC (outside the wall)"; fi
else
  report fail unix_socket_outside "no listener: nc -lU did not bind (sun_path is 104 bytes)"
fi
# the job's own socket, under the write set, is the one that must still connect
listen "$W" job.sock
wait_for_socket "$W/job.sock" || true
expect_ok unix_socket_inside "nc -U ./job.sock < /dev/null"

# --- rule 7: DNS resolves inside the wall, and does so because of the socket ---
# curl to a NAME, not an IP: any HTTP status at all means the name resolved and the
# connection was made (200 and 404 both count). rc=6 / 000 is "could not resolve host".
dns_cmd="curl -s -o /dev/null -w '%{http_code}' --max-time 15 https://example.com"
set +e; DNS_OUT="$(walled "$dns_cmd" 2>/dev/null)"; set -e
case "$DNS_OUT" in
  000|"") report fail dns_resolves "http_code=$DNS_OUT (the name did not resolve)";;
  *)      report ok dns_resolves ;;
esac
# the control: the SAME profile with the mDNSResponder literal removed must NOT resolve,
# so that dns_resolves cannot pass by the socket being irrelevant.
NODNS="$SCRATCH/p-nodns.sb"
sed 's| (literal "/private/var/run/mDNSResponder")||' "$PROFILE" > "$NODNS"
set +e
DNS_CTL="$(cd -- "$W" && /usr/bin/env -i HOME="$HOME_DIR" \
  PATH="/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin" TMPDIR="$NTMP" \
  /usr/bin/sandbox-exec -p "$(cat "$NODNS")" \
    -D READ0="$REF" -D WRITE0="$W" -D HOME="$HOME_DIR" \
    -- /bin/sh -c "$dns_cmd" 2>/dev/null)"
set -e
case "$DNS_CTL" in
  000|"") report ok dns_resolves_control ;;
  *)      report fail dns_resolves_control "http_code=$DNS_CTL WITHOUT the socket: DNS is reaching the resolver some other way";;
esac

# --- rule 7: mach-lookup is narrowed, and the clipboard is the witness ---------
expect_deny clipboard_denied "pbpaste > /dev/null"

# --- rule 4: a sandbox cannot be nested inside this one -----------------------
# run-emma.sh's `gemini --sandbox` is exactly this, and it dies:
# sandbox-exec: sandbox_apply: Operation not permitted. A wrapped launcher must drop
# its own sandbox flag rather than discover this at run time.
expect_deny nested_sandbox_refused \
  "/usr/bin/sandbox-exec -p '(version 1)(allow default)' /bin/sh -c true"

# --- rule 9: the agent is gone from the child environment ---------------------
# The caller's environment held SSH_AUTH_SOCK and GPG_AGENT_INFO; the child must hold
# neither, and must still hold what the caller meant to pass (the provider key arrives
# this way, rule 6).
expect_ok env_no_ssh_auth_sock \
  "test -z \"\${SSH_AUTH_SOCK:-}\" && test -z \"\${GPG_AGENT_INFO:-}\" && test \"\${NOVA_KEEP:-}\" = kept"

exit "$FAILED"
