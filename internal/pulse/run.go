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
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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
	Stdout io.Writer
	Stderr io.Writer

	Now   func() time.Time
	Sleep func(time.Duration)

	// GateEvery is how many ticks apart the gate's own probe runs; 0 is gateEvery. It is a
	// field and not a constant so a bench with a slow gh and a bench with a fixture can
	// each say how often the probe is worth its seconds.
	GateEvery int

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

	ticks, sent, reds, stops, undecided int
	seamed                              map[string]bool
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

	r := &runner{
		in:     in,
		roots:  splitList(in.Roots),
		notes:  bounded.Capped(in.Stderr, in.Max, "RUN", "note", "read "+filepath.Join(in.Queue, "ESCALATE")),
		noted:  loadNoted(in.Queue),
		seamed: map[string]bool{},
	}

	start := in.Now()
	end := start.Add(time.Duration(in.Hours * float64(time.Hour)))
	for {
		r.ticks++
		r.tick()
		if in.Once || !in.Now().Before(end) {
			break
		}
		in.Sleep(in.Tick)
	}
	r.notes.More()
	fmt.Fprintf(in.Stdout, "RUN OK ticks=%d notes=%d reds=%d stops=%d undecided=%d hours=%v\n",
		r.ticks, r.sent, r.reds, r.stops, r.undecided, in.Hours)
	return 0
}

// tick is one turn of the loop: gate, harvest, sweep, reap, refill, launch, one WIDTH line.
// Nothing in it calls a model, and at most one thing in it calls a person.
func (r *runner) tick() {
	var t tickCounts
	t.n = r.ticks
	t.benches = len(r.roots)

	var undecided []Undecided

	// 1. Gate. A red branch is STOP, and STOP skips the work of the tick: a broken branch
	// is stop everything and fix the red (Glenn 2026-09-11, "red means stop").
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
		// 6. Launch.
		if s := r.in.Launcher; s != nil {
			launched, free, err := s.Launch(r.ticks)
			t.launched, t.free = launched, free
			r.stepErr("launch", err)
		} else {
			r.seam("launch", "the shipped nova-pulse launch verb")
		}
	}

	// The one call to a person this tick may make: ONE note, for the first undecided case
	// nobody has been told about, carrying its packet. The rest are counted on the line.
	r.undecided += len(undecided)
	t.undecided = len(undecided)
	if n := r.note(undecided); n {
		t.noted = 1
	}

	fmt.Fprintln(r.in.Stdout, t.line())
}

// tickCounts is what one tick did, and the WIDTH line is exactly these fields: counts, one
// line, no list (SPEC.md's bounded output is the coordinator's rule too, G3).
type tickCounts struct {
	n, benches                                             int
	stopped                                                bool
	harvested, swept, requeued, failed, refilled, launched int
	free, undecided, noted                                 int
}

func (t tickCounts) line() string {
	stop := "no"
	if t.stopped {
		stop = "yes"
	}
	return fmt.Sprintf("PULSE WIDTH tick=%d benches=%d stop=%s harvested=%d swept=%d requeued=%d failed=%d refilled=%d launched=%d free=%d undecided=%d noted=%d",
		t.n, t.benches, stop, t.harvested, t.swept, t.requeued, t.failed, t.refilled, t.launched, t.free, t.undecided, t.noted)
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
				_ = os.WriteFile(stopPath, []byte(fmt.Sprintf("MAIN-RED %s repo=%s branch=%s at=%s: revert first, then fix on a branch\n",
					oneline.Field(why), field(r.in.Repo), field(r.in.Branch), r.in.Now().Format(time.RFC3339))), 0o644)
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
	name := filepath.Join(dir, "packets", oneline.Field(subject)+".md")
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
