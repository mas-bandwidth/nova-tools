#!/usr/bin/env bash
# Every shipped binary reports the tag it was built from.
#
# Before #118 this was two names -- nova-bus and nova-board -- hand-written into
# release.yml beside a build loop that ships `cmd/*/`. A stamp read by two binaries says
# nothing about the other nine, and since #107 it is nova-wake's stamp that decides which
# nova-bus it will talk to: an unstamped nova-wake answers `devel`, and `devel` matches no
# released bus, so the whole release refuses itself while the release page looks finished.
#
# THE TOOL LIST IS DISCOVERED FROM THE TREE, never written here. The build loop ships
# every `cmd/*/`, so this walks the same directories; a tool added tomorrow is REQUIRED to
# report the tag without anyone editing this file.
#
# WHAT IS REQUIRED IS NOT DISCOVERED. An earlier revision of this file decided per tool,
# from its source, whether it DECLARED the stamp -- a package-level `version` that
# `-X main.version` can write -- and asserted the tag only on the tools that did. Stella's
# review of #125 is the hole in that, and it is the hole #118 itself names: renaming or
# removing that declaration is one of the failure modes this check exists to catch, and
# under a declaration-based rule it makes the tool ELIGIBLE FOR NOTHING -- `stamped=no`, a
# NOTE, exit 0. The check reported on the property by asking the property's own question.
# No amount of parser widening repairs that, because the parser was never the defect: the
# required set must not be a function of the thing being checked. So the required set is
# the shipped set, and the only tools not in it are named below, one by one, by a person.
#
# THE EXEMPTIONS ARE A DEBT, NOT A CLASSIFICATION, and the debt is PAID. The list held six
# names -- nova-check, nova-fuse, nova-memory, nova-self-talk and nova-swarm refused
# `version`, and nova-merge printed a sha256 of its own bytes where every other binary
# prints its build identity. #121's common verb landed on all six, so all eleven shipped
# tools are required to report the tag and the list is empty, which is where #118's
# all-binaries requirement is genuinely met. It stays here, empty, because the property is
# worth keeping readable: a name can only be added or removed by editing this line, so a
# tool cannot quietly rejoin the exempt set by losing a symbol, and the stale-name check
# below still refuses a list that has drifted from the tree.
LEGACY_NO_VERSION_VERB=""

set -euo pipefail

if [ "$#" -lt 2 ] || [ "$#" -gt 3 ]; then
	echo "usage: $0 <expected-tag> <path-template> [cmd-dir]" >&2
	echo "  path-template holds one %s, replaced by the tool name: 'dist/%s_v1.2.3_linux_amd64'" >&2
	echo "  cmd-dir defaults to cmd" >&2
	exit 2
fi

tag=$1
template=$2
cmddir=${3:-cmd}

if [ -z "$tag" ]; then
	echo "refusing: no expected tag given; this check would assert nothing and pass" >&2
	exit 2
fi
case "$template" in
*%s*) ;;
*)
	echo "refusing: the path template <$template> holds no %s, so every tool would resolve to one path" >&2
	exit 2
	;;
esac

# EXACTLY ONE %s AND NO OTHER %, because the template IS the format string below and the
# caller builds it with the tag inside it: release.yml passes "dist/%s_${TAG}_linux_amd64".
# `%` is legal in a git refname, so a tag of `v1%s` or `v1%d` made printf read a directive
# that is not there -- printf exits 1, the script dies under `set -e` before printing
# anything, and the job failed with no FAIL line naming a cause. Refused here by name, and
# refused one step earlier in release-ldflags.sh so the build never runs at all.
before=${template%%"%s"*}
after=${template#*"%s"}
case "$before$after" in
*%*)
	echo "refusing: the path template <$template> holds a % beyond its single %s" >&2
	echo "  the template is this script's printf format; a stray % reads a directive that is not there" >&2
	echo "  (a tag carrying % is refused by release-ldflags.sh before anything is built)" >&2
	exit 2
	;;
esac

# A tag this check could never match is a check that would only ever fail. The match below
# is whole-token with `=` counted as a separator (nova-sandbox prints version=<tag>), so a
# tag containing `=` or whitespace can never be that token however the binary prints it.
# release-ldflags.sh refuses both before the build; this is the same refusal at the other
# end of the workflow, where it names itself rather than failing every tool in turn.
case "$tag" in
*[[:space:]]*)
	echo "refusing: the expected tag <$tag> carries whitespace; no printed token can equal it" >&2
	exit 2
	;;
*=*)
	echo "refusing: the expected tag <$tag> contains =, which this check reads as a token separator" >&2
	echo "  (release-ldflags.sh refuses such a tag before the build)" >&2
	exit 2
	;;
esac

