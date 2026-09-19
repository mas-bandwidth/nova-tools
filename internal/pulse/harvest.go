package pulse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	hyg "github.com/mas-bandwidth/nova-tools/internal/hygiene"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// HarvestInput is everything the harvest verb needs, held apart from command-line parsing
// so a test can drive it with fake directories and a fake nova-swarm/nova-pulse on PATH.
type HarvestInput struct {
	ID           string
	Root         string
	Sources      string
	Templates    string
	MaxBodyBytes int
	Max          int
	Stdout       io.Writer
	Stderr       io.Writer
	Now          func() time.Time

	// Decide turns on the harvest's one typed decision per finished job: before
	// any push each done card's RESULT.md is classified behind Floor as fixed,
	// already-fixed, no-change, failed or off-branch. Decider is the typed
	// decision seam (the shipped *decide.Client; a test's fake). A nil Decider
	// leaves the harvest exactly as it was.
	Decide  bool
	Floor   float64
	Decider Decider

	// The accept gate (SPEC-TOOLWORK §1 rule 1): for every done card whose header
	// names a gated kind, harvest runs accept after the line-1 verify and before any
	// push. Gate is the seam (nil is pulse.Accept; a test's fake); GateBench, Cert,
	// Identities, Sandbox, Fixtures and Build are what the gate needs on THIS host, and
	// Queue is where <queue>/decide/outcomes.jsonl is appended (empty appends nothing).
	// There is no --no-gate.
	Gate       func(AcceptInput) int
	GateBench  string
	Cert       string
	Identities []hyg.Identity
	Sandbox    string
	Fixtures   fs.FS
	Build      string
	Queue      string

	// Bench turns the harvest around: the jobs are read on that bench over the
	// BenchShell seam rather than under a local root, and Root names the swarm
	// root ON the bench (comma separated for more than one). A bench harvest
	// wants no --sources, no --templates and no --id -- which a `cut --rows`
	// never produces -- and never relaunches: it folds what is there.
	Bench string
	// Machines is the machines registry --bench is held against before the first ssh
	// (Glenn's lock of 2026-09-18: runner hosts are CI-only). An empty path leaves the
	// verb unguarded, which is what a test and a by-hand run want; cmd/nova-pulse names
	// the registry on every real invocation.
	Machines     string
	SSH          string   // the ssh program the shipped shell runs; "" is "ssh"
	Clones       []string // <owner>/<name>=<dir>, or a bare <dir> for any repo
	Session      string   // only jobs whose RESULT.md names this session
	BranchPrefix string   // only branches under this prefix; "" is rowan/
	Base         string   // the base of a card that names none; "" is dev
	Since        time.Duration
	Shell        BenchShell
	Forge        Forge

	// Launched is the queue directory `fill` moves live cards into. When it is
	// named, harvest drains it: a card whose job has finished leaves it for Done
	// or Failed, its launched marker goes with it, and its lane is free.
	Launched string
	Done     string
	Failed   string
	// The working layout (SPEC-PULSE, "Harvest on the working layout"): every
	// path from a flag. --working names the bench's working root, --roots the
	// swarm roots it also folds, --base the ref the commit is measured past,
	// --since (SinceStamp) the session window and --timer installs the bench's
	// own clock.
	Working    string
	Roots      string
	SinceStamp string
	Timer      string
}

func field(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return oneline.Field(s)
}

func refusal(w io.Writer, token string, err error) int {
	fmt.Fprintf(w, "%s REFUSED: %s\n", token, oneline.Err(err))
	return 2
}

