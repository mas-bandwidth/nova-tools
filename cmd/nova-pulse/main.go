package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

var version string

// The verbs block, from SPEC-PULSE's "The verbs". This card implements only `cut`; the
// rest are other cards and are refused by name until they land.
const verbsBlock = `nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]
nova-pulse width   --root <dir> --pool <pool.tsv>
nova-pulse version
nova-pulse help`

// refuse is what an unusable invocation costs: one line naming what was wrong and the door
// to the usage, never the banner.
func refuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-pulse: %s; run: nova-pulse help\n", oneline.Escape(what))
	return 2
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "a verb is required (run: nova-pulse help)")
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "help", "-h", "--help":
		fmt.Fprintln(stdout, verbsBlock)
		return 0
	case "version", "--version":
		if len(rest) != 0 {
			return refuse(stderr, "version takes no arguments (run nova-pulse version)")
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-pulse", version))
		return 0
	case "cut":
		return cmdCut(rest, stdout, stderr)
	case "pool", "launch", "harvest", "width":
		return refuse(stderr, fmt.Sprintf("verb %q is not built yet (this card implements only cut)", verb))
	}
	return refuse(stderr, fmt.Sprintf("unknown verb %q (run: nova-pulse help)", verb))
}

// flags is one verb's flag set with package flag's mouth closed on its usage dump.
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
		refuse(stderr, f.verb+": "+oneline.Cap(err.Error(), oneline.TailBytes))
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		fmt.Fprintf(stderr, "nova-pulse %s: takes no positional arguments, got %d\n", f.verb, n)
		return false
	}
	return true
}

func (f *flags) want(value, name, wants string) {
	if value == "" {
		f.problems = append(f.problems, fmt.Sprintf("--%s is required; it wants %s; refusing to guess", name, wants))
	}
}

func (f *flags) refused(stderr io.Writer) bool {
	for _, p := range f.problems {
		fmt.Fprintf(stderr, "nova-pulse %s: %s\n", f.verb, oneline.Escape(p))
	}
	return len(f.problems) > 0
}

func cmdCut(args []string, stdout, stderr io.Writer) int {
	f := newFlags("cut")
	pool := f.fs.String("pool", "", "")
	templates := f.fs.String("templates", "", "")
	out := f.fs.String("out", "", "")
	root := f.fs.String("root", "", "")
	local := f.fs.String("local", "", "")
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the pool.tsv of candidates to cut")
	f.want(*templates, "templates", "the directory holding the typed templates and models.tsv")
	f.want(*out, "out", "the directory the cut cards go into")
	f.want(*root, "root", "the state root; skipped.tsv is written here")
	if *max < 0 {
		f.problems = append(f.problems, fmt.Sprintf("--max is 0 or more, got %d; 0 already means all, so a negative ceiling is a typo with two readings", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Cut(pulse.CutInput{
		Pool:      *pool,
		Templates: *templates,
		Out:       *out,
		Root:      *root,
		Local:     *local,
		Max:       *max,
		Stdout:    stdout,
		Stderr:    stderr,
	})
}
