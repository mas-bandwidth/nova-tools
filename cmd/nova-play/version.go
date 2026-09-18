// nova-play version: which build is running.
package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
)

var version string

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "nova-play version: takes no flags and no arguments, got %d\n", len(args))
		return 2
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-play", version))
	return 0
}