// Harvest folds one pulse's cards: it pushes and opens a draft PR for every card whose
// RESULT.md line 1 equals its contract line, sends every abstain (or missing RESULT) to
// retry.tsv with the harness log's last refusal line, cuts a read card per PR, and then
// pulses again -- queue first. It never merges.
func Harvest(in HarvestInput) int {
	if in.Now == nil {
		in.Now = time.Now
	}
	if in.MaxBodyBytes <= 0 {
		in.MaxBodyBytes = 4096
	}
	started := in.Now()

	// A bench harvest is the whole verb: the jobs are on the bench, the cards.tsv
	// this fold reads is not, and there is nothing here to relaunch.
	if strings.TrimSpace(in.Bench) != "" {
		return harvestBench(in)
	}

	if strings.TrimSpace(in.ID) == "" {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("missing --id; refusing to guess (supply the pulse id)"))
	}
	if strings.TrimSpace(in.Root) == "" {
		return refusal(in.Stderr, "HARVEST", fmt.Errorf("missing --root; refusing to guess (supply the root directory)"))
	}

	// A root `nova-pulse cut` wrote has a cards.tsv and is folded from it. A bare
	// swarm root a caller handed straight to `nova-swarm batch` has none, and harvest
	// folds every job dir under it instead: the RESULT.md files are the record, and
	// their own line 1 is the contract (rule 11). The refusal stands only when
	// neither the file nor a job dir is there.
	//
	// THE PULSE TABLE FIRST (issue #1818). `launch` writes the cards it admitted to
	// <root>/cards/<id>/cards.tsv -- docs/CLI.md and SPEC-PULSE rule 10 both say so, and
	// the --then launch chains is this exact verb with this exact --id. Harvest read
	// <root>/cards.tsv and nothing else, so every chained harvest of a successful pulse
	// refused with "run: nova-pulse cut" -- the wrong door, because launch had already
	// written the table. The root table stays as the fallback for a root `cut` wrote.
	cardsPath := pulseCardsPath(in.Root, in.ID)
	cards, err := readCards(cardsPath)
	if err != nil {
		rootPath := filepath.Join(in.Root, "cards.tsv")
		if rootCards, rootErr := readCards(rootPath); rootErr == nil {
			cards, err, cardsPath = rootCards, nil, rootPath
		}
	}
	if err != nil || len(cards) == 0 {
		if found := discoverRootCards(in.Root); len(found) > 0 {
			cards, err = found, nil
		}
		if len(cards) == 0 {
			return refusal(in.Stderr, "HARVEST", fmt.Errorf("cannot read %s, %s or any job directory under %s (harvest folds the pulse launch admitted; check the --id, or run: nova-pulse cut)",
				oneline.Field(pulseCardsPath(in.Root, in.ID)), oneline.Field(filepath.Join(in.Root, "cards.tsv")), oneline.Field(in.Root)))
		}
	}
	_ = cardsPath

	var done, pushed, prs, abstain, mismatch, refused, retried, elsewhere int
	var rejected, benched int  // the gate's own two: a card it sent back, and one the bench could not judge
	lines := make([]string, 0) // HARVEST PR / RETRY / REFUSED per-card lines
	var indexDirs []string     // finished jobs to append to the root's status index (#1088)

	for _, c := range cards {
		jobDir := jobDir(in.Root, c.Slot, c.Label)
		contract := cardContract(c.Card)
		state, branch, repo, resultLines := classify(jobDir, c, contract)

		// The pool layout beside the slot layout (SPEC-PULSE rule 12): launch
		// admits cards into <root>/pool and run finalizes each task's report
		// to pool/reports/<id>/RESULT.md with the task file in
		// pool/{done,failed}/<id>.task. A pool card is never under the slot
		// job directory, so without this it is never harvested.
		if state != "done" {
			if ps, pb, pr, pl, pd := classifyPool(in.Root, c, contract); ps != "" {
				state, branch, repo, resultLines, jobDir = ps, pb, pr, pl, pd
			} else if state == "abstain" && !isDir(jobDir) {
				// The card has no job directory under this root at all: it did
				// not abstain here, it ran somewhere else. Counting it as an
				// abstain rewrote a card that had finished green on a bench and
				// reported `retry=1` for a success (dogfood, 2026-09-18). It is
				// its own state, and the note names the remedy.
				state = "elsewhere"
			}
		}
		// Every folded job joins its root's status index whether it landed or not: a
		// failed or abstained run's usage is part of the day's spend too (#1088).
		indexDirs = append(indexDirs, jobDir)

		switch state {
		case "mismatch":
			mismatch++
			writeSeen(in.Root, c, "mismatch")
		case "abstain":
			abstain++
			retried++
			refusal := lastRefusal(filepath.Join(jobDir, "harness.log"))
			writeSeen(in.Root, c, "retry")
			lines = append(lines, fmt.Sprintf("HARVEST RETRY label=%s card=%s: %s",
				field(c.Label), field(c.Card), oneline.Escape(refusal)))
			appendRetry(in.Root, c, refusal)
		case "elsewhere":
			elsewhere++
			fmt.Fprintf(in.Stderr, "HARVEST NOTE label=%s: no job directory under %s; this card ran somewhere else (harvest it from the bench: nova-pulse harvest --bench <name> --root <the bench's swarm root>)\n",
				field(c.Label), field(in.Root))
		case "refused":
			refused++
			writeSeen(in.Root, c, "refused")
			fmt.Fprintf(in.Stderr, "HARVEST REFUSED label=%s: fix card with no red: line and no test file in its diff (add the red test output before the fix)\n", field(c.Label))
		case "done":
			// THE KEY-SHAPE SCAN COMES FIRST (#1814), before the typed decision and long
			// before any push: the two things a harvest publishes are the card's commits
			// and its RESULT.md, copied into the PR body, and neither leaves this machine
			// until both have been read for the shape of a key. A hit refuses, quarantines
			// the job and writes the HUMAN line, and prints nothing it matched
			// (harvest_secret.go).
			findings, scanErr := secretScan(jobDir, resultLines)
			if scanErr != nil {
				refused++
				writeSeen(in.Root, c, "refused")
				fmt.Fprintln(in.Stderr, secretScanRefusalLine("harvest", c.Label, scanErr))
				continue
			}
			if len(findings) > 0 {
				refused++
				writeSeen(in.Root, c, "refused")
				secretRefusal{Site: "harvest", Label: c.Label, JobDir: jobDir,
					QuarantineRoot: in.Root, HumanDir: in.Root, Out: in.Stderr}.refuse(findings)
				continue
			}
			done++
			cardStart := in.Now()
			// The accept gate is the harvest's default step, between rule 12's line-1
			// verify and its push (SPEC-TOOLWORK §1 rule 1). There is no --no-gate: a
			// kind that declares none is skipped and SAYS so on its row.
			g := in.runGate(jobDir, c.Card, c.Label)
			oc := outcome{
				Label: c.Label, Kind: cardKindName(c.Card), Gather: "done",
				Accept: g.verdict, Reason: g.reason, Class: "-", Conf: "-",
				Head: g.head, Base: short12(g.base), Bench: in.GateBench,
				Cert: g.cert, Control: g.control, PR: "-",
			}
			finish := func(dir string) {
				oc.Took = fmt.Sprintf("%d", in.Now().Sub(cardStart).Milliseconds())
				writeOutcome(dir, oc)
				appendOutcomeRow(in.Queue, oc)
				recordRouteOutcome(in, c.Label, g.verdict)
			}
			gateTail := " gate=" + field(g.verdict)
			if cls, decided := classForGate(g.verdict); decided {
				// §4 rule 2: a typed judgment over evidence a verb already settled is a
				// paid coin-flip over a known answer. No decide call is made.
				oc.Class, oc.Conf = cls, "-"
				gateTail += fmt.Sprintf(" class=%s conf=- floor=- below=-", field(cls))
			}
			switch g.verdict {
			case "reject":
				rejected++
				gateTail += " reason=" + field(g.reason) + " at=" + field(g.at)
				dir := jobDir
				if g.reason == "secret" {
					// A key in a worker's diff means a key reached a worker: the job is
					// moved aside and a person is told. It is never deleted (§3 rule 6).
					if q := quarantine(in, c, jobDir, g.at); q != "" {
						dir = q
						gateTail += " quarantine=" + field(q)
					}
				}
				writeSeen(in.Root, c, "rejected")
				appendRetry(in.Root, c, "accept reject reason="+g.reason+" at="+g.at)
				retried++
				lines = append(lines, fmt.Sprintf("HARVEST REJECT label=%s kind=%s%s",
					field(c.Label), field(oc.Kind), gateTail))
				finish(dir)
				continue
			case "abstain":
				benched++
				gateTail += " reason=" + field(g.reason)
				// The bench could not judge it: nothing is pushed and nothing is
				// requeued, and unless the kind was merely paused it is one line for a
				// person in <root>/bench.tsv.
				appendBench(in, c, g.reason)
				lines = append(lines, fmt.Sprintf("HARVEST ABSTAIN label=%s kind=%s%s",
					field(c.Label), field(oc.Kind), gateTail))
				finish(jobDir)
				continue
			}
			// The typed decision is asked only where the gate did not decide: fixed and
			// failed push as today, no-change and already-fixed push nothing and are
			// marked harvested, off-branch pushes nothing and prints the remedy, and
			// anything below the floor leaves today's path exactly as it was.
			classTail := ""
			if in.Decide && oc.Class == "-" {
				class := in.decideClass(jobDir, branch, resultLines)
				classTail = " " + classFields(class, in.Floor)
				oc.Class, oc.Conf = class.kind, fmt.Sprintf("%.2f", class.conf)
				switch class.kind {
				case "no-change", "already-fixed":
					writeSeen(in.Root, c, "done")
					line := fmt.Sprintf("HARVEST SKIP label=%s%s%s", field(c.Label), gateTail, classTail)
					if class.kind == "already-fixed" {
						line += " test=" + field(class.test)
					}
					lines = append(lines, line)
					finish(jobDir)
					continue
				case "off-branch":
					lines = append(lines, fmt.Sprintf("HARVEST SKIP label=%s%s%s remedy=%s",
						field(c.Label), gateTail, classTail, "push the commits to the branch named on the RESULT.md BRANCH line, or cut a card for the branch they belong to"))
					finish(jobDir)
					continue
				}
			}
			// A RESULT.md is a report, never an instruction (SPEC-SWARM:40-44). The
			// REPO and BRANCH lines on it are a worker's claim about where its work
			// belongs, and harvest used to form https://github.com/<REPO>.git from
			// that claim and push there -- a RESULT naming a repo nobody in this
			// pulse has ever heard of opened a draft PR on it (issue #1824).
			if err := allowedPush(in, jobDir, c, repo, branch); err != nil {
				refused++
				writeSeen(in.Root, c, "refused")
				fmt.Fprintf(in.Stderr, "HARVEST REFUSED label=%s: %s\n", field(c.Label), oneline.Err(err))
				continue
			}
			// WHERE this pushes is resolveDestination's answer, and the answer is the
			// LAUNCH RECORD -- c.Card, the card file the pulse cut before this worker
			// existed -- or, for a bare swarm root that has no card, the `--clone` the
			// operator typed. The RESULT's REPO line and the job clone's own `origin`
			// are both things the worker writes, and both are only ever compared
			// against it (Johnny's HOLDs of #1809 at 8bfa4020 and at 7f692ef6).
			// Resolved here so the refusal is counted and named; push and openPR
			// resolve it again for themselves, because a leaf handed a URL is a leaf
			// with no rule.
			d, derr := managerDispatch(c.Card, in.Clones)
			if derr != nil {
				refused++
				writeSeen(in.Root, c, "refused")
				fmt.Fprintf(in.Stderr, "HARVEST REFUSED repo-unknown card=%s: %s\n", field(c.Label), oneline.Err(derr))
				continue
			}
			dest, err := resolveDestination(c.Label, d, jobDir, repo)
			if err != nil {
				refused++
				writeSeen(in.Root, c, "refused")
				fmt.Fprintf(in.Stderr, "%s\n", oneline.Err(err))
				continue
			}
			// A gated card is pushed by the sha the gate judged, and only if the job's
			// HEAD is still that object: a head that moved under the gate is a verdict
			// on something that is no longer there, and it is not the card's fault
			// either, so nothing is pushed and nobody is charged.
			judged := g.judgedHead()
			if g.verdict == "ok" && judged == "" {
				benched++
				oc.Accept, oc.Reason, oc.Class, oc.Conf = "abstain", "head-moved", "-", "-"
				fmt.Fprintf(in.Stderr, "HARVEST NOTE label=%s: the job's HEAD moved while the gate ran (gate judged %s, the clone is at %s); nothing pushed\n",
					field(c.Label), field(g.head), field(short12(g.sha)))
				lines = append(lines, fmt.Sprintf("HARVEST ABSTAIN label=%s kind=%s gate=abstain reason=head-moved",
					field(c.Label), field(oc.Kind)))
				finish(jobDir)
				continue
			}
			if err := push(in, jobDir, c.Card, repo, branch, c.Label, judged); err != nil {
				fmt.Fprintf(in.Stderr, "HARVEST NOTE push failed label=%s: %s\n", field(c.Label), oneline.Err(err))
				finish(jobDir)
				continue
			}
			pushed++
			// An accepted card's PR body opens with the line the gate printed, so the
			// first thing a reader sees is the verdict and not the worker's prose.
			body := resultLines
			if g.line != "" {
				body = append([]string{g.line, ""}, resultLines...)
			}
			// WHERE it opens stays c.Card's answer and never a URL this loop hands
			// down: openPR resolves the destination for itself, because a leaf handed a
			// URL is a leaf with no rule (#1809, Johnny's HOLDs at 8bfa4020, 7f692ef6).
			pr, err := openPR(in, jobDir, c.Card, repo, c.Label, branch, body)
			if err != nil {
				fmt.Fprintf(in.Stderr, "HARVEST NOTE pr failed label=%s: %s\n", field(c.Label), oneline.Err(err))
				finish(jobDir)
				continue
			}
			prs++
			oc.PR = fmt.Sprintf("%d", pr)
			// The line and the next-card record name the RESOLVED repository, never
			// the RESULT's claim: an operator reading HARVEST PR is reading where the
			// branch actually went. The gate's verdict rides on the same line, so one
			// row says both where it went and what judged it.
			lines = append(lines, fmt.Sprintf("HARVEST PR repo=%s pr=%d label=%s branch=%s%s%s",
				field(dest.repo), pr, field(c.Label), field(branch), gateTail, classTail))
			appendNext(in.Root, dest.repo, pr, c.Label)
			finish(jobDir)
		}
	}

	// The folded jobs are appended to the root's status index once, so the next status tick
	// reads the index and never re-opens a job's usage.tsv (#1088). run drives this same
	// Harvest through the seam, so this is the run path's append too.
	appendStatusIndex(in.Root, indexDirs)

	usd := readUSD(filepath.Join(in.Root, "pulses", in.ID+".packet"))

	grouped := bound(in.Stdout, in.Max)
	for _, l := range lines {
		grouped.Line(l)
	}
	// A local harvest drains --launched too when it is named: the lane of a card whose
	// job under this root has finished is released here, not only by `manager`.
	drained := drainLaunched(in, localJobStates(in.Root), grouped)
	grouped.More()

	code := 0
	result := "OK"
	if mismatch > 0 || abstain > 0 || refused > 0 || rejected > 0 || benched > 0 {
		code = 1
	}
	tail := ""
	if strings.TrimSpace(in.Launched) != "" {
		tail = fmt.Sprintf(" drained=%d", drained)
	}
	fmt.Fprintf(in.Stdout, "HARVEST %s id=%s done=%d pushed=%d prs=%d abstain=%d rejected=%d benched=%d mismatch=%d refused=%d retry=%d elsewhere=%d usd=%s took=%s%s\n",
		result, field(in.ID), done, pushed, prs, abstain, rejected, benched, mismatch, refused, retried, elsewhere, usd,
		in.Now().Sub(started).Round(time.Millisecond), tail)

	// Rule 15: harvest pulses again, queue first. The PULSE line (or PULSE POOL EMPTY) is
	// harvest's own last line.
	if rc := relaunch(in); rc != 0 && code == 0 {
		code = rc
	}
	return code
}

