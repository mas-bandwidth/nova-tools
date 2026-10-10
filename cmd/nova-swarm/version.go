// nova-swarm version: which build is running.
//
// A refusal, a green or a line somebody pastes into a note is evidence about a BUILD, and
// until this verb existed this binary could not say which one it was: `nova-swarm version`
// was exit 2, unknown subcommand. So "we are all running the same nova-swarm" was a belief
// rather than a reading, and the release could not assert over the set what no member of
// the set would answer.
//
// The version is NOT a constant maintained by hand -- a hand-maintained constant is wrong
// exactly at the commit after the release, where it still names the release. It is read
// from the build itself by pkg/buildinfo, which holds the resolution order and the
// shape of the line for every binary here, so that eleven tools answer this question in
// one spelling rather than eleven.
package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
	"github.com/mas-bandwidth/nova-tools/pkg/nsprint/verbflag"
)

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var, and it is package-level and unexported for the same reason.
var version string

// cmdVersion prints the one line. It takes no flags and no arguments: there is no
// --short, no --json and no --long, because a second output shape is a second thing to
// agree about and this verb exists to end an argument rather than to start one.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	verbflag.HelpIfAsked(args, "version")
	if len(args) > 0 {
		return refuse(stderr, " version", fmt.Sprintf("takes no flags and no arguments, got %d; run: nova-swarm version -h", len(args)))
	}
	return cmdVersionStamp(version, stdout, stderr)
}

// cmdVersionStamp prints the one line for a named build identity. The package var
// `version` reaches it only as the production default at cmdVersion; a test names its
// own stamp here instead of swapping the var under every other parallel test.
func cmdVersionStamp(stamp string, stdout, stderr io.Writer) int {
	fmt.Fprintln(stdout, buildinfo.Line("nova-swarm", stamp))
	return 0
}
