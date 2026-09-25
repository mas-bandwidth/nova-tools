// Package verbflag is the one FlagSet constructor for the nova-sprint verbs
// (#3254). A parse error stays quiet and comes back from Parse, so the verb
// prints its own one-line refusal; -h, -help or --help on a flag set that does
// not define them instead unwinds to the dispatcher, which prints the verb's
// usage line and every flag the set defines on stdout and exits 2.
package verbflag

import (
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
)

// Help is the panic value -h raises from Parse. Only the dispatcher's
// Recover catches it; nothing ran before it but flag parsing.
type Help struct{ FS *flag.FlagSet }

// sink swallows the flag package's own output and remembers whether a parse
// error wrote its message: failf writes the message before calling Usage, the
// help path calls Usage with nothing written.
type sink struct{ wrote bool }

func (s *sink) Write(p []byte) (int, error) {
	s.wrote = true
	return len(p), nil
}

// New returns a ContinueOnError flag set named for the verb and subverb
// ("task push"): quiet on a parse error, Help on -h.
func New(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	s := &sink{}
	fs.SetOutput(s)
	fs.Usage = func() {
		if s.wrote {
			s.wrote = false
			return
		}
		panic(Help{FS: fs})
	}
	return fs
}

// Recover is deferred by the dispatcher. A Help panic becomes the usage text
// on out and *code 2; any other panic goes on unwinding.
func Recover(out io.Writer, prog string, code *int) {
	r := recover()
	if r == nil {
		return
	}
	h, ok := r.(Help)
	if !ok {
		panic(r)
	}
	Print(out, prog, h.FS)
	*code = 2
}

// Print writes `usage: <prog> <name> [flags]`, one line per flag the set
// defines (sorted, with its value type; no defaults, since a default can come
// from the environment and a help line must never print a secret), and the
// exit codes.
func Print(out io.Writer, prog string, fs *flag.FlagSet) {
	var b strings.Builder
	fmt.Fprintf(&b, "usage: %s %s [flags]\n", prog, fs.Name())
	var names []string
	fs.VisitAll(func(f *flag.Flag) { names = append(names, f.Name) })
	sort.Strings(names)
	if len(names) > 0 {
		b.WriteString("flags:\n")
	}
	for _, n := range names {
		f := fs.Lookup(n)
		kind, text := flag.UnquoteUsage(f)
		line := "  --" + n
		if kind != "" {
			line += " <" + kind + ">"
		}
		if text != "" {
			line += "  " + text
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("exit codes: 0 done, 1 refused, 2 usage\n")
	_, _ = io.WriteString(out, b.String())
}

// HelpIfAsked is for the few verbs that read their flags by hand: when args
// hold -h, -help or --help (before any --), it raises Help with a flag set of
// the named string flags, so the dispatcher prints the same usage text.
func HelpIfAsked(args []string, name string, flags ...string) {
	for _, a := range args {
		if a == "--" {
			return
		}
		if a == "-h" || a == "-help" || a == "--help" || a == "--h" {
			fs := flag.NewFlagSet(name, flag.ContinueOnError)
			for _, f := range flags {
				fs.String(f, "", "")
			}
			panic(Help{FS: fs})
		}
	}
}