# A STALE EXEMPTION IS A DEFECT, and it is checked before anything is run. A name that no
# longer has a directory is either a tool that was renamed -- in which case the new name is
# silently required, which is right, but the old line still reads as a live debt against
# #121 -- or a list that was never pointed at this tree. Either way the list has stopped
# saying what it claims to say, and this file's whole argument is against a rule nobody can
# read off the page.
for exempt_name in $LEGACY_NO_VERSION_VERB; do
	if [ ! -d "$cmddir/$exempt_name" ]; then
		echo "FAIL: $exempt_name is exempted from the stamp assertion but $cmddir/$exempt_name does not exist"
		echo "  the exemption list is a debt against #121, not a place names are left behind"
		echo "  take the name off LEGACY_NO_VERSION_VERB, or point this check at the shipped tree"
		exit 1
	fi
done

notes=""
note() { notes="${notes}NOTE: $1"$'\n'; }

asserted=0
exempted=0
seen=0
for dir in "$cmddir"/*/; do
	name=$(basename "$dir")
	seen=$((seen + 1))
	# shellcheck disable=SC2059 -- the template IS the format string, checked above
	bin=$(printf "$template" "$name")
	if [ ! -x "$bin" ]; then
		echo "FAIL: $name is built by the release loop but there is no runnable binary at $bin"
		exit 1
	fi

	set +e
	out=$("$bin" version 2>&1)
	rc=$?
	set -e
	# One line, MATCHED WHOLE, SHOWN bounded. A refusal can be a whole usage message --
	# nova-self-talk's is several hundred characters -- and a check that pastes eleven of
	# those is a check nobody reads to the end, so what is PRINTED is cut at 200
	# characters and marked when it was cut. What is MATCHED below is the full output: a
	# tool whose version line carries the tag past character 200 would otherwise fail the
	# assertion for a defect in this file rather than for anything wrong with the binary,
	# and would do it in a message naming the tag the binary had just printed.
	out=$(printf '%s' "$out" | tr '\n' ' ')
	shown=$(printf '%s' "$out" | cut -c1-200)
	[ "${#shown}" -eq "${#out}" ] || shown="$shown..."

	# The exempt six, named and printed rather than skipped in silence -- silence over
	# nine tools is how the two-name loop survived. What they print today is shown so
	# that the day one of them starts answering the tag is visible on the release log.
	case " $LEGACY_NO_VERSION_VERB " in
	*" $name "*)
		if [ "$rc" -ne 0 ]; then
			note "$name has no version print today: \`$name version\` exited $rc: $shown"
		else
			note "$name prints an identity that is not the release stamp: $shown"
		fi
		note "  it is exempt by name until the common version verb lands: #121"
		exempted=$((exempted + 1))
		continue
		;;
	esac

	if [ "$rc" -ne 0 ]; then
		echo "FAIL: $name is a shipped binary and must report the tag, but \`$name version\` exited $rc"
		echo "  it printed: $shown"
		echo "  a stamp no verb can read is a stamp nobody can check; wire version into main.go's dispatch"
		echo "  (the only tools not held to the tag are the #121 legacy six, named in this script)"
		exit 1
	fi

	# The tag as a WHOLE token, so v0.1 does not pass for v0.11 and a tag appearing inside
	# a path or a go version does not count. `=` is a separator too: nova-sandbox prints
	# version=<tag> rather than a bare field.
	case " $(printf '%s' "$out" | tr '=' ' ') " in
	*" $tag "*) ;;
	*)
		echo "FAIL: $name does not report the tag it was built from"
		echo "  want the token: $tag"
		echo "  \`$name version\` printed: $shown"
		echo "  the linker ignores -X main.version in silence when the symbol is missing or renamed"
		exit 1
		;;
	esac

	# The print names itself. Two binaries answering with the same tag and no name is one
	# copy-paste away from a version verb that reports another tool's identity.
	case "$out" in
	*"$name"*) ;;
	*)
		echo "FAIL: $name version does not name the tool it is reporting for"
		echo "  \`$name version\` printed: $shown"
		exit 1
		;;
	esac

	echo "ok: $shown"
	asserted=$((asserted + 1))
done

if [ "$seen" -eq 0 ]; then
	echo "FAIL: $cmddir/ matched no tool directories; this check asserted nothing and would pass"
	exit 1
fi
# Every shipped tool exempt is a run that asserted nothing while printing eleven NOTEs.
# It cannot happen while one name is required, and it says so rather than exiting 0.
if [ "$asserted" -eq 0 ]; then
	echo "FAIL: every one of the $seen shipped tools is on the #121 exemption list, so this check"
	echo "  asserted no stamp at all; either the list has outgrown the tree or $cmddir is not"
	echo "  this repository's tree"
	exit 1
fi

printf '%s' "$notes"
echo "asserted the $tag stamp on $asserted of $seen shipped tools ($exempted exempt until #121)"
