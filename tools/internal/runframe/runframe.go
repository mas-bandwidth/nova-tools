// Package runframe is the argument parse, environment read and dispatch frame
// the tools/ci and tools/ghrelease commands share. A tool keeps its own env,
// its verbs and its banner; the frame holds the one run loop, so a missing
// verb, a help request, an unknown verb and a verb's own exit code answer the
// same way in both (the rule: a duplicated function becomes one lower-level
// module the others depend on; a copy is a bug).
package runframe

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
)

// Verb is one command a frame dispatches: the help text `help <name>` prints
// and the function that runs it. E is the tool's own env, so a verb reads the
// fields its work needs.
type Verb[E any] interface {
	// Help is the text `help <name>` prints on stdout.
	Help() string
	// Do runs the verb and returns the process's exit code: 0 done, 1 the
	// thing checked is not so, 2 refused as used (bad usage, or the verb
	// could not run).
	Do(e E, args []string) int
}

// Env is what the frame reads from a tool's env: its two streams. A tool's env
// satisfies it with Out and Err.
type Env interface {
	Out() io.Writer
	Err() io.Writer
}

// Run parses args, reads the streams from e and dispatches. No argument prints
// the banner on stderr and exits 2; "help", "-h" and "--help" print the banner
// on stdout, or for "help <verb>" that verb's help, and exit 0; an unknown verb
// is refused as usage on stderr at exit 2; otherwise the verb runs with the
// arguments after its name and its exit code is returned unchanged. tool names
// the command in every message, cmd is the line a refusal tells the reader to
// run, banner renders the help header, and names are the verb names sorted.
func Run[E Env, V Verb[E]](e E, args []string, tool, cmd string, banner func() string, names []string, registry map[string]V) int {
	if len(args) == 0 {
		fmt.Fprint(e.Err(), banner())
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 && args[0] == "help" {
			v, ok := registry[args[1]]
			if !ok {
				fmt.Fprintf(e.Err(), "%s help: unknown verb %s; run: %s help\n", tool, oneline.Quote(args[1]), cmd)
				return 2
			}
			fmt.Fprint(e.Out(), v.Help())
			return 0
		}
		fmt.Fprint(e.Out(), banner())
		return 0
	}
	v, ok := registry[args[0]]
	if !ok {
		fmt.Fprintf(e.Err(), "%s: unknown verb %s; the verbs are %s; run: %s help\n", tool, oneline.Quote(args[0]), strings.Join(names, ", "), cmd)
		return 2
	}
	return v.Do(e, args[1:])
}
