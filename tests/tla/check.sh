#!/usr/bin/env bash
# check.sh: run TLC over every model case in the repository, in parallel.
#
#   tests/tla/check.sh JAR      every cmd/<tool>/tla directory that holds a module
#   tests/tla/check.sh JAR DIR...     only these directories
#
# A directory runs the rows of its CASES.tsv (config, module, expected,
# property, deadlock). Expected is "pass", or the kind of violation the case
# is a reversed witness for (invariant, action, temporal) with the property it
# must break: TLC must name that invariant or action property as violated, and
# a temporal case must end in a violated temporal property and nothing else.
# Deadlock is "check" or "ignore-terminal" (TLC -deadlock).
#
# TLC writes into a private copy of the directory, never into the checkout.
# Each case is bounded by TLC_TIMEOUT seconds (default 600); a timeout fails.
# Each TLC has 4 workers: the small cases end in seconds and leave their cores to
# the long ones (the trace checks), which would otherwise run on one core each.
# With TLC_SKIP_LARGE=1 a config whose name contains "Large" (a bigger instance of
# a config that still runs) is left out: per-commit CI sets it to stay inside its
# 2 minutes, and the nightly (schedule) run checks every case.
# With TLC_SHARD=K/N only shard K of N runs: the cases sorted, then every Nth
# from the Kth, so the N shards together run every case exactly once. CI runs
# one job per shard, since the whole set on one runner overran 2 minutes.
set -euo pipefail

if [ "${1:-}" = "--case" ]; then
	shift
	dir=$1 cfg=$2 module=$3 expected=$4 property=$5 deadlock=$6
	work=$(mktemp -d)
	trap 'rm -rf "$work"' EXIT
	cp "$dir"/*.tla "$dir"/*.cfg "$work"/
	flags=()
	[ "$deadlock" = "ignore-terminal" ] && flags+=(-deadlock)
	set +e
	out=$(cd "$work" && timeout "${TLC_TIMEOUT:-600}" java -XX:+UseParallelGC -Djava.io.tmpdir="$work" \
		-cp "$TLC_JAR" tlc2.TLC -workers 4 -metadir "$work/meta" -config "$cfg" "${flags[@]}" "$module" 2>&1)
	code=$?
	set -e
	ok=0
	case "$expected" in
	pass) [ "$code" -eq 0 ] && ok=1 ;;
	invariant | action)
		if [ "$code" -ge 10 ] && [ "$code" -le 14 ]; then
			IFS='|' read -r -a props <<<"$property"
			for p in "${props[@]}"; do
				grep -q -E "(Invariant|Action property) $p is violated" <<<"$out" && ok=1
			done
		fi
		;;
	# 13 is TLC's exit status for a violated temporal property; its output does not say which
	temporal) [ "$code" -eq 13 ] && ok=1 ;;
	esac
	if [ "$ok" -eq 1 ]; then
		echo "ok   $dir/$cfg ($expected)"
		exit 0
	fi
	echo "FAIL $dir/$cfg: expected $expected $property, TLC exit $code"
	tail -n 30 <<<"$out"
	exit 1
fi

[ $# -ge 1 ] || { echo "usage: tests/tla/check.sh JAR [DIR...]" >&2; exit 2; }
TLC_JAR=$1
shift
[ -f "$TLC_JAR" ] || { echo "no TLC jar at $TLC_JAR" >&2; exit 2; }
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
want=$(cut -d' ' -f1 tests/tla/tla2tools.sha256)
have=$( (sha256sum "$TLC_JAR" 2>/dev/null || shasum -a 256 "$TLC_JAR") | cut -d' ' -f1)
[ "$want" = "$have" ] || { echo "TLC jar $TLC_JAR is not the pinned tla2tools.jar ($have, want $want)" >&2; exit 2; }
export TLC_JAR

dirs=("$@")
if [ ${#dirs[@]} -eq 0 ]; then
	for d in cmd/*/tla; do compgen -G "$d/*.tla" >/dev/null && dirs+=("$d"); done
fi

cases=$(mktemp)
trap 'rm -f "$cases"' EXIT
for d in "${dirs[@]}"; do
	[ -f "$d/CASES.tsv" ] || { echo "$d: no CASES.tsv" >&2; exit 2; }
	tail -n +2 "$d/CASES.tsv" | while IFS=$'\t' read -r cfg module expected property deadlock; do
		[ -n "${TLC_SKIP_LARGE:-}" ] && [[ $cfg == *Large* ]] && { echo "skip $d/$cfg (TLC_SKIP_LARGE)" >&2; continue; }
		printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$d" "$cfg" "$module" "$expected" "$property" "$deadlock"
	done >>"$cases"
done

if [ -n "${TLC_SHARD:-}" ]; then
	[[ $TLC_SHARD =~ ^([0-9]+)/([0-9]+)$ ]] && [ "${BASH_REMATCH[1]}" -ge 1 ] && [ "${BASH_REMATCH[1]}" -le "${BASH_REMATCH[2]}" ] ||
		{ echo "TLC_SHARD=$TLC_SHARD: want K/N with 1 <= K <= N" >&2; exit 2; }
	LC_ALL=C sort "$cases" | awk -v k="${BASH_REMATCH[1]}" -v n="${BASH_REMATCH[2]}" '(NR - 1) % n == k - 1' >"$cases.shard"
	mv "$cases.shard" "$cases"
	echo "shard $TLC_SHARD"
fi
jobs=$(getconf _NPROCESSORS_ONLN)
echo "$(wc -l <"$cases" | tr -d ' ') cases, $jobs at a time"
tr '\t' '\n' <"$cases" | xargs -d '\n' -n 6 -P "$jobs" "$0" --case
