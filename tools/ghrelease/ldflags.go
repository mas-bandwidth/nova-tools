package main

import (
	"fmt"
	"strings"
)

func init() {
	register(verb{
		name:    "ldflags",
		summary: "print the -ldflags value for a release build of a stamp, or refuse it",
		help: `usage: go run ./tools/ghrelease ldflags <stamp>

Prints the -ldflags value for a release build of the stamp, "-s -w -X
main.version=<stamp>", or refuses. Exit 0 printed; 1 the stamp is refused; 2
usage.

One place composes the flags, so the -X stamp cannot be dropped by an edit to a
long go build line nobody rereads. "-X main.version=" with nothing after it is
a legal linker flag: it writes the empty string into main.version, the binary
falls back to its vcs stamp or to devel, and every check downstream passes
because the build never complained. A release whose nova-wake answered devel
would refuse every nova-bus in the same release. So the refusal lives here.

A stamp is refused when it is empty, when it carries whitespace (the linker
flag would split and stamp the first word), when it carries % (the stamp
reaches a printf format further down the release, where % reads a directive
that is not there and fails the job with no message), and when it carries =
(the stamp assertion reads = as a token separator and pkg/oneline escapes
= when a binary prints it, so no stamped tool could report the tag as the token
it was built from).
`,
		do: doLdflags,
	})
}

func doLdflags(e env, args []string) int {
	if len(args) != 1 {
		fmt.Fprintf(e.stderr, "usage: %s ldflags <stamp>\n", tool)
		fmt.Fprintln(e.stderr, "  prints the -ldflags value for a release build of that stamp, or refuses")
		return 2
	}
	flags, refusal := composeLdflags(args[0])
	if refusal != nil {
		for _, l := range refusal {
			fmt.Fprintln(e.stderr, l)
		}
		return 1
	}
	fmt.Fprintln(e.stdout, flags)
	return 0
}

// composeLdflags returns the linker flags of a release build of stamp, or the
// lines that refuse the stamp. Every caller that builds a release goes through
// here, which is why the refusals are here.
func composeLdflags(stamp string) (string, []string) {
	if stamp == "" {
		// Refused rather than defaulted: a default would be a version string
		// nobody can trace.
		return "", []string{
			"refusing: the release stamp is empty, so -X main.version= would write an empty",
			"  version and every binary would report devel while the release page says a tag",
		}
	}
	if hasBlank(stamp) {
		return "", []string{fmt.Sprintf("refusing: the release stamp <%s> carries whitespace; it would split the linker flag", stamp)}
	}
	if strings.Contains(stamp, "%") {
		return "", []string{
			fmt.Sprintf("refusing: the release stamp <%s> contains %%; it is substituted into a printf", stamp),
			"  format further down the release (the per-tool binary path), where % reads a",
			"  directive that is not there and fails the job with no message at all",
		}
	}
	if strings.Contains(stamp, "=") {
		return "", []string{
			fmt.Sprintf("refusing: the release stamp <%s> contains =, which the stamp assertion reads as", stamp),
			"  a token separator and pkg/oneline escapes when a binary prints it; no",
			"  stamped tool could report this tag as the token it was built from",
		}
	}
	flags := "-s -w -X main.version=" + stamp
	// The composed string is checked, not just its input: this is what the
	// build passes.
	if !strings.Contains(flags, "-X main.version=") || strings.HasSuffix(flags, "-X main.version=") {
		return "", []string{"refusing: composed ldflags carry no non-empty -X main.version: " + flags}
	}
	return flags, nil
}
