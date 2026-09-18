package pulse

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The nine red tests SPEC-PULSE.md's "## Watch" section names (#1142). Each fake stands in
// for the real thing that is a network, a bench or a clock: the queue is a directory of
// append-only rows, the bus is a checkout of note files, the job roots are a fake bench
// tree, and the clock is advanced only by the sleeps the verb itself asks for.

// watchClock is the fake clock: Now is read, Sleep advances it and records the interval,
// so a test can prove no two polls are closer than the 30 s floor.
type watchClock struct {
	now   time.Time
	slept []time.Duration
}

func newWatchClock() *watchClock {
	return &watchClock{now: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)}
}

func (c *watchClock) Now() time.Time { return c.now }

func (c *watchClock) Sleep(d time.Duration) {
	c.slept = append(c.slept, d)
	c.now = c.now.Add(d)
}

// watchTicks drives the fake clock and runs one hook after each sleep, which is how a test
// makes an entry, a merge, a removal, a note or a RESULT.md appear between two polls.
type watchTicks struct {
	clock *watchClock
	hooks map[int]func()
	n     int
}

func (w *watchTicks) sleep(d time.Duration) {
	w.clock.Sleep(d)
	w.n++
	if fn := w.hooks[w.n]; fn != nil {
		fn()
	}
}

func watchRun(t *testing.T, in WatchInput) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	in.Stdout, in.Stderr = &out, &errb
	code := Watch(in)
	return code, out.String(), errb.String()
}

func writeWatchFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func appendWatchFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// watchEstate makes the three directories a watch needs, all empty, with an OWNER row
// naming Rowan so a note from Rowan is the caller's own and is skipped.
func watchEstate(t *testing.T) (queue, bus, jobs string) {
	t.Helper()
	dir := t.TempDir()
	queue = filepath.Join(dir, "queue")
	bus = filepath.Join(dir, "bus")
	jobs = filepath.Join(dir, "jobs")
	for _, d := range []string{queue, filepath.Join(bus, "from-ada"), jobs} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeWatchFile(t, filepath.Join(queue, "OWNER"), "Rowan\tbench\t1234\t2026-09-17T00:00:00Z\n")
	return queue, bus, jobs
}

func changeLines(out string) []string {
	var lines []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "QUEUE ") || strings.HasPrefix(line, "NOTE ") || strings.HasPrefix(line, "JOB ") {
			lines = append(lines, line)
		}
	}
	return lines
}

// watch-prints-one-line-per-queue-change: a fake queue dir whose entry, merge and removal
// each appear between two polls yields exactly one QUEUE line each and no other line.
func TestWatchPrintsOneLinePerQueueChange(t *testing.T) {
	queue, bus, jobs := watchEstate(t)
	clock := newWatchClock()
	ticks := &watchTicks{clock: clock, hooks: map[int]func(){
		1: func() {
			appendWatchFile(t, filepath.Join(queue, "ENQUEUED"), "pr=1 at=2026-09-17T00:00:30Z\n")
		},
		2: func() {
			appendWatchFile(t, filepath.Join(queue, "MERGED"), "pr=1 at=2026-09-17T00:01:00Z\n")
		},
		3: func() {
			appendWatchFile(t, filepath.Join(queue, "REMOVED"), "pr=1 at=2026-09-17T00:01:30Z\n")
		},
	}}

	code, out, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=9 merged", Cap: 120 * time.Second,
		Now: clock.Now, Sleep: ticks.sleep,
	})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the cap); stderr=%s\nout=%s", code, errs, out)
	}
	got := changeLines(out)
	want := []string{
		"QUEUE enqueued pr=1 at=2026-09-17T00:00:30Z",
		"QUEUE merged pr=1 at=2026-09-17T00:01:00Z",
		"QUEUE removed pr=1 at=2026-09-17T00:01:30Z",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("change lines =\n%q\nwant\n%q\nfull out=%s", got, want, out)
	}
}

