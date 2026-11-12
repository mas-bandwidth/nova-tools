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
#   push-ok REF, limited EVENT, overdue NOW DEADLINE, limit-text SECONDS, on-battery < `pmset -g batt` output,
#   cgroup-of < /proc/self/cgroup, sample PEAK ANON DIR (a cgroup directory: memory.current, memory.stat),
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

# on-battery < the output of `pmset -g batt`: true when its first line says the machine draws from its battery. A
# laptop runner on battery is about to be carried and may sleep with the lid shut, so no job starts there; on AC power,
# or on a machine without pmset (Linux), every job starts as before.
on_battery() {
	IFS= read -r first || return 1
	case $first in *"'Battery Power'"*) return 0 ;; esac
	return 1
}

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

# hook_gone PATH: a hook that names a file that is not there is never chained: it would fail before every job.
hook_gone() {
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
	if command -v pmset >/dev/null 2>&1 && pmset -g batt 2>/dev/null | on_battery; then
		echo "::error::CI rule: $(hostname -s) is on battery power (pmset -g batt), so it may sleep with this job in flight; the job is refused before it starts; rerun it and another runner takes it"
		exit 1
	fi
	chain "${NOVA_CI_CHAIN_JOB_STARTED:-}" || exit $?
	# measuring never fails or delays a job: any error inside is dropped
	(measure_start) >/dev/null 2>&1 || true
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
	# the job's measurement lines; measuring never changes the job's result
	(measure_end) 2>/dev/null || true
	chain "${NOVA_CI_CHAIN_JOB_COMPLETED:-}" || rc=$?
	if [ -e "$state/fired" ]; then
		rm -f "$state/fired"
		echo "::error::CI rule: per-commit jobs finish in $(limit_text "$limit"); move longer work to a nightly (schedule) workflow"
		exit 1
	fi
	exit $rc
}

# ---- the job's measurement (memory and load), printed into the job's log at its end and appended to the host's
# ci-jobs log (NOVA_CI_JOBS_LOG, default ~/.local/state/nova/ci-jobs.log). It sets the runner caps from data:
#   MEMORY-PEAK host runner repo workflow job run secs peak_mib anon_mib   every job on Linux: the largest memory.current
#               and memory.stat anon of the runner's cgroup, sampled each second while the job ran
#   JOB-LOAD    the same fields, load1 holders why                           a job that ran over 100 s: the machine's load
#               and its nova-reserve holders at the job's end
# Nothing here can fail or hold a job: started and completed call it in a subshell whose failure is dropped.

# cgroup-of < /proc/self/cgroup: the cgroup v2 path of the caller ("0::<path>").
cgroup_of() { sed -n 's/^0::\(.*\)$/\1/p' | head -n 1; }

# sample PEAK ANON DIR: the larger of PEAK and DIR/memory.current, and of ANON and DIR/memory.stat's anon; fails when
# DIR has no memory.current.
sample() {
	cur=$(cat "$3/memory.current" 2>/dev/null) || return 1
	anon=$(awk '$1 == "anon" { print $2; exit }' "$3/memory.stat" 2>/dev/null)
	p=$1 a=$2
	[ "${cur:-0}" -gt "$p" ] && p=$cur
	[ "${anon:-0}" -gt "$a" ] && a=$anon
	echo "$p $a"
}

# sampler DIR OUT WORKER: (internal) once a second, the job's maxima so far into OUT, until DIR or the job's
# Runner.Worker (WORKER) is gone, or it is stopped.
sampler() {
	p=0 a=0
	while kill -0 "$3" 2>/dev/null && v=$(sample "$p" "$a" "$1"); do
		p=${v% *} a=${v#* }
		echo "$v" >"$2.tmp" && mv "$2.tmp" "$2"
		sleep 1
	done
}

stop_sampler() {
	if [ -r "$state/sampler.pid" ]; then
		s=$(cat "$state/sampler.pid")
		case $(ps -o command= -p "$s" 2>/dev/null) in *nova-ci-rules.sh\ sampler*) kill "$s" 2>/dev/null ;; esac
		rm -f "$state/sampler.pid"
	fi
}

measure_start() {
	mkdir -p "$state"
	stop_sampler
	rm -f "$state/mem" "$state/job.start"
	date +%s >"$state/job.start"
	cg=${NOVA_CI_CGROUP:-}
	[ -n "$cg" ] || { [ -r /proc/self/cgroup ] && cg=/sys/fs/cgroup$(cgroup_of </proc/self/cgroup); }
	# only inside a job: the sampler ends with the job's Runner.Worker
	w=$(procs comm | ancestor $$ Runner.Worker) || w=
	if [ -n "$w" ] && [ -n "$cg" ] && [ -r "$cg/memory.current" ]; then
		nohup sh "$here/nova-ci-rules.sh" sampler "$cg" "$state/mem" "$w" </dev/null >/dev/null 2>&1 &
		echo $! >"$state/sampler.pid"
	fi
}

