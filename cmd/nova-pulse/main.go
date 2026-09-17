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
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--local <tag>] [--max <n>]
nova-pulse cut     --kind read|fix|replay|spec --repo <o/n> --out <dir> --queue <dir> [--pr <n>] [--head <sha>] [--issue <n>] [--title <t>] [--body-file <f>] [--prior <text>] [--names <a,b>] [--spec-lines <L1-L2>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--max <n>]
nova-pulse harvest --id <pulse id> --root <dir> --sources <file> --templates <dir> [--max-body-bytes <n>] [--max <n>]
nova-pulse beat    --queue <dir> --cairn <file> --title <text> [--resume <text>]
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n> [--max <n>]
nova-pulse status  --queue <dir> --roots <dirs> [--day <d>] [--oneline] [--timeout <s>] [--max <n>] [--expanding-hours <n>]
nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]
nova-pulse gate    --repo <owner/name> --branch <name> --queue <dir> [--source <file>] [--timeout <s>]
nova-pulse run     --queue <dir> --roots <dirs> --repo <o/n> --branch <b> --hours <n> [--tick <s>] [--once] [--deadline <s>] [--timeout <s>] [--bus <clone>] [--as <name>] [--max <n>]
nova-pulse triage  --case <kind> --queue <dir> --out <card> [--ref <r>] [--evidence <file>]
nova-pulse sweep   --repo <o/n> --queue <dir> [--source <file>] [--timeout <s>]
nova-pulse reap    --roots <dirs> --queue <dir> --deadline <s> [--dry-run] [--timeout <s>]
nova-pulse fleet   suspend --benches <file> --bench <name>[,<name>] [--ssh <path>] [--if-idle] [--force] [--timeout <s>] [--max <n>]
nova-pulse fleet   wake --benches <file> --bench <name>[,<name>] [--ssh <path>] [--wait <duration>] [--timeout <s>] [--max <n>]
nova-pulse width   --root <dir> --pool <pool.tsv>  (not yet implemented)
nova-pulse version
nova-pulse help

launch reads a cards.tsv of label<TAB>slot<TAB>model<TAB>card, counts the free
slots in <root>/pool, and hands the cards that fit -- the whole admitted set as
one cards.tsv -- to nova-swarm batch's card form (--id --cards --deadline
--runner --root), queueing the rest only when --queue is set. --slots is the
ceiling on the free slots it may use, and --deadline is the whole pulse's one
deadline in whole seconds. It makes no model call itself: nova-swarm must be on
your PATH.

example:
  nova-pulse launch --cards ./cards.tsv --root . --slots 2 --deadline 120 --queue
  nova-pulse launch --cards ./cards.tsv --root . --slots 3 --deadline 120

./cards.tsv and . there are a pulse root of your own; cmd/nova-pulse/testdata/example-pulse
in this repo is a fixture the size of a first run, and every line above is run
against it by the tests.

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

run holds the loop so the coordinator's turns are decisions and never ticks. Each
tick is gate, harvest, sweep, reap, refill, launch and one PULSE WIDTH line, all
mechanical; it makes no model call, and it writes ONE bus note -- carrying the
triage packet of nova-pulse triage -- only when a rule cannot decide, once per
(case, ref). --once runs exactly one tick. With no --bus a note is appended to
<queue>/ESCALATE with its packet beside it.

