// path.go holds the path verb: its flags, its run and the helpers only it uses.

package main

import (
	"fmt"
	"io"
)

// cmdPath echoes the box path this invocation would use. With no default paths anywhere,
// this verb exists to verify plumbing: what one caller passes is what another sees.
func cmdPath(rest []string, stdout, stderr io.Writer, inv invocation) int {
	box, positional, ok, parsed := parseBox("path", rest, stderr, inv.getenv)
	if !parsed {
		return 2
	}
	if len(positional) > 0 {
		refuse(stderr, " path", fmt.Sprintf("unexpected argument %q", positional[0]))
		ok = false
	}
	if !ok {
		return 2
	}
	fmt.Fprintln(stdout, box)
	return 0
}
