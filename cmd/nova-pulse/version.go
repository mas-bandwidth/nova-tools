// nova-pulse version: which build is running.
//
// A pulse is several cards cut from one template and folded back together, and the first
// question after it misbehaves is which build cut the cards. Until this verb existed the
// honest answer was that nobody could say: `nova-pulse version` printed a hand-written
// "dev" that was wrong exactly at the commit after the release. So "we are all running
// the same nova-pulse" was a belief rather than a reading.
//
// The version is NOT a constant maintained by hand -- a hand-maintained constant is wrong
// exactly at the commit after the release, where it still names the release. It is read
// from the build itself by internal/buildinfo, which holds the resolution order and the
// shape of the line for every binary here, so that every tool answers this question in
// one spelling rather than one per tool.
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
		fmt.Fprintf(stderr, "nova-pulse version: takes no flags and no arguments, got %d\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-pulse", version))
	return 0
}
