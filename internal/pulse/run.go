package pulse

// Run is G1 of the pit stop's class G (#828): ONE TURN PER DECISION, NEVER PER TICK.
//
// Measured on 2026-09-16 the coordinator's own window was five to six swarms of spend --
// 4,800 turns at an average context of 551k -- and most of those turns were polls: a log
// tail, a run list, a width line. None of them was a decision. The loop that produced them
// was bin/pulse-loop.sh, and this verb is that loop with the model taken out of it: every
// tick is gate, harvest, sweep, reap, refill, launch and ONE WIDTH line, all mechanical,
// and the coordinator hears from it only when a rule cannot decide -- once, as a bus note
// carrying the triage packet of triage.go (G2).
//
// The six steps are SEAMS. Each is a small interface satisfied by the verb or the sibling
// that owns it, so this file holds the ORDER, the counting, the STOP, the one-note rule and
// the WIDTH line, and holds no policy of its own. A nil seam is a step this bench has not
// wired yet: it is named once, on the first tick, and counted zero -- never silently
// skipped, because a step that silently does nothing reads as a quiet day.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tokens"
)

// Undecided is one thing no rule could decide, and the whole evidence for it. It is what
// a step hands back when its rule table has no row: the case kind (one of TriageKinds),
// the thing it is about, the RESULT lines and the harness's refusal line. Nothing else --
// a log, a transcript or a report body in here would be the coordinator's turn again.
type Undecided struct {
	Case    string
	Ref     string
	Result  []string
	Refusal string
}

// key is what makes a note happen once. A case about the same ref is the same decision
// however many ticks it survives, and a loop that noted it every tick would be the poll
// this verb exists to end.
// Each half goes through oneline.Field, so the key is two tokens with no whitespace in
// them: it is written to <queue>/NOTED as one line, and a key an escape rewrote on its way
// to disk is a key that never matches again -- which is a person told the same thing twice
// after every restart.
func (u Undecided) key() string { return oneline.Field(u.Case) + "/" + oneline.Field(u.Ref) }

// Gater is the mechanical gate on the branch this bench merges into: it answers whether
// that branch is red, and the loop does the rest (STOP, the note, the skip). It is a seam:
// the gh probe and the admission gates live on the sibling branch
// rowan/pulse-config-gate-admission. A nil Gater is the STOP file alone.
type Gater interface {
	// Gate reports whether the branch is red this tick, the sha or reason that says so,
	// and an error when the probe itself could not run (never a verdict).
	Gate(tick int) (red bool, why string, err error)
}

// Harvester folds every card whose job has written a RESULT.md. Seam: the shipped
// `nova-pulse harvest` verb, driven per pulse id.
type Harvester interface {
	Harvest(tick int) (done int, undecided []Undecided, err error)
}

// Sweeper retires what is finished: merged PRs, closed issues, stale job directories.
// Seam: rowan/pulse-ledger-reap-cut.
type Sweeper interface {
	Sweep(tick int) (swept int, err error)
}

// Reaper requeues or fails a card whose slot went quiet. Seam: rowan/pulse-ledger-reap-cut.
// A card it can neither requeue nor fail by a rule comes back Undecided.
type Reaper interface {
	Reap(tick int) (requeued, failed int, undecided []Undecided, err error)
}

// Refiller tops the queue up to its floor from the declared sources. Seam:
// rowan/pulse-config-gate-admission (the policy's floor and scope live with the config).
type Refiller interface {
	Refill(tick int) (added int, err error)
}

// Launcher fills every free slot on every bench. Seam: the shipped `nova-pulse launch`.
type Launcher interface {
	Launch(tick int) (launched, free int, err error)
}

// Notifier is the ONE call a tick may make to a person. The real one shells `nova-bus
// send`; with no --bus the loop appends the note to <queue>/ESCALATE, which is the same
// inbox the hand loop wrote to.
type Notifier interface {
	Note(subject, body string) error
}

