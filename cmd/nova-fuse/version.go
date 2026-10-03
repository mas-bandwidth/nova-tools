// nova-fuse version: which build is running. A refusal, a green or a pasted
// line is evidence about a build, so the binary says which one it is. The
// version is read from the build itself by internal/buildinfo, which holds the
// resolution order and the shape of the line for every tool in this repository,
// so each answers in one spelling; it is never a constant maintained by hand,
// which would be wrong at the first commit after a release.
package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// version is empty in an ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>" (a var, because -X writes only a string var).
var version string

// cmdVersion prints the one line. It takes no flags and no arguments: one output shape
// is one thing to agree about.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "nova-fuse version REFUSED: takes no flags and no arguments, got %d; run: nova-fuse help version\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-fuse", version))
	return 0
}
