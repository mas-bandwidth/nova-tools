// nova-check version: which build is running.
//
// A refusal, a green or a line somebody pastes into a note is evidence about a BUILD, and
// until this verb existed this binary could not say which one it was: `nova-check version`
// was exit 2, unknown subcommand. So "we are all running the same nova-check" was a belief
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
	verbflag.HelpIfAsked(args, "version")
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return refuse(stderr, " version", "takes no flags and no arguments except --json: "+err.Error())
	}
	args = fs.Args()
	if len(args) > 0 {
		if asJSON {
			return refuse(stderr, " version", "takes no arguments; use --json alone")
		}
		fmt.Fprintf(stderr, "nova-check version: takes no flags and no arguments, got %d; run: nova-check help\n", len(args))
		return 2
	}
	if asJSON {
		return renderVersion(stdout, ver)
	}
	fmt.Fprintln(stdout, buildinfo.Line("nova-check", ver))
	return 0
}
