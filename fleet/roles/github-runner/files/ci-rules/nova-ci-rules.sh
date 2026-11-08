#!/bin/sh
# nova-ci-rules: the CI rules every GitHub Actions runner nova-fleet sets up enforces (fleet/README.md, "CI rules").
#
# The runner runs job-started.sh before each job and job-completed.sh after it (ACTIONS_RUNNER_HOOK_JOB_STARTED and
# _COMPLETED in its .env); both call this script, which lives beside them in <runner dir>/.nova-ci-rules/.
#
#   started             a push outside the allowed branches (push-branches) and tags is refused; then the hook the .env held before
#                       (NOVA_CI_CHAIN_JOB_STARTED) runs; then a push, pull_request or merge_group job gets a watchdog
#   completed           the watchdog is stopped; NOVA_CI_CHAIN_JOB_COMPLETED runs; a job the watchdog stopped fails
#   watchdog W DEADLINE (internal) at DEADLINE (unix seconds) kills every process under Runner.Worker W but the hooks
#   apply LIMIT BRANCHES [DIR..]
#                       (nova-fleet) puts the hooks, LIMIT and BRANCHES (the allowed push branch globs, one word, split
#                       at blanks) into each runner directory (DIR, or every running runner) and restarts an idle
#                       runner whose .env changed
#
# The parts below are pure functions of their arguments and stdin, so the unit tests call them with no clock:
#   push-ok REF, limited EVENT, overdue NOW DEADLINE, limit-text SECONDS,
#   ancestor PID NAME < "pid ppid comm" lines, tree ROOT EXCLUDE < "pid ppid command" lines,
#   env-edit HOOKDIR < .env
set -u

here=$(cd "$(dirname "$0")" && pwd)
state=$here/state
limit=120
[ -r "$here/limit" ] && limit=$(cat "$here/limit")
branches="main sprint/*"
[ -r "$here/push-branches" ] && branches=$(cat "$here/push-branches")

# push-ok REF: a push may run CI on a tag and on a branch matching one of the globs in push-branches, where * and ? stop
# at a / (sprint/* is one segment) and no segment is empty.
push_ok() {
	case $1 in
	refs/tags/?*) return 0 ;;
	refs/heads/?*) ;;
	*) return 1 ;;
	esac
	b=${1#refs/heads/}
	case /$b/ in *//*) return 1 ;; esac
	slashes=$(printf %s "$b" | tr -dc /)
	set -f
	for p in $branches; do
		[ "$(printf %s "$p" | tr -dc /)" = "$slashes" ] || continue
		# shellcheck disable=SC2254 # the pattern is a glob on purpose
		case $b in $p) set +f; return 0 ;; esac
	done
	set +f
	return 1
}

# limited EVENT: per-commit events have the time limit; schedule and workflow_dispatch (nightly, manual) do not.
limited() {
	case $1 in push | pull_request | merge_group) return 0 ;; esac
	return 1
}

overdue() { [ "$1" -ge "$2" ]; }

limit_text() {
	if [ $(($1 % 60)) -eq 0 ]; then echo "$(($1 / 60))m"; else echo "${1}s"; fi
}

# ancestor PID NAME: the nearest process at or above PID whose command name ends in NAME.
ancestor() {
	awk -v start="$1" -v name="$2" '
		{ parent[$1] = $2; comm[$1] = $3 }
		END {
			p = start
			for (i = 0; i < 64 && p != "" && p > 1; i++) {
				c = comm[p]
				if (length(c) >= length(name) && substr(c, length(c) - length(name) + 1) == name) { print p; exit 0 }
				p = parent[p]
			}
			exit 1
		}'
}

# tree ROOT EXCLUDE: every process under ROOT, leaving out (with all under it) any whose command line holds EXCLUDE.
tree() {
	awk -v root="$1" -v ex="$2" '
		{ pid = $1; parent[pid] = $2; $1 = ""; $2 = ""; cmd[pid] = $0; n++; order[n] = pid }
		END {
			keep[root] = 1
			for (more = 1; more; ) {
				more = 0
				for (i = 1; i <= n; i++) {
					p = order[i]
					if (!(p in keep) && (parent[p] in keep) && !(ex != "" && index(cmd[p], ex))) { keep[p] = 1; more = 1 }
				}
			}
			for (i = 1; i <= n; i++) if (order[i] != root && (order[i] in keep)) print order[i]
		}'
}

