#!/bin/zsh
# examples/run-worker.sh — ONE AGENT, ONE TASK, WRAPPED BY nova-sandbox
#
# usage:
#   run-worker.sh --worker <dir> --harness <cmd> [--model <name>] [--label <text>]
#                 [--key-var <NAME> --key-file <path>] [--deadline <seconds>]
#                 [--log-dir <dir>] [--job-dir <dir>] [--slot <n>]
#                 [--sandbox <path>] -- "<task>"
#
# Every path, command and name comes from a flag or the matching environment
# variable below. There is no default worker directory, no default harness and no
# default key file: this is an example you configure, not a launcher that knows
# who you are. `--worker` is the directory the agent runs in and may write; the
# task's own output goes under `--job-dir` (default `<worker>/jobs/<label>`).
#
# It is here as a WORKED EXAMPLE of the caller half of docs/SPEC-SANDBOX.md's
# "what every launcher must do to be wrappable", because those four things are
# the ones a launcher gets wrong the first time:
#
#   1. HOME is set to a directory INSIDE a --write (rule 9). A harness whose
#      first config write lands on the real home is denied by the wall, and the
#      error it prints will be about something else.
#   2. The API key is read from the key file AS DATA before the wrap and passed
#      by environment. The key file is in NEITHER list, and `read` closes its
#      descriptor before the exec (rule 12 — /dev/fd re-opens an inherited one).
#      The key is never in this script, in an argument, or in the prompt.
#   3. No nested sandbox: the harness is not given a sandbox flag of its own. A
#      sandbox inside this one fails as `sandbox_apply: Operation not permitted`.
#   4. A PATH whose git is a real git. On macOS `/usr/bin/git` is the Xcode shim
#      and dies inside the wall, so put the package manager's bin first.
#
# environment (each is the default for the flag of the same name):
#   WORKER_DIR, WORKER_HARNESS, WORKER_MODEL, WORKER_KEY_VAR, WORKER_KEY_FILE,
#   WORKER_DEADLINE_S (default 3600), WORKER_LOG_DIR, WORKER_JOB_DIR,
#   WORKER_SLOT (default 0), NOVA_SANDBOX (default `nova-sandbox` on PATH)
#
# One task per run, headless, with a log and a deadline: the agent loads itself,
# does the one task, writes RESULT.md, and stops. No loop inside the agent — if
# you want a pool, the pool is the loop, and every run still dies at --deadline.
set -u

die() { echo "run-worker: $1" >&2; exit 2; }

worker="${WORKER_DIR:-}"
harness="${WORKER_HARNESS:-}"
model="${WORKER_MODEL:-}"
label=""
key_var="${WORKER_KEY_VAR:-}"
key_file="${WORKER_KEY_FILE:-}"
deadline="${WORKER_DEADLINE_S:-3600}"
logdir="${WORKER_LOG_DIR:-}"
jobdir="${WORKER_JOB_DIR:-}"
slot="${WORKER_SLOT:-0}"
sandbox="${NOVA_SANDBOX:-nova-sandbox}"
task=""

while [ $# -gt 0 ]; do
  case "$1" in
    --worker)   worker="${2:?--worker wants a directory}"; shift 2 ;;
    --harness)  harness="${2:?--harness wants a command}"; shift 2 ;;
    --model)    model="${2:?--model wants a name}"; shift 2 ;;
    --label)    label="${2:?--label wants a word}"; shift 2 ;;
    --key-var)  key_var="${2:?--key-var wants an environment variable name}"; shift 2 ;;
    --key-file) key_file="${2:?--key-file wants a path}"; shift 2 ;;
    --deadline) deadline="${2:?--deadline wants seconds}"; shift 2 ;;
    --log-dir)  logdir="${2:?--log-dir wants a directory}"; shift 2 ;;
    --job-dir)  jobdir="${2:?--job-dir wants a directory}"; shift 2 ;;
    --slot)     slot="${2:?--slot wants a number}"; shift 2 ;;
    --sandbox)  sandbox="${2:?--sandbox wants a path}"; shift 2 ;;
    --)         shift; task="${1:-}"; shift $(( $# > 0 ? 1 : 0 )) ;;
    *)          die "unknown argument \"$1\"; run-worker.sh --worker <dir> --harness <cmd> -- \"<task>\"" ;;
  esac
done

[ -n "$worker" ]  || die "--worker <dir> is required: the directory this agent runs in and may write"
[ -n "$harness" ] || die "--harness <cmd> is required: the headless agent command to run"
[ -n "$task" ]    || die "the task is required, after --: run-worker.sh ... -- \"<task>\""
[ -d "$worker" ]  || die "--worker $worker is not a directory"
case "$deadline" in ''|*[!0-9]*) die "--deadline wants whole seconds, not \"$deadline\"";; esac

