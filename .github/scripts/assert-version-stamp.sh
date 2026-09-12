#!/usr/bin/env bash
# Every shipped binary that carries the release stamp reports the tag it was built from.
#
# Before #118 this was two names -- nova-bus and nova-board -- hand-written into
# release.yml beside a build loop that ships `cmd/*/`. A stamp read by two binaries says
# nothing about the other nine, and since #107 it is nova-wake's stamp that decides which
# nova-bus it will talk to: an unstamped nova-wake answers `devel`, and `devel` matches no
# released bus, so the whole release refuses itself while the release page looks finished.
#
# THE TOOL LIST IS DISCOVERED FROM THE TREE, never written here. The build loop ships
# every `cmd/*/`, so this walks the same directories; a tool added tomorrow is asserted
# without anyone remembering this file. What is discovered per tool is whether it declares
# the stamp -- a package-level string `version` in package main is the symbol
# `-X main.version` writes and is the tool's own statement that it intends to report the
# release it came from. Every spelling the LINKER accepts counts as that statement; see
# declares_stamp below for why a narrower test is the same silent demotion this file
# exists to refuse.
#
# THREE CLASSES, and the difference between them is the point:
#
#   declares the stamp          MUST print the tag. A mismatch fails the job by name.
#   no stamp, has a print       NOTE. nova-merge prints a hash of its own bytes: a real
#                               build identity that is not the release stamp.
#   no stamp, no print          NOTE. nova-check, nova-fuse, nova-memory, nova-self-talk
#                               and nova-swarm refuse `version` today.
#
# The NOTEs are printed and named, never skipped in silence -- silence is how the first
# two-name loop survived nine tools. Making the print common across the set is the
# packaging question in #121 and is deliberately NOT decided here: this file asserts what
# the tools claim today and says out loud what is missing. Stella's addition to #121 is
# the rule the first class enforces: a development build must not silently impersonate a
# release.
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

# declares_stamp <file> -- does this file declare the package-level string variable that
# `-X main.version=` writes?
#
# WHAT THE LINKER ACCEPTS is the whole of the rule, and cmd/link states it: -X
# importpath.name=value "is only effective if the variable is declared in the source code
# either uninitialized or initialized to a constant string expression". So all of these
# are stamped, and an earlier revision of this file recognised only the first:
#
#   var version string              var version = "devel"        var (
#   var version string = "devel"    var version = `devel`                version string
#                                                                )
#
# The one that made this worth widening: `var version = ""` is stamped by the linker
# exactly as `var version string` is, and a single exact-line grep demoted that tool to a
# NOTE -- a shipped binary quietly dropped out of the asserted set, which is this file's
# own argument against silent demotion turned on itself. A `version` initialised to
# something that is NOT a constant string -- `var version = buildID()` -- the linker
# genuinely cannot write, so it is genuinely not a stamp and is not recognised here.
#
# TOP-LEVEL declarations only. gofmt indents every declaration inside a function, so a
# line beginning `var` at column zero, and the body of a top-level `var (` block, are
# exactly the package-level ones.
declares_stamp() {
	awk '
		function names_version(names,   n, p, i, s) {
			n = split(names, p, ",")
			for (i = 1; i <= n; i++) {
				s = p[i]
				gsub(/^[ \t]+|[ \t]+$/, "", s)
				if (s == "version") return 1
			}
			return 0
		}
		{ line = $0; sub(/\/\/.*$/, "", line) }
		!inblock && line ~ /^var[ \t]*\([ \t]*$/ { inblock = 1; next }
		inblock && line ~ /^[ \t]*\)/ { inblock = 0; next }
		{
			if (line ~ /^var[ \t]+/) { decl = line; sub(/^var[ \t]+/, "", decl) }
			else if (inblock && line ~ /^[ \t]+[A-Za-z_]/) { decl = line; sub(/^[ \t]+/, "", decl) }
			else next

			eq = index(decl, "=")
			if (eq > 0) { names = substr(decl, 1, eq - 1); init = substr(decl, eq + 1) }
			else { names = decl; init = "" }
			gsub(/^[ \t]+|[ \t]+$/, "", names)
			gsub(/^[ \t]+|[ \t]+$/, "", init)

			# the type, when written, is the last word of the name list
			typed = 0
			if (names ~ /[ \t]string$/) { typed = 1; sub(/[ \t]+string$/, "", names) }
			if (!names_version(names)) next

			# uninitialised must say `string`; initialised must be a string literal
			if (init == "") { if (typed) found = 1 }
			else if (init ~ /^"/ || init ~ /^`/) found = 1
		}
		END { exit found ? 0 : 1 }
	' "$1"
}

notes=""
note() { notes="${notes}NOTE: $1"$'\n'; }

asserted=0
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

	# Top-level files only, and not the tests: cmd/nova-wake/testdata holds a second
	# package main that is a fixture, and a fixture's stamp is not shipped.
	stamped=no
	for f in "$dir"*.go; do
		case "$f" in
		*_test.go) continue ;;
		esac
		[ -f "$f" ] || continue
		if declares_stamp "$f"; then
			stamped=yes
			break
		fi
	done

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

	if [ "$stamped" = no ]; then
		if [ "$rc" -ne 0 ]; then
			note "$name has no version print today: \`$name version\` exited $rc: $shown"
		else
			note "$name prints an identity that is not the release stamp: $shown"
		fi
		note "  the common version verb across the set is #121; this job does not decide it"
		continue
	fi

	if [ "$rc" -ne 0 ]; then
		echo "FAIL: $name declares the -X main.version stamp but \`$name version\` exited $rc"
		echo "  it printed: $shown"
		echo "  a stamp no verb can read is a stamp nobody can check; wire version into main.go's dispatch"
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
if [ "$asserted" -eq 0 ]; then
	echo "FAIL: not one shipped binary declares the release stamp; either the stamp was removed"
	echo "  from every tool or $cmddir is not this repository's tree"
	exit 1
fi

printf '%s' "$notes"
echo "asserted the $tag stamp on $asserted of $seen shipped tools"
