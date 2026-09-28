package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The verb flag helper the Postgres retirement (#2623, 83898a6) deleted with its last
// callers; restored here unchanged for the two verbs that came in through
// stream/nova-work-w2 and still use it: dogfood (#2762) and visualize (#2883).

// flags is one verb's flag set with package flag's two mouths closed: its error text quotes
// the argument it could not parse and its usage dump is discarded, so an argument beginning
// with a dash cannot author a line of stderr before any code here runs.
type flags struct {
	verb     string
	fs       *flag.FlagSet
	problems []string
}

func newFlags(verb string) *flags {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return &flags{verb: verb, fs: fs}
}

func (f *flags) parse(args []string, stderr io.Writer) bool {
	if err := f.fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			printVerbHelp(stderr, f.verb)
			return false
		}
		refuse(stderr, " "+f.verb, oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		fmt.Fprintf(stderr, "nova-work %s: takes no positional arguments, got %d (flags come before arguments)\n", oneline.Escape(f.verb), n)
		return false
	}
	return true
}

// want records a missing required flag with what it WANTS, never only what was wrong.
func (f *flags) want(value, name, wants string) {
	if value == "" {
		f.problems = append(f.problems, fmt.Sprintf("--%s is required; it wants %s; refusing to guess", oneline.Escape(name), oneline.Escape(wants)))
	}
}

func (f *flags) add(problem string) { f.problems = append(f.problems, problem) }

// refused prints every problem this run found, one line each, and reports whether there
// were any.
func (f *flags) refused(stderr io.Writer) bool {
	for _, p := range f.problems {
		fmt.Fprintf(stderr, "nova-work %s: %s\n", oneline.Escape(f.verb), oneline.Escape(p))
	}
	return len(f.problems) > 0
}