func bound(w io.Writer, max int) *boundedList {
	return &boundedList{w: w, max: max}
}

// boundedList mirrors bounded.List's cap for the HARVEST event lines without a package cycle.
type boundedList struct {
	w     io.Writer
	max   int
	shown int
	total int
}

func (l *boundedList) Line(line string) {
	l.total++
	if l.max > 0 && l.shown >= l.max {
		return
	}
	fmt.Fprintf(l.w, "%s\n", oneline.Escape(line))
	l.shown++
}

// More prints the MORE line when lines were elided.
func (l *boundedList) More() {
	if l.max > 0 && l.total > l.shown {
		fmt.Fprintf(l.w, "HARVEST MORE kind=event shown=%d total=%d use --max 0 to show all\n", l.shown, l.total)
	}
}

// readCards reads cards.tsv (label<TAB>slot<TAB>model<TAB>card-path) into rows.
func readCards(path string) ([]CardRow, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s (harvest folds the cards cut by launch; run: nova-pulse cut)", path)
	}
	var out []CardRow
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		p := strings.Split(line, "\t")
		if len(p) != 4 {
			return nil, fmt.Errorf("%s wants label<TAB>slot<TAB>model<TAB>card-path, got %d fields", path, len(p))
		}
		out = append(out, CardRow{Label: p[0], Slot: p[1], Model: p[2], Card: p[3]})
	}
	return out, nil
}

