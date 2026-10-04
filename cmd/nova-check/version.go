// nova-check version: which build is running. A refusal, a green or a pasted line
// is evidence about a build, so the binary says which one it is. The version is read
// from the build itself by internal/buildinfo, which holds the resolution order and
// the shape of the line for every tool in this repository, so each answers in one
// spelling; it is never a constant maintained by hand, which would be wrong at the
// first commit after a release.
package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var, and it is package-level and unexported for the same reason.
var version string

// cmdVersion prints the build identity, optionally in the shared JSON envelope.
// STANDARD section 2 keeps the payload identical in both renderings.
func cmdVersion(args []string, stdout, stderr io.Writer) int {
	return cmdVersionWith(args, stdout, stderr, version)
}

func cmdVersionWith(args []string, stdout, stderr io.Writer, ver string) int {
	var asJSON bool
	stdout, stderr = jsonWriters(stdout, stderr, &asJSON)
	defer stderr.(*jsonOutput).finish()
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.BoolVar(&asJSON, "json", false, "print this build identity in a JSON envelope")
	fs.SetOutput(io.Discard)
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, " version", "takes no flags and no arguments except --json: "+verbflag.Explain(fs, err))
	}
	args = fs.Args()
	if len(args) > 0 {
		if asJSON {
			return refuse(stderr, " version", "takes no arguments; use --json alone")
		}
		fmt.Fprintf(stderr, "nova-check version REFUSED: takes no flags and no arguments, got %d; run: nova-check help\n", len(args))
		return 2
	}
	if asJSON {
		return renderVersion(stdout, ver)
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-check", ver))
	return 0
}