measure_end() {
	stop_sampler
	[ -r "$state/job.start" ] || return 0
	start=$(cat "$state/job.start")
	rm -f "$state/job.start"
	secs=$(($(date +%s) - start))
	id="host=$(hostname -s) runner=${RUNNER_NAME:-?} repo=${GITHUB_REPOSITORY:-?} workflow=\"${GITHUB_WORKFLOW:-?}\" job=${GITHUB_JOB:-?} run=${GITHUB_RUN_ID:-?} secs=$secs"
	log=${NOVA_CI_JOBS_LOG:-$HOME/.local/state/nova/ci-jobs.log}
	mkdir -p "$(dirname "$log")"
	at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
	if [ -r "$state/mem" ]; then
		read -r peak anon <"$state/mem"
		line="MEMORY-PEAK $id peak_mib=$((peak / 1048576)) anon_mib=$((anon / 1048576))"
		echo "$line"
		echo "$at $line" >>"$log"
		rm -f "$state/mem"
	fi
	if [ "$secs" -gt 100 ]; then
		load=$(cut -d' ' -f1 /proc/loadavg 2>/dev/null || sysctl -n vm.loadavg 2>/dev/null | awk '{ print $2 }')
		holders=$(${NOVA_CI_RESERVE:-nova-reserve} status 2>/dev/null | sed -n 's/^slot[0-9]* held .*why=\([^ ]*\).*$/\1/p')
		n=$(printf '%s' "$holders" | grep -c . || true)
		line="JOB-LOAD $id load1=${load:-?} holders=$n why=$(printf '%s' "$holders" | tr '\n' ',' | sed 's/,$//')"
		echo "$line"
		echo "$at $line" >>"$log"
	fi
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

# same NAME: the working directory is the directory NAME holds, NAME looked up without following a last link (ls -d
# reads the name itself), so a name another login turned into a link, or swapped, after the cd is caught.
same() { [ ! -L "$1" ] && [ . -ef "$1" ] && [ "$(ls -di . | awk '{ print $1 }')" = "$(ls -di "$1" | awk '{ print $1 }')" ]; }

# links-owned PATH USER: every link on the absolute PATH is root's or USER's, so no other login chose where it leads.
links_owned() {
	p='' rest=${1#/}
	while [ -n "$rest" ]; do
		c=${rest%%/*}
		case $rest in */*) rest=${rest#*/} ;; *) rest='' ;; esac
		[ -n "$c" ] || continue
		p=$p/$c
		if [ -L "$p" ]; then
			o=$(owner "$p")
			[ "$o" = root ] || [ "$o" = "$2" ] || return 1
		fi
	done
}

# in-hooks HOOKDIR CMD..: CMD run inside HOOKDIR, entered and checked as apply enters it.
in_hooks() { (cd "$1" && same "$1" && shift && "$@"); }

# as-owner USER CMD..: CMD run as USER when root and USER is not, else as this login; given two minutes where timeout
# is there, so a FIFO the login left in place of its .env does not hold root's run.
as_owner() {
	u=$1
	shift
	if [ "$(id -u)" != 0 ] || [ "$(id -u "$u")" = 0 ]; then
		"$@"
	elif command -v runuser >/dev/null 2>&1; then
		timeout 120 runuser -u "$u" -- "$@"
	else
		sudo -n -u "$u" -- "$@"
	fi
}

# env-apply HOOKDIR DIR: DIR/.env with its hooks pointing into HOOKDIR (env-edit), replaced only when that changes it;
# prints set then. Run as the runner's login (apply), so nothing of another user's is read or written through it.
env_apply() {
	cd "$2" || return 1
	touch .env || return 1
	env_edit "$1" <.env >.env.nova-new || return 1
	if cmp -s .env.nova-new .env; then
		rm -f .env.nova-new
	else
		mv .env.nova-new .env && echo set
	fi
}

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
		# each listener's uid with its directory: a process names any path it likes, so only the directory's owner's
		# listener counts for it (below)
		# shellcheck disable=SC2046 # runner directories hold no spaces
		set -- $(ps -A -o uid= -o command= | awk '$2 ~ /\/bin\/Runner\.Listener$/ { sub(/\/bin\/Runner\.Listener$/, "", $2); print $1 ":" $2 }' | sort -u)
	fi
	bad=0
	for given in "$@"; do
		uid=''
		case $given in [0-9]*:/*) uid=${given%%:*} given=${given#*:} ;; esac
		# the listener runs from the physical path (run-helper.sh cd -P), so a symlinked directory is resolved first
		dir=$(cd -P "$given" 2>/dev/null && pwd -P) || {
			echo "CI-RULES $given denied=not-reachable-by-$(id -un)"
			bad=1
			continue
		}
		# a directory reached through a link another login owns, or named by another login's process, is not the
		# runner's: that login could point root at any directory
		if ! links_owned "$given" "$(owner "$dir")" || { [ -n "$uid" ] && [ "$uid" != "$(id -u "$(owner "$dir")")" ]; }; then
			echo "CI-RULES $given denied=not-its-owners"
			bad=1
			continue
		fi
		hd=$dir/.nova-ci-rules
		if ! [ -w "$dir" ] || { [ -e "$dir/.env" ] && ! [ -w "$dir/.env" ]; } || { [ -e "$hd" ] && ! [ -w "$hd" ]; }; then
			echo "CI-RULES $dir denied=not-writable-by-$(id -un)"
			bad=1
			continue
		fi
		who=$(owner "$dir")
		# Root in a directory another login owns reaches nothing there by a name that login could point elsewhere: the
		# hooks directory is entered, checked to be the one its name holds (and root's), and written only relative to
		# it; the .env is read and written as its owner. Prints the hooks' state and whether the .env is the one the
		# runner was last started on.
		seen=$(
			cd "$dir" && [ "$(pwd -P)" = "$dir" ] && [ "$(owner .)" = "$who" ] || exit 1
			if [ -L .nova-ci-rules ] || { [ "$(id -u)" = 0 ] && [ -e .nova-ci-rules ] && [ "$(owner .nova-ci-rules)" != root ]; }; then
				rm -rf ./.nova-ci-rules || exit 1
			fi
			mkdir -p .nova-ci-rules && cd .nova-ci-rules && same "$hd" || exit 1
			[ "$(id -u)" != 0 ] || [ "$(owner .)" = root ] || exit 1
			h=ok
			for f in nova-ci-rules.sh job-started.sh job-completed.sh; do
				cmp -s "$src/$f" "./$f" || { cp "$src/$f" "./$f" && h='set'; }
				chmod 0755 "./$f"
			done
			[ "$(cat limit 2>/dev/null)" = "$lim" ] || { echo "$lim" >limit && h='set'; }
			[ "$(cat push-branches 2>/dev/null)" = "$br" ] || { echo "$br" >push-branches && h='set'; }
			mkdir -p state
			if [ "$(id -u)" = 0 ]; then
				chown 0:0 . nova-ci-rules.sh job-started.sh job-completed.sh limit push-branches
				chmod 0755 .
				chmod 0644 limit push-branches
				chown -R "$who" state
			fi
			e=$(as_owner "$who" sh ./nova-ci-rules.sh env-apply "$hd" "$dir") || exit 1
			[ "$e" = set ] && h='set'
			as_owner "$who" cat "$dir/.env" >env.now || exit 1
			if cmp -s env.now env.applied; then echo "$h fresh"; else echo "$h stale"; fi
		) || {
			echo "CI-RULES $dir denied=hooks-not-reachable-as-$(id -un)"
			bad=1
			continue
		}
		hooks=${seen% *}
		pid=$(listener_of "$dir")
		r=no
		if [ "${seen#* }" = fresh ]; then
			r=no
		elif [ -z "$pid" ]; then
			r=stopped
			in_hooks "$hd" mv env.now env.applied
		elif ps -A -o ppid= -o command= | awk -v p="$pid" '$1 == p && /Runner\.Worker/ { found = 1 } END { exit !found }'; then
			r=busy
		else
			restart "$dir" "$pid" >/dev/null 2>&1
			case $? in
			0)
				r='done'
				in_hooks "$hd" mv env.now env.applied
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
on-battery) on_battery ;;
sampler) sampler "$1" "$2" "$3" ;;
cgroup-of) cgroup_of ;;
sample) sample "$1" "$2" "$3" ;;
limit-text) limit_text "$1" ;;
ancestor) ancestor "$1" "$2" ;;
tree) tree "$1" "$2" ;;
env-edit) env_edit "$1" ;;
env-apply) env_apply "$1" "$2" ;;
*)
	echo "nova-ci-rules: want started, completed, watchdog, sampler, apply, push-ok, limited, overdue, on-battery, cgroup-of, sample, limit-text, ancestor, tree, env-edit or env-apply" >&2
	exit 2
	;;
esac