// RunInput is the run verb, apart from flag parsing, so a test drives it against fake
// directories, a fake clock and stub seams -- no network, no model, no sleep.
type RunInput struct {
	Queue  string // the queue directory: pending, launched, done, failed, STOP, RULES.tsv
	Roots  string // the benches, comma separated
	Repo   string // owner/name, the repo the gate watches
	Branch string // the branch the gate watches
	Hours  float64
	Tick   time.Duration
	Once   bool
	Max    int
	Bus    string // a nova-bus clone; empty sends notes to <queue>/ESCALATE
	As     string // the name notes are sent to
	// Locked says this shift runs inside a caller that already holds the queue's lock (the
	// `loop` verb), so it takes none of its own.
	Locked bool
	Stdout io.Writer
	Stderr io.Writer

	Now   func() time.Time
	Sleep func(time.Duration)

	// GateEvery is how many ticks apart the gate's own probe runs; 0 is gateEvery. It is a
	// field and not a constant so a bench with a slow gh and a bench with a fixture can
	// each say how often the probe is worth its seconds.
	GateEvery int

	// Configured, when set, is handed the configuration after every re-read, so the seams
	// run on the same values this loop does and pulse.toml has ONE reader per tick.
	Configured func(Config)

	// Load is the machine's one-minute load average, the WIDTH line's load= field. It is a
	// seam so a test's line is the same on every machine; nil takes the host's own.
	Load func() int

	Gate     Gater
	Harvest  Harvester
	Sweep    Sweeper
	Reap     Reaper
	Refill   Refiller
	Launcher Launcher
	Notifier Notifier
}

// DefaultTick is the manager's tick (SPEC-PULSE, "Rate and convergence" rule 1).
const DefaultTick = 10 * time.Second

// gateEvery is how often the gate's own probe runs, in ticks: a minute of ticks, which is
// what the hand loop settled on after a red main sat unnoticed for thirty minutes.
const gateEvery = 6

// runner is one shift's state.
type runner struct {
	in    RunInput
	roots []string
	notes *bounded.List

	noted map[string]bool // the (case, ref) keys already sent, loaded from <queue>/NOTED

	// cfg is the tick's configuration, re-read from <queue>/pulse.toml every tick: a value
	// edited between two ticks takes effect on the next one, with no restart and nothing
	// forgotten (class A, bugs 2 and 3).
	cfg Config

	ticks, shift, sent, reds, stops, undecided, gateRuns int
	seamed                                               map[string]bool

	// starve counts the consecutive ticks with an empty pool and an almost empty bench --
	// the script's STARVED escalation, which only `status` still knew how to say.
	starve    int
	starvedAt time.Time
}

// Run holds the loop for --hours and prints one WIDTH line per tick. It returns 0 when the
// shift ended by itself and 2 on a refusal that never started.
func Run(in RunInput) int {
	if in.Now == nil {
		in.Now = func() time.Time { return time.Now().UTC() }
	}
	if in.Sleep == nil {
		in.Sleep = time.Sleep
	}
	if in.Tick <= 0 {
		in.Tick = DefaultTick
	}
	if in.Max == 0 {
		in.Max = bounded.Default
	}
	for _, r := range []struct{ v, name, wants string }{
		{in.Queue, "queue", "the queue directory holding pending, launched, done, failed and the state files"},
		{in.Roots, "roots", "the benches this shift runs on, comma separated"},
	} {
		if strings.TrimSpace(r.v) == "" {
			return refusal(in.Stderr, "RUN", fmt.Errorf("missing --%s; refusing to guess (%s)", r.name, r.wants))
		}
	}
	if in.Hours < 0 {
		return refusal(in.Stderr, "RUN", fmt.Errorf("--hours is 0 or more, got %v (0 with --once runs exactly one tick)", in.Hours))
	}
	if err := os.MkdirAll(in.Queue, 0o755); err != nil {
		return refusal(in.Stderr, "RUN", fmt.Errorf("cannot open the queue: %s (name a writable --queue)", oneline.Err(err)))
	}

	// The counters are a FILE, not a shell variable: a loop restarted to take a change used
	// to lose them, and the gate counter it lost was the reason a green branch stayed
	// stopped for thirty minutes (class A, bug 3). A malformed state file is a refusal and
	// never a zero state.
	state, err := LoadState(in.Queue)
	if err != nil {
		return refusal(in.Stderr, "RUN", err)
	}
	// ONE WRITER PER QUEUE (queuelock.go). A shift moves cards, writes the state file and
	// cuts cards under <queue>/NEXT; two shifts on one queue race all three.
	if !in.Locked {
		lock, err := LockQueue(in.Queue, "run")
		if err != nil {
			return refusal(in.Stderr, "RUN", err)
		}
		defer lock.Release()
	}

	r := &runner{
		in:     in,
		roots:  splitList(in.Roots),
		notes:  bounded.Capped(in.Stderr, in.Max, "RUN", "note", "read "+filepath.Join(in.Queue, "ESCALATE")),
		noted:  loadNoted(in.Queue),
		seamed: map[string]bool{},
		cfg:    defaultConfig(),
	}
	// The tick number carries on across a restart, so the gate's cadence and the log's own
	// numbering are one run of numbers and not one per process.
	r.ticks, r.gateRuns = state.Tick, state.Gate

	start := in.Now()
	end := start.Add(time.Duration(in.Hours * float64(time.Hour)))
	for {
		r.ticks++
		r.shift++
		r.tick()
		if in.Once || !in.Now().Before(end) {
			break
		}
		in.Sleep(in.Tick)
	}
	r.notes.More()
	fmt.Fprintf(in.Stdout, "RUN OK ticks=%d notes=%d reds=%d stops=%d undecided=%d hours=%v\n",
		r.shift, r.sent, r.reds, r.stops, r.undecided, in.Hours)
	return 0
}

