package pulse

// The simulated day (G1 of #828). The fixture is built here, from small structs, and never
// from files bigger than the claim needs: 200 cards, 40 PRs, two reds and one flake, over a
// day of ticks. What it pins is the thing the class exists for -- the coordinator's turns
// are the DECISIONS and not the ticks -- so the assertion is a count of notes, not a count
// of work.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------- the day, as small structs

// dayCard is one card of the simulated day. A card that is `undecided` is one no rule
// covers, and it is the only kind that may ever cost a note.
type dayCard struct {
	label     string
	undecided string // "" when a rule decided it; else the triage case
}

// dayPR is one PR of the simulated day: swept when it merges.
type dayPR struct {
	n         int
	undecided string
}

// dayRed is one red of the branch the gate watches, by the tick it arrives on.
type dayRed struct {
	tick int
	sha  string
}

// simulatedDay is the whole fixture and every seam of it at once.
type simulatedDay struct {
	cards []dayCard
	prs   []dayPR
	reds  []dayRed
	flake string // the one card that failed for a known flake: a rule decides it, no note

	perTick int // cards harvested per tick

	// what the stubs recorded
	harvested, swept, reaped, refilled, launched int
	gateCalls                                    int
}

func (d *simulatedDay) Gate(tick int) (bool, string, error) {
	d.gateCalls++
	for _, r := range d.reds {
		if r.tick == tick {
			return true, r.sha, nil
		}
	}
	return false, "success", nil
}

func (d *simulatedDay) Harvest(tick int) (int, []Undecided, error) {
	n := 0
	for n < d.perTick && d.harvested < len(d.cards) {
		d.harvested++
		n++
	}
	return n, d.openCards(), nil
}

// openCards is every card no rule decided. The stub hands back the SAME list every tick --
// the undecided case does not go away because a tick passed -- which is exactly what the
// one-note rule has to survive.
func (d *simulatedDay) openCards() []Undecided {
	var out []Undecided
	for _, c := range d.cards {
		if c.undecided == "" {
			continue
		}
		out = append(out, Undecided{
			Case: c.undecided, Ref: c.label,
			Result:  []string{"RESULT " + c.label + " sha=0123456789ab", "ABSTAIN reason=" + c.undecided},
			Refusal: "REFUSED " + c.undecided + " on " + c.label,
		})
	}
	for _, p := range d.prs {
		if p.undecided == "" {
			continue
		}
		out = append(out, Undecided{
			Case: p.undecided, Ref: fmt.Sprintf("pr-%d", p.n),
			Result:  []string{fmt.Sprintf("READ PR%d: HOLD", p.n)},
			Refusal: "HOLD: " + p.undecided,
		})
	}
	return out
}

func (d *simulatedDay) Sweep(tick int) (int, error) {
	if d.swept < len(d.prs) {
		d.swept++
		return 1, nil
	}
	return 0, nil
}

// Reap fails the one flake and decides it by the rule table: a known flake is a requeue,
// and a requeue nobody had to be told about is the whole point of a rule row.
func (d *simulatedDay) Reap(tick int) (int, int, []Undecided, error) {
	if tick == 3 && d.flake != "" {
		d.reaped++
		return 1, 0, nil, nil
	}
	return 0, 0, nil, nil
}

func (d *simulatedDay) Refill(tick int) (int, error) {
	d.refilled++
	return 1, nil
}

func (d *simulatedDay) Launch(tick int) (int, int, error) {
	d.launched += 4
	return 4, 12, nil
}

// recorder is the Notifier the test puts in place of nova-bus: it keeps every note, so the
// assertion is over what a person would have been sent.
type recorder struct{ subjects, bodies []string }

func (r *recorder) Note(subject, body string) error {
	r.subjects = append(r.subjects, subject)
	r.bodies = append(r.bodies, body)
	return nil
}