# hook_gone PATH: a hook that names a retired place (v1's ci-hold under C:\rowan-jobs\, retired 2026-11-07) or a file that is
# not there is never chained: it would fail, or run v1, before every job.
hook_gone() {
	case $1 in [Cc]:'\rowan-jobs\'* | /mnt/[Cc]/rowan-jobs/*) return 0 ;; esac
	[ ! -f "$1" ]
}

# env-edit HOOKDIR: the .env on stdin with the two hooks pointing into HOOKDIR. A hook that pointed elsewhere moves to
# NOVA_CI_CHAIN_JOB_STARTED / _COMPLETED (unless one is already there), so it still runs, from ours; a chain (or a hook
# about to be chained) that hook_gone names is dropped first: the unchain rule.
env_edit() {
	while IFS= read -r l || [ -n "$l" ]; do
		case $l in
		ACTIONS_RUNNER_HOOK_JOB_STARTED="$1/job-started.sh" | ACTIONS_RUNNER_HOOK_JOB_COMPLETED="$1/job-completed.sh") ;;
		ACTIONS_RUNNER_HOOK_JOB_*=* | NOVA_CI_CHAIN_JOB_*=*) hook_gone "${l#*=}" && continue ;;
		esac
		printf '%s\n' "$l"
	done | awk -v dir="$1" '
		function val(line) { return substr(line, index(line, "=") + 1) }
		/^ACTIONS_RUNNER_HOOK_JOB_STARTED=/ { old["STARTED"] = val($0); next }
		/^ACTIONS_RUNNER_HOOK_JOB_COMPLETED=/ { old["COMPLETED"] = val($0); next }
		/^NOVA_CI_CHAIN_JOB_STARTED=/ { chain["STARTED"] = 1 }
		/^NOVA_CI_CHAIN_JOB_COMPLETED=/ { chain["COMPLETED"] = 1 }
		{ print }
		END {
			ours["STARTED"] = dir "/job-started.sh"; ours["COMPLETED"] = dir "/job-completed.sh"
			for (k in ours) if ((k in old) && old[k] != "" && old[k] != ours[k] && !(k in chain)) print "NOVA_CI_CHAIN_JOB_" k "=" old[k]
			print "ACTIONS_RUNNER_HOOK_JOB_STARTED=" ours["STARTED"]
			print "ACTIONS_RUNNER_HOOK_JOB_COMPLETED=" ours["COMPLETED"]
		}'
}

procs() { ps -A -o pid= -o ppid= -o "$1=" | awk '{$1 = $1; print}'; }


# chain HOOK: the hook the .env held before ours, run as the runner would run it (by its extension).
chain() {
	[ -n "$1" ] || return 0
	echo "CI rule: running the chained hook $1"
	case $1 in
	*.ps1) pwsh -NoProfile -File "$1" ;;
	*) bash -e "$1" ;;
	esac
}

# disarm: stop a watchdog left from an earlier job (pid checked against its command line) and forget its verdict.
disarm() {
	if [ -r "$state/watchdog.pid" ]; then
		w=$(cat "$state/watchdog.pid")
		case $(ps -o command= -p "$w" 2>/dev/null) in *nova-ci-rules.sh\ watchdog*) kill "$w" 2>/dev/null ;; esac
		rm -f "$state/watchdog.pid"
	fi
}

started() {
	# whatever an earlier job left (a runner that died before its completed hook) never touches this one
	disarm
	rm -f "$state/fired"
	event=${GITHUB_EVENT_NAME:-} ref=${GITHUB_REF:-}
	if [ "$event" = push ] && ! push_ok "$ref"; then
		set -f
		echo "::error::CI rule: push CI runs only on $(echo $branches | sed 's/ /, /g') and tags, not $ref; set on.push.branches to [$(echo $branches | sed "s/[^ ]*/'&'/g; s/ /, /g")] and let this branch get CI through its pull request"
		exit 1
	fi
	chain "${NOVA_CI_CHAIN_JOB_STARTED:-}" || exit $?
	if ! limited "$event"; then
		echo "CI rule: a $event job has no time limit"
		exit 0
	fi
	worker=$(procs comm | ancestor $$ Runner.Worker) || {
		echo "::error::CI rule: no Runner.Worker above this hook, so the $(limit_text "$limit") limit cannot be enforced; tell the fleet admin"
		exit 1
	}
	mkdir -p "$state"
	deadline=$(($(date +%s) + limit))
	nohup sh "$here/nova-ci-rules.sh" watchdog "$worker" "$deadline" </dev/null >>"$state/watchdog.log" 2>&1 &
	echo $! >"$state/watchdog.pid"
	echo "CI rule: a $event job finishes in $(limit_text "$limit"); longer work belongs in a nightly (schedule) workflow"
}