// watch-prints-a-new-note-once: a fake bus checkout holding one To: note from a friend
// yields one NOTE line on the next poll and none after; a note From: the OWNER name is
// skipped.
func TestWatchPrintsANewNoteOnce(t *testing.T) {
	queue, bus, jobs := watchEstate(t)
	clock := newWatchClock()
	ticks := &watchTicks{clock: clock, hooks: map[int]func(){
		1: func() {
			writeWatchFile(t, filepath.Join(bus, "from-ada", "2026-09-17T0000Z-hi.md"),
				"From: Ada\nTo: Rowan\nId: from-ada-1\n\nhello\n")
			writeWatchFile(t, filepath.Join(bus, "from-ada", "2026-09-17T0001Z-self.md"),
				"From: Rowan\nTo: Ada\nId: from-rowan-1\n\nmine\n")
		},
	}}

	code, out, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=9 merged", Cap: 120 * time.Second,
		Now: clock.Now, Sleep: ticks.sleep,
	})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the cap); stderr=%s\nout=%s", code, errs, out)
	}
	got := changeLines(out)
	want := []string{"NOTE from=Ada to=Rowan id=from-ada-1"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("change lines =\n%q\nwant\n%q\nfull out=%s", got, want, out)
	}
}

// watch-prints-a-result-md-appearance: a fake job root where a RESULT.md appears yields one
// JOB line carrying the label and its line 2 verdict.
func TestWatchPrintsAResultMDAppearance(t *testing.T) {
	queue, bus, jobs := watchEstate(t)
	clock := newWatchClock()
	var resultPath string
	ticks := &watchTicks{clock: clock, hooks: map[int]func(){
		1: func() {
			resultPath = filepath.Join(jobs, "slot-1", "jobs", "card-9004", "RESULT.md")
			writeWatchFile(t, resultPath, "RESULT: the card\nabstain\nthe reason\n")
		},
	}}

	code, out, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=9 merged", Cap: 120 * time.Second,
		Now: clock.Now, Sleep: ticks.sleep,
	})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the cap); stderr=%s\nout=%s", code, errs, out)
	}
	got := changeLines(out)
	want := []string{"JOB label=card-9004 state=abstain path=" + resultPath}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("change lines =\n%q\nwant\n%q\nfull out=%s", got, want, out)
	}
}

// watch-polls-no-faster-than-30s: a fake clock logs every poll, and no two polls are
// closer than 30 s.
func TestWatchPollsNoFasterThan30s(t *testing.T) {
	queue, bus, jobs := watchEstate(t)
	clock := newWatchClock()
	ticks := &watchTicks{clock: clock}

	code, out, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=9 merged", Cap: 90 * time.Second,
		Now: clock.Now, Sleep: ticks.sleep,
	})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the cap); stderr=%s\nout=%s", code, errs, out)
	}
	if len(clock.slept) < 2 {
		t.Fatalf("the clock logged %d sleeps, want at least 2 polls: %v", len(clock.slept), clock.slept)
	}
	for i, d := range clock.slept {
		if d < 30*time.Second {
			t.Fatalf("sleep %d = %s, want no poll faster than 30s", i, d)
		}
	}
}

// watch-prints-nothing-when-nothing-changes: a fake queue, bus and job root unchanged
// across three ticks yields no change line at all.
func TestWatchPrintsNothingWhenNothingChanges(t *testing.T) {
	queue, bus, jobs := watchEstate(t)
	writeWatchFile(t, filepath.Join(queue, "ENQUEUED"), "pr=3 at=2026-09-17T00:00:00Z\n")
	writeWatchFile(t, filepath.Join(bus, "from-ada", "2026-09-17T0000Z-hi.md"),
		"From: Ada\nTo: Rowan\nId: from-ada-3\n\nhello\n")
	writeWatchFile(t, filepath.Join(jobs, "slot-1", "jobs", "card-3", "RESULT.md"),
		"RESULT: the card\ndone\n")
	clock := newWatchClock()
	ticks := &watchTicks{clock: clock}

	code, out, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=9 merged", Cap: 120 * time.Second,
		Now: clock.Now, Sleep: ticks.sleep,
	})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the cap); stderr=%s\nout=%s", code, errs, out)
	}
	if got := changeLines(out); len(got) != 0 {
		t.Fatalf("unchanged estate printed %q; want no change line", got)
	}
	if len(clock.slept) < 3 {
		t.Fatalf("only %d polls; want three ticks", len(clock.slept))
	}
}