// discoverRootCards folds a bare swarm root that no cards.tsv was cut into: one
// card per <root>/<slot>/jobs/<label>/RESULT.md, whoever put it there. With no
// cards.tsv to name a contract, the RESULT.md's own line 1 is the contract
// (rule 11), so the card path points at the result; the model is unknown, and no
// pro-only red: gate can be applied. The slot name is the directory above jobs/.
func discoverRootCards(root string) []CardRow {
	slots, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var out []CardRow
	for _, s := range slots {
		if !s.IsDir() {
			continue
		}
		jobsDir := filepath.Join(root, s.Name(), "jobs")
		jobs, err := os.ReadDir(jobsDir)
		if err != nil {
			continue
		}
		for _, j := range jobs {
			if !j.IsDir() {
				continue
			}
			result := filepath.Join(jobsDir, j.Name(), "RESULT.md")
			if _, err := os.Stat(result); err != nil {
				continue
			}
			out = append(out, CardRow{Label: j.Name(), Slot: s.Name(), Model: "", Card: result})
		}
	}
	return out
}

// jobDir is a card's job directory under the root: <root>/<slot>/jobs/<label>.
func jobDir(root, slot, label string) string {
	if slot == "" || slot == "-" {
		slot = "0"
	}
	return filepath.Join(root, slot, "jobs", label)
}

