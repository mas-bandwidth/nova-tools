// nova-pulse enumerates bounded open work, cuts cards from templates, admits them through
// nova-swarm batch, and folds what comes back. It makes no model call: every token is a
// card's, and every cycle's reading is one PULSE line.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

const usage = `nova-pulse — one tool, five verbs, no model call

nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]  (not yet implemented)
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]  (not yet implemented)
nova-pulse width   --root <dir> --pool <pool.tsv>  (not yet implemented)
nova-pulse version
nova-pulse help

launch reads a cards.tsv of label<TAB>slot<TAB>model<TAB>card, counts the free
slots in <root>/pool, and hands the cards that fit one model at a time to nova-swarm
batch, queueing the rest only when --queue is set. --slots is the ceiling on the
free slots it may use, and --deadline is the whole pulse's one deadline in whole
seconds. It makes no model call itself: nova-swarm must be on your PATH.

example:
  nova-pulse launch --cards ./cards.tsv --root . --slots 2 --deadline 120 --queue
  nova-pulse launch --cards ./cards.tsv --root . --slots 3 --deadline 120

./cards.tsv and . there are a pulse root of your own; cmd/nova-pulse/testdata/example-pulse
in this repo is a fixture the size of a first run, and every line above is run
against it by the tests.

example:
  nova-pulse pool --sources cmd/nova-pulse/testdata/sources.tsv --root ./root

cmd/nova-pulse/testdata/sources.tsv there is a one-line source: a roadmap file
with two cells that name a card and one that names none, declared to the pool.
The line above runs "nova-pulse pool" from the repo root — it reads the roadmap,
skips the cell without a card, and writes the two candidates to ./root/pool.tsv.
That is a whole first run of the pool verb, no network and no model call, and
docs/TESTS.md carries the transcript it prints.
`

// refuse is what an unusable invocation costs: one line naming what was wrong and the door
// to the usage.
func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-pulse%s: %s; run: nova-pulse help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

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
		return cmdVersion(rest, stdout, stderr)
	case "pool":
		return cmdPool(rest, stdout, stderr)
	case "launch":
		return cmdLaunch(rest, stdout, stderr, now)
	case "cut", "harvest", "width":
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

func (f *flags) add(problem string) { f.problems = append(f.problems, problem) }

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
	max := f.fs.Int("max", 0, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*sources, "sources", "the declared sources file: kind, locator, template, one per line")
	f.want(*root, "root", "the directory holding seen.tsv and the pool state")
	if *timeout < 1 {
		f.problems = append(f.problems, fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.problems = append(f.problems, fmt.Sprintf("--max wants a whole number of candidates or 0 for no bound, got %d", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Pool(pulse.PoolInput{
		Sources: *sources,
		Root:    *root,
		Out:     *out,
		Timeout: time.Duration(*timeout) * time.Second,
		Max:     *max,
		Stdout:  stdout,
		Stderr:  stderr,
	})
}

func cmdLaunch(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("launch")
	cards := f.fs.String("cards", "", "")
	root := f.fs.String("root", "", "")
	slots := f.fs.Int("slots", 0, "")
	deadline := f.fs.String("deadline", "", "")
	queue := f.fs.Bool("queue", false, "")
	max := f.fs.Int("max", bounded.Default, "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*cards, "cards", "a cards.tsv of label, slot, model, card path")
	f.want(*root, "root", "the pulse root this pulse's state hangs under")
	if *slots < 1 {
		f.add(fmt.Sprintf("--slots is required and is at least 1, got %d; it is the ceiling on the free slots this pulse may use", *slots))
	}
	if !isDeadlineSeconds(*deadline) {
		f.add(fmt.Sprintf("--deadline is required and wants a whole number of seconds, got %q", *deadline))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Launch(pulse.LaunchInput{
		Cards: *cards, Root: *root, Slots: *slots, Deadline: *deadline, Queue: *queue,
		Stdout: stdout, Stderr: stderr, Now: func() time.Time { return now },
	})
}

func isDeadlineSeconds(s string) bool {
	if s == "" {
		return false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
		n = n*10 + int(r-'0')
	}
	return n >= 1
}
