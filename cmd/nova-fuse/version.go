// nova-fuse version: which build is running. A refusal, a green or a pasted
// line is evidence about a build, so the binary says which one it is. The
// version is read from the build itself by pkg/buildinfo, which holds the
// resolution order and the shape of the line for every tool in this repository,
// so each answers in one spelling; it is never a constant maintained by hand,
// which would be wrong at the first commit after a release.
package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/buildinfo"
)

// version is empty in an ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>" (a var, because -X writes only a string var).
var version string

// cmdVersionWith prints the one line for stamp. A test passes its own stamp
// instead of writing the package var (docs/STANDARD.md section 8).
func cmdVersionWith(args []string, stdout, stderr io.Writer, stamp string) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "nova-fuse version REFUSED: takes no flags and no arguments, got %d; run: nova-fuse help version\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-fuse", stamp))
	return 0
}
