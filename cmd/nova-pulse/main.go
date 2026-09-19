// nova-pulse enumerates bounded open work, cuts cards from templates, admits them through
// nova-swarm batch, and folds what comes back. It makes no model call: every token is a
// card's, and every cycle's reading is one PULSE line.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

const usage = `nova-pulse: bounded open work, cut into cards and folded back, no model call (see docs/SPEC-PULSE.md)

nova-pulse pool    --sources <file> --root <dir> [--out <pool.tsv>] [--timeout <s>] [--max <n>]
nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--max <n>]
nova-pulse cut     --templates <dir> --out <dir> --repo <clone> (--issue <repo>#<n> | --rows <file.tsv> | --branch-from <repo>#<n>) [--base <branch>] [--cards <file.tsv>] [--max <n>]
nova-pulse cut     --kind read|fix|replay|spec --repo <o/n> --out <dir> --queue <dir> [--pr <n>] [--head <sha>] [--issue <n>] [--title <t>] [--body-file <f>] [--prior <text>] [--names <a,b>] [--spec-lines <L1-L2>]
nova-pulse launch  --cards <cards.tsv> --root <dir> --slots <n> --deadline <s> [--queue] [--routes <routes.tsv>] [--floor <f>] [--key-env <name>] [--base-url <url>] [--max <n>]
nova-pulse fill    --ready <dir> --launched <dir> --machines <file> [--lanes <file>] [--session <id>] [--bench <name>]... [--only <glob>]... [--capacity <n>] [--launcher <path>] [--swarm-root <path>] [--deadline <s>] [--launch-grace <d>] [--once]
nova-pulse harvest --id <pulse id> --root <dir> [--sources <file>] [--templates <dir>] [--launched <dir>] [--done <dir>] [--failed <dir>] [--max-body-bytes <n>] [--max <n>] [--decide] [--floor 0.9] [--key-env JEV_API_KEY] [--base-url <url>]
nova-pulse harvest --bench <name> --root <bench root>[,<root>] --clone [<o/n>=]<dir>... [--session <id>] [--branch-prefix rowan/] [--base <branch>] [--since <d>] [--launched <dir>] [--done <dir>] [--failed <dir>] [--ssh <path>] [--max <n>]
nova-pulse harvest --working <dir> [--roots <dirs>] [--base <ref>] [--since <stamp>] [--timer install] [--max <n>]
nova-pulse beat    --queue <dir> --cairn <file> --title <text> [--resume <text>]
nova-pulse watch --queue <dir> --bus <dir> --jobs <root> --until <event> --cap <duration>
nova-pulse manager --policy <file> --queue <dir> --roots <dirs> --bus <clone> --as <name> --hours <n> [--max <n>]
nova-pulse status  --queue <dir> --roots <dirs> [--batches <dir>] [--day <d>] [--oneline] [--timeout <s>] [--max <n>] [--expanding-hours <n>]
nova-pulse status  --html <out> --machines <registry> [--benches <file>, retired] [--queue <dir>] [--ssh <path>] [--timeout <s|duration>]
        [--publish <host:dir>] [--self <name>] [--loop <label>=<pattern>]... [--branch <name>]
        [--day-start <HH:MMZ>] [--gh-config <dir>]
nova-pulse progress --queue <dir> --roots <dirs> [--day <d>]
nova-pulse capacity --bench <name> [--cores <n>] [--load1 <n>] [--free-gb <n>] [--memfree-gb <n>]
nova-pulse gate    --repo <owner/name> --branch <name> --queue <dir> [--source <file>] [--timeout <s>] [--decide] [--floor 0.9] [--key-env JEV_API_KEY] [--base-url <url>]
nova-pulse run     --queue <dir> --roots <dirs> --repo <o/n> --branch <b> --hours <n> [--tick <s>] [--once] [--deadline <s>] [--timeout <s>] [--bus <clone>] [--as <name>] [--decide [--floor <f>] [--key-env <var>] [--base-url <url>]] [--max <n>]
nova-pulse triage  --case <kind> --queue <dir> --out <card> [--ref <r>] [--evidence <file>] [--decide] [--floor 0.9] [--key-env JEV_API_KEY] [--base-url <url>]
nova-pulse sweep   --repo <o/n> --queue <dir> [--source <file>] [--timeout <s>]
nova-pulse reap    --roots <dirs> --queue <dir> --deadline <s> [--dry-run] [--timeout <s>]
nova-pulse fleet registry --machines <file> [--role bench|runner|coordination|services] [--max <n>]
nova-pulse fleet add <bench> --queue <dir> --roots <dirs> [--probe <file>]
nova-pulse hygiene run --home <dir> [--dry-run] [--hostname <name>]
nova-pulse hygiene reap <slot> --home <dir>
nova-pulse hygiene delete-job <slot> <job> --home <dir>
nova-pulse hygiene delete-slot <slot> --home <dir>
nova-pulse hygiene drop-cache --home <dir>
nova-pulse hygiene log [n] --home <dir>
nova-pulse fleet survey --benches <file> [--machines <file>] [--ssh <path>] [--timeout <s>] [--max <n>]
nova-pulse fleet   suspend --benches <file> --bench <name>[,<name>] [--machines <file>] [--ssh <path>] [--if-idle] [--force] [--timeout <s>] [--max <n>]
nova-pulse fleet   wake --benches <file> --bench <name>[,<name>] [--machines <file>] [--ssh <path>] [--wait <duration>] [--timeout <s>] [--max <n>]
nova-pulse fleet   reboot --benches <file> --bench <name>[,<name>] [--machines <file>] [--ssh <path>] [--wait <duration>] [--timeout <s>] [--max <n>]
nova-pulse fleet   secrets --benches <file> [--ssh <path>] [--timeout <s>] [--max <n>]
nova-pulse fleet   standard --benches <file> --bench <name> [--machines <file>] [--want <stamp>] [--go <ver>] [--os linux|darwin] [--min-free <gb>] [--ssh <path>] [--timeout <s>] [--max <n>]
nova-pulse fleet   mirror --benches <file> --bench <name> [--machines <file>] --repo <url> --path <remote path> [--ssh <path>] [--timeout <s>]
nova-pulse fleet   join --benches <file> --bench <name> [--machines <file>] --tailscale <path> --authkey-env <NAME> [--ssh <path>] [--timeout <s>]
nova-pulse fleet   sleep --benches <file> --bench <name> [--machines <file>] [--ssh <path>] [--if-idle] [--force] [--timeout <s>] [--max <n>]
nova-pulse wake    --bench <name>... --registry <file> [--timeout <duration, default 8m>]
nova-pulse sleep   --bench <name>... [--idle <duration, default 30m>]
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
docs-only, nosha, orphan, fence and hold-line. With --decide the bounded packet
is also put to one typed verdict question behind --floor, and the answer is
printed as one advisory TRIAGE DECIDE line carrying the evidence pointer; below
the floor the suggestion is '?' with its confidence and the packet is unchanged.

example:
  nova-pulse triage --case nosha --queue ./queue --out ./cards/triage-nosha.md --ref card-892

status --oneline is the whole day in one line under 400 bytes: width per bench,
pool, STOP, the day's reds, merges, cards done and failed, spend, and the pit-stop
note when <queue>/PITSTOP exists. A fresh window needs that line and the policy,
never the transcript.
status --batches <dir> folds the swarm's own health from the newest batch-*.out
outputs in that directory: STATUS SWARM first_attempt=<done/(done+abstain)> with
the hedge the rate calls for below 0.90, one STATUS FAULT line per abstain reason
loudest first, and STATUS PIT-STOP when one reason recurs five times or more --
fix the machinery before more cards. It is a path and a flag, there is no default
to guess, and without it the report says nothing about the swarm; a --batches that
cannot be read is refused rather than folded as a swarm with no faults.
status --html renders a bench that does not answer as DOWN and counts it on the
STATUS HTML line. A row of zeros reads as a bench with nothing to do, which is how
a fleet nobody could see looked healthy on 2026-09-17. The page also draws the
metrics.tsv series it writes beside itself.

status --html carries the whole page bin/status-page.sh carried, after an
adoption attempt refused it over twelve gaps. Its rows: the branch tip and its CI
run, the merge queue by state, the pending and ready cards, what the fill loop
launched and what capacity refused, the hygiene actions of the last hour summed
over the benches, one row per bench, and the time series.
  --publish <host:dir>  ships index.html and metrics.tsv there over ssh, the same
        door the benches are read through. A failed publish is loud and exits 3:
        a page that quietly stopped shipping goes stale while everybody reads it.
        Without it the verb writes locally and says published=-.
  --self <name>         the host running the verb is a bench too, with its own
        columns: CI runners, cores, load, free disk, orphans. The Studio drowned
        at load 147 on 2026-09-17 and the page showed four Linux benches idling.
  --loop <label>=<pat>  repeatable; counts long-running loops on the --self host
        by the pattern YOU name. No loop name is baked into this tool: a verb
        carrying harvest-loop.sh in its source would freeze the scripts it exists
        to retire.
  --branch <name>       whose merge queue and tip the page shows; dev by default.
  --day-start <HH:MMZ>  when the merged counter resets; 02:00Z by default,
        because INSTALL-fleet.md's rate_counter resets there and a page that
        disagrees with every other instrument for two hours a day is not read.
  --gh-config <dir>     GH_CONFIG_DIR for the gh children. gh answers as whoever
        that says, so a page run from a service manager with a bare environment
        must be given it here or in the environment it inherits; without the flag
        the caller's own GH_CONFIG_DIR goes through untouched.
  --timeout             takes a whole number of seconds or a duration (90s, 2m).
A count nobody took is a DASH, never a zero: with no <queue>/REPO there is nobody
to ask, so merged, opened and the merge queue read as a dash on the page, in the
metrics row and on the STATUS HTML line. gh list calls ask for 500, because gh's own
default is thirty and a capped count flatlines the series rather than failing.
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

hygiene is the bench's clean-as-we-work pass, the Go half of bin/bench-hygiene.sh:
run reaps dead slots, deletes read jobs and drops the build cache when the disk is
low, and prints one HYGIENE line; reap <slot>, delete-job <slot> <job>,
delete-slot <slot>, drop-cache and log [n] are the six verbs it is made of. Every
path hangs under --home and every deletion goes through internal/safepath: a slot
name is [A-Za-z0-9._-]+, the path is the join of a literal root and that name,
resolved and checked to sit strictly below its root, and a target that is not —
outside the roots, holding "..", or a symlink escape — is refused, exit 2, one
line. Each deletion is one <utc> <verb> <path> line in <home>/hygiene.log.

example:
  nova-pulse hygiene run --home "$HOME"

fleet survey runs tools/bench-standard.sh on every bench named in --benches
(name, ssh target and home per tab-separated line) over the ssh command
"ssh <target> bash -s", in parallel under --timeout, and prints one line per
bench: the standard's DRIFT lines and its last line prefixed FLEET <name>. Exit
0 when every bench says
STANDARD OK, 2 on any DRIFT, 3 when a bench is unreachable (FLEET <name>
UNREACHABLE <error>). --max caps the lines; 0 means all.

example:
  nova-pulse fleet survey --benches ./fleet.tsv

wake and sleep are the Mac benches' power verbs (Glenn 2026-09-17: each iMac Pro
draws 100 W and the fleet runs on solar). --registry is one name,mac,lan-bench
per line, validated whole before any ssh. To wake a bench the magic packet is
built in Go and sent three times from the named lan-bench to UDP broadcast port
9; then ssh is polled for up to ninety seconds, a user-activity assertion turns
the dark wake into a full one, and the bench is awake only when its runners show
online in GitHub inside --timeout; the line is WAKE <bench> up after <s>s
runners=<n> or WAKE FAIL <bench> <stage> <reason>. sleep refuses while any of
the bench's runners is busy (SLEEP REFUSED <bench> busy=<n>) and otherwise sets
idle sleep, printing SLEEP <bench> idle=<m>. Both take the benches as a
repeatable --bench or as bare arguments.
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
	case "fill":
		return cmdFill(rest, stdout, stderr, now)
	case "cut":
		if hasKindFlag(rest) {
			return cmdCutKind(rest, stdout, stderr)
		}
		return cmdCut(rest, stdout, stderr)
	case "harvest":
		return cmdHarvest(rest, stdout, stderr, now)
	case "beat":
		return cmdBeat(rest, stdout, stderr, now)
	case "watch":
		return cmdWatch(rest, stdout, stderr, now)
	case "manager":
		return cmdManager(rest, stdout, stderr)
	case "status":
		return cmdStatus(rest, stdout, stderr, now)
	case "progress":
		return cmdProgress(rest, stdout, stderr)
	case "capacity":
		return cmdCapacity(rest, stdout, stderr)
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
	case "hygiene":
		return cmdHygiene(rest, stdout, stderr, now)
	case "fleet":
		return cmdFleet(rest, stdout, stderr)
	case "wake":
		return cmdWake(rest, stdout, stderr)
	case "sleep":
		return cmdSleep(rest, stdout, stderr)
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

// parseAny is parse for a verb that also reads positional arguments (wake and sleep take
// the bench names either as a repeatable --bench or bare).
func (f *flags) parseAny(args []string, stderr io.Writer) bool {
	if err := f.fs.Parse(args); err != nil {
		fmt.Fprintf(stderr, "nova-pulse %s: %s\n", f.verb, err)
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
	routes := f.fs.String("routes", "", "")
	floor := f.fs.Float64("floor", 0.9, "")
	keyEnv := f.fs.String("key-env", decide.DefaultKeyEnv, "")
	baseURL := f.fs.String("base-url", decide.DefaultBaseURL, "")

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
	if *floor < 0 || *floor > 1 {
		f.add(fmt.Sprintf("--floor is between 0 and 1, got %g; answers below it keep the card's own worker", *floor))
	}
	if f.refused(stderr) {
		return 2
	}
	return pulse.Launch(pulse.LaunchInput{
		Cards: *cards, Root: *root, Slots: *slots, Deadline: *deadline, Queue: *queue,
		Routes: *routes, Floor: *floor, KeyEnv: *keyEnv, BaseURL: *baseURL,
		Stdout: stdout, Stderr: stderr, Now: func() time.Time { return now },
		Log: stderr,
	})
}

func cmdHarvest(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("harvest")
	id := f.fs.String("id", "", "")
	root := f.fs.String("root", "", "")
	sources := f.fs.String("sources", "", "")
	templates := f.fs.String("templates", "", "")
	maxBodyBytes := f.fs.Int("max-body-bytes", 4096, "")
	max := f.fs.Int("max", 20, "")
	decideOn := f.fs.Bool("decide", false, "")
	floor := f.fs.Float64("floor", 0.9, "")
	keyEnv := f.fs.String("key-env", decide.DefaultKeyEnv, "")
	baseURL := f.fs.String("base-url", decide.DefaultBaseURL, "")
	bench := f.fs.String("bench", "", "")
	machines := f.fs.String("machines", "", "")
	ssh := f.fs.String("ssh", "", "")
	session := f.fs.String("session", "", "")
	branchPrefix := f.fs.String("branch-prefix", pulse.DefaultBranchPrefix, "")
	base := f.fs.String("base", "", "")
	since := f.fs.String("since", "", "")
	launched := f.fs.String("launched", "", "")
	doneDir := f.fs.String("done", "", "")
	failedDir := f.fs.String("failed", "", "")
	var clones benchFlag
	f.fs.Var(&clones, "clone", "")
	working := f.fs.String("working", "", "")
	roots := f.fs.String("roots", "", "")
	timer := f.fs.String("timer", "", "")

	if !f.parse(args, stderr) {
		return 2
	}
	// The working layout names no --id and no --root: it folds the bench's jobs
	// under --working and the swarm roots under --roots. The old layout is
	// unchanged and still wants both.
	if strings.TrimSpace(*working) != "" || strings.TrimSpace(*roots) != "" {
		if *max < 0 {
			f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
		}
		if f.refused(stderr) {
			return 2
		}
		return pulse.HarvestWorking(pulse.HarvestInput{
			Working:    *working,
			Roots:      *roots,
			Base:       *base,
			SinceStamp: *since,
			Timer:      *timer,
			Max:        *max,
			Stdout:     stdout,
			Stderr:     stderr,
			Now:        func() time.Time { return now },
		})
	}
	// A bench harvest folds what is on the bench. There is no pulse packet to name and no
	// relaunch to feed, so --id, --sources and --templates are not its to supply: a
	// `cut --rows` produces none of the three (dogfood, 2026-09-18).
	onBench := strings.TrimSpace(*bench) != ""
	if !onBench {
		f.want(*id, "id", "the pulse id whose cards this harvest folds")
	}
	f.want(*root, "root", "the pulse root this pulse's state hangs under, or with --bench the swarm root ON the bench")
	var age time.Duration
	if s := strings.TrimSpace(*since); s != "" {
		d, err := time.ParseDuration(s)
		switch {
		case err != nil:
			f.add(fmt.Sprintf("--since wants a duration like 6h, got %q", s))
		case d < 0:
			f.add(fmt.Sprintf("--since is 0 or more, got %s", d))
		default:
			age = d
		}
	}
	if onBench && len(clones) == 0 {
		f.add("--clone is required with --bench; the branch is pushed from a clone HERE, never from the bench (pass --clone <dir>, or --clone <owner>/<name>=<dir> per repo)")
	}
	if *maxBodyBytes <= 0 {
		f.add(fmt.Sprintf("--max-body-bytes wants a positive byte count, got %d", *maxBodyBytes))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if *decideOn && (*floor < 0 || *floor > 1) {
		f.add(fmt.Sprintf("--floor is between 0 and 1, got %g", *floor))
	}
	if f.refused(stderr) {
		return 2
	}
	in := pulse.HarvestInput{
		ID:           *id,
		Root:         *root,
		Sources:      *sources,
		Templates:    *templates,
		MaxBodyBytes: *maxBodyBytes,
		Max:          *max,
		Stdout:       stdout,
		Stderr:       stderr,
		Bench:        *bench,
		Machines:     *machines,
		SSH:          *ssh,
		Clones:       []string(clones),
		Session:      *session,
		BranchPrefix: *branchPrefix,
		Base:         *base,
		Since:        age,
		Launched:     *launched,
		Done:         *doneDir,
		Failed:       *failedDir,
	}
	if *decideOn {
		client, err := decide.New(*baseURL, *keyEnv)
		if err != nil {
			return refuse(stderr, " harvest", oneline.Cap(err.Error(), oneline.TailBytes))
		}
		in.Decide, in.Floor, in.Decider = true, *floor, client
	}
	return pulse.Harvest(in)
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

func cmdWatch(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("watch")
	queue := f.fs.String("queue", "", "")
	busDir := f.fs.String("bus", "", "")
	jobs := f.fs.String("jobs", "", "")
	until := f.fs.String("until", "", "")
	capFlag := f.fs.String("cap", "", "")

	if !f.parse(args, stderr) {
		return 2
	}
	var capDur time.Duration
	if strings.TrimSpace(*capFlag) != "" {
		d, err := time.ParseDuration(*capFlag)
		if err != nil {
			fmt.Fprintf(stderr, "WATCH REFUSED cap=%s (a duration like 90s or 5m)\n", oneline.Field(*capFlag))
			return 2
		}
		capDur = d
	}
	return pulse.Watch(pulse.WatchInput{
		Queue: *queue, Bus: *busDir, Jobs: *jobs, Until: *until, Cap: capDur,
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

func cmdStatus(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("status")
	queue := f.fs.String("queue", "", "")
	roots := f.fs.String("roots", "", "")
	slotsStore := f.fs.String("slots-store", "", "")
	batches := f.fs.String("batches", "", "")
	day := f.fs.String("day", "", "")
	oneLine := f.fs.Bool("oneline", false, "")
	html := f.fs.String("html", "", "")
	benches := f.fs.String("benches", "", "")
	machines := f.fs.String("machines", "", "")
	ssh := f.fs.String("ssh", "ssh", "")
	publish := f.fs.String("publish", "", "")
	ghConfig := f.fs.String("gh-config", "", "")
	dayStart := f.fs.String("day-start", "", "")
	branch := f.fs.String("branch", "", "")
	self := f.fs.String("self", "", "")
	var loops repeatable
	f.fs.Var(&loops, "loop", "")
	// --timeout takes a bare number of seconds or a duration. It was seconds only while the
	// verb's own progress line printed a duration, so a reader who copied what the tool said
	// got a flag parse error: a flag that will not accept what the tool prints is a trap.
	timeoutRaw := f.fs.String("timeout", "120", "")
	max := f.fs.Int("max", bounded.Default, "")
	expandingHours := f.fs.Int("expanding-hours", 2, "")

	if !f.parse(args, stderr) {
		return 2
	}
	timeout, terr := pulse.ParseTimeout(*timeoutRaw)
	if terr != nil {
		f.add(fmt.Sprintf("--timeout %s", terr))
	}
	if *max < 0 {
		f.add(fmt.Sprintf("--max is 0 or more, got %d; 0 already means all", *max))
	}
	if *expandingHours < 1 {
		f.add(fmt.Sprintf("--expanding-hours wants a whole number of hours, got %d", *expandingHours))
	}
	// --html is the fleet status page as a verb (status-page.sh folded in). It reads the
	// machines registry and the queue, counts live cards from running card processes, and
	// prints one STATUS HTML line; the eight-line report and --oneline are untouched.
	// --benches is the retired four-column file, read for one more release.
	if *html != "" {
		if strings.TrimSpace(*machines) == "" && strings.TrimSpace(*benches) == "" {
			f.want(*machines, "machines", "the machines registry, as in --machines queue/control/machines.tsv "+
				"(the retired --benches file still reads for one release)")
		}
		if f.refused(stderr) {
			return 2
		}
		return pulse.StatusHTML(pulse.StatusHTMLInput{
			HTML:     *html,
			Machines: *machines,
			Benches:  *benches,
			Queue:    *queue,
			SSH:      *ssh,
			Publish:  *publish,
			GhConfig: *ghConfig,
			DayStart: *dayStart,
			Branch:   *branch,
			Self:     *self,
			Loops:    loops,
			Timeout:  timeout,
			Reader:   statusHTMLReader,
			SelfRead: statusHTMLSelfReader,
			Ship:     statusHTMLPublisher,
			Now:      func() time.Time { return now },
			Stdout:   stdout,
			Stderr:   stderr,
		})
	}
	f.want(*queue, "queue", "the queue directory holding pending, launched, done and the state files")
	f.want(*roots, "roots", "the benches to report, comma separated")
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
			Timeout:        timeout,
			ExpandingHours: *expandingHours,
			Stdout:         stdout,
			Stderr:         stderr,
		})
	}
	return pulse.Status(pulse.StatusInput{
		Queue:          *queue,
		Roots:          *roots,
		SlotsStores:    *slotsStore,
		Batches:        *batches,
		Day:            *day,
		Max:            *max,
		Timeout:        timeout,
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

// cmdCapacity is the allowed-cards formula as one verb: for --bench <name> it prints the
// one CAPACITY line every launcher reads, and with no numbers given it reads this host's own
// cores, load, free disk and free memory, so a bench can ask itself instead of shelling a
// remote script. A number passed on the command line wins over the local read; that is what
// lets a test fix all four and touch neither /proc nor df.
//
// THE LOCAL READ IS LINUX. /proc/loadavg, /proc/meminfo and `df -BG` are what cap() reads
// and they are a linux bench's facts; darwin has no /proc and its df has no -BG. On such a
// host the verb REFUSES and names the flag that was not given, rather than standing a
// number up out of nothing: every launcher in the fleet acts on this one line, and a guess
// here is a guess about how much work a machine can take. The fleet's own capacity still
// comes from the remote script in fill.go, which runs on the linux benches.
func cmdCapacity(args []string, stdout, stderr io.Writer) int {
	f := newFlags("capacity")
	bench := f.fs.String("bench", "", "")
	cores := f.fs.Int("cores", -1, "")
	load1 := f.fs.Int("load1", -1, "")
	freeGB := f.fs.Int("free-gb", -1, "")
	memFreeGB := f.fs.Int("memfree-gb", -1, "")
	if !f.parse(args, stderr) {
		return 2
	}
	c := *cores
	if c < 0 {
		c = runtime.NumCPU()
	}
	l := *load1
	if l < 0 {
		v, err := hostLoad1()
		if err != nil {
			f.add(fmt.Sprintf("--load1 was not given and /proc/loadavg could not be read: %s", oneline.Err(err)))
		}
		l = v
	}
	fg := *freeGB
	if fg < 0 {
		v, err := hostFreeGB()
		if err != nil {
			f.add(fmt.Sprintf("--free-gb was not given and df on the home filesystem failed: %s", oneline.Err(err)))
		}
		fg = v
	}
	mg := *memFreeGB
	if mg < 0 {
		v, err := hostMemFreeGB()
		if err != nil {
			f.add(fmt.Sprintf("--memfree-gb was not given and /proc/meminfo could not be read: %s", oneline.Err(err)))
		}
		mg = v
	}
	if f.refused(stderr) {
		return 2
	}
	allowed := pulse.AllowedCards(c, l, fg, mg)
	fmt.Fprintf(stdout, "CAPACITY bench=%s cores=%d load=%d free=%dG memfree=%dG allowed=%d\n",
		oneline.Field(*bench), c, l, fg, mg, allowed)
	return 0
}

// hostLoad1 reads the whole part of the one-minute load average, the way cap()'s
// `cut -d. -f1 /proc/loadavg` does.
func hostLoad1() (int, error) {
	raw, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(raw))
	if len(fields) == 0 {
		return 0, fmt.Errorf("empty /proc/loadavg")
	}
	whole := strings.SplitN(fields[0], ".", 2)[0]
	n, err := strconv.Atoi(whole)
	if err != nil {
		return 0, fmt.Errorf("load %q is not a number", fields[0])
	}
	return n, nil
}

// hostMemFreeGB reads MemAvailable from /proc/meminfo as whole GB, the way cap()'s
// `$2/1048576` does.
func hostMemFreeGB() (int, error) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		rest, ok := strings.CutPrefix(line, "MemAvailable:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return 0, fmt.Errorf("MemAvailable has no value")
		}
		kb, err := strconv.Atoi(fields[0])
		if err != nil {
			return 0, fmt.Errorf("MemAvailable %q is not a number", fields[0])
		}
		return kb / 1048576, nil
	}
	return 0, fmt.Errorf("no MemAvailable line")
}

// hostFreeGB reads the free space on the home filesystem as whole GB, the way cap()'s
// `df -BG "$HOME" | awk 'NR==2{gsub("G","",$4); print $4}'` does.
func hostFreeGB() (int, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.Getenv("HOME")
	}
	out, err := exec.Command("df", "-BG", home).Output()
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("df printed no filesystem line")
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 4 {
		return 0, fmt.Errorf("df line has %d fields", len(fields))
	}
	n, err := strconv.Atoi(strings.TrimSuffix(fields[3], "G"))
	if err != nil {
		return 0, fmt.Errorf("df available %q is not a number", fields[3])
	}
	return n, nil
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
	issue := f.fs.String("issue", "", "")
	rows := f.fs.String("rows", "", "")
	branchFrom := f.fs.String("branch-from", "", "")
	templates := f.fs.String("templates", "", "")
	out := f.fs.String("out", "", "")
	root := f.fs.String("root", "", "")
	repo := f.fs.String("repo", "", "")
	base := f.fs.String("base", "dev", "")
	cardsTSV := f.fs.String("cards", "", "")
	max := f.fs.Int("max", bounded.Default, "")
	probe := f.fs.Bool("probe", false, "")
	history := f.fs.String("history", "", "")
	probeBudget := f.fs.Int("probe-budget", 0, "")
	if !f.parse(args, stderr) {
		return 2
	}
	sources := 0
	for _, s := range []string{*pool, *issue, *rows, *branchFrom} {
		if strings.TrimSpace(s) != "" {
			sources++
		}
	}
	if sources == 0 {
		f.problems = append(f.problems, "cut wants one source; it wants --pool, --issue, --rows or --branch-from")
	} else if sources > 1 {
		f.problems = append(f.problems, "cut reads one source; pass only one of --pool, --issue, --rows or --branch-from")
	}
	f.want(*templates, "templates", "the directory holding the typed templates and benches.tsv (or routes.tsv)")
	f.want(*out, "out", "the directory the cut cards go into")
	// --root is the pool form's alone: skipped.tsv is written under it, and the
	// validated-template forms write nothing there. It was required of all four and used
	// by one, so a caller passed a path that was never opened.
	validated := *issue != "" || *rows != "" || *branchFrom != ""
	if !validated {
		f.want(*root, "root", "the state root; --pool writes skipped.tsv here")
	}
	if validated {
		f.want(*repo, "repo", "the clone every git call runs in (git -C); cut never reads the working directory")
	}
	if *max < 0 {
		f.problems = append(f.problems, fmt.Sprintf("--max is the number of cards to cut, 0 or more, got %d; 0 already means no bound", *max))
	}
	if f.refused(stderr) {
		return 2
	}
	switch {
	case *issue != "":
		return pulse.CutValidated(pulse.CutValidatedInput{
			Source: "issue", Issue: *issue, Templates: *templates, Out: *out, Repo: *repo,
			Base: *base, Cards: *cardsTSV, Max: *max, Stdout: stdout, Stderr: stderr,
		})
	case *rows != "":
		return pulse.CutValidated(pulse.CutValidatedInput{
			Source: "rows", Rows: *rows, Templates: *templates, Out: *out, Repo: *repo,
			Base: *base, Cards: *cardsTSV, Max: *max, Stdout: stdout, Stderr: stderr,
		})
	case *branchFrom != "":
		return pulse.CutValidated(pulse.CutValidatedInput{
			Source: "branch-from", BranchFrom: *branchFrom, Templates: *templates, Out: *out, Repo: *repo,
			Base: *base, Cards: *cardsTSV, Max: *max, Stdout: stdout, Stderr: stderr,
		})
	}
	return pulse.Cut(pulse.CutInput{
		Pool:      *pool,
		Templates: *templates,
		Out:       *out,
		Root:      *root,
		Max:       *max,
		Probe:     *probe,
		History:   *history,
		Budget:    *probeBudget,
		Stdout:    stdout,
		Stderr:    stderr,
	})
}