// watch-exits-0-on-the-until-event: --until 'pr=7 merged' against a fake queue that records
// the merge returns WATCH OK and exit 0 at the poll that sees it.
func TestWatchExits0OnTheUntilEvent(t *testing.T) {
	queue, bus, jobs := watchEstate(t)
	clock := newWatchClock()
	ticks := &watchTicks{clock: clock, hooks: map[int]func(){
		1: func() {
			appendWatchFile(t, filepath.Join(queue, "MERGED"), "pr=7 at=2026-09-17T00:00:30Z\n")
		},
	}}

	code, out, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=7 merged", Cap: 300 * time.Second,
		Now: clock.Now, Sleep: ticks.sleep,
	})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s\nout=%s", code, errs, out)
	}
	if !strings.Contains(out, "QUEUE merged pr=7 at=2026-09-17T00:00:30Z") {
		t.Fatalf("out=%q, want the merge change line", out)
	}
	if !strings.Contains(out, "WATCH OK until=pr=7 merged") {
		t.Fatalf("out=%q, want WATCH OK until=pr=7 merged", out)
	}
	if strings.Contains(out, "WATCH CAP") {
		t.Fatalf("out=%q, want no WATCH CAP", out)
	}
}

// watch-exits-3-at-the-cap: a fake clock driven past --cap with no event returns WATCH CAP
// and exit 3.
func TestWatchExits3AtTheCap(t *testing.T) {
	queue, bus, jobs := watchEstate(t)
	clock := newWatchClock()
	ticks := &watchTicks{clock: clock}

	code, out, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=9 merged", Cap: 60 * time.Second,
		Now: clock.Now, Sleep: ticks.sleep,
	})
	if code != 3 {
		t.Fatalf("exit = %d, want 3; stderr=%s\nout=%s", code, errs, out)
	}
	if !strings.Contains(out, "WATCH CAP until=pr=9 merged changes=0 polls=2 cap=1m0s") {
		t.Fatalf("out=%q, want WATCH CAP with polls=2", out)
	}
}

// watch-refuses-a-bad-until-or-cap: an unknown --until and a --cap below 30 s are each one
// WATCH REFUSED with a remedy, exit 2, and no poll is made.
func TestWatchRefusesABadUntilOrCap(t *testing.T) {
	queue, bus, jobs := watchEstate(t)

	clock := newWatchClock()
	code, _, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "whenever", Cap: 60 * time.Second,
		Now: clock.Now, Sleep: clock.Sleep,
	})
	if code != 2 {
		t.Fatalf("unknown --until exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "WATCH REFUSED until=whenever (name one of pr=<n> merged, note-from=<name>, job=<label> done)") {
		t.Fatalf("stderr=%q, want the until refusal", errs)
	}
	if len(clock.slept) != 0 {
		t.Fatalf("a refused --until polled: %v", clock.slept)
	}

	clock = newWatchClock()
	code, _, errs = watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=7 merged", Cap: 10 * time.Second,
		Now: clock.Now, Sleep: clock.Sleep,
	})
	if code != 2 {
		t.Fatalf("short --cap exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "WATCH REFUSED cap=10s (raise the cap to at least the 30s poll floor)") {
		t.Fatalf("stderr=%q, want the cap refusal", errs)
	}
	if len(clock.slept) != 0 {
		t.Fatalf("a refused --cap polled: %v", clock.slept)
	}
}

// watch-refuses-missing-paths: a missing --bus and a missing --jobs are each one WATCH
// REFUSED with a remedy, exit 2.
func TestWatchRefusesMissingPaths(t *testing.T) {
	queue, bus, jobs := watchEstate(t)
	clock := newWatchClock()

	code, _, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: "", Jobs: jobs, Until: "pr=7 merged", Cap: 60 * time.Second,
		Now: clock.Now, Sleep: clock.Sleep,
	})
	if code != 2 {
		t.Fatalf("missing --bus exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "WATCH REFUSED: refusing to guess (bus is required)") {
		t.Fatalf("stderr=%q, want the missing --bus refusal", errs)
	}

	clock = newWatchClock()
	code, _, errs = watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: "", Until: "pr=7 merged", Cap: 60 * time.Second,
		Now: clock.Now, Sleep: clock.Sleep,
	})
	if code != 2 {
		t.Fatalf("missing --jobs exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "WATCH REFUSED: refusing to guess (jobs is required)") {
		t.Fatalf("stderr=%q, want the missing --jobs refusal", errs)
	}
}
