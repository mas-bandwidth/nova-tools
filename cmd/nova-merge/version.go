// nova-merge version: which build is running, and which bytes are on disk.
//
// This binary already answered a question no other one here answers -- `buildID`, twelve
// hex of the sha256 of its own file, which rule 16 needs because a run has to notice that
// the binary underneath it changed mid-lane. It printed that alone: `nova-merge
// ed95537c43b4`, two tokens, an identity nothing else in the set shares a spelling with.
// A person holding that line and a `nova-bus version` line could not compare them, and a
// release assertion reading field two off every binary read a file hash here and a tag
// everywhere else.
//
// So the line now leads with the SAME four tokens every other binary prints -- the build
// identity from internal/buildinfo in field two -- and keeps the file hash as a fifth,
// named `build=`, because it is this binary's own answer to a different question and
// dropping it would cost the loop the thing rule 16 needs.
package main

import (
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// version is empty in every ordinary build and is the one override: a release stamps it
// with -ldflags "-X main.version=<tag>". It is a var rather than a const because -X can
// only write a string var, and it is package-level and unexported for the same reason.
var version string

// cmdVersion prints the one line. The build id comes through Deps like every other thing
// this binary reaches outside itself, so a test drives it without a binary on disk.
func cmdVersion(args []string, stdout, stderr io.Writer, deps Deps) int {
	if len(args) > 0 {
		fmt.Fprintf(stderr, "nova-merge version: takes no flags and no arguments, got %d\n", len(args))
		return 2
	}
	fmt.Fprintf(stdout, "%s build=%s\n", buildinfo.Line("nova-merge", version), oneline.Field(deps.BuildID()))
	return 0
}