func newSimulatedDay() *simulatedDay {
	d := &simulatedDay{perTick: 6, flake: "card-flake"}
	// 200 cards; three of them are cases no rule covers.
	undecidedAt := map[int]string{17: "signature", 88: "scope", 151: "nosha"}
	for i := 1; i <= 200; i++ {
		d.cards = append(d.cards, dayCard{label: fmt.Sprintf("card-%03d", i), undecided: undecidedAt[i]})
	}
	// 40 PRs; two of them hold on something no rule covers.
	prUndecided := map[int]string{7: "docs-only", 31: "hold-line"}
	for i := 1; i <= 40; i++ {
		d.prs = append(d.prs, dayPR{n: 900 + i, undecided: prUndecided[i]})
	}
	d.reds = []dayRed{{tick: 5, sha: "aaaa1111"}, {tick: 30, sha: "bbbb2222"}}
	return d
}

// ------------------------------------------------------------------------------- the tests

// TestRunSimulatedDayCostsUnderTwentyNotes is the claim of G1: a whole day of ticks costs
// the coordinator fewer than twenty turns. The mutation that matters: a loop that notes an
// undecided case again on the next tick -- 60 ticks of the same five cases is 300 notes, and
// that is the poll this verb was written to end.
func TestRunSimulatedDayCostsUnderTwentyNotes(t *testing.T) {
	queue := t.TempDir()
	day := newSimulatedDay()
	notes := &recorder{}
	var out, errs bytes.Buffer

	clock := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	exit := Run(RunInput{
		Queue: queue, Roots: t.TempDir() + "," + t.TempDir(),
		Repo: "mas-bandwidth/nova-tools", Branch: "dev",
		Hours: 1, Tick: time.Minute, GateEvery: 1,
		Stdout: &out, Stderr: &errs, Max: 0,
		Now:   func() time.Time { return clock },
		Sleep: func(d time.Duration) { clock = clock.Add(d) },
		Gate:  day, Harvest: day, Sweep: day, Reap: day, Refill: day, Launcher: day,
		Notifier: notes,
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0\n%s%s", exit, out.String(), errs.String())
	}

	// Sixty ticks of work, and the notes are the decisions: three card cases, two PR cases,
	// two reds. Seven, and never one per tick.
	if len(notes.subjects) >= 20 {
		t.Errorf("notes=%d, want fewer than 20: %v", len(notes.subjects), notes.subjects)
	}
	if len(notes.subjects) != 7 {
		t.Errorf("notes=%d, want 7 (3 cards, 2 PRs, 2 reds): %v", len(notes.subjects), notes.subjects)
	}
	// One note per tick at most: the loop calls a person once, whatever the tick held.
	for i, line := range widthLines(out.String()) {
		if n := field2(line, "noted="); n != "0" && n != "1" {
			t.Errorf("tick %d: noted=%s, want 0 or 1 (a tick may call a person once)", i+1, n)
		}
	}
	// The work happened: every card harvested, every PR swept.
	if day.harvested != 200 {
		t.Errorf("harvested=%d, want 200", day.harvested)
	}
	if day.swept != 40 {
		t.Errorf("swept=%d, want 40", day.swept)
	}
	// Every note carries its packet, and the packet is under the ceiling.
	for i, body := range notes.bodies {
		if !strings.HasPrefix(body, "RESULT triage-") {
			t.Errorf("note %d does not carry a triage packet: %.60q", i, body)
		}
		if len(body) > PacketMax {
			t.Errorf("note %d packet is %d bytes, over %d", i, len(body), PacketMax)
		}
	}
}

// TestRunEveryRedIsOneStopAndOneNote is the other half of the claim: a red branch stops the
// bench once and tells a person once, however many ticks it survives. The mutation that
// matters: a STOP rewritten every tick, or a red that stops the bench and tells nobody.
func TestRunEveryRedIsOneStopAndOneNote(t *testing.T) {
	queue := t.TempDir()
	day := newSimulatedDay()
	notes := &recorder{}
	var out, errs bytes.Buffer

	clock := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	Run(RunInput{
		Queue: queue, Roots: t.TempDir(),
		Repo: "mas-bandwidth/nova-tools", Branch: "dev",
		Hours: 1, Tick: time.Minute, GateEvery: 1,
		Stdout: &out, Stderr: &errs, Max: 0,
		Now:   func() time.Time { return clock },
		Sleep: func(d time.Duration) { clock = clock.Add(d) },
		Gate:  day, Harvest: day, Sweep: day, Reap: day, Refill: day, Launcher: day,
		Notifier: notes,
	})

	reds := readLines(filepath.Join(queue, "REDS"))
	stops, resumed := 0, 0
	for _, l := range reds {
		switch {
		case strings.Contains(l, "MAIN-RED"):
			stops++
		case strings.Contains(l, "RESUMED"):
			resumed++
		}
	}
	if stops != 2 {
		t.Errorf("MAIN-RED lines=%d, want 2 (one per red, not one per tick): %v", stops, reds)
	}
	if resumed != 2 {
		t.Errorf("RESUMED lines=%d, want 2 (the loop clears the STOP it wrote)", resumed)
	}
	redNotes := 0
	for _, s := range notes.subjects {
		if strings.Contains(s, "fence") {
			redNotes++
		}
	}
	if redNotes != 2 {
		t.Errorf("red notes=%d, want 2: %v", redNotes, notes.subjects)
	}
	// A red stops the tick it lands on: no work runs behind a red branch.
	lines := widthLines(out.String())
	if got := field2(lines[4], "stop="); got != "yes" {
		t.Errorf("tick 5 stop=%s, want yes (a red branch is stop everything)", got)
	}
	if got := field2(lines[4], "harvested="); got != "0" {
		t.Errorf("tick 5 harvested=%s, want 0 (never pile work onto a red)", got)
	}
	// And it is over by the next tick, because the gate went green and this loop's own STOP
	// is this loop's to clear.
	if got := field2(lines[5], "stop="); got != "no" {
		t.Errorf("tick 6 stop=%s, want no (green clears the STOP the loop wrote)", got)
	}
}

// TestRunOnceIsOneTickAndOneWidthLine pins the output: one line per tick and one verdict
// line, whatever the day held. A verb whose output grows with its state is the thing G3
// forbids.
func TestRunOnceIsOneTickAndOneWidthLine(t *testing.T) {
	queue := t.TempDir()
	day := newSimulatedDay()
	var out, errs bytes.Buffer
	now := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)

	exit := Run(RunInput{
		Queue: queue, Roots: t.TempDir(), Repo: "o/n", Branch: "dev",
		Once: true, GateEvery: 1, Stdout: &out, Stderr: &errs,
		Now:      func() time.Time { return now },
		Sleep:    func(time.Duration) { t.Fatal("--once must not sleep") },
		Gate:     day,
		Harvest:  day,
		Sweep:    day,
		Reap:     day,
		Refill:   day,
		Launcher: day,
		Notifier: &recorder{},
	})
	if exit != 0 {
		t.Fatalf("exit %d, want 0: %s%s", exit, out.String(), errs.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("stdout is %d lines, want 2 (one WIDTH, one RUN OK):\n%s", len(lines), out.String())
	}
	if !strings.HasPrefix(lines[0], "PULSE WIDTH tick=1 ") {
		t.Errorf("first line is %q, want the tick's WIDTH line", lines[0])
	}
	if !strings.HasPrefix(lines[1], "RUN OK ticks=1 ") {
		t.Errorf("last line is %q, want RUN OK", lines[1])
	}
}

// TestRunNamesAnUnwiredSeamOnce: a step nobody has wired is counted zero and NAMED, once.
// A nil step silently skipped reads as a quiet day, which is the thing that lets a bench sit
// idle for an hour without anybody noticing.
func TestRunNamesAnUnwiredSeamOnce(t *testing.T) {
	var out, errs bytes.Buffer
	now := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	clock := now
	Run(RunInput{
		Queue: t.TempDir(), Roots: t.TempDir(), Hours: 0.05, Tick: time.Minute,
		Stdout: &out, Stderr: &errs, Max: 0,
		Now:   func() time.Time { return clock },
		Sleep: func(d time.Duration) { clock = clock.Add(d) },
	})
	for _, want := range []string{"seam=harvest", "seam=sweep", "seam=reap", "seam=refill", "seam=launch"} {
		if n := strings.Count(errs.String(), want); n != 1 {
			t.Errorf("%s named %d times, want exactly 1:\n%s", want, n, errs.String())
		}
	}
}

// TestRunRefusesWithoutQueueOrRoots: no path is guessed.
func TestRunRefusesWithoutQueueOrRoots(t *testing.T) {
	for _, c := range []struct{ queue, roots, want string }{
		{"", "x", "--queue"},
		{"x", "", "--roots"},
	} {
		var out, errs bytes.Buffer
		if exit := Run(RunInput{Queue: c.queue, Roots: c.roots, Stdout: &out, Stderr: &errs}); exit != 2 {
			t.Errorf("exit %d, want 2 for %+v", exit, c)
		}
		if !strings.Contains(errs.String(), c.want) || !strings.Contains(errs.String(), "refusing to guess") {
			t.Errorf("refusal does not name %s and refuse to guess: %s", c.want, errs.String())
		}
	}
}

// TestRunNotesSurviveARestart: <queue>/NOTED is the record, so a shift that restarts does
// not tell a person the same thing twice.
func TestRunNotesSurviveARestart(t *testing.T) {
	queue := t.TempDir()
	now := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	run := func(notes *recorder) {
		day := newSimulatedDay()
		var out, errs bytes.Buffer
		Run(RunInput{
			Queue: queue, Roots: t.TempDir(), Once: true, GateEvery: 99,
			Stdout: &out, Stderr: &errs, Now: func() time.Time { return now },
			Harvest: day, Notifier: notes,
		})
	}
	first, second := &recorder{}, &recorder{}
	run(first)
	run(second)
	if len(first.subjects) != 1 {
		t.Fatalf("first shift sent %d notes, want 1", len(first.subjects))
	}
	if len(second.subjects) != 1 || second.subjects[0] == first.subjects[0] {
		t.Errorf("the second shift sent %v; it must not repeat %v", second.subjects, first.subjects)
	}
	if _, err := os.Stat(filepath.Join(queue, "NOTED")); err != nil {
		t.Errorf("no NOTED record after a shift: %v", err)
	}
}

// TestRunFileNotifierWritesTheInbox: with no --bus the note goes where a person already
// looks, and the packet is beside it, named on the line.
func TestRunFileNotifierWritesTheInbox(t *testing.T) {
	queue := t.TempDir()
	day := newSimulatedDay()
	var out, errs bytes.Buffer
	now := time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	Run(RunInput{
		Queue: queue, Roots: t.TempDir(), Once: true, GateEvery: 99,
		Stdout: &out, Stderr: &errs, Now: func() time.Time { return now },
		Harvest: day,
	})
	lines := readLines(filepath.Join(queue, "ESCALATE"))
	if len(lines) != 1 || !strings.Contains(lines[0], "UNDECIDED signature") || !strings.Contains(lines[0], "packet=") {
		t.Fatalf("ESCALATE is %v, want one UNDECIDED line naming its packet", lines)
	}
	name := lines[0][strings.Index(lines[0], "packet=")+len("packet="):]
	body, err := os.ReadFile(strings.TrimSpace(name))
	if err != nil {
		t.Fatalf("the packet the line names is not there: %v", err)
	}
	if len(body) > PacketMax || !strings.HasPrefix(string(body), "RESULT triage-") {
		t.Errorf("the packet is %d bytes and begins %.40q", len(body), body)
	}
}

// ------------------------------------------------------------------------------- helpers

func widthLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "PULSE WIDTH ") {
			out = append(out, l)
		}
	}
	return out
}

// field2 reads one key=value off a one-line record.
func field2(line, key string) string {
	i := strings.Index(line, key)
	if i < 0 {
		return ""
	}
	rest := line[i+len(key):]
	if j := strings.IndexByte(rest, ' '); j >= 0 {
		return rest[:j]
	}
	return rest
}
