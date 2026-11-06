#!/usr/bin/env bash
# check.sh: run TLC over every model case in the repository, in parallel.
#
#   tests/tla/check.sh JAR      every <tool>/tla directory that holds a module
#   tests/tla/check.sh JAR DIR...     only these directories
#
# A directory with a CASES.tsv runs its rows (config, module, expected,
# property, deadlock). Expected is "pass", or the kind of violation the case
# is a reversed witness for (invariant, action, temporal) with the property it
# must break. Deadlock is "check" or "ignore-terminal" (TLC -deadlock).
# A directory with no CASES.tsv holds one module: each .cfg runs against it,
# a .cfg whose name contains "Broken" must end in a violation, and every
# other .cfg must pass.
#
# TLC writes into a private copy of the directory, never into the checkout.
# Each case is bounded by TLC_TIMEOUT seconds (default 600); a timeout fails.
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
		-cp "$TLC_JAR" tlc2.TLC -workers 1 -metadir "$work/meta" -config "$cfg" "${flags[@]}" "$module" 2>&1)
	code=$?
	set -e
	ok=0
	case "$expected" in
	pass) [ "$code" -eq 0 ] && ok=1 ;;
	*)
		if [ "$code" -ge 10 ] && [ "$code" -le 14 ]; then
			ok=1
			if [ "$expected" = invariant ] || [ "$expected" = action ]; then
				ok=0
				IFS='|' read -r -a props <<<"$property"
				for p in "${props[@]}"; do
					grep -q -E "(Invariant|Action property) $p is violated" <<<"$out" && ok=1
				done
			fi
		fi
		;;
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
	for d in */tla; do compgen -G "$d/*.tla" >/dev/null && dirs+=("$d"); done
fi

cases=$(mktemp)
trap 'rm -f "$cases"' EXIT
for d in "${dirs[@]}"; do
	if [ -f "$d/CASES.tsv" ]; then
		tail -n +2 "$d/CASES.tsv" | while IFS=$'\t' read -r cfg module expected property deadlock; do
			printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$d" "$cfg" "$module" "$expected" "$property" "$deadlock"
		done >>"$cases"
		continue
	fi
	mods=("$d"/[!M]*.tla)
	[ ${#mods[@]} -eq 1 ] && [ -f "${mods[0]}" ] || { echo "$d: no CASES.tsv and not exactly one module" >&2; exit 2; }
	module=$(basename "${mods[0]}")
	for c in "$d"/*.cfg; do
		cfg=$(basename "$c")
		expected=pass
		case "$cfg" in *Broken*) expected=violation ;; esac
		printf '%s\t%s\t%s\t%s\t-\tcheck\n' "$d" "$cfg" "$module" "$expected" >>"$cases"
	done
done

jobs=$(getconf _NPROCESSORS_ONLN)
echo "$(wc -l <"$cases" | tr -d ' ') cases, $jobs at a time"
tr '\t' '\n' <"$cases" | xargs -d '\n' -n 6 -P "$jobs" "$0" --case
