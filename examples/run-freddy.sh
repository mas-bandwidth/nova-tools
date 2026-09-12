#!/bin/zsh
# examples/run-freddy.sh "<task>" [label]   — THE WRAPPED FORM
#
# This is ~/rowan-working/bin/run-freddy.sh with the four things docs/SPEC-SANDBOX.md's
# "what every launcher must do to be wrappable" asks for, and nothing else changed:
#   1. HOME is set to $fdir/.data/home, which is inside a --write (rule 9). The live
#      script sets XDG_DATA_HOME only, and the harness's first config write then lands
#      on the real home, which the wall denies.
#   2. INCEPTION_API_KEY is read from the key file AS DATA before the wrap and passed by
#      environment; the key file is in neither list, and the `read` closes its descriptor
#      before the exec (rule 12 — /dev/fd re-opens an inherited one).
#   3. No nested sandbox: opencode is not given a sandbox flag of its own. (gemini
#      --sandbox under the wrap is sandbox_apply: Operation not permitted.)
#   4. Homebrew git before /usr/bin: /usr/bin/git is the Xcode shim and dies inside.
#
# run-freddy.sh "<task>" [label]
#
# Launch Freddy (OpenCode, Inception Mercury) on ONE task, headless, with a log and a deadline.
# One task per Freddy: he loads himself, tells the table, does the task, tells the table, and stops.
# No loops inside Freddy; the loop, if any, is the pool (freddy-pool.sh).
#
# The Inception key is never in this script, in an argument, or in any file a line at the table
# reads. The shell reads it from $FREDDY_ENV_FILE (default ~/.config/freddy/env), a file only
# Glenn writes, chmod 600, one line: the bare key, or INCEPTION_API_KEY=<key>. It is read as data, never sourced.
#
#   FREDDY_DEADLINE_S   seconds before the run is killed by this machinery (default 3600)
#   FREDDY_LOG_DIR      where logs go (default ~/rowan-working/freddy-runs)
#   FREDDY_DIR          Freddy's working directory (default ~/freddy-working)
#   FREDDY_JOB_DIR      per-task working directory (default $FREDDY_DIR/jobs/<label>)
set -u
env_file="${FREDDY_ENV_FILE:-$HOME/.config/freddy/env}"
task="${1:?usage: run-freddy.sh \"<task description>\" [label]}"
label="${2:-task}"
label="${label//[^A-Za-z0-9_.-]/-}"
case "$label" in 20[0-9][0-9][01][0-9][0-3][0-9]T*) stamped=1;; *) stamped=0;; esac
deadline="${FREDDY_DEADLINE_S:-3600}"
logdir="${FREDDY_LOG_DIR:-$HOME/rowan-working/freddy-runs}"
home_dir="$HOME/freddy-working"
slot="${FREDDY_SLOT:-0}"
if [ "$slot" != "0" ]; then
  # A swarm slot: its own working directory and its own OpenCode data home, because OpenCode keeps
  # one sqlite database per data home and concurrent runs on one database lock each other out
  # (measured 2026-09-10, six workers, "database is locked"). The self is refreshed from the home
  # copy at every start, one way; nothing in a slot is written back to $home_dir.
  fdir="$HOME/freddy-working-$slot"
  mkdir -p "$fdir/jobs" "$fdir/data"
  rsync -a --delete "$home_dir/freddy/" "$fdir/freddy/"
  [ -d "$fdir/bin" ] || cp -R "$home_dir/bin" "$fdir/bin"
  export XDG_DATA_HOME="$fdir/data"
else
  fdir="${FREDDY_DIR:-$home_dir}"
fi
jobdir="${FREDDY_JOB_DIR:-$fdir/jobs/$label}"

if [ ! -r "$env_file" ]; then
  echo "run-freddy: no key file at $env_file (Glenn writes it: INCEPTION_API_KEY=..., chmod 600)" >&2
  exit 2
fi
mkdir -p "$logdir" "$jobdir"
stamp=$(date -u +%Y%m%dT%H%M%SZ)
if [ "$stamped" = 1 ]; then log="$logdir/$label.log"; else log="$logdir/$stamp-$label.log"; fi

# Read the key file as data, never as shell: one line, either INCEPTION_API_KEY=<key> or the bare key.
IFS= read -r keyline < "$env_file" || keyline=""
keyline="${keyline#export }"
keyline="${keyline#INCEPTION_API_KEY=}"
keyline="${keyline//[[:space:]]/}"
if [ -z "$keyline" ]; then
  echo "run-freddy: $env_file is empty (one line: the key, or INCEPTION_API_KEY=<key>)" >&2
  exit 2
fi
export INCEPTION_API_KEY="$keyline"
unset keyline
export OPENCODE_EXPERIMENTAL_BASH_DEFAULT_TIMEOUT_MS=4000000

prompt="Please load yourself from $fdir/freddy and $task

This is one task, run as a child through the family's launcher (label: $label). You are running in $fdir; put anything you clone or write for this task under $jobdir. Do not send anything to the table and do not use bus-send.sh: the swarm has no bus (Glenn's ruling, 2026-09-10). Write what you are about to do at the top of $jobdir/RESULT.md, do the task, write what you found and what you did into $jobdir/RESULT.md, and then stop. Rowan reads RESULT.md and carries it to the table. Do not loop, poll, or wait for replies: this run is killed by machinery after ${deadline}s."
echo "run-freddy: start $stamp label=$label slot=$slot dir=$fdir deadline=${deadline}s job=$jobdir log=$log"
# cwd is Freddy's working directory, not the job dir: OpenCode headless auto-rejects reads
# outside its working directory, and his self, his bin and his clones all live under $fdir.
mkdir -p "$fdir/.data/home"
# stdout and stderr go to $log, which the LAUNCHER opens outside the wall and which lives
# outside the write set: rule 12 wants a pipe the caller drains or a path inside the write
# set, so the wrap's output is piped through `cat`, which writes the log from out here.
(
  cd "$fdir" || exit 3
  PATH="/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin" \
  HOME="$fdir/.data/home" \
  INCEPTION_API_KEY="$INCEPTION_API_KEY" \
  "${NOVA_SANDBOX:-nova-sandbox}" --read "$fdir/freddy" --write "$fdir" --write "$jobdir" \
    --cwd "$fdir" --name "freddy-$label" \
    -- opencode run --model inception/mercury-2.5 --title "$label" -- "$prompt"
) 2>&1 | cat >"$log" &
pid=$!

elapsed=0
while kill -0 "$pid" 2>/dev/null; do
  if [ "$elapsed" -ge "$deadline" ]; then
    echo "run-freddy: deadline ${deadline}s reached, stopping pid $pid" | tee -a "$log" >&2
    kill "$pid" 2>/dev/null; sleep 2; kill -9 "$pid" 2>/dev/null
    if [ -d "$fdir/freddy/.git" ]; then git -C "$fdir/freddy" push -q origin HEAD >>"$log" 2>&1 || echo "run-freddy: self push failed, see $log" >&2; fi
    echo "run-freddy: exit=124 (deadline) log=$log"
    exit 124
  fi
  sleep 1; elapsed=$((elapsed+1))
done
wait "$pid"; rc=$?
if [ -d "$fdir/freddy/.git" ]; then git -C "$fdir/freddy" push -q origin HEAD >>"$log" 2>&1 || echo "run-freddy: self push failed, see $log" >&2; fi
echo "run-freddy: exit=$rc after ${elapsed}s log=$log"
exit $rc
