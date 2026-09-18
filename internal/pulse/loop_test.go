package pulse

// ONE VERB IS THE LOOP (loop.go). bin/pulse-loop.sh is one program and nova-pulse had its
// body as three verbs that nothing made agree (the manager dogfood, gap 10). These drive the
// whole tick with recorders: no bench, no bus, no forge, no sleep.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// loopBench is a queue, a registry and a lanes table -- everything the loop refuses to guess.
type loopBench struct {
	queue, machines, lanes, root string
}

func newLoopBench(t *testing.T) loopBench {
	t.Helper()
	base := t.TempDir()
	b := loopBench{
		queue: filepath.Join(base, "queue"),
		root:  filepath.Join(base, "swarm-bench-a"),
	}
	for _, d := range []string{"pending", "launched", "done", "failed", "ready"} {
		if err := os.MkdirAll(filepath.Join(b.queue, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(b.root, 0o755); err != nil {
		t.Fatal(err)
	}
	b.machines = machinesFile(t, base, []string{"bench-a"}, []string{"batman"})
	b.lanes = laneFile(t, base, "pulse\tinternal/pulse/")
	return b
}

// TestLoopTickIsRunFillManagerInOrder: one tick calls the three steps once each, in the
// script's order, and answers ONE line. The order is the point -- the script harvests before
// it fills, so a slot freed this tick is filled this tick and not the next.
func TestLoopTickIsRunFillManagerInOrder(t *testing.T) {
	b := newLoopBench(t)
	var order []string
	var out, errb bytes.Buffer
	code := Loop(LoopInput{
		Queue: b.queue, Machines: b.machines, Lanes: b.lanes, Roots: b.root,
		Policy: filepath.Join(b.queue, "POLICY"), Once: true,
		Stdout: &out, Stderr: &errb,
		Now:   func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) },
		Sleep: func(time.Duration) {},
		RunStep: func(in RunInput) int {
			order = append(order, "run")
			if !in.Once || !in.Locked {
				t.Errorf("the loop's run step is %+v, want one locked tick", in)
			}
			fmt.Fprintln(in.Stdout, "PULSE WIDTH tick=1 benches=1 stop=no harvested=4 launched=2 free=0")
			return 0
		},
		FillStep: func(in FillInput) int {
			order = append(order, "fill")
			if in.Machines != b.machines || in.Lanes != b.lanes {
				t.Errorf("the fill step got machines=%q lanes=%q, want the loop's own", in.Machines, in.Lanes)
			}
			fmt.Fprintln(in.Stdout, "FILL tick=1 bench-a:launched=3,failed=0 ready=1 gated=0")
			fmt.Fprintln(in.Stdout, "FILL HELD card=card-009.md lane=pulse live=card-008.md")
			fmt.Fprintln(in.Stdout, "FILL GATED card=card-010.md after=PR7 state=OPEN")
			return 0
		},
		ManagerStep: func(in ManagerInput) int {
			order = append(order, "manager")
			if !in.Once || !in.Locked {
				t.Errorf("the loop's manager step is %+v, want one locked cycle", in)
			}
			return 0
		},
	})
	if code != 0 {
		t.Fatalf("loop exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if got := strings.Join(order, ","); got != "run,fill,manager" {
		t.Fatalf("the tick ran %q, want run,fill,manager", got)
	}
	want := "LOOP TICK n=1 ran=2 filled=3 harvested=4 held=2 refused=0 dead=0"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("the loop's line is\n  %q\nwant\n  %q", got, want)
	}
	// The three steps' own lines go where the hand loop put them, and not on the console.
	log, err := os.ReadFile(filepath.Join(b.queue, "pulse.log"))
	if err != nil {
		t.Fatalf("the loop wrote no pulse.log: %v", err)
	}
	for _, line := range []string{"PULSE WIDTH tick=1", "FILL tick=1", "FILL GATED card=card-010.md"} {
		if !strings.Contains(string(log), line) {
			t.Fatalf("pulse.log is missing %q: %q", line, log)
		}
	}
	if strings.Contains(out.String(), "FILL tick=") {
		t.Fatalf("a step's own line reached the console: %q", out.String())
	}
}

// TestLoopRefusesWithoutTheRegistry: the registry and the lanes are the two tables EVERY
// placement is held against, so the loop will not start without them. A loop that guessed
// them is the road a card took onto a runner host.
func TestLoopRefusesWithoutTheRegistry(t *testing.T) {
	b := newLoopBench(t)
	for _, miss := range []string{"machines", "lanes", "roots", "deadline"} {
		in := LoopInput{Queue: b.queue, Machines: b.machines, Lanes: b.lanes, Roots: b.root,
			Once: true, Stderr: io.Discard,
			RunStep:  func(RunInput) int { t.Error("a refused loop ran a step"); return 0 },
			FillStep: func(FillInput) int { return 0 }, ManagerStep: func(ManagerInput) int { return 0 }}
		switch miss {
		case "machines":
			in.Machines = ""
		case "lanes":
			in.Lanes = ""
		case "roots":
			in.Roots = ""
		case "deadline":
			in.Once = false // and no --deadline
		}
		var errb bytes.Buffer
		in.Stderr = &errb
		if code := Loop(in); code != 2 {
			t.Fatalf("loop with no --%s exited %d, want 2", miss, code)
		}
		if !strings.Contains(errb.String(), "--"+miss) {
			t.Fatalf("the refusal does not name --%s: %q", miss, errb.String())
		}
	}
}

// TestLoopLaunchDeadRequeuesAndReleasesTheLane is bin/pulse-loop.sh's launch_check as a
// per-tick check. A card whose job directory never appeared did not start: it goes back to
// pending and its marker goes with it, so THE LANE IT WAS HOLDING IS RELEASED. A lane held
// by a card that never ran is a serial area stopped for as long as nobody looks.
func TestLoopLaunchDeadRequeuesAndReleasesTheLane(t *testing.T) {
	b := newLoopBench(t)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	launched := filepath.Join(b.queue, "launched")

	// card-001 never started: a marker two minutes old and no job directory anywhere.
	writeCard(t, launched, "card-001.md", "RESULT: CARD-1\nLANE: pulse\n")
	writeMarker(t, launched, "card-001.md", "pulse", "bench-a", now.Add(-2*time.Minute))
	// card-002 started: its job directory is on the root, so the probe never touches it.
	writeCard(t, launched, "card-002.md", "RESULT: CARD-2\n")
	writeMarker(t, launched, "card-002.md", "", "bench-a", now.Add(-2*time.Minute))
	if err := os.MkdirAll(filepath.Join(b.root, "1", "jobs", "card-002"), 0o755); err != nil {
		t.Fatal(err)
	}
	// card-003 is inside its grace: young, unstarted, and not this tick's to judge.
	writeCard(t, launched, "card-003.md", "RESULT: CARD-3\n")
	writeMarker(t, launched, "card-003.md", "", "bench-a", now.Add(-10*time.Second))

	var out, errb bytes.Buffer
	code := Loop(LoopInput{
		Queue: b.queue, Machines: b.machines, Lanes: b.lanes, Roots: b.root,
		Once: true, Stdout: &out, Stderr: &errb,
		Now: func() time.Time { return now }, Sleep: func(time.Duration) {},
		RunStep:  func(RunInput) int { return 0 },
		FillStep: func(FillInput) int { return 0 },
	})
	if code != 0 {
		t.Fatalf("loop exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "dead=1") {
		t.Fatalf("the LOOP TICK line does not count the dead launch: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(b.queue, "pending", "card-001.md")); err != nil {
		t.Fatalf("the dead launch was not requeued: %v", err)
	}
	if _, err := os.Stat(launchedMarker(launched, "card-001.md")); !os.IsNotExist(err) {
		t.Fatalf("the marker that holds the lane stayed behind: %v", err)
	}
	if live := liveLanes(launched); live["pulse"] != "" {
		t.Fatalf("the lane is still held by %q after its card was given back", live["pulse"])
	}
	for _, kept := range []string{"card-002.md", "card-003.md"} {
		if _, err := os.Stat(filepath.Join(launched, kept)); err != nil {
			t.Fatalf("the probe took %s, which is alive or inside its grace: %v", kept, err)
		}
	}
	log, _ := os.ReadFile(filepath.Join(b.queue, "pulse.log"))
	if !strings.Contains(string(log), "LOOP LAUNCH-DEAD card=card-001.md bench=bench-a lane=pulse") {
		t.Fatalf("pulse.log does not name the dead launch, its bench and its lane: %q", log)
	}
}

// TestLoopDryRunChangesNothing: --dry-run reads everything and moves nothing, and the line
// says so, so a person can point the verb at a real queue to see what it would do.
func TestLoopDryRunChangesNothing(t *testing.T) {
	b := newLoopBench(t)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	launched := filepath.Join(b.queue, "launched")
	writeCard(t, launched, "card-001.md", "RESULT: CARD-1\n")
	writeMarker(t, launched, "card-001.md", "", "bench-a", now.Add(-time.Hour))

	var out bytes.Buffer
	var fillDry bool
	Loop(LoopInput{
		Queue: b.queue, Machines: b.machines, Lanes: b.lanes, Roots: b.root,
		Once: true, DryRun: true, Stdout: &out, Stderr: io.Discard,
		Now: func() time.Time { return now }, Sleep: func(time.Duration) {},
		RunStep:  func(RunInput) int { return 0 },
		FillStep: func(in FillInput) int { fillDry = in.DryRun; return 0 },
	})
	if !fillDry {
		t.Fatalf("the loop's --dry-run did not reach the fill step")
	}
	if !strings.Contains(out.String(), "dry-run=yes") {
		t.Fatalf("the LOOP TICK line does not say it changed nothing: %q", out.String())
	}
	if _, err := os.Stat(filepath.Join(launched, "card-001.md")); err != nil {
		t.Fatalf("a dry run moved a card an hour past its grace: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b.queue, QueueLockName)); !os.IsNotExist(err) {
		t.Fatalf("a dry run took the queue's lock")
	}
}

// writeMarker plants a launched marker, which is what the probe and the lane rule read.
func writeMarker(t *testing.T, dir, base, lane, bench string, at time.Time) {
	t.Helper()
	body := fmt.Sprintf("lane=%s\nbench=%s\nlabel=%s\ncard=%s\nat=%s\n",
		lane, bench, strings.TrimSuffix(base, ".md"), base, at.UTC().Format(time.RFC3339))
	if err := os.WriteFile(launchedMarker(dir, base), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLoopStampsEachLineOnceAndCountsTheRunRoad: the wired verbs stamp their own lines and
// the loop stamps every line it files, so the log read `19:51:51Z 19:51:51Z LAUNCH REFUSED`
// with the marker no longer at the start -- and the LOOP TICK line above it read refused=0
// while the refusal sat two lines below it. Found dogfooding this verb against a private copy
// of the queue, 2026-09-18.
func TestLoopStampsEachLineOnceAndCountsTheRunRoad(t *testing.T) {
	b := newLoopBench(t)
	var out bytes.Buffer
	Loop(LoopInput{
		Queue: b.queue, Machines: b.machines, Lanes: b.lanes, Roots: b.root,
		Once: true, Stdout: &out, Stderr: io.Discard,
		Now:   func() time.Time { return time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC) },
		Sleep: func(time.Duration) {},
		RunStep: func(in RunInput) int {
			// Exactly what Wiring.log writes: the verb's line behind its own stamp.
			fmt.Fprintln(in.Stdout, "12:00:00Z LAUNCH REFUSED bench=studio reason=runner-host")
			fmt.Fprintln(in.Stdout, "12:00:00Z LAUNCH HELD card=card-002.md lane=pulse live=card-001.md")
			fmt.Fprintln(in.Stdout, "PULSE WIDTH tick=1 benches=1 stop=no harvested=0 launched=0 free=0")
			return 0
		},
		FillStep: func(FillInput) int { return 0 },
	})
	if !strings.Contains(out.String(), "held=1 refused=1") {
		t.Fatalf("the run road's refusal and hold reached the LOOP TICK line as %q", strings.TrimSpace(out.String()))
	}
	log, err := os.ReadFile(filepath.Join(b.queue, "pulse.log"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(log), "12:00:00Z 12:00:00Z") {
		t.Fatalf("a line was stamped twice: %q", log)
	}
	if !strings.Contains(string(log), "12:00:00Z LAUNCH REFUSED bench=studio") {
		t.Fatalf("the refusal is not in the log under one stamp: %q", log)
	}
}