// cardContract is line 1 of a card's text file, the contract line its RESULT must equal.
func cardContract(cardPath string) string {
	if cardPath == "" {
		return ""
	}
	raw, err := os.ReadFile(cardPath)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmpty(strings.Split(string(raw), "\n")))
}

// classify reads a card's RESULT.md and returns its disposition and the push details.
// done -> pushed unless the branch is main or a pro card lacks a red: line (refused).
func classify(jobDir string, c CardRow, contract string) (state, branch, repo string, resultLines []string) {
	raw, err := os.ReadFile(filepath.Join(jobDir, "RESULT.md"))
	if err != nil {
		return "abstain", "", "", nil
	}
	return classifyResult(c, contract, string(raw))
}

// classifyResult folds one RESULT.md body by the card's own two lines -- line 1
// the contract, line 2 the verdict -- and by the BRANCH line.
func classifyResult(c CardRow, contract, body string) (state, branch, repo string, resultLines []string) {
	norm := strings.ReplaceAll(body, "\r\n", "\n")
	lines := strings.Split(norm, "\n")
	// PREFIX, not equality (issue #1823). The card generator can truncate the issue
	// title, so the card's contract line may be a PREFIX of the RESULT.md line 1 rather
	// than the whole of it -- docs/SPEC-SWARM.md:1482-1491, and `nova-swarm batch`'s own
	// gather has scored it that way all along. Harvest compared with != and called a card
	// the batch had already scored `done` a mismatch, so it was never pushed. Trailing
	// spaces are trimmed on both sides first, exactly as the spec words it.
	line1 := strings.TrimSpace(firstNonEmpty(lines))
	want := strings.TrimRight(strings.TrimSpace(contract), " \t")
	if want == "" || !strings.HasPrefix(strings.TrimRight(line1, " \t"), want) {
		return "mismatch", "", "", lines
	}
	line2 := ""
	if len(lines) > 1 {
		line2 = strings.TrimSpace(lines[1])
	}
	if strings.HasPrefix(line2, "ABSTAIN") {
		return "abstain", "", "", lines
	}
	if strings.HasPrefix(line2, "BLOCKED") {
		return "mismatch", "", "", lines
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "BRANCH ") {
			branch = strings.TrimSpace(strings.TrimPrefix(t, "BRANCH "))
		}
		if strings.HasPrefix(t, "REPO ") {
			repo = strings.TrimSpace(strings.TrimPrefix(t, "REPO "))
			repo = strings.TrimPrefix(repo, "github.com/")
		}
	}
	if branch == "" || branch == "main" || branch == "master" {
		return "mismatch", branch, repo, lines
	}
	if c.Model == "pro" && !hasRedLine(lines) {
		return "refused", branch, repo, lines
	}
	return "done", branch, repo, lines
}

