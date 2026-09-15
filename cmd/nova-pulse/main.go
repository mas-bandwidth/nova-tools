// nova-pulse enumerates bounded open work, cuts cards from templates, admits them through
// nova-swarm batch, and folds what comes back. It makes no model call: every token is a
// card's, and every cycle's reading is one PULSE line.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

var version string

const usage = `nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]
nova-pulse width   --root <dir> --pool <pool.tsv>
nova-pulse version
nova-pulse help
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
		return refuse(stderr, "", "no verb given")
	}
	switch args[0] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		fmt.Fprintln(stdout, oneline.Field(version))
		return 0
	case "launch":
		return cmdLaunch(args[1:], stdout, stderr, now)
	}
	return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
}

// flags is launch's flag set with package flag's two mouths closed, matching the house shape.
type flags struct {
	fs       *flag.FlagSet
	problems []string
}

func (f *flags) want(value, name, wants string) {
	if value == "" {
		f.problems = append(f.problems, fmt.Sprintf("--%s is required; it wants %s; refusing to guess", name, wants))
	}
}

func (f *flags) add(problem string) { f.problems = append(f.problems, problem) }

func (f *flags) refused(stderr io.Writer) bool {
	for _, p := range f.problems {
		fmt.Fprintf(stderr, "nova-pulse launch: %s\n", oneline.Escape(p))
	}
	return len(f.problems) > 0
}

func cmdLaunch(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := &flags{fs: flag.NewFlagSet("launch", flag.ContinueOnError)}
	f.fs.SetOutput(io.Discard)
	f.fs.Usage = func() {}
	cards := f.fs.String("cards", "", "")
	root := f.fs.String("root", "", "")
	slots := f.fs.Int("slots", 0, "")
	deadline := f.fs.String("deadline", "", "")
	queue := f.fs.Bool("queue", false, "")
	max := f.fs.Int("max", bounded.Default, "")

	if err := f.fs.Parse(args); err != nil {
		return refuse(stderr, " launch", oneline.Cap(err.Error(), oneline.TailBytes))
	}
	if f.fs.NArg() > 0 {
		fmt.Fprintf(stderr, "nova-pulse launch: takes no positional arguments, got %d (flags come before arguments)\n", f.fs.NArg())
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
