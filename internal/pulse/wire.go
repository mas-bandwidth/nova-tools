package pulse

// THE SEAMS, WIRED. run.go holds the order of a tick and none of the policy; this file is
// the policy, and it is the hand loop's -- bin/pulse-loop.sh and bin/refill.sh on Rowan's
// bench, which ran the day of 2026-09-16 and which this replaces. Every step is one of the
// verbs a person can run alone: gate (gate.go), harvest (harvest.go), sweep (ledger.go),
// reap (reap.go), refill (cutkind.go, by the rule of refill.sh) and launch (launch.go).
//
// Two rules shape the file:
//
//   - BOUNDED OUTPUT. A verb prints one line. Those lines go to <queue>/pulse.log, which is
//     where the hand loop wrote them and where a person already looks; the console keeps
//     one PULSE WIDTH line per tick and the RUN OK at the end. Six verb lines a tick on a
//     console is the poll this verb was written to end.
//   - NO NETWORK IN A TEST. Every outside thing -- the ci run list, the PR views, the merge
//     queue, the process table, the runner list, the open work -- is an interface with the
//     shipped implementation as its default, so a test hands in a fake and nothing this
//     package does reaches GitHub.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// DefaultCardDeadline is how long a card may run before the reaper collects it: the batch
// deadline the hand loop passed nova-swarm, 25 minutes.
const DefaultCardDeadline = 25 * time.Minute

// defaultModel is the route a card takes when it names none and the queue declares none.
const defaultModel = "opencode/deepseek-v4-flash"

// WorkSource is the open work of a repo: the PRs a read is owed on, and the issues a fix is
// owed on. The shipped one is two bounded gh calls; a test drives a fake.
type WorkSource interface {
	OpenPRs(repo string) ([]OpenPR, error)
	OpenIssues(repo string) ([]OpenIssue, error)
}

// OpenPR is one open pull request, as the refill rule reads it.
type OpenPR struct {
	Number int
	Head   string
	Draft  bool
	Title  string
	Body   string
}

// OpenIssue is one open issue, as the refill rule reads it.
type OpenIssue struct {
	Number int
	Title  string
	Body   string
}

// WiringInput is everything the wired seams need. Every interface field is optional: nil
// takes the shipped implementation, and a test puts a fake in its place.
type WiringInput struct {
	Queue    string
	Roots    string
	Repo     string
	Branch   string
	Deadline time.Duration // the batch deadline; 0 is DefaultCardDeadline
	Timeout  time.Duration // the bound on every child; 0 is childTimeout
	Max      int
	TempGlob string // the swarm test leftovers reap collects; empty is reap's own default
	TempRoot string // the directory the temp glob must sit under; empty derives it from the glob's prefix
	Now      func() time.Time
	Config   func() Config // the tick's configuration, re-read by run.go each tick
	Log      io.Writer     // where each verb's one line goes; nil is <queue>/pulse.log

	Runs      RunSource
	PRs       PRSource
	Enqueuer  Enqueuer
	Procs     ProcessTable
	Runners   RunnerTable
	Restarter RunnerRestarter
	Work      WorkSource
}

// Wiring is the six seams of RunInput at once: it satisfies Gater, Harvester, Sweeper,
// Reaper, Refiller and Launcher, so one value fills every seam of a run.
type Wiring struct {
	in    WiringInput
	roots []string
}

// NewWiring fills in the defaults and returns the seams. It makes no call of its own.
func NewWiring(in WiringInput) *Wiring {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Deadline <= 0 {
		in.Deadline = DefaultCardDeadline
	}
	if in.Timeout <= 0 {
		in.Timeout = childTimeout
	}
	if in.Config == nil {
		in.Config = func() Config { return defaultConfig() }
	}
	if in.Runs == nil {
		in.Runs = NewGHRunSource(in.Timeout)
	}
	if in.PRs == nil {
		in.PRs = GHSource{Timeout: in.Timeout}
	}
	if in.Enqueuer == nil {
		in.Enqueuer = GHEnqueuer{Timeout: in.Timeout}
	}
	if in.Procs == nil {
		in.Procs = OSProcs{Timeout: in.Timeout}
	}
	if in.Runners == nil {
		in.Runners = GHRunners{Timeout: in.Timeout}
	}
	if in.Restarter == nil {
		in.Restarter = ServiceRestarter{Queue: in.Queue, Timeout: in.Timeout}
	}
	if in.Work == nil {
		in.Work = GHWork{Timeout: in.Timeout}
	}
	return &Wiring{in: in, roots: splitList(in.Roots)}
}