completed() {
	disarm
	rc=0
	chain "${NOVA_CI_CHAIN_JOB_COMPLETED:-}" || rc=$?
	if [ -e "$state/fired" ]; then
		rm -f "$state/fired"
		echo "::error::CI rule: per-commit jobs finish in $(limit_text "$limit"); move longer work to a nightly (schedule) workflow"
		exit 1
	fi
	exit $rc
}

# The sweep stops every process under the worker first, so none forks or is reparented away between the listing and
# the kill, then kills them; it repeats each second until the worker is gone or the completed hook stops it.
watchdog() {
	worker=$1 deadline=$2
	while kill -0 "$worker" 2>/dev/null; do
		if overdue "$(date +%s)" "$deadline"; then
			[ -e "$state/fired" ] || echo "watchdog: $worker passed its deadline $deadline" | tee "$state/fired"
			ps1=$(procs command | tree "$worker" "$here")
			# shellcheck disable=SC2086 # the pid lists split into words on purpose
			[ -n "$ps1" ] && kill -STOP $ps1 2>/dev/null
			ps2=$(procs command | tree "$worker" "$here")
			# shellcheck disable=SC2086
			[ -n "$ps1$ps2" ] && kill -KILL $ps1 $ps2 2>/dev/null
		fi
		sleep 1
	done
}

owner() { stat -c %U "$1" 2>/dev/null || stat -f %Su "$1"; }

listener_of() { ps -A -o pid= -o command= | awk -v want="$1/bin/Runner.Listener" '$2 == want { print $1; exit }'; }

# gone PID SECONDS: true once PID has exited, waiting up to SECONDS.
gone() {
	i=0
	while kill -0 "$1" 2>/dev/null; do
		[ "$i" -ge "$2" ] && return 1
		sleep 1
		i=$((i + 1))
	done
}