// classifyPool folds the pool layout beside the slot layout: launch admits cards
// into <root>/pool and run finalizes each task's published report to
// pool/reports/<id>/RESULT.md while the task file sits in
// pool/{done,failed}/<id>.task. The task id maps back to its card label through
// the task file's own label line -- line 1, the card's RESULT contract line --
// or the sidecar's label written by nova-swarm add --label. A task in failed/
// whose RESULT.md exists with a first line equal to the card's RESULT line is
// harvested as a result, not a failure: done/ is read first, failed/ second,
// latest task first. It returns "" when no pool task carries this card's label
// with a published report.
func classifyPool(root string, c CardRow, contract string) (state, branch, repo string, resultLines []string, dir string) {
	pool := filepath.Join(root, "pool")
	for _, st := range []string{"done", "failed"} {
		for _, id := range poolTaskIDs(pool, st, c.Label) {
			raw, err := os.ReadFile(filepath.Join(pool, "reports", id, "RESULT.md"))
			if err != nil {
				continue
			}
			state, branch, repo, resultLines := classifyResult(c, contract, string(raw))
			return state, branch, repo, resultLines, poolPushDir(pool, st, id)
		}
	}
	return "", "", "", nil, ""
}

// poolTaskIDs returns the task ids in pool/<state>/ whose task file's label line
// or sidecar label names the card, latest first.
func poolTaskIDs(pool, state, label string) []string {
	entries, err := os.ReadDir(filepath.Join(pool, state))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".task") {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".task")
		if poolTaskLabel(pool, state, id) == label {
			out = append(out, id)
		}
	}
	// ReadDir sorts by name and an id begins with its UTC stamp: reverse for latest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// poolTaskLabel is the card label a pool task carries: the label on the task
