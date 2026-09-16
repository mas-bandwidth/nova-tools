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
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--model <id>] [--local <tag>] [--max <n>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --deadline <s> [--slots <lo-hi>] [--benches <file>] [--bench <names>] [--id <id>] [--runner <cmd>] [--harness <path>] [--auth <path>] [--idle <s>] [--check <s>] [--swarm <path>] [--max <n>]
nova-pulse check   --root <dir> [--id <pulse id>] [--after <s>] [--benches <file>] [--bench <names>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--publish] [--deadline <s>] [--slots <lo-hi>] [--max-body-bytes <n>] [--max <n>]
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n> [--max <n>]
nova-pulse status  --queue <dir> --roots <dirs> [--day <d>] [--timeout <s>] [--max <n>]
nova-pulse width   --root <dir> --pool <pool.tsv>  (not yet implemented)
nova-pulse version
nova-pulse help

launch reads a cards.tsv of label<TAB>slot<TAB>model<TAB>card and hands it to
"nova-swarm batch" in its card form -- --id --cards --deadline --root, with
--runner or --harness, and --benches/--bench for a bench -- which is the form that
runs a card. It fills only free slots: A SLOT IS FREE WHEN THE BATCH LOCK
<root>/<slot>/BATCH is absent or its holder pid is dead, never when a process
probe says so, and the allocation itself is the batch's, under that lock. A card
that is empty or younger than five seconds is skipped by name. Every launched card
is one row of <root>/launch.tsv (id, label, slot, bench, model, card sha, stamp),
and --check <s> (default 90) counts the started cards afterwards by job directory:
LAUNCH-OK, LAUNCH-DEAD or LAUNCH-UNKNOWN. One launch per root: a second one is
refused with the holder's pid.

example:
  nova-pulse launch --cards ./cards.tsv --root ./swarm-root --deadline 1500 --slots 1-16 --harness /path/to/opencode
  nova-pulse launch --cards ./cards.tsv --root ./swarm-root --deadline 1500 --slots 101-160 --benches ./benches.tsv --bench space
  nova-pulse check  --root ./swarm-root --id 20260916T014455Z-pulse-7c1a20

check counts the cards of a pulse that have a job directory -- made before the
harness's first line -- and says so in one line; a bench that does not answer is
LAUNCH-UNKNOWN, never dead.

manager is the manager tier: a bounded controller, no model call. Each cycle is
wait, notes, harvest, triage, merge, refill and one MANAGER line; an unknown
policy key is a refusal; --hours 0 runs one cycle and the shift ends SHIFT END.

example:
  nova-pulse manager --policy ./queue/POLICY --queue ./queue --roots ./swarm-root,./swarm-root-space --bus ./bus --as Rowan --hours 6

example:
  nova-pulse pool --sources cmd/nova-pulse/testdata/sources.tsv --root ./root

cmd/nova-pulse/testdata/sources.tsv there is a one-line source: a roadmap file
with two cells that name a card and one that names none, declared to the pool.
The line above runs "nova-pulse pool" from the repo root — it reads the roadmap,
skips the cell without a card, and writes the two candidates to ./root/pool.tsv.
That is a whole first run of the pool verb, no network and no model call, and
docs/TESTS.md carries the transcript it prints.

example:
  nova-pulse cut --pool cmd/nova-pulse/testdata/pool.tsv --templates cmd/nova-pulse/testdata/templates --out ./cards --root ./root
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
	case "cut":
		return cmdCut(rest, stdout, stderr)
	case "harvest":
		return cmdHarvest(rest, stdout, stderr)
	case "check":
		return cmdCheck(rest, stdout, stderr)
	case "manager":
		return cmdManager(rest, stdout, stderr)
	case "status":
		return cmdStatus(rest, stdout, stderr)
	case "width":
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
	slots := f.fs.String("slots", "", "")
	deadline := f.fs.String("deadline", "", "")
	benches := f.fs.String("benches", "", "")
	bench := f.fs.String("bench", "", "")
	id := f.fs.String("id", "", "")
	runner := f.fs.String("runner", "", "")
	harness := f.fs.String("harness", "", "")
	auth := f.fs.String("auth", "", "")
	idle := f.fs.Int("idle", 0, "")
	check := f.fs.Int("check", 90, "")
	swarmBin := f.fs.String("swarm", "", "")
	max := f.fs.Int("max", bounded.Default, "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*cards, "cards", "a cards.tsv of label, slot, model, card path")
	f.want(*root, "root", "the swarm root this pulse's slots and job directories hang under")
	if !isDeadlineSeconds(*deadline) {
		f.add(fmt.Sprintf("--deadline is required and wants a whole number of seconds, got %q", *deadline))
	}
	if *check < 0 {
		f.add(fmt.Sprintf("--check is 0 or more seconds, got %d; 0 runs no launch check", *check))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Launch(pulse.LaunchInput{
		Cards: *cards, Root: *root, Slots: *slots, Deadline: *deadline,
		Benches: *benches, Bench: *bench, ID: *id, Runner: *runner,
		Harness: *harness, Auth: *auth, Idle: *idle, Check: *check, Max: *max,
		Swarm:  *swarmBin,
		Stdout: stdout, Stderr: stderr, Now: func() time.Time { return now },
	})
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	f := newFlags("check")
	id := f.fs.String("id", "", "")
	root := f.fs.String("root", "", "")
	after := f.fs.Int("after", 0, "")
	benches := f.fs.String("benches", "", "")
	bench := f.fs.String("bench", "", "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*root, "root", "the swarm root the pulse's job directories hang under")
	if *after < 0 {
		f.add(fmt.Sprintf("--after is 0 or more seconds, got %d; 0 counts the started cards now", *after))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Check(pulse.CheckInput{
		ID: *id, Root: *root, After: *after, Benches: *benches, Bench: *bench,
		Stdout: stdout, Stderr: stderr,
	})
}

func cmdHarvest(args []string, stdout, stderr io.Writer) int {
	f := newFlags("harvest")
	id := f.fs.String("id", "", "")
	root := f.fs.String("root", "", "")
	sources := f.fs.String("sources", "", "")
	templates := f.fs.String("templates", "", "")
	maxBodyBytes := f.fs.Int("max-body-bytes", 4096, "")
	max := f.fs.Int("max", 20, "")
	publish := f.fs.Bool("publish", false, "")
	deadline := f.fs.String("deadline", "", "")
	slots := f.fs.String("slots", "", "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*root, "root", "the pulse root this pulse's state hangs under")
	if *maxBodyBytes <= 0 {
		f.add(fmt.Sprintf("--max-body-bytes wants a positive byte count, got %d", *maxBodyBytes))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Harvest(pulse.HarvestInput{
		ID:           *id,
		Root:         *root,
		Publish:      *publish,
		Deadline:     *deadline,
		Slots:        *slots,
		Sources:      *sources,
		Templates:    *templates,
		MaxBodyBytes: *maxBodyBytes,
		Max:          *max,
		Stdout:       stdout,
		Stderr:       stderr,
	})
}

func cmdManager(args []string, stdout, stderr io.Writer) int {
	f := newFlags("manager")
	policy := f.fs.String("policy", "", "")
	queue := f.fs.String("queue", "", "")
	roots := f.fs.String("roots", "", "")
	bus := f.fs.String("bus", "", "")
	as := f.fs.String("as", "", "")
	hours := f.fs.Float64("hours", -1, "")
	max := f.fs.Int("max", bounded.Default, "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*policy, "policy", "the approved policy file this shift executes, key=value lines")
	f.want(*queue, "queue", "the queue directory holding pending, launched, done and the state files")
	f.want(*roots, "roots", "the benches this shift harvests, comma separated")
	f.want(*bus, "bus", "the nova-bus clone this shift is the single waiter on")
	f.want(*as, "as", "the name this shift waits and receipts as")
	if *hours < 0 {
		f.add(fmt.Sprintf("--hours is required and is 0 or more, got %v; 0 runs exactly one cycle", *hours))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Manager(pulse.ManagerInput{
		Policy: *policy, Queue: *queue, Roots: *roots, Bus: *bus, As: *as,
		Hours: *hours, Max: *max, Stdout: stdout, Stderr: stderr,
	})
}

func cmdStatus(args []string, stdout, stderr io.Writer) int {
	f := newFlags("status")
	queue := f.fs.String("queue", "", "")
	roots := f.fs.String("roots", "", "")
	day := f.fs.String("day", "", "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", bounded.Default, "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory holding pending, launched, done and the state files")
	f.want(*roots, "roots", "the benches to report, comma separated")
	if *timeout < 1 {
		f.add(fmt.Sprintf("--timeout wants a whole number of seconds, got %d", *timeout))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Status(pulse.StatusInput{
		Queue:   *queue,
		Roots:   *roots,
		Day:     *day,
		Max:     *max,
		Timeout: time.Duration(*timeout) * time.Second,
		Stdout:  stdout,
		Stderr:  stderr,
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

func cmdCut(args []string, stdout, stderr io.Writer) int {
	f := newFlags("cut")
	pool := f.fs.String("pool", "", "")
	templates := f.fs.String("templates", "", "")
	out := f.fs.String("out", "", "")
	root := f.fs.String("root", "", "")
	local := f.fs.String("local", "", "")
	model := f.fs.String("model", "", "")
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
		Model:     *model,
		Max:       *max,
		Stdout:    stdout,
		Stderr:    stderr,
	})
}
