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
# something that is NOT a constant string -- `var version = buildID()`, `var version =
# "x" + suffix` -- the linker genuinely cannot write, so it is genuinely not a stamp and
# is not recognised here. THE TWO ERRORS ARE NOT SYMMETRIC but both are real: reading a
# stamp as absent drops a shipped binary out of the asserted set in silence, and reading
# an absent stamp as present fails a release for a tool that never claimed the tag.
#
# TOP-LEVEL declarations only. gofmt indents every declaration inside a function, so a
# line beginning `var` at column zero, and the body of a top-level `var (` block, are
# exactly the package-level ones. A `var (` block ENDS ON A `)` AT COLUMN ZERO: gofmt
# indents the closing paren of every multi-line call inside the block, so closing the
# block on any indented `)` ended it early at the first
#
#   var (
#           greeting = strings.Join(
#                   []string{"a"},
#           )                          <- not the end of the block
#           version string             <- and this was read as a function body, demoted
#   )
#
# COMMENTS ARE STRIPPED, AND ONLY OUTSIDE STRING LITERALS. `//` alone was stripped, so a
# declaration sitting inside a top-level `/* ... */` -- commented out, which is exactly
# the way a stamp gets removed -- counted as a declaration and held the tool to a tag it
# no longer carries. The stripper walks the line rather than substituting, because a `//`
# or a `/*` inside a string literal (`var version = "//devel"`) is text, not a comment,
# and cutting there would leave an unbalanced literal behind.
declares_stamp() {
	awk '
		# Comments removed outside string literals; `incomment` carries a /* across lines.
		function strip_comments(s,   i, c, n, out, q) {
			out = ""; i = 1; n = length(s)
			while (i <= n) {
				c = substr(s, i, 1)
				if (incomment) {
					if (c == "*" && substr(s, i + 1, 1) == "/") { incomment = 0; i += 2 }
					else i++
					continue
				}
				if (c == "\"" || c == "`" || c == "'\''") {
					q = c; out = out c; i++
					while (i <= n) {
						c = substr(s, i, 1); out = out c; i++
						if (c == "\\" && q != "`") {
							if (i <= n) { out = out substr(s, i, 1); i++ }
							continue
						}
						if (c == q) break
					}
					continue
				}
				if (c == "/" && substr(s, i + 1, 1) == "/") break
				if (c == "/" && substr(s, i + 1, 1) == "*") { incomment = 1; i += 2; out = out " "; continue }
				out = out c; i++
			}
			return out
		}
		# Split on the commas that separate the declaration'\''s elements: depth zero, and
		# not inside a literal, so `f(a, b)` and "a,b" stay one element.
		function split_top(s, arr,   i, c, n, depth, cur, k, q) {
			n = length(s); depth = 0; cur = ""; k = 0
			for (i = 1; i <= n; i++) {
				c = substr(s, i, 1)
				if (c == "\"" || c == "`" || c == "'\''") {
					q = c; cur = cur c; i++
					while (i <= n) {
						c = substr(s, i, 1); cur = cur c
						if (c == "\\" && q != "`") { i++; if (i <= n) cur = cur substr(s, i, 1); i++; continue }
						i++
						if (c == q) break
					}
					i--
					continue
				}
				if (c == "(" || c == "[" || c == "{") depth++
				else if (c == ")" || c == "]" || c == "}") depth--
				else if (c == "," && depth == 0) { k++; arr[k] = cur; cur = ""; continue }
				cur = cur c
			}
			k++; arr[k] = cur
			return k
		}
		# A lone constant string literal and nothing else: `"devel"`, `` `devel` ``, `""`.
		# `"x" + suffix` is not one, and the linker will not write it.
		function is_lone_string(s,   n, i, c, q) {
			gsub(/^[ \t]+|[ \t]+$/, "", s)
			n = length(s)
			if (n < 2) return 0
			q = substr(s, 1, 1)
			if (q == "`") { return index(substr(s, 2), "`") == n - 1 }
			if (q != "\"") return 0
			for (i = 2; i <= n; i++) {
				c = substr(s, i, 1)
				if (c == "\\") { i++; continue }
				if (c == "\"") return i == n
			}
			return 0
		}
		function version_pos(names, parts,   n, i, s) {
			n = split_top(names, parts)
			for (i = 1; i <= n; i++) {
				s = parts[i]
				gsub(/^[ \t]+|[ \t]+$/, "", s)
				if (s == "version") return i
			}
			return 0
		}
		{ line = strip_comments($0) }
		!inblock && line ~ /^var[ \t]*\([ \t]*$/ { inblock = 1; next }
		# Column zero only: an indented `)` closes a call inside the block, not the block.
		inblock && line ~ /^\)/ { inblock = 0; next }
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
			pos = version_pos(names, nameparts)
			if (!pos) next

			# uninitialised must say `string`; initialised must be a lone string literal
			# IN VERSION'\''S OWN POSITION: `var x, version = "a", 1` stamps nothing.
			if (init == "") { if (typed) found = 1 }
			else {
				ninit = split_top(init, initparts)
				if (ninit == split_top(names, nameparts) && is_lone_string(initparts[pos])) found = 1
			}
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