// tick is one turn of the loop: gate, harvest, sweep, reap, refill, launch, one WIDTH line.
// Nothing in it calls a model, and at most one thing in it calls a person.
func (r *runner) tick() {
	var t tickCounts
	t.n = r.ticks
	t.benches = len(r.roots)

	// 0. The configuration, re-read. It is not a step of the loop and takes no line unless
	// it moved: a CONFIG line every tick is the poll this verb exists to end.
	r.loadConfig()

	var undecided []Undecided

	// 1. Gate. A red branch is STOP, and STOP skips the work of the tick -- every step but
	// the launcher, which admits the red's own fix card and nothing else (step 6). A broken
	// branch is stop everything and fix the red (Glenn 2026-09-11, "red means stop").
	stopped, red := r.gate(r.ticks)
	if red != nil {
		undecided = append(undecided, *red)
	}
	t.stopped = stopped

	if !stopped {
		// 2. Harvest.
		if h := r.in.Harvest; h != nil {
			done, u, err := h.Harvest(r.ticks)
			t.harvested = done
			undecided = append(undecided, u...)
			r.stepErr("harvest", err)
		} else {
			r.seam("harvest", "the shipped nova-pulse harvest verb")
		}
		// 2b. Fold every root's pool usage into the month's ledger, after harvest. It is
		// mechanical accounting -- no model call, no child -- and it stays silent unless a
		// root's rows moved the ledger, so a tick with nothing new to fold says nothing.
		r.foldLedgers()
		// 3. Sweep.
		if s := r.in.Sweep; s != nil {
			n, err := s.Sweep(r.ticks)
			t.swept = n
			r.stepErr("sweep", err)
		} else {
			r.seam("sweep", "rowan/pulse-ledger-reap-cut")
		}
		// 4. Reap.
		if s := r.in.Reap; s != nil {
			requeued, failed, u, err := s.Reap(r.ticks)
			t.requeued, t.failed = requeued, failed
			undecided = append(undecided, u...)
			r.stepErr("reap", err)
		} else {
			r.seam("reap", "rowan/pulse-ledger-reap-cut")
		}
		// 5. Refill.
		if s := r.in.Refill; s != nil {
			n, err := s.Refill(r.ticks)
			t.refilled = n
			r.stepErr("refill", err)
		} else {
			r.seam("refill", "rowan/pulse-config-gate-admission")
		}
	}
	// 6. Launch, STOPPED OR NOT. While the gate's STOP stands the launcher admits only the
	// cards whose line 1 names the red (admission.go), which is class C: an all-or-nothing
	// STOP stopped the red's own fix card and it went out by hand three times (bug 6).
	// Nothing else runs behind a red -- never pile work onto a red.
	if s := r.in.Launcher; s != nil {
		launched, free, err := s.Launch(r.ticks)
		t.launched, t.free = launched, free
		r.stepErr("launch", err)
	} else {
		r.seam("launch", "the shipped nova-pulse launch verb")
	}

	// The one call to a person this tick may make: ONE note, for the first undecided case
	// nobody has been told about, carrying its packet. The rest are counted on the line.
	r.undecided += len(undecided)
	t.undecided = len(undecided)
	if n := r.note(undecided); n {
		t.noted = 1
	}

	r.width(&t)
	r.writeTicks(t)
	fmt.Fprintln(r.in.Stdout, t.line())
	r.saveState()
}

