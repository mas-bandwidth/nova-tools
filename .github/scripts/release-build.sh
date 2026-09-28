#!/usr/bin/env bash
# Builds every shipped tool for ONE shipped platform, the way a release ships it.
#
# release.yml's build matrix and certification.yml's release-build matrix both run this
# script and nothing else to compile, so the dry-run builds exactly what the release builds:
# the same tool set, the same flags, the same names. One leg per platform, in parallel,
# each under the two-minute cap; release-sums.sh then checks and sums the whole set on one
# machine.
#
# THE TOOL SET IS cmd/*/, walked here rather than listed, so a tool added to this
# repository ships without anyone editing this file. deprecated/ is its own module and is
# never reached.
#
# THE LDFLAGS ARE COMPOSED BY release-ldflags.sh, WHICH CAN REFUSE, once, before anything
# is compiled. `-X main.version=` with an empty value is a legal linker flag that stamps the
# empty string (#118); a refused stamp stops this script under `set -e` rather than
# building a set of unstamped artifacts. -trimpath so a binary holds no path from the
# runner, CGO_ENABLED=0 so it runs on any machine of its platform. The linker ignores -X
# for a tool with no `version` var, which is why assert-version-stamp.sh runs the binaries.
#
# One `go build` over every tool, not one per tool: the package graph is loaded once and
# the compiles run in parallel. The binaries go to a scratch directory and are renamed into
# the release names, `<tool>_<stamp>_<goos>_<goarch>[.exe]`, which is the only naming a
# release uses.
set -euo pipefail

if [ "$#" -ne 4 ]; then
	echo "usage: $0 <stamp> <goos> <goarch> <out-dir>" >&2
	echo "  builds every cmd/*/ tool for <goos>/<goarch> into <out-dir>/<tool>_<stamp>_<goos>_<goarch>[.exe]" >&2
	exit 2
fi

stamp=$1
goos=$2
goarch=$3
out=$4
here=$(cd "$(dirname "$0")" && pwd)

if ! grep -qx "$goos $goarch" "$here/release-targets"; then
	echo "refusing: $goos/$goarch is not a shipped platform (.github/scripts/release-targets)" >&2
	exit 1
fi

LDFLAGS=$("$here/release-ldflags.sh" "$stamp")
echo "ldflags: $LDFLAGS"

ext=""
[ "$goos" = windows ] && ext=".exe"

pkgs=()
for dir in cmd/*/; do
	pkgs+=("./${dir%/}")
done
[ "${#pkgs[@]}" -gt 0 ] || { echo "cmd/ matched nothing: this release would ship an empty set" >&2; exit 1; }

scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 go build \
	-trimpath -ldflags "$LDFLAGS" -o "$scratch/" "${pkgs[@]}"

mkdir -p "$out"
built=0
for pkg in "${pkgs[@]}"; do
	name=$(basename "$pkg")
	[ -f "$scratch/$name$ext" ] || { echo "go build wrote no binary for $name" >&2; exit 1; }
	mv "$scratch/$name$ext" "$out/${name}_${stamp}_${goos}_${goarch}${ext}"
	built=$((built + 1))
done
[ -f "$out/nova-bus_${stamp}_${goos}_${goarch}${ext}" ] || { echo "nova-bus is not in the shipped set" >&2; exit 1; }
echo "built $built tools for $goos/$goarch"
ls -l "$out"