// Wire fills every seam of a RunInput this caller left nil. It is what `nova-pulse run`
// does and what a test that wants the real steps does, with fakes in WiringInput.
func Wire(run *RunInput, w *Wiring) {
	if run.Gate == nil {
		run.Gate = w
	}
	if run.Harvest == nil {
		run.Harvest = w
	}
	if run.Sweep == nil {
		run.Sweep = w
	}
	if run.Reap == nil {
		run.Reap = w
	}
	if run.Refill == nil {
		run.Refill = w
	}
	if run.Launcher == nil {
		run.Launcher = w
	}
}

// DefaultConfig is the documented configuration, for a caller that has not read a file yet.
func DefaultConfig() Config { return defaultConfig() }

// defaultConfig is the documented configuration, for a caller that names no file.
func defaultConfig() Config {
	cfg, _ := configFrom(defaultValues(), ConfigFile)
	return cfg
}

// log writes one verb's one line where a person reads it. A line with no line is nothing:
// a verb that printed nothing had nothing to say.
func (w *Wiring) log(lines string) {
	for _, l := range strings.Split(strings.TrimRight(lines, "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		if w.in.Log != nil {
			fmt.Fprintf(w.in.Log, "%s %s\n", w.in.Now().UTC().Format("15:04:05Z"), l)
			continue
		}
		appendLine(filepath.Join(w.in.Queue, "pulse.log"), w.in.Now().UTC().Format("15:04:05Z")+" "+l)
	}
}

// ------------------------------------------------------------------------------ 1. gate

// Gate runs the gate verb over the branch this bench merges into. The verb owns the STOP
// file -- it writes the admission name on line 2, which is what admission.go reads while
// red -- so run.go's own STOP is only ever the fallback for a bench with no gate wired.
func (w *Wiring) Gate(tick int) (bool, string, error) {
	var out, errs bytes.Buffer
	code := Gate(GateInput{
		Repo: w.in.Repo, Branch: w.in.Branch, Queue: w.in.Queue,
		Source: w.in.Runs, Stdout: &out, Stderr: &errs,
	})
	w.log(out.String())
	w.log(errs.String())
	line := strings.TrimSpace(out.String())
	switch code {
	case 1:
		return true, tokenOf(line, "sha="), nil
	case 2:
		return false, "", fmt.Errorf("the gate could not read %s %s: %s", field(w.in.Repo), field(w.in.Branch),
			oneline.Escape(firstOf(errs.String())))
	}
	// Green and held are both "not red": a cancelled or still-running ci run is not a
	// verdict, and reading one as red froze the bench for six minutes (bug 8).
	return false, tokenOf(line, "sha="), nil
}

// --------------------------------------------------------------------------- 2. harvest

// Harvest folds every bench that has cards in flight. A root with no cards.tsv has never
// launched anything and is skipped rather than refused.
func (w *Wiring) Harvest(tick int) (int, []Undecided, error) {
	done := 0
	var undecided []Undecided
	for _, root := range w.roots {
		if _, err := os.Stat(filepath.Join(root, "cards.tsv")); err != nil {
			continue
		}
		var out, errs bytes.Buffer
		Harvest(HarvestInput{
			ID: fmt.Sprintf("tick-%d", tick), Root: root, Max: w.in.Max,
			Stdout: &out, Stderr: &errs, Now: w.in.Now,
		})
		w.log(out.String())
		w.log(errs.String())
		for _, l := range strings.Split(out.String(), "\n") {
			switch {
			case strings.HasPrefix(l, "HARVEST OK "), strings.HasPrefix(l, "HARVEST INCOMPLETE "):
				done += atoiField(l, "done=")
			case strings.HasPrefix(l, "HARVEST RETRY "):
				// A card that came back without its contract line is a case for a rule, and
				// the refusal line is the whole evidence of it.
				ref := tokenOf(l, "label=")
				refusal := l[strings.Index(l, ":")+1:]
				undecided = append(undecided, Undecided{
					Case: kindOfRefusal(refusal), Ref: ref, Result: []string{l}, Refusal: strings.TrimSpace(refusal),
				})
			}
		}
	}
	return done, undecided, nil
}

