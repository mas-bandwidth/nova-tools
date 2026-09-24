// nova-pulse is frozen, superseded by nova-sprint. Three verbs remain: status renders the
// status page, cut cuts cards from templates until nova-sprint cuts them, and harvest folds
// finished card work back. It makes no model call.
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

const usage = `nova-pulse: frozen; superseded by nova-sprint. Three verbs remain: status (the status page), cut (card cutting until nova-sprint cuts cards) and harvest.

nova-pulse cut     --pool <pool.tsv> --templates <dir> --out <dir> --root <dir> [--validate-contract] [--depends-on <cards>] [--max <n>]
nova-pulse cut     --templates <dir> --out <dir> --repo <clone> (--issue <repo>#<n> | --rows <file.tsv> | --branch-from <repo>#<n>) [--base <branch>] [--cards <file.tsv>] [--max <n>]
nova-pulse cut     --kind read|fix|replay|spec|guard|recut --repo <o/n> --out <dir> --queue <dir> [--pr <n>] [--head <sha>] [--issue <n>] [--title <t>] [--body-file <f>] [--prior <text>] [--names <a,b>] [--spec-lines <L1-L2>] [--diff-file <f>] [--dir <dir>] [--hold-file <path>]
nova-pulse harvest --id <pulse id> --root <dir> [--sources <file>] [--templates <dir>] [--launched <dir>] [--done <dir>] [--failed <dir>] [--max-body-bytes <n>] [--max <n>] [--decide] [--floor 0.9] [--key-env JEV_API_KEY] [--base-url <url>] [--commit]
nova-pulse harvest --bench <name> --root <bench root>[,<root>] --clone [<o/n>=]<dir>... [--machines <file>] [--session <id>] [--branch-prefix rowan/] [--base <branch>] [--since <d>] [--launched <dir>] [--done <dir>] [--failed <dir>] [--ssh <path>] [--max <n>] [--events-store <host:port>] [--commit] [--batch [--store <host:port>] [--store-user <name>] [--password-env <NAME>]]
nova-pulse harvest --working <dir> [--roots <dirs>] [--base <ref>] [--since <stamp>] [--timer install] [--max <n>] [--commit]
nova-pulse status  --queue <dir> --roots <dirs> [--results-root <dir>] [--batches <dir>] [--day <d>] [--oneline] [--timeout <s>] [--max <n>] [--expanding-hours <n>]
nova-pulse status  --html <out> --machines <registry> [--benches <file>, retired] [--queue <dir>] [--ssh <path>] [--timeout <s|duration>]
        [--publish <host:dir>] [--self <name>] [--loop <label>=<pattern>]... [--branch <name>]
        [--day-start <HH:MMZ>] [--gh-config <dir>]

version and help print the build and this text.

status --oneline is the whole day in one line under 400 bytes: width per bench,
pool, STOP, the day's reds, merges, cards done and failed, spend, and the pit-stop
note when <queue>/PITSTOP exists. A fresh window needs that line and the policy,
never the transcript.
status counts attempt results as card_fail and gateway on their own columns
(STATUS FAILURES, and the same two fields on --oneline). A gateway death ended
with no model turn -- a provider 5xx, or no tokens and no error on an attempt
that still failed -- and does not increment card_fail. QUEUE failed= stays the
count of cards in the failed directory.
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
  nova-pulse cut --pool cmd/nova-pulse/testdata/pool.tsv --templates cmd/nova-pulse/testdata/templates --out ./cards --root ./root

That line runs from the repo root against the fixture pool in cmd/nova-pulse/testdata; it
reaches no network and makes no model call.
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
	case "cut":
		if hasKindFlag(rest) {
			return cmdCutKind(rest, stdout, stderr)
		}
		return cmdCut(rest, stdout, stderr)
	case "harvest":
		return cmdHarvest(rest, stdout, stderr, now)
	case "status":
		return cmdStatus(rest, stdout, stderr, now)
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

func cmdStatus(args []string, stdout, stderr io.Writer, now time.Time) int {
	f := newFlags("status")
	queue := f.fs.String("queue", "", "")
	roots := f.fs.String("roots", "", "")
	// --results-root is where nova-swarm native published RESULT.md, usage.tsv
	// and the report (issue #2632). When it is set, the spend is read from there
	// and not from the job directories under --roots, which a sweep may already
	// have deleted. Width still comes from --roots.
	resultsRoot := f.fs.String("results-root", "", "")
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
			ResultsRoot:    *resultsRoot,
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
		ResultsRoot:    *resultsRoot,
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
	validateContract := f.fs.Bool("validate-contract", false, "")
	dependsOn := f.fs.String("depends-on", "", "")
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
		if *probe && strings.TrimSpace(*history) == "" {
			f.problems = append(f.problems, "--probe requires --history; the checklist is cut from the abstain history (one class per line)")
		}
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
	var deps []string
	if strings.TrimSpace(*dependsOn) != "" {
		deps = pulse.ParseDependsOn(*dependsOn)
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
		Pool:             *pool,
		Templates:        *templates,
		Out:              *out,
		Root:             *root,
		Max:              *max,
		Probe:            *probe,
		History:          *history,
		Budget:           *probeBudget,
		ValidateContract: *validateContract,
		DependsOn:        deps,
		Stdout:           stdout,
		Stderr:           stderr,
	})
}

// benchFlag collects a repeatable flag, split on commas.
type benchFlag []string

func (b *benchFlag) String() string { return strings.Join(*b, ",") }

func (b *benchFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if s := strings.TrimSpace(part); s != "" {
			*b = append(*b, s)
		}
	}
	return nil
}