Every step is the verb of the same name, wired: the gate over --branch, the
harvest of every bench with cards in flight, the sweep of the approvals ledger,
the reap of what the benches leak, the refill that cuts a read card per unread PR
head and a fix card per uncut issue in <queue>/WORKSET, and the launch that fills
the free slots (while a STOP stands, only the red's own card). Each verb's one
line goes to <queue>/pulse.log; the console keeps the WIDTH line. <queue>/pulse.toml
is re-read every tick -- a changed value takes effect on the next one and is named
on one CONFIG line, with no restart -- and the counters live in <queue>/pulse.state,
so a restart carries on rather than starting again.

example:
  nova-pulse run --queue ./queue --roots ./swarm-root,./swarm-root-space --repo mas-bandwidth/nova-tools --branch dev --hours 6

triage cuts the decision packet for one undecided case to a card for the text
route: the RESULT lines, the refusal line and the candidate rows of
<queue>/RULES.tsv, under 5000 bytes, demanding one line back --
TRIAGE <case> <verdict> <rule-row-or-NEW>. The seven cases are signature, scope,
docs-only, nosha, orphan, fence and hold-line.

example:
  nova-pulse triage --case nosha --queue ./queue --out ./cards/triage-nosha.md --ref card-892

status --oneline is the whole day in one line under 400 bytes: width per bench,
pool, STOP, the day's reds, merges, cards done and failed, spend, and the pit-stop
note when <queue>/PITSTOP exists. A fresh window needs that line and the policy,
never the transcript.
cut --kind is the typed cutter and the only numberer: the card number comes from
the queue state file's next_card under the queue's lock, so two cutters never
share one and there is no --number flag to pass. cut without --kind is unchanged.

example:
  nova-pulse cut --kind read --repo mas-bandwidth/nova-tools --pr 812 --head 5f544272a1b0 --out ./queue/pending --queue ./queue

sweep walks the approvals ledger: every read verdict is a row in <queue>/ledger.tsv,
and each sweep enqueues the approved, green, undrafted, unheld ones exactly once,
marks a moved head stale, and closes a merged or closed PR. --source replays it
from a file of PR states instead of gh, and enqueues into <queue>/enqueued.tsv.

example:
  nova-pulse sweep --repo mas-bandwidth/nova-tools --queue ./queue

reap collects what the benches leak: processes under a swarm root older than the
deadline, slot locks whose pid is dead, launched cards whose job directory is gone
(requeued once, then failed) and swarm test directories older than 30 minutes.
--dry-run changes nothing and prints the same counts.

example:
  nova-pulse reap --roots ./swarm-root,./swarm-root-space --queue ./queue --deadline 1800 --dry-run
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
		if hasKindFlag(rest) {
			return cmdCutKind(rest, stdout, stderr)
		}
		return cmdCut(rest, stdout, stderr)
	case "harvest":
		return cmdHarvest(rest, stdout, stderr)
	case "beat":
		return cmdBeat(rest, stdout, stderr, now)
	case "manager":
		return cmdManager(rest, stdout, stderr)
	case "status":
		return cmdStatus(rest, stdout, stderr)
	case "progress":
		return cmdProgress(rest, stdout, stderr)
	case "gate":
		return cmdGate(rest, stdout, stderr)
	case "run":
		return cmdRun(rest, stdout, stderr, now)
	case "triage":
		return cmdTriage(rest, stdout, stderr)
	case "sweep":
		return cmdSweep(rest, stdout, stderr)
	case "reap":
		return cmdReap(rest, stdout, stderr)
	case "fleet":
		return cmdFleet(rest, stdout, stderr)
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

func cmdHarvest(args []string, stdout, stderr io.Writer) int {
	f := newFlags("harvest")
	id := f.fs.String("id", "", "")
	root := f.fs.String("root", "", "")
	sources := f.fs.String("sources", "", "")
	templates := f.fs.String("templates", "", "")
	maxBodyBytes := f.fs.Int("max-body-bytes", 4096, "")
	max := f.fs.Int("max", 20, "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*id, "id", "the pulse id whose cards this harvest folds")
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
		Sources:      *sources,
		Templates:    *templates,
		MaxBodyBytes: *maxBodyBytes,
		Max:          *max,
		Stdout:       stdout,
		Stderr:       stderr,
	})
}

func cmdBeat(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("beat")
	queue := f.fs.String("queue", "", "")
	cairn := f.fs.String("cairn", "", "")
	title := f.fs.String("title", "", "")
	resume := f.fs.String("resume", "", "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory holding pending, launched, done, failed and the state files")
	f.want(*cairn, "cairn", "the cairn file this beat is appended to")
	f.want(*title, "title", "the one-line title of this beat")
	if f.refused(stderr) {
		return 2
	}
	return pulse.Beat(pulse.BeatInput{
		Queue: *queue, Cairn: *cairn, Title: *title, Resume: *resume,
		Now: func() time.Time { return now }, Stdout: stdout, Stderr: stderr,
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
	oneLine := f.fs.Bool("oneline", false, "")
	timeout := f.fs.Int("timeout", 120, "")
	max := f.fs.Int("max", bounded.Default, "")
	expandingHours := f.fs.Int("expanding-hours", 2, "")

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
	if *expandingHours < 1 {
		f.add(fmt.Sprintf("--expanding-hours wants a whole number of hours, got %d", *expandingHours))
	}
	if f.refused(stderr) {
		return 2
	}
	// --oneline is G4 of pit stop 3 (#828): the same day in one line under 400 bytes, for a
	// fresh window that needs the state and not the report. The eight-line default is
	// untouched.
	if *oneLine {
		return pulse.StatusLine(pulse.StatusInput{
			Queue:          *queue,
			Roots:          *roots,
			Day:            *day,
			Max:            *max,
			Timeout:        time.Duration(*timeout) * time.Second,
			ExpandingHours: *expandingHours,
			Stdout:         stdout,
			Stderr:         stderr,
		})
	}
	return pulse.Status(pulse.StatusInput{
		Queue:          *queue,
		Roots:          *roots,
		Day:            *day,
		Max:            *max,
		Timeout:        time.Duration(*timeout) * time.Second,
		ExpandingHours: *expandingHours,
		Stdout:         stdout,
		Stderr:         stderr,
	})
}

func cmdProgress(args []string, stdout, stderr io.Writer) int {
	f := newFlags("progress")
	queue := f.fs.String("queue", "", "")
	roots := f.fs.String("roots", "", "")
	day := f.fs.String("day", "", "")

	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the queue directory holding pending, launched, done and the POLICY")
	f.want(*roots, "roots", "the swarm roots whose usage.tsv is read, comma separated")
	if f.refused(stderr) {
		return 2
	}
	return pulse.Progress(pulse.ProgressInput{
		Queue:  *queue,
		Roots:  *roots,
		Day:    *day,
		Stdout: stdout,
		Stderr: stderr,
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
	max := f.fs.Int("max", bounded.Default, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*pool, "pool", "the pool.tsv of candidates to cut")
	f.want(*templates, "templates", "the directory holding the typed templates and models.tsv")
	f.want(*out, "out", "the directory the cut cards go into")
	f.want(*root, "root", "the state root; skipped.tsv is written here")
	if *max < 0 {
		f.problems = append(f.problems, fmt.Sprintf("--max is the number of cards to cut, 0 or more, got %d; 0 already means no bound", *max))
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