// ----------------------------------------------------------------------------- 3. sweep

// Sweep walks the approvals ledger: green, undrafted, unheld approvals are enqueued once,
// a moved head is marked stale, and a merged PR closes its row and writes the MERGED record
// `status --oneline` counts.
func (w *Wiring) Sweep(tick int) (int, error) {
	var out, errs bytes.Buffer
	code := Sweep(SweepInput{
		Repo: w.in.Repo, Queue: w.in.Queue, Source: w.in.PRs, Enqueuer: w.in.Enqueuer,
		Now: w.in.Now, Stdout: &out, Stderr: &errs,
	})
	w.log(out.String())
	w.log(errs.String())
	if code != 0 {
		return 0, fmt.Errorf("%s", oneline.Escape(firstOf(errs.String())))
	}
	line := strings.TrimSpace(out.String())
	return atoiField(line, "enqueued=") + atoiField(line, "stale=") + atoiField(line, "closed="), nil
}

// ------------------------------------------------------------------------------ 4. reap

// Reap collects what the benches leak, and restarts a runner stuck busy with nothing
// running (rule E2). The counts are the reap verb's own line.
func (w *Wiring) Reap(tick int) (int, int, []Undecided, error) {
	var out, errs bytes.Buffer
	code := Reap(ReapInput{
		Roots: w.in.Roots, Queue: w.in.Queue, Deadline: w.in.Deadline,
		Procs: w.in.Procs, Repo: w.in.Repo, Runners: w.in.Runners, Restarter: w.in.Restarter,
		TempGlob: w.in.TempGlob, TempRoot: w.in.TempRoot,
		Now: w.in.Now, Stdout: &out, Stderr: &errs,
	})
	w.log(out.String())
	w.log(errs.String())
	if code != 0 {
		return 0, 0, nil, fmt.Errorf("%s", oneline.Escape(firstOf(errs.String())))
	}
	line := strings.TrimSpace(out.String())
	return atoiField(line, "requeued="), atoiField(line, "failed="), nil, nil
}

// ---------------------------------------------------------------------------- 5. refill

// Refill is the rule of bin/refill.sh, in the tool: a READ card for every open non-draft PR
// in <queue>/WORKSET with no card at its current head, and a FIX card for every WORKSET
// issue with no open PR naming it and no card pending or launched for it, the prior attempts
// named so the worker fixes the prompt's failure instead of repeating it (Glenn: fix the
// prompt, not retry). Every card is cut by `cut --kind`, which is the only numberer.
//
// It runs on the configured cadence and only while the pool is under the floor -- the floor
// being every slot on every bench, because a queue deeper than the benches is a queue that
// went stale before it ran. WORKSET is the whole admission: an issue outside it is work
// nobody asked for (bug: 25 reads refused by mistake at 15:20Z, and the answer was the
// WORKSET, not a wider net).
func (w *Wiring) Refill(tick int) (int, error) {
	cfg := w.in.Config()
	state, err := LoadState(w.in.Queue)
	if err != nil {
		return 0, err
	}
	cadence := cfg.RefillCadence
	if cadence < 1 {
		cadence = 1
	}
	if state.RefillTick != 0 && tick-state.RefillTick < cadence {
		return 0, nil
	}
	floor := 0
	for _, b := range Benches {
		floor += cfg.Slots[b]
	}
	pending := cardsIn(filepath.Join(w.in.Queue, "pending"))
	if len(pending) >= floor {
		return 0, nil
	}
	state.RefillTick = tick
	if err := state.Save(w.in.Queue); err != nil {
		return 0, err
	}

	workset := readWorkset(w.in.Queue)
	if len(workset) == 0 {
		w.log("REFILL WORKSET-EMPTY reads=0 fixes=0 (nothing is admitted; name the work in " +
			filepath.Join(w.in.Queue, "WORKSET") + ", and never invent work)")
		return 0, nil
	}

	reads, fixes := w.refillReads(workset), w.refillFixes(workset)
	w.log(fmt.Sprintf("REFILL reads=%d fixes=%d pending=%d floor=%d", reads, fixes,
		len(cardsIn(filepath.Join(w.in.Queue, "pending"))), floor))
	return reads + fixes, nil
}