// file's own first line (the card's RESULT contract line), else the sidecar's
// label written by nova-swarm add --label.
func poolTaskLabel(pool, state, id string) string {
	if raw, err := os.ReadFile(filepath.Join(pool, state, id+".task")); err == nil {
		norm := strings.ReplaceAll(string(raw), "\r\n", "\n")
		if label := contractLabel(firstNonEmpty(strings.Split(norm, "\n"))); label != "" {
			return label
		}
	}
	if raw, err := os.ReadFile(filepath.Join(pool, state, id+".json")); err == nil {
		var sc struct {
			Label string `json:"label"`
		}
		if json.Unmarshal(raw, &sc) == nil {
			return sc.Label
		}
	}
	return ""
}

// contractLabel is the label on a card's RESULT contract line: the word after RESULT.
func contractLabel(line string) string {
	f := strings.Fields(strings.TrimSpace(line))
	// Both spellings are read: `cut --kind` writes `RESULT: CARD-<n> ...` and a hand-cut
	// card writes `RESULT <label> ...`. A gate that knew only the second keyed every line
	// it printed on an empty label for every card of a real pulse (T06a, #1651). The colon
	// is the separator, not part of the token.
	if len(f) >= 2 && (f[0] == "RESULT" || f[0] == "RESULT:") {
		return f[1]
	}
	return ""
}

// poolPushDir is where harvest pushes a pool card's branch from: the job's own
// directory while the sidecar still names one on disk, else the retained report
// directory.
func poolPushDir(pool, state, id string) string {
	if raw, err := os.ReadFile(filepath.Join(pool, state, id+".json")); err == nil {
		var sc struct {
			Job string `json:"job"`
		}
		if json.Unmarshal(raw, &sc) == nil && sc.Job != "" {
			if fi, err := os.Stat(sc.Job); err == nil && fi.IsDir() {
				return sc.Job
			}
		}
	}
	return filepath.Join(pool, "reports", id)
}

func hasRedLine(lines []string) bool {
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "red:") || strings.HasPrefix(t, "red ") {
			return true
		}
	}
	return false
}

func firstNonEmpty(lines []string) string {
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			return l
		}
	}
	return ""
}

// lastRefusal is the last line of a harness log carrying a refusal word, else its last line.
func lastRefusal(logPath string) string {
	raw, err := os.ReadFile(logPath)
	if err != nil {
		return "-"
	}
	lines := []string{}
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		lines = append(lines, strings.TrimSpace(l))
	}
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.ToLower(lines[i])
		if strings.Contains(l, "permission") || strings.Contains(l, "denied") || strings.Contains(l, "refused") {
			return oneline.Cap(lines[i], 200)
		}
	}
	if len(lines) == 0 {
		return "-"
	}
	return oneline.Cap(lines[len(lines)-1], 200)
}

// push runs git push <resolved url> <branch>:<branch> from the job's clone; never a bare
// git push, and never a URL its caller formed.
//
// `record` is the launch record (the card file the pulse cut) and it ANSWERS; `dir` is the
// job's own clone and `claimed` the RESULT.md's REPO line, and those two are the worker's,
// so they are compared and never read. The leaf resolves rather than taking a URL, so that
// the rule holds whichever caller reaches it (Johnny's HOLD of #1809: a rule implemented
// once per caller is as many rules as there are callers).
// push runs git push <url> <src>:refs/heads/<branch> from the job's clone; never a bare
// git push. The url is the RESOLVER's answer -- the launch record's repository, never the
// RESULT's claim (#1809, Johnny's holds at 8bfa4020 and 7f692ef6). src is the SHA the
// accept gate judged when there is one: `branch:branch` re-resolves the branch in the
// worker's clone at push time, and a worker that commits again between the verdict and
// the push would get that commit published under a verdict that never saw it (the red
// team of 98e3f3a9, item 9).
func push(in HarvestInput, dir, record, claimed, branch, label, sha string) error {
	// The branch rule, at the push itself and not only at the caller that decided to
	// push (Johnny's hold on #1809). One implementation, every path.
	if err := mustBranchPrefix(branch); err != nil {
		return err
	}
	d, derr := managerDispatch(record, in.Clones)
	if derr != nil {
		return derr
	}
	dest, err := resolveDestination(label, d, dir, claimed)
	if err != nil {
		return err
	}
	src := branch
	if fullSHA.MatchString(sha) {
		src = sha
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "push", dest.url, src+":refs/heads/"+branch)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", oneline.Err(err), oneline.Cap(string(out), 200))
	}
	return nil
}

