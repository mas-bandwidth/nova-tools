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
# No /tmp: the scratch lives beside this script. Works from any cwd.

set -euo pipefail

SELF_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
TMPL="$SELF_DIR/darwin.sb.tmpl"
SCRATCH="$SELF_DIR/../.darwin-check-scratch.$$"

[[ "$(uname -s)" == "Darwin" ]] || { echo "CHECK FAIL name=platform (this script is darwin only)"; exit 1; }
[[ -f "$TMPL" ]] || { echo "CHECK FAIL name=template_present path=$TMPL"; exit 1; }

mkdir -p "$SCRATCH"
SCRATCH="$(cd -- "$SCRATCH" && pwd -P)"
cleanup() { chmod -R u+w "$SCRATCH" 2>/dev/null || true; rm -rf "$SCRATCH"; }
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
for r in /opt/homebrew /opt/local "$(dirname -- "$GIT")" /private/var/db; do
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
WRITES='(allow file-read* file-write* (subpath (param "WRITE0")))'
NET='(allow network*)'

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
    /usr/bin/sandbox-exec -f "$PROFILE" \
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

exit "$FAILED"