// refillReads cuts one read card per open non-draft PR in the workset that has no card at
// its current head. The head is in the card's line 1, so a PR that moved is read again and
// a PR that did not is never read twice.
func (w *Wiring) refillReads(workset map[int]bool) int {
	prs, err := w.in.Work.OpenPRs(w.in.Repo)
	if err != nil {
		w.log("REFILL NOTE the open pull requests could not be read: " + oneline.Err(err))
		return 0
	}
	cut := 0
	for _, pr := range prs {
		if pr.Draft || !workset[pr.Number] {
			continue
		}
		head := shortHead(pr.Head)
		if w.cardExists(fmt.Sprintf("PR%d at %s", pr.Number, head), "pending", "launched", "done") {
			continue
		}
		if w.cutKind(CutKindInput{
			Kind: "read", Repo: w.in.Repo, PR: pr.Number, Head: head, Title: pr.Title,
			Out: filepath.Join(w.in.Queue, "pending"), Queue: w.in.Queue,
		}) {
			cut++
		}
	}
	return cut
}

// refillFixes cuts one fix card per workset issue with no open PR naming it and no card in
// flight for it. A card in done/ or failed/ is history and never a block -- 22 admitted
// issues sat uncut behind old cards on 2026-09-16 -- but it IS named on the new card, with
// what became of it, so the worker fixes the prompt rather than repeating it.
func (w *Wiring) refillFixes(workset map[int]bool) int {
	issues, err := w.in.Work.OpenIssues(w.in.Repo)
	if err != nil {
		w.log("REFILL NOTE the open issues could not be read: " + oneline.Err(err))
		return 0
	}
	prs, err := w.in.Work.OpenPRs(w.in.Repo)
	if err != nil {
		w.log("REFILL NOTE the open pull requests could not be read: " + oneline.Err(err))
		return 0
	}
	claimed := map[int]bool{}
	for _, pr := range prs {
		for _, n := range issueRefs(pr.Title + " " + pr.Body) {
			claimed[n] = true
		}
	}
	cut := 0
	for _, issue := range issues {
		if !workset[issue.Number] || claimed[issue.Number] {
			continue
		}
		mark := fmt.Sprintf("%s #%d ", repoShort(w.in.Repo), issue.Number)
		if w.cardExists(mark, "pending", "launched") {
			continue
		}
		body := ""
		if b := strings.TrimSpace(issue.Body); b != "" {
			path := filepath.Join(w.in.Queue, fmt.Sprintf(".refill-%d.md", issue.Number))
			if err := os.WriteFile(path, []byte(b), 0o644); err == nil {
				body = path
				defer os.Remove(path)
			}
		}
		if w.cutKind(CutKindInput{
			Kind: "fix", Repo: w.in.Repo, Issue: issue.Number, Title: issue.Title,
			BodyFile: body, Prior: w.priorAttempts(issue.Number),
			Out: filepath.Join(w.in.Queue, "pending"), Queue: w.in.Queue,
		}) {
			cut++
		}
	}
	return cut
}

// priorAttempts names every card already cut for this issue that came to nothing, and what
// became of it, from the loop's own log. A prior attempt nobody names is an attempt the
// next worker repeats.
func (w *Wiring) priorAttempts(issue int) string {
	mark := fmt.Sprintf("%s #%d ", repoShort(w.in.Repo), issue)
	var out []string
	for _, dir := range []string{"done", "failed"} {
		for _, card := range cardsIn(filepath.Join(w.in.Queue, dir)) {
			raw, err := os.ReadFile(card)
			if err != nil || !strings.Contains(string(raw), mark) {
				continue
			}
			name := strings.TrimSuffix(filepath.Base(card), ".md")
			out = append(out, name+": "+nonEmpty(w.lastLogLine(name), "no PR came of it"))
		}
	}
	if len(out) == 0 {
		return ""
	}
	return strings.Join(out, "; ")
}

