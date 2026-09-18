package pulse

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// SPEC-JOBS.md section 8, "Events over polling", names two red tests. Both are driven with
// the same fakes internal/pulse/watch_test.go builds: a fake queue and bus checkout, a fake
// bench tree, and a fake clock that advances only when the watch itself asks to sleep.

// watch-returns-once-per-change: each change is one return. Four events -- a queue change,
// a bus note, a job done and a CI verdict (a merge) -- each appearing between two polls
// print exactly one line apiece and nothing else, and the watch does not replay them on the
// quiet polls that follow.
func TestWatchReturnsOncePerChange(t *testing.T) {
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
			writeWatchFile(t, filepath.Join(bus, "from-ada", "2026-09-17T0000Z-hi.md"),
				"From: Ada\nTo: Rowan\nId: from-ada-9\n\nhello\n")
		},
		4: func() {
			writeWatchFile(t, filepath.Join(jobs, "slot-1", "jobs", "card-9306", "RESULT.md"),
				"RESULT: the card\ndone\n")
		},
	}}

	code, out, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=9 merged", Cap: 150 * time.Second,
		Now: clock.Now, Sleep: ticks.sleep,
	})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the cap); stderr=%s\nout=%s", code, errs, out)
	}
	got := changeLines(out)
	want := []string{
		"QUEUE enqueued pr=1 at=2026-09-17T00:00:30Z",
		"QUEUE merged pr=1 at=2026-09-17T00:01:00Z",
		"NOTE from=Ada to=Rowan id=from-ada-9",
		"JOB label=card-9306 state=done path=" + filepath.Join(jobs, "slot-1", "jobs", "card-9306", "RESULT.md"),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("one return per change =\n%q\nwant\n%q\nfull out=%s", got, want, out)
	}
}

// quiet-time-makes-no-model-call: a quiet estate reaches its cap through the injected clock
// alone. The watch completes with no wall-clock wait, prints no change line and writes no
// stderr, so it made no model call and ran no subprocess poll -- any such turn would have
// needed a seam this test did not provide. Every sleep is the in-process 30 s floor.
func TestQuietTimeMakesNoModelCall(t *testing.T) {
	queue, bus, jobs := watchEstate(t)
	writeWatchFile(t, filepath.Join(queue, "ENQUEUED"), "pr=3 at=2026-09-17T00:00:00Z\n")
	writeWatchFile(t, filepath.Join(bus, "from-ada", "2026-09-17T0000Z-hi.md"),
		"From: Ada\nTo: Rowan\nId: from-ada-3\n\nhello\n")
	writeWatchFile(t, filepath.Join(jobs, "slot-1", "jobs", "card-3", "RESULT.md"),
		"RESULT: the card\ndone\n")
	clock := newWatchClock()
	ticks := &watchTicks{clock: clock}

	code, out, errs := watchRun(t, WatchInput{
		Queue: queue, Bus: bus, Jobs: jobs, Until: "pr=9 merged", Cap: 90 * time.Second,
		Now: clock.Now, Sleep: ticks.sleep,
	})
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (the cap); stderr=%s\nout=%s", code, errs, out)
	}
	if got := changeLines(out); len(got) != 0 {
		t.Fatalf("quiet time printed %q; want no change line", got)
	}
	if errs != "" {
		t.Fatalf("quiet time wrote stderr %q; a watch makes no model call", errs)
	}
	if len(clock.slept) < 2 {
		t.Fatalf("only %d sleeps; want the quiet polls", len(clock.slept))
	}
	for i, d := range clock.slept {
		if d != watchPollFloor {
			t.Fatalf("sleep %d = %s, want the %s in-process floor", i, d, watchPollFloor)
		}
	}
}
