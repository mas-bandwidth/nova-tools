package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

const usage = `nova-pulse — one tool, five verbs, no model call

nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]
nova-pulse width   --root <dir> --pool <pool.tsv>
nova-pulse version
nova-pulse help

example:
  nova-pulse pool --sources cmd/nova-pulse/testdata/sources.tsv --root ./root

cmd/nova-pulse/testdata/sources.tsv there is a one-line source: a roadmap file
with two cells that name a card and one that names none, declared to the pool.
The line above runs "nova-pulse pool" from the repo root — it reads the roadmap,
skips the cell without a card, and writes the two candidates to ./root/pool.tsv.
That is a whole first run of the pool verb, no network and no model call, and
docs/TESTS.md carries the transcript it prints.
`

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC())) }

func run(args []string, stdout, stderr io.Writer, now time.Time) int {
	if len(args) == 0 {
		fmt.Fprintf(stderr, "nova-pulse: no verb given; run: nova-pulse help\n")
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintln(stdout, "nova-pulse dev")
		return 0
	case "pool":
		return cmdPool(rest, stdout, stderr)
	case "cut", "launch", "harvest", "width":
		fmt.Fprintf(stderr, "nova-pulse %s: not implemented in this card\n", cmd)
		return 2
	}
	fmt.Fprintf(stderr, "nova-pulse: unknown subcommand %q\n", cmd)
	return 2
}

// flags is one verb's flag set with its usage dump discarded.
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
		fmt.Fprintf(stderr, "nova-pulse %s: %s\n", f.verb, err)
		return false
	}
	if n := f.fs.NArg(); n > 0 {
		fmt.Fprintf(stderr, "nova-pulse %s: takes no positional arguments, got %d (flags come before arguments)\n", f.verb, n)
		return false
	}
	return true
}

func (f *flags) want(value, name, wants string) {
	if strings.TrimSpace(value) == "" {
		f.problems = append(f.problems, fmt.Sprintf("--%s is required; it wants %s; refusing to guess", name, wants))
	}
}

func (f *flags) refused(stderr io.Writer) bool {
	for _, p := range f.problems {
		fmt.Fprintf(stderr, "nova-pulse %s: %s\n", f.verb, p)
	}
	return len(f.problems) > 0
}

func cmdPool(args []string, stdout, stderr io.Writer) int {
	f := newFlags("pool")
	sources := f.fs.String("sources", "", "")
	root := f.fs.String("root", "", "")
	out := f.fs.String("out", "", "")
	timeout := f.fs.Int("timeout", 120, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*sources, "sources", "the declared sources file: kind, locator, template, one per line")
	f.want(*root, "root", "the directory holding seen.tsv and the pool state")
	if *timeout < 1 {
		f.problems = append(f.problems, fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Pool(pulse.PoolInput{
		Sources: *sources,
		Root:    *root,
		Out:     *out,
		Timeout: time.Duration(*timeout) * time.Second,
		Stdout:  stdout,
		Stderr:  stderr,
	})
}
