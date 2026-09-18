// nova-ci version: which build made a CI verdict.
//
// A CI-SLOW line is only worth reading if the binary that printed it is the one that was
// built from the commit under review, and the release assertion holds every shipped tool
// to reporting its tag. The identity is read from the build itself -- a release's
// -ldflags "-X main.version=<tag>", then the installed module version, then the vcs
// stamp, then the honest floor `devel` -- by internal/buildinfo, so all shipped tools
// answer in the same one line: `<tool> <version> <goos>/<goarch> <go version>`.
package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

// version is empty in ordinary builds and is filled only by a release stamp.
var version string

// cmdVersion prints the one line. It takes no flags and no arguments: a second output
// shape is a second thing to agree about, and this verb exists to end an argument rather
// than to start one.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "nova-ci version: takes no flags and no arguments, got %d\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-ci", version))
	return 0
}
