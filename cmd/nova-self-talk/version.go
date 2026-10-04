// nova-self-talk version: which build is running.
//
// A refusal, a green or a line pasted into a note is evidence about a BUILD, so the binary
// says which one it is. The version is not a constant kept by hand, which is wrong at the
// commit after a release: it is read from the build by internal/buildinfo, which holds the
// resolution order and the shape of the line for every nova tool, so that each answers the
// question in one spelling.
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
		fmt.Fprintf(stderr, "nova-self-talk version REFUSED: takes no flags and no arguments, got %d; run: nova-self-talk version -h\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-self-talk", version))
	return 0
}