# restart DIR PID: the runner of DIR (its listener PID) restarted by what supervises it, then a new listener seen.
# Exit 0 done, 1 failed, 2 needs root (a launchd daemon from a login that is not root).
restart() {
	if [ "$(uname)" = Linux ]; then
		unit=$(awk -F/ 'END { print $NF }' "/proc/$2/cgroup")
		uid=$(awk -F/ 'END { for (i = 1; i <= NF; i++) if ($i ~ /^user@[0-9]+\.service$/) { sub(/^user@/, "", $i); sub(/\.service$/, "", $i); print $i } }' "/proc/$2/cgroup")
		case $unit in *.service) ;; *) return 1 ;; esac
		set -- "$1" "$2" systemctl
		[ -n "$uid" ] && set -- "$1" "$2" systemctl --user -M "$(id -nu "$uid")@"
		d=$1 old=$2
		shift 2
		# SIGINT to every process of the unit, so the listener ends cleanly even where the unit is KillMode=process
		# and its main process (run.sh) would not pass a stop on; then start it again
		"$@" kill --kill-who=all --signal=SIGINT "$unit" || return 1
		gone "$old" 60 || { kill -KILL "$old" 2>/dev/null; gone "$old" 5; }
		"$@" restart "$unit" || return 1
	else
		d=$1 old=$2 label='' domain=''
		for plist in /Library/LaunchDaemons/*.plist "$(eval echo "~$(owner "$d")")"/Library/LaunchAgents/*.plist; do
			[ -f "$plist" ] || continue
			[ "$(plutil -extract WorkingDirectory raw "$plist" 2>/dev/null)" = "$d" ] || continue
			label=$(plutil -extract Label raw "$plist") || return 1
			case $plist in
			/Library/*) domain=system ;;
			*) domain=gui/$(id -u "$(owner "$d")") ;;
			esac
			break
		done
		[ -n "$label" ] || return 1
		[ "$domain" = system ] && [ "$(id -u)" != 0 ] && return 2
		launchctl kickstart -k "$domain/$label" || return 1
		gone "$old" 60 || return 1
	fi
	i=0
	while :; do
		new=$(listener_of "$d")
		[ -n "$new" ] && [ "$new" != "$old" ] && return 0
		[ "$i" -ge 60 ] && return 1
		sleep 1
		i=$((i + 1))
	done
}

# apply LIMIT BRANCHES [DIR..]: one line per runner, CI-RULES <dir> hooks=<ok|set> restart=<no|done|busy|stopped|failed|needs-root>,
# or CI-RULES <dir> denied=<why> where this login cannot write the runner's directory. Exit 1 when any is denied,
# failed or needs root. As root (Linux) the hooks and the limit belong to root, so a job cannot rewrite them; only
# state/ (the watchdog's) belongs to the runner's login.
apply() {
	lim=$1 br=${2:-}
	[ -n "$br" ] || { echo "nova-ci-rules: apply wants LIMIT BRANCHES [DIR..]" >&2; return 2; }
	shift 2
	src=$here
	if [ $# -eq 0 ]; then
		# shellcheck disable=SC2046 # runner directories hold no spaces
		set -- $(ps -A -o command= | awk '$1 ~ /\/bin\/Runner\.Listener$/ { sub(/\/bin\/Runner\.Listener$/, "", $1); print $1 }' | sort -u)
	fi
	bad=0
	for given in "$@"; do
		# the listener runs from the physical path (run-helper.sh cd -P), so a symlinked directory is resolved first
		dir=$(cd -P "$given" 2>/dev/null && pwd -P) || {
			echo "CI-RULES $given denied=not-reachable-by-$(id -un)"
			bad=1
			continue
		}
		hd=$dir/.nova-ci-rules
		if ! [ -w "$dir" ] || { [ -e "$dir/.env" ] && ! [ -w "$dir/.env" ]; } || { [ -e "$hd" ] && ! [ -w "$hd" ]; }; then
			echo "CI-RULES $dir denied=not-writable-by-$(id -un)"
			bad=1
			continue
		fi
		who=$(owner "$dir")
		hooks=ok
		mkdir -p "$hd/state"
		for f in nova-ci-rules.sh job-started.sh job-completed.sh; do
			cmp -s "$src/$f" "$hd/$f" || { cp "$src/$f" "$hd/$f" && hooks='set'; }
			chmod 0755 "$hd/$f"
		done
		[ "$(cat "$hd/limit" 2>/dev/null)" = "$lim" ] || { echo "$lim" >"$hd/limit" && hooks='set'; }
		[ "$(cat "$hd/push-branches" 2>/dev/null)" = "$br" ] || { echo "$br" >"$hd/push-branches" && hooks='set'; }
		touch "$dir/.env"
		env_edit "$hd" <"$dir/.env" >"$hd/env.new"
		cmp -s "$hd/env.new" "$dir/.env" || { cat "$hd/env.new" >"$dir/.env" && hooks='set'; }
		rm -f "$hd/env.new"
		if [ "$(id -u)" = 0 ]; then
			chown -R 0:0 "$hd"
			chmod 0755 "$hd"
			chmod 0644 "$hd/limit" "$hd/push-branches"
			chown -R "$who" "$hd/state" "$dir/.env"
		fi
		pid=$(listener_of "$dir")
		r=no
		if cmp -s "$dir/.env" "$hd/env.applied"; then
			r=no
		elif [ -z "$pid" ]; then
			r=stopped
			cp "$dir/.env" "$hd/env.applied"
		elif ps -A -o ppid= -o command= | awk -v p="$pid" '$1 == p && /Runner\.Worker/ { found = 1 } END { exit !found }'; then
			r=busy
		else
			restart "$dir" "$pid" >/dev/null 2>&1
			case $? in
			0)
				r='done'
				cp "$dir/.env" "$hd/env.applied"
				;;
			2)
				r=needs-root
				bad=1
				;;
			*)
				r=failed
				bad=1
				;;
			esac
		fi
		echo "CI-RULES $dir hooks=$hooks restart=$r"
	done
	return $bad
}


cmd=${1:-}
[ $# -gt 0 ] && shift
case $cmd in
started) started ;;
completed) completed ;;
watchdog) watchdog "$@" ;;
apply) apply "$@" ;;
push-ok) push_ok "$1" ;;
limited) limited "$1" ;;
overdue) overdue "$1" "$2" ;;
limit-text) limit_text "$1" ;;
ancestor) ancestor "$1" "$2" ;;
tree) tree "$1" "$2" ;;
env-edit) env_edit "$1" ;;
*)
	echo "nova-ci-rules: want started, completed, watchdog, apply, push-ok, limited, overdue, limit-text, ancestor, tree or env-edit" >&2
	exit 2
	;;
esac
