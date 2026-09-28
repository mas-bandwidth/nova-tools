#!/usr/bin/env bash
# Checks that <dist> holds exactly the shipped set, then writes and verifies SHA256SUMS over
# the whole of it, on this one machine.
#
# The binaries are built one platform per runner (release-build.sh) and arrive here as
# artifacts. This is the single place that sees every one of them, so it is where the set
# is checked and summed: SHA256SUMS lists every artifact of the release and is computed
# over the bytes on this disk, never assembled from per-platform pieces. release.yml runs it
# in the step that attaches the set to the release; certification.yml's release-dry-run
# runs it over the dry-run set.
#
# EXACTLY THE SHIPPED SET: every cmd/*/ tool for every platform in release-targets, and
# nothing else. A leg that uploaded nothing, a tool one platform lost, or a stray file from
# a runner is refused by name here, before a checksum file can agree with it.
#
# SHA256SUMS is written outside <dist> and moved in, so it cannot list itself, and it is
# verified at once: a checksum file nobody has checked is a file whose first reader is the
# person it was supposed to reassure.
set -euo pipefail

if [ "$#" -ne 2 ]; then
	echo "usage: $0 <stamp> <dist>" >&2
	exit 2
fi

stamp=$1
dist=$2
here=$(cd "$(dirname "$0")" && pwd)

[ -d "$dist" ] || { echo "refusing: $dist is not a directory" >&2; exit 1; }
[ ! -e "$dist/SHA256SUMS" ] || { echo "refusing: $dist already holds a SHA256SUMS" >&2; exit 1; }

want=$(mktemp)
have=$(mktemp)
sums=$(mktemp)
trap 'rm -f "$want" "$have" "$sums"' EXIT

while read -r goos goarch; do
	case "$goos" in '' | '#'*) continue ;; esac
	ext=""
	[ "$goos" = windows ] && ext=".exe"
	for dir in cmd/*/; do
		echo "$(basename "$dir")_${stamp}_${goos}_${goarch}${ext}"
	done
done <"$here/release-targets" | LC_ALL=C sort >"$want"
[ -s "$want" ] || { echo "refusing: the shipped set is empty (cmd/*/ x release-targets)" >&2; exit 1; }

(cd "$dist" && ls -A) | LC_ALL=C sort >"$have"
if ! diff -u "$want" "$have"; then
	echo "refusing: $dist is not the shipped set (- missing, + not shipped)" >&2
	exit 1
fi

(cd "$dist" && sha256sum -- *) >"$sums"
mv "$sums" "$dist/SHA256SUMS"
chmod 0644 "$dist/SHA256SUMS"
(cd "$dist" && sha256sum -c SHA256SUMS)
echo "SHA256SUMS over $(wc -l <"$want" | tr -d ' ') artifacts:"
cat "$dist/SHA256SUMS"
