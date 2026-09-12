#!/usr/bin/env bash
# The ldflags a release build uses -- printed by one place, so that the -X stamp cannot be
# dropped by an edit to a long `go build` line nobody rereads.
#
# WHY THIS IS A SCRIPT AND NOT A STRING IN THE WORKFLOW. `-X main.version=` with nothing
# after it is a legal linker flag: it writes the empty string into main.version, the binary
# falls back to its vcs stamp or to `devel`, and every check downstream passes because the
# build itself never complained. That is the hole #118 names at release.yml:119. A release
# whose nova-wake answered `devel` would, since #107, refuse every nova-bus in the same
# release -- the tools would be mutually unusable and the tag would look fine.
#
# So the refusal lives here, one caller, and ci.yml's release-dry-run asserts THIS SCRIPT
# refuses an empty stamp. An assertion nothing exercises is a belief.
set -euo pipefail

if [ "$#" -ne 1 ]; then
	echo "usage: $0 <stamp>" >&2
	echo "  prints the -ldflags value for a release build of that stamp, or refuses" >&2
	exit 2
fi

stamp=$1

# Refused rather than defaulted. A default here would be a version string nobody can
# trace, which cmd/nova-bus/version.go argues at length is worse than none.
if [ -z "$stamp" ]; then
	echo "refusing: the release stamp is empty, so -X main.version= would write an empty" >&2
	echo "  version and every binary would report devel while the release page says a tag" >&2
	exit 1
fi

# Whitespace would split the flag: `-X main.version=v1 2` hands the linker a second
# argument and stamps `v1`. The version verbs put every field through oneline.Field for
# the same reason -- this is the same defence one step earlier, where it can still refuse.
case "$stamp" in
*[[:space:]]*)
	echo "refusing: the release stamp <$stamp> carries whitespace; it would split the linker flag" >&2
	exit 1
	;;
esac

ldflags="-s -w -X main.version=${stamp}"

# The composed string is checked, not just its input: this is what the build actually
# passes, and what the dry run pins.
case "$ldflags" in
*"-X main.version="?*) ;;
*)
	echo "refusing: composed ldflags carry no non-empty -X main.version: $ldflags" >&2
	exit 1
	;;
esac

printf '%s\n' "$ldflags"