// writeTicks appends this tick's convergence record -- cards opened (refilled/cut) and
// cards closed (harvested) -- for the queue's stream, so `status` folds the rolling
// two-hour window (SPEC-PULSE, "Status") from a file and never from memory. A queue with
// no cards yet writes the `all` stream; the line is plain tab-separated, not escaped,
// because TICKS is a record file and not an event line.
func (r *runner) writeTicks(t tickCounts) {
	stream := r.queueStream()
	line := fmt.Sprintf("at=%s\tstream=%s\topened=%d\tclosed=%d",
		r.in.Now().UTC().Format(time.RFC3339), oneline.Field(stream), t.refilled, t.harvested)
	f, err := os.OpenFile(filepath.Join(r.in.Queue, "TICKS"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}

// queueStream is the stream this queue's cards belong to: the prefix before the first
// '-' of the first card label found, else `all` on a queue that has cut no cards yet.
func (r *runner) queueStream() string {
	for _, sub := range []string{"pending", "launched", "done"} {
		matches, _ := filepath.Glob(filepath.Join(r.in.Queue, sub, "card-*.md"))
		if len(matches) > 0 {
			return streamOf(filepath.Base(matches[0]))
		}
	}
	return "all"
}

// loadConfig re-reads <queue>/pulse.toml. The last good configuration stands when the file
// is unreadable: a typo in a value is not a reason to change how the bench runs.
func (r *runner) loadConfig() {
	var buf bytes.Buffer
	cfg, err := LoadConfig(r.in.Queue, &buf, r.in.Max)
	if err != nil {
		r.stepErr("config", err)
		return
	}
	r.cfg = cfg
	if r.in.Configured != nil {
		r.in.Configured(cfg)
	}
	// ONE line, and only when a value moved. LoadConfig compares against the snapshot in
	// the queue, not against this process, so a value edited while the loop was down is a
	// change the next tick names -- and a first load on a fresh queue, which changed
	// nothing, says nothing.
	if !strings.Contains(buf.String(), "changed=0") {
		fmt.Fprint(r.in.Stdout, buf.String())
	}
}

// saveState writes the counters this loop owns -- the tick and the gate -- and touches
// nothing else in the file. next_card is the cutter's, taken under the cutter's lock, and a
// loop that wrote back the number it read at the start of the shift would hand out a number
// that is already on a launched card (bug 1, the five overwritten cards).
func (r *runner) saveState() {
	state, err := LoadState(r.in.Queue)
	if err != nil {
		r.stepErr("state", err)
		return
	}
	state.Tick, state.Gate = r.ticks, r.gateRuns
	if n := peekNextCard(r.in.Queue); n > state.NextCard {
		state.NextCard = n
	}
	if err := state.Save(r.in.Queue); err != nil {
		r.stepErr("state", err)
	}
}

// tickCounts is what one tick did, and the WIDTH line is exactly these fields: counts, one
// line, no list (SPEC.md's bounded output is the coordinator's rule too, G3).
type tickCounts struct {
	n, benches                                             int
	stopped                                                bool
	harvested, swept, requeued, failed, refilled, launched int
	free, undecided, noted                                 int

	// THE SEVEN THE SCRIPT CARRIED AND THIS LINE LOST (the manager dogfood, edge 9).
	// bin/pulse-loop.sh's WIDTH answered "is the machine full, and is there work for it" in
	// one line: the load it is under, what is waiting, what is waiting on a PR, what is
	// finished, what failed, the fraction of the bench in use and the cached estimate. The
	// twelve-field line answered only what the tick itself did, so the two questions a
	// person actually asks -- is it saturated, is it starved -- needed `status` and a second
	// window. Every field prints its zero (Glenn: fixed tables write every field).
	//
	// failed-cards= and not failed=: failed= is what THIS TICK's reap failed, and the
	// script's was the standing count of queue/failed. Two counts, two names.
	load                            int
	pending, gated, done, failedDir int
	slots                           int    // every bench's slots added up; util= is (slots-free)/slots
	est                             string // the cached ESTIMATE line, or "-"
	marks                           []string
}

func (t tickCounts) line() string {
	stop := "no"
	if t.stopped {
		stop = "yes"
	}
	line := fmt.Sprintf("PULSE WIDTH tick=%d benches=%d stop=%s harvested=%d swept=%d requeued=%d failed=%d refilled=%d launched=%d free=%d undecided=%d noted=%d load=%d pending=%d gated=%d done=%d failed-cards=%d util=%d/%d est=%s",
		t.n, t.benches, stop, t.harvested, t.swept, t.requeued, t.failed, t.refilled, t.launched, t.free, t.undecided, t.noted,
		t.load, t.pending, t.gated, t.done, t.failedDir, t.slots-t.free, t.slots, nonEmpty(t.est, "-"))
	for _, m := range t.marks {
		line += " " + m
	}
	return line
}

// width fills the seven standing fields and the three markers. It counts DIRECTORIES, which
// is what the script counted: the tick's own numbers say what happened, and these say what
// is there.
func (r *runner) width(t *tickCounts) {
	queue := r.in.Queue
	t.load = r.load()
	// pending= is EVERY pending card, gated ones included, exactly as the script counted
	// it: gated= then says how many of them are waiting on a pull request. (status.go's own
	// pending excludes them, which is the right number for a different question.)
	t.pending = countCards(queue, "pending")
	t.gated = countGated(queue, "pending")
	t.done = countCards(queue, "done")
	t.failedDir = countCards(queue, "failed")
	for _, root := range r.roots {
		t.slots += r.cfg.Slots[benchOf(root)]
	}
	t.est = estField(filepath.Join(queue, "EST"))
	inflight := t.slots - t.free
	switch {
	case t.pending == 0:
		t.marks = append(t.marks, "POOL-EMPTY")
	case inflight < t.slots:
		t.marks = append(t.marks, "UNDER-WIDTH")
	}
	// STARVED: an empty pool AND an almost empty bench, three ticks running. It is the one
	// state nothing mechanical can fix -- there is no work -- so it is the one that reaches
	// a person, and no more than once every ten minutes.
	if t.pending == 0 && inflight < starveFloor {
		r.starve++
	} else {
		r.starve = 0
	}
	if r.starve >= starveTicks {
		t.marks = append(t.marks, "STARVED")
		now := r.in.Now()
		if now.Sub(r.starvedAt) >= starveQuiet {
			r.starvedAt = now
			appendLine(filepath.Join(queue, "ESCALATE"), fmt.Sprintf(
				"%s STARVED in-flight=%d pending=0 for %d ticks: the queue is empty and slots are free; refill to the floor now",
				now.UTC().Format(time.RFC3339), inflight, r.starve))
		}
	}
}

// The STARVED rule, as bin/pulse-loop.sh settled it: fewer than eight cards in flight with
// nothing pending, for three ticks, and never told to a person more than once in ten minutes.
const (
	starveFloor = 8
	starveTicks = 3
	starveQuiet = 10 * time.Minute
)

// estField is the cached estimate as ONE field. The estimate is refreshed in the background
// by `status` and `progress` (progress blocked a tick for fifteen seconds when the loop
// computed it inline), and it arrives as several words: they are joined with commas rather
// than escaped, because est=remaining_cards\x3d12\x20hours\x3d3.5 is a field nobody reads.
func estField(path string) string {
	fields := strings.Fields(firstLine(path))
	if len(fields) == 0 {
		return ""
	}
	return strings.Join(fields, ",")
}

// load is the machine's one-minute load average, through the seam.
func (r *runner) load() int {
	if r.in.Load != nil {
		return r.in.Load()
	}
	return hostLoad()
}

// gate answers two things: is work stopped this tick, and did a red arrive that nobody has
// been told about. A red writes STOP once per red -- keyed on the reason the gate gave, so
// a branch that stays red is one STOP and one note, not one per tick.
func (r *runner) gate(tick int) (stopped bool, red *Undecided) {
	stopPath := filepath.Join(r.in.Queue, "STOP")

	every := r.in.GateEvery
	if every <= 0 {
		every = gateEvery
	}
	// The FIRST tick probes, and every `every` ticks after it. `tick%every == 1` reads the
	// same at every > 1 and is never true at every == 1, which is a gate a test (or a bench
	// that wants a probe per tick) silently never calls.
	if g := r.in.Gate; g != nil && (tick-1)%every == 0 {
		r.gateRuns++
		isRed, why, err := g.Gate(tick)
		r.stepErr("gate", err)
		switch {
		case err != nil:
			// A probe that could not run is not a verdict: the tick proceeds on what the
			// STOP file says, and the note line above says the probe failed.
		case isRed:
			u := Undecided{
				Case: "fence", Ref: oneline.Field(nonEmpty(why, "red")),
				Result:  []string{"the gate reports " + oneline.Escape(why) + " on " + field(r.in.Repo) + " " + field(r.in.Branch)},
				Refusal: "MAIN-RED " + oneline.Escape(why) + ": revert first, then fix on a branch",
			}
			if !r.noted[u.key()] {
				r.reds++
				r.stops++
				// The gate verb writes a STOP of its own, and its line 2 is the admission
				// name the launcher reads while red. Rewriting it here would drop that name
				// and shut the red's own fix card out, so this STOP is only ever the one
				// for a bench whose gate wrote none.
				if !strings.HasPrefix(firstLine(stopPath), StopMark) {
					_ = os.WriteFile(stopPath, []byte(fmt.Sprintf("MAIN-RED %s repo=%s branch=%s at=%s: revert first, then fix on a branch\n",
						oneline.Field(why), field(r.in.Repo), field(r.in.Branch), r.in.Now().Format(time.RFC3339))), 0o644)
				}
				appendLine(filepath.Join(r.in.Queue, "REDS"), fmt.Sprintf("%s\tMAIN-RED\t%s\t%s",
					r.in.Now().Format(time.RFC3339), oneline.Field(r.in.Branch), oneline.Field(why)))
				red = &u
			}
		default:
			// Green, and the STOP this loop wrote is this loop's to clear. A STOP a person
			// wrote says nothing about the branch and is never removed by a machine.
			if strings.HasPrefix(firstLine(stopPath), "MAIN-RED ") {
				_ = os.Remove(stopPath)
				appendLine(filepath.Join(r.in.Queue, "REDS"), fmt.Sprintf("%s\tRESUMED\t%s\t%s",
					r.in.Now().Format(time.RFC3339), oneline.Field(r.in.Branch), oneline.Field(why)))
			}
		}
	}
	if _, err := os.Stat(stopPath); err == nil {
		return true, red
	}
	return false, red
}

// note sends at most ONE bus note, for the first undecided case whose (case, ref) nobody
// has been told about, carrying the G2 decision packet. It reports whether it sent one.
func (r *runner) note(items []Undecided) bool {
	for _, u := range items {
		if r.noted[u.key()] {
			continue
		}
		r.noted[u.key()] = true
		appendLine(filepath.Join(r.in.Queue, "NOTED"), u.key())

		kind := u.Case
		if !KnownTriageKind(kind) {
			kind = "fence"
		}
		p := BuildPacket(r.in.Queue, kind, u.Ref, u.Result, u.Refusal)
		subject := fmt.Sprintf("UNDECIDED %s %s", oneline.Field(u.Case), field(u.Ref))
		if err := r.notifier().Note(subject, p.Card()); err != nil {
			r.notes.Line(fmt.Sprintf("RUN NOTE send failed case=%s: %s", oneline.Field(u.Case), oneline.Err(err)))
			return false
		}
		r.sent++
		return true
	}
	return false
}

func (r *runner) notifier() Notifier {
	if r.in.Notifier != nil {
		return r.in.Notifier
	}
	if strings.TrimSpace(r.in.Bus) != "" {
		return busNotifier{bus: r.in.Bus, as: r.in.As}
	}
	return fileNotifier{path: filepath.Join(r.in.Queue, "ESCALATE")}
}

// stepErr prints one bounded NOTE line for a step that could not run. It is never a
// verdict: a step that failed is a step that did nothing this tick, and the next tick runs.
func (r *runner) stepErr(step string, err error) {
	if err == nil {
		return
	}
	r.notes.Line(fmt.Sprintf("RUN NOTE step=%s: %s", step, oneline.Err(err)))
}

// seam names a step this bench has not wired, ONCE per shift. A nil step counted zero and
// never named would read as a quiet day.
func (r *runner) seam(step, owner string) {
	if r.seamed[step] {
		return
	}
	r.seamed[step] = true
	r.notes.Line(fmt.Sprintf("RUN NOTE seam=%s not wired: %s", step, owner))
}

// foldLedgers folds every root's pool usage into the queue's monthly ledger with
// nova-tokens fold-pool semantics (SPEC-TOKENS), called as a library and never shelled
// out. One FOLD line prints per root whose fold moved the ledger; a fold that changes
// nothing is silent, which is what makes the step idempotent on every tick.
func (r *runner) foldLedgers() {
	month := r.in.Now().UTC().Format("2006-01")
	for _, root := range r.roots {
		pool := filepath.Join(root, "pool")
		if fi, err := os.Stat(pool); err != nil || !fi.IsDir() {
			continue
		}
		groups, tasks, err := tokens.FoldPool(pool, "")
		if err != nil {
			r.stepErr("fold", err)
			continue
		}
		if len(groups) == 0 {
			continue
		}
		ledger := filepath.Join(r.in.Queue, "ledger-"+month+".tsv")
		before, _ := os.ReadFile(ledger)
		if err := tokens.WritePoolLedger(ledger, groups); err != nil {
			r.stepErr("fold", err)
			continue
		}
		after, err := os.ReadFile(ledger)
		if err != nil {
			r.stepErr("fold", err)
			continue
		}
		if !bytes.Equal(before, after) {
			fmt.Fprintf(r.in.Stdout, "FOLD rows=%d tasks=%d ledger=%s\n", len(groups), tasks, oneline.Field(ledger))
		}
	}
}

// loadNoted reads <queue>/NOTED, the keys this queue has already told a person about, so a
// restarted shift does not tell them again.
func loadNoted(queue string) map[string]bool {
	out := map[string]bool{}
	for _, l := range readLines(filepath.Join(queue, "NOTED")) {
		out[strings.TrimSpace(l)] = true
	}
	return out
}

// fileNotifier is the default inbox: one line in <queue>/ESCALATE and the packet beside it,
// which is what the hand loop did and what a person already reads.
type fileNotifier struct{ path string }

func (f fileNotifier) Note(subject, body string) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(filepath.Join(dir, "packets"), 0o755); err != nil {
		return err
	}
	name := filepath.Join(dir, "packets", packetFileName(subject)+".md")
	if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
		return err
	}
	appendLine(f.path, subject+" packet="+name)
	return nil
}

// busNotifier is the real one: one `nova-bus send`, bounded by childTimeout, carrying the
// packet as the note's body.
type busNotifier struct{ bus, as string }

func (b busNotifier) Note(subject, body string) error {
	dir, err := os.MkdirTemp("", "pulse-note")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "note.md")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return err
	}
	args := []string{"send", "--bus", b.bus, "--subject", subject, "--body-file", path}
	if strings.TrimSpace(b.as) != "" {
		args = append(args, "--as", b.as)
	}
	cmd := exec.Command("nova-bus", args...)
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	done := make(chan error, 1)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		return err
	case <-time.After(childTimeout):
		_ = cmd.Process.Kill()
		return fmt.Errorf("nova-bus send did not answer in %s", childTimeout)
	}
}

// packetFileName makes a note subject safe as ONE file name on every OS. oneline.Field
// escapes a space as `\x20`, and on Windows that backslash is a path separator, so the
// packet write failed and ESCALATE stayed empty (the dev Windows leg, 2026-09-16): every
// rune outside [A-Za-z0-9._-] becomes `_`.
func packetFileName(subject string) string {
	var b strings.Builder
	b.Grow(len(subject))
	for _, r := range subject {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