// openPR opens a draft PR (or updates an existing one) whose body is the RESULT.md lines,
// capped at MaxBodyBytes. It returns the PR number.
func openPR(in HarvestInput, dir, record, claimed, label, branch string, resultLines []string) (int, error) {
	if err := mustBranchPrefix(branch); err != nil {
		return 0, err
	}
	// The repository the pull request is opened on is the resolver's answer -- the launch
	// record's, not the job clone's and not the RESULT's -- named explicitly with -R
	// rather than left to whatever remote gh infers from the working directory, which on
	// this path IS the worker's clone.
	d, derr := managerDispatch(record, in.Clones)
	if derr != nil {
		return 0, derr
	}
	dest, err := resolveDestination(label, d, dir, claimed)
	if err != nil {
		return 0, err
	}
	body := strings.Join(resultLines, "\n")
	if len(body) > in.MaxBodyBytes {
		body = body[:in.MaxBodyBytes]
	}
	ctx, cancel := context.WithTimeout(context.Background(), childTimeout)
	defer cancel()

	update := exec.CommandContext(ctx, "gh", "pr", "view", branch, "-R", dest.repo, "--json", "number")
	update.Dir = dir
	if _, err := update.CombinedOutput(); err == nil {
		// An existing PR is updated in place; the number is re-read from the view output.
		edit := exec.CommandContext(ctx, "gh", "pr", "edit", branch, "-R", dest.repo, "--body-file", "-")
		edit.Dir = dir
		edit.Stdin = strings.NewReader(body)
		if _, err := edit.CombinedOutput(); err != nil {
			return 0, err
		}
		view2 := exec.CommandContext(ctx, "gh", "pr", "view", branch, "-R", dest.repo, "--json", "number")
		view2.Dir = dir
		out, err := view2.CombinedOutput()
		if err != nil {
			return 0, err
		}
		return parsePRNumber(string(out)), nil
	}

	create := exec.CommandContext(ctx, "gh", "pr", "create", "-R", dest.repo, "--draft", "--head", branch, "--title", label, "--body-file", "-")
	create.Dir = dir
	create.Stdin = strings.NewReader(body)
	out, err := create.CombinedOutput()
	if err != nil {
		return 0, err
	}
	return parsePRNumber(string(out)), nil
}

func parsePRNumber(s string) int {
	// Accept a trailing /pull/<n> or a bare trailing number.
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	if i := strings.LastIndex(s, "/pull/"); i >= 0 {
		n, err := strconv.Atoi(strings.TrimSpace(s[i+len("/pull/"):]))
		if err == nil {
			return n
		}
	}
	fields := strings.Fields(s)
	if len(fields) > 0 {
		if n, err := strconv.Atoi(fields[len(fields)-1]); err == nil {
			return n
		}
	}
	return 0
}

// readUSD parses the usd token from a saved swarm packet's BATCH line, else "-".
func readUSD(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "-"
	}
	first := strings.SplitN(string(raw), "\n", 2)[0]
	for _, tok := range strings.Fields(first) {
		if v, ok := strings.CutPrefix(tok, "usd="); ok {
			return v
		}
	}
	return "-"
}

// writeSeen appends or updates a seen.tsv row: source<TAB>id<TAB>state.
func writeSeen(root string, c CardRow, state string) {
	path := filepath.Join(root, "seen.tsv")
	line := fmt.Sprintf("%s\t%s\t%s\n", "card", c.Label, state)
	existing, _ := os.ReadFile(path)
	var b strings.Builder
	b.Write(existing)
	b.WriteString(line)
	_ = os.WriteFile(path, []byte(b.String()), 0o644)
}

// appendRetry appends label<TAB>card<TAB>refusal to retry.tsv.
func appendRetry(root string, c CardRow, refusal string) {
	f, err := os.OpenFile(filepath.Join(root, "retry.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%s\n", c.Label, c.Card, refusal)
}

// appendNext appends a read card to next.tsv: pr<TAB>repo#n<TAB>read<TAB>title<TAB>read,
// or template tone for a seed page (SPEC-PULSE rule 13). A seed page is a title
// naming one; the kind stays read either way.
func appendNext(root, repo string, pr int, title string) {
	tmpl := "read"
	if strings.Contains(strings.ToLower(title), "seed") {
		tmpl = "tone"
	}
	f, err := os.OpenFile(filepath.Join(root, "next.tsv"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "pr\t%s#%d\tread\t%s\t%s\n", repo, pr, title, tmpl)
}

const childTimeout = 120 * time.Second
