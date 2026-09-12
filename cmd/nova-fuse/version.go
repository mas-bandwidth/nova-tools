// nova-fuse version: which build is running.
//
// A refusal, a green or a line somebody pastes into a note is evidence about a BUILD, and
// until this verb existed this binary could not say which one it was: `nova-fuse version`
// was exit 2, unknown subcommand. So "we are all running the same nova-fuse" was a belief
// rather than a reading, and the release could not assert over the set what no member of
// the set would answer.
//
// The version is NOT a constant maintained by hand -- a hand-maintained constant is wrong
// exactly at the commit after the release, where it still names the release. It is read
// from the build itself by internal/buildinfo, which holds the resolution order and the
// shape of the line for every binary here, so that eleven tools answer this question in
// one spelling rather than eleven.
package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var, and it is package-level and unexported for the same reason.
var version string

// cmdVersion prints the one line. It takes no flags and no arguments: there is no
// --short, no --json and no --long, because a second output shape is a second thing to
// agree about and this verb exists to end an argument rather than to start one.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "nova-fuse version: takes no flags and no arguments, got %d\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-fuse", version))
	return 0
}