[ -n "$label" ] || label="task"
label="${label//[^A-Za-z0-9_.-]/-}"
case "$label" in 20[0-9][0-9][01][0-9][0-3][0-9]T*) stamped=1;; *) stamped=0;; esac

# A slot is one worker of several running at once. Each gets its own working
# directory and its own harness data home, because a harness that keeps one
# sqlite database per data home locks concurrent runs out of it ("database is
# locked", measured with six workers on one data home). The agent's own files
# are refreshed from the configured worker directory one way at every start;
# nothing in a slot is copied back.
if [ "$slot" != "0" ]; then
  wdir="$worker-$slot"
  mkdir -p "$wdir/jobs" "$wdir/data" || die "cannot make the slot directory $wdir"
  rsync -a --delete "$worker/self/" "$wdir/self/" 2>/dev/null || true
  export XDG_DATA_HOME="$wdir/data"
else
  wdir="$worker"
fi

[ -n "$jobdir" ] || jobdir="$wdir/jobs/$label"
[ -n "$logdir" ] || logdir="$wdir/logs"

# The key, if this harness needs one: read as DATA, never sourced, one line, the
# bare key or NAME=<key>. The file belongs to whoever holds the credential; this
# script never writes it and never names a path of its own for it.
if [ -n "$key_var" ]; then
  [ -n "$key_file" ] || die "--key-var $key_var also wants --key-file <path>: the one line to read the key from"
  [ -r "$key_file" ] || die "no readable key file at $key_file (one line, chmod 600: $key_var=... or the bare key)"
  IFS= read -r keyline < "$key_file" || keyline=""
  keyline="${keyline#export }"
  keyline="${keyline#$key_var=}"
  keyline="${keyline//[[:space:]]/}"
  [ -n "$keyline" ] || die "$key_file is empty (one line: the key, or $key_var=<key>)"
  export "$key_var=$keyline"
  unset keyline
fi

mkdir -p "$logdir" "$jobdir" "$wdir/.data/home" || die "cannot make the log, job or home directory"
stamp=$(date -u +%Y%m%dT%H%M%SZ)
if [ "$stamped" = 1 ]; then log="$logdir/$label.log"; else log="$logdir/$stamp-$label.log"; fi

# The prompt is the task and the contract this launcher keeps: one task, its own
# directory, a report written where the caller will read it, and no waiting on
# anybody — because the run is killed from out here at the deadline.
prompt="$task

This is one task (label: $label). You are running in $wdir; put anything you
clone or write for this task under $jobdir. Write what you are about to do at the
top of $jobdir/RESULT.md, do the task, write what you found and what you did into
$jobdir/RESULT.md, and then stop. Do not loop, poll, or wait for a reply: this run
is killed by machinery after ${deadline}s, and RESULT.md is how it is read."

echo "run-worker: start $stamp label=$label slot=$slot dir=$wdir deadline=${deadline}s job=$jobdir log=$log"

# cwd is the worker directory rather than the job directory: a headless harness
# commonly refuses reads outside its working directory, and the agent's own
# files live under $wdir.
#
# stdout and stderr go to $log, which the LAUNCHER opens out here and which lives
# OUTSIDE the write set: rule 12 wants a pipe the caller drains or a path inside
# the write set, so the wrap's output is piped through `cat`, which writes the log
# from outside the wall.
(
  cd "$wdir" || exit 3
  PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
  HOME="$wdir/.data/home" \
  "$sandbox" --read "$wdir/self" --write "$wdir" --write "$jobdir" \
    --cwd "$wdir" --name "worker-$label" \
    -- "$harness" ${model:+--model "$model"} -- "$prompt"
) 2>&1 | cat >"$log" &
pid=$!

elapsed=0
while kill -0 "$pid" 2>/dev/null; do
  if [ "$elapsed" -ge "$deadline" ]; then
    echo "run-worker: deadline ${deadline}s reached, stopping pid $pid" | tee -a "$log" >&2
    kill "$pid" 2>/dev/null; sleep 2; kill -9 "$pid" 2>/dev/null
    echo "run-worker: exit=124 (deadline) log=$log"
    exit 124
  fi
  sleep 1; elapsed=$((elapsed+1))
done
wait "$pid"; rc=$?
echo "run-worker: exit=$rc after ${elapsed}s log=$log"
exit $rc