// lastLogLine is the last thing the loop said about a card: its refusal, or the PR it
// opened.
func (w *Wiring) lastLogLine(card string) string {
	last := ""
	for _, l := range readLines(filepath.Join(w.in.Queue, "pulse.log")) {
		if !strings.Contains(l, card+" ") && !strings.Contains(l, card+"\t") {
			continue
		}
		if i := strings.Index(l, "REFUSED"); i >= 0 {
			last = strings.TrimSpace(l[i:])
			continue
		}
		if i := strings.Index(l, "HARVEST PR "); i >= 0 {
			last = strings.TrimSpace(l[i:])
		}
	}
	return last
}

// cutKind cuts one card and says whether it was cut. A refusal is one log line and never
// the end of the tick: the next card is still owed.
func (w *Wiring) cutKind(in CutKindInput) bool {
	var out, errs bytes.Buffer
	in.Stdout, in.Stderr = &out, &errs
	if code := CutKind(in); code != 0 {
		w.log(errs.String())
		return false
	}
	w.log(out.String())
	return true
}

// cardExists says whether any card in the named queue directories carries this text. It is
// the dedup of refill.sh, and it is by TEXT and never by memory: a loop that remembers what
// it cut cuts it again at every restart.
func (w *Wiring) cardExists(mark string, dirs ...string) bool {
	for _, dir := range dirs {
		for _, card := range cardsIn(filepath.Join(w.in.Queue, dir)) {
			raw, err := os.ReadFile(card)
			if err != nil {
				continue
			}
			if strings.Contains(string(raw), mark) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------- 6. launch

// Launch fills every free slot on every bench from queue/pending, oldest card first. The
// cards it takes are moved to queue/launched before the batch starts, so a card is in
// exactly one place at every moment -- the stale listing that launched card-892 on both
// benches at once is what a move, and not a copy, prevents.
//
// The STOP admission is launch.go's: while the gate's STOP stands, only a card whose line 1
// names the red launches. That is class C, and it is why the red's own fix card no longer
// goes out by hand.
func (w *Wiring) Launch(tick int) (int, int, error) {
	cfg := w.in.Config()
	launched, free := 0, 0
	for _, root := range w.roots {
		bench := benchOf(root)
		slots, headroom := cfg.Slots[bench], cfg.Headroom[bench]
		if slots <= 0 {
			continue
		}
		openSlots := freeSlots(root, slots, w.in.Now())
		free += openSlots
		take := headroomTake(openSlots, headroom)
		if take <= 0 {
			continue
		}
		pending := cardsIn(filepath.Join(w.in.Queue, "pending"))
		if len(pending) == 0 {
			continue
		}
		if take > len(pending) {
			take = len(pending)
		}
		rows := make([]CardRow, 0, take)
		for _, card := range pending[:take] {
			label := strings.TrimSuffix(filepath.Base(card), ".md")
			moved := filepath.Join(w.in.Queue, "launched", filepath.Base(card))
			if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
				return launched, free, err
			}
			if err := os.Rename(card, moved); err != nil {
				continue // another tick or another hand took it; never launch it twice
			}
			rows = append(rows, CardRow{Label: label, Slot: "-", Model: w.modelFor(moved), Card: moved})
		}
		if len(rows) == 0 {
			continue
		}
		tsv := filepath.Join(root, "cards.tsv")
		if err := writeCardsTSV(tsv, rows); err != nil {
			return launched, free, err
		}
		var out, errs bytes.Buffer
		code := Launch(LaunchInput{
			Cards: tsv, Root: root, Slots: slots, Deadline: strconv.Itoa(int(w.in.Deadline / time.Second)),
			Files: cfg.Files, Tokens: cfg.Tokens,
			QueueDir: w.in.Queue, Stdout: &out, Stderr: &errs, Now: w.in.Now,
		})
		w.log(out.String())
		w.log(errs.String())
		if code != 0 {
			// The batch never started: the cards go back to pending rather than sitting in
			// launched with no job behind them for the reaper to find.
			for _, r := range rows {
				_ = os.Rename(r.Card, filepath.Join(w.in.Queue, "pending", filepath.Base(r.Card)))
			}
			return launched, free, fmt.Errorf("launch on %s: %s", field(bench), oneline.Escape(firstOf(errs.String())))
		}
		launched += len(rows)
	}
	return launched, free, nil
}

// headroomTake caps a tick's take at the bench's headroom -- "fill the machine, never
// oversaturate it". Headroom is a ratio of cores, so it is a decimal (issue #869), and half
// a card is not a card: the cap is the whole cards that fit under it. A headroom of zero is
// no cap at all, which is what it has always been.
func headroomTake(open int, headroom float64) int {
	if headroom <= 0 {
		return open
	}
	if float64(open) <= headroom {
		return open
	}
	return int(math.Floor(headroom))
}

// modelFor is the card's route: its own MODEL: line, else the next route in the queue's
// list for its class, round robin, skipping a benched one. Every provider is its own rate
// limit, so a spread of routes is a wider bench (Glenn 2026-09-16).
func (w *Wiring) modelFor(card string) string {
	raw, err := os.ReadFile(card)
	if err != nil {
		return defaultModel
	}
	text := string(raw)
	for _, l := range strings.Split(text, "\n") {
		if m, ok := strings.CutPrefix(strings.TrimSpace(l), "MODEL: "); ok && strings.TrimSpace(m) != "" {
			return strings.TrimSpace(m)
		}
	}
	list := "ROUTES-code"
	for _, mark := range []string{"TEXT-ONLY", "a reader", "a writer", "spec reader", "spec editor", "docs editor"} {
		if strings.Contains(text, mark) {
			list = "ROUTES-text"
			break
		}
	}
	var routes []string
	for _, l := range readLines(filepath.Join(w.in.Queue, list)) {
		r := strings.TrimSpace(l)
		if r == "" || strings.HasPrefix(r, "#") {
			continue
		}
		if _, err := os.Stat(filepath.Join(w.in.Queue, "ROUTE-BENCHED-"+strings.ReplaceAll(r, "/", "_"))); err == nil {
			continue
		}
		routes = append(routes, r)
	}
	if len(routes) == 0 {
		return defaultModel
	}
	rr := filepath.Join(w.in.Queue, "ROUTE-RR")
	n := 0
	if raw, err := os.ReadFile(rr); err == nil {
		n, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
	}
	n++
	_ = os.WriteFile(rr, []byte(strconv.Itoa(n)+"\n"), 0o644)
	return routes[n%len(routes)]
}

// ---------------------------------------------------------------------------- the shared

// benchOf names the bench a root belongs to, which is how a root takes its slots and its
// headroom from the configuration.
func benchOf(root string) string {
	name := strings.ToLower(filepath.Base(strings.TrimRight(root, string(os.PathSeparator))))
	switch {
	case strings.Contains(name, "space"):
		return "space"
	case strings.Contains(name, "local"):
		return "local"
	default:
		return "studio"
	}
}

// cardsIn lists a queue directory's cards, oldest number first, so the queue is a queue.
func cardsIn(dir string) []string {
	cards, _ := filepath.Glob(filepath.Join(dir, "card-*.md"))
	sort.Slice(cards, func(i, j int) bool { return cardNumber(cards[i]) < cardNumber(cards[j]) })
	return cards
}

func cardNumber(path string) int {
	name := strings.TrimSuffix(filepath.Base(path), ".md")
	n, err := strconv.Atoi(strings.TrimPrefix(name, "card-"))
	if err != nil {
		return 1 << 30
	}
	return n
}

// readWorkset reads <queue>/WORKSET: the issue and PR numbers this bench is allowed to
// work on, one per line, `#` a comment. Nothing outside it is cut.
func readWorkset(queue string) map[int]bool {
	out := map[int]bool{}
	for _, l := range readLines(filepath.Join(queue, "WORKSET")) {
		for _, f := range strings.Fields(strings.TrimSpace(l)) {
			if strings.HasPrefix(f, "#") && len(f) > 1 {
				f = f[1:]
			}
			if n, err := strconv.Atoi(f); err == nil && n > 0 {
				out[n] = true
			} else {
				break // a comment, or prose: the rest of the line is not numbers
			}
		}
	}
	return out
}

// issueRefs is every #<n> in a PR's title and body: the issues it claims.
func issueRefs(s string) []int {
	var out []int
	for _, m := range issueRef.FindAllStringSubmatch(s, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// kindOfRefusal reads a refusal line as one of the seven triage cases, so the note carries
// the right packet. A refusal no word decides is a fence, which is the case for "a person
// has to look".
func kindOfRefusal(s string) string {
	l := strings.ToLower(s)
	switch {
	case strings.Contains(l, "signature"), strings.Contains(l, "contract line"):
		return "signature"
	case strings.Contains(l, "scope"), strings.Contains(l, "workset"):
		return "scope"
	case strings.Contains(l, "docs-only"), strings.Contains(l, "docs only"):
		return "docs-only"
	case strings.Contains(l, "sha"):
		return "nosha"
	case strings.Contains(l, "orphan"), strings.Contains(l, "job dir"):
		return "orphan"
	case strings.Contains(l, "hold"):
		return "hold-line"
	}
	return "fence"
}

func shortHead(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// tokenOf reads `key=value` out of a one-line verb line.
func tokenOf(line, key string) string {
	i := strings.Index(line, key)
	if i < 0 {
		return ""
	}
	rest := line[i+len(key):]
	if j := strings.IndexAny(rest, " \t"); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

func atoiField(line, key string) int {
	n, _ := strconv.Atoi(tokenOf(line, key))
	return n
}

func firstOf(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			return strings.TrimSpace(l)
		}
	}
	return "no reason given"
}

// peekNextCard is the cutter's next number, READ and not taken: the loop mirrors it into
// its own counters so a person reading either file sees one fact, and the number itself
// still comes only from cut, under the cutter's lock.
func peekNextCard(queue string) int {
	state, err := readState(queue)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(strings.TrimSpace(state[nextCardKey]))
	return n
}

// GHWork is the real open work: one `gh pr list` and one `gh issue list`, each bounded.
type GHWork struct{ Timeout time.Duration }

type ghPRRow struct {
	Number  int    `json:"number"`
	IsDraft bool   `json:"isDraft"`
	Head    string `json:"headRefOid"`
	Title   string `json:"title"`
	Body    string `json:"body"`
}

type ghIssueRow struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
}

func (g GHWork) OpenPRs(repo string) ([]OpenPR, error) {
	raw, err := ghJSON(g.Timeout, "pr", "list", "-R", repo, "--state", "open", "--limit", "100",
		"--json", "number,isDraft,headRefOid,title,body")
	if err != nil {
		return nil, err
	}
	var rows []ghPRRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	out := make([]OpenPR, 0, len(rows))
	for _, r := range rows {
		out = append(out, OpenPR{Number: r.Number, Head: r.Head, Draft: r.IsDraft, Title: r.Title, Body: r.Body})
	}
	return out, nil
}

func (g GHWork) OpenIssues(repo string) ([]OpenIssue, error) {
	raw, err := ghJSON(g.Timeout, "issue", "list", "-R", repo, "--state", "open", "--limit", "100",
		"--json", "number,title,body")
	if err != nil {
		return nil, err
	}
	var rows []ghIssueRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		return nil, err
	}
	out := make([]OpenIssue, 0, len(rows))
	for _, r := range rows {
		out = append(out, OpenIssue{Number: r.Number, Title: r.Title, Body: r.Body})
	}
	return out, nil
}

// ghJSON runs one bounded gh call and hands back its json. A gh that does not answer inside
// the bound is an error and never an empty list: an empty list reads as "no open work",
// which is a bench that quietly stops refilling.
func ghJSON(timeout time.Duration, args ...string) (string, error) {
	if timeout <= 0 {
		timeout = childTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	raw, err := exec.CommandContext(ctx, "gh", args...).Output()
	if err != nil {
		return "", fmt.Errorf("gh %s: %w", strings.Join(args, " "), err)
	}
	if strings.TrimSpace(string(raw)) == "" {
		return "[]", nil
	}
	return string(raw), nil
}
