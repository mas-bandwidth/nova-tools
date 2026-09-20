//go:build !windows

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE CARD THAT HUNG, AND WHAT IT COST. `js-under-20-bytes` (2026-09-19) took a refusal,
// stopped making progress, and was reaped by its --deadline 1200s twenty minutes later:
// rc=-1, wall=1200.04s, NO RESULT.md, $0.0244 spent, a bench slot held, and a coordinator
// with nothing at all to read for eighteen of those minutes. `batch` has watched its cards
// for exactly this since issue #593; `native`, the verb every card on every bench runs
// through, had no watch -- its wait ended only when the child exited, at the deadline, or on
// a TERM from outside.
//
// AND HOW THESE TESTS USED TO ASK ABOUT IT, WHICH WAS WRONG. The first two of them arranged
// REAL TIME -- `--idle 2s` against a `FAKE-SLEEP 60` child -- and then asserted on what the
// run had managed to print by the time that arrangement worked out. That is a wall-clock
// assertion in an event's clothing, and it held only while the machine was quiet.
// `TestNativeIdleEndsAStillCardLongBeforeItsDeadline` went red in landing batch 16an on
// hulk, under the gate's whole-suite load (GOMAXPROCS=8 -p 2 -parallel 4 across the repo),
// and took #1831 out of the batch. Its own ci-ok had been green because ci.yml:302's CL
// tier shards run only the touched packages, lightly loaded -- the one condition the
// assumption survives.
//
// So the idle end is now DELIVERED, not waited for. nativeWatchIdle is the seam (see
// native.go); a test hands the wait the very IdleEnd the watch would have sent and asserts
// the ORDER of what follows. Nothing sleeps and compares. What the real watch decides, and
// when, is internal/swarm's question and internal/swarm's tests answer it under their own
// injected clock.

// idleSeam swaps the three functions the wait reaches the outside world through and records
// the order they are called in. Every recorder CALLS THROUGH to the real function, so the
// run still reaps a real process group: the seam observes, it does not simulate.
type idleSeam struct {
	mu     sync.Mutex
	events []string
	ends   chan swarm.IdleEnd
}

// newIdleSeam installs the seam for one test and restores the real functions after it.
// deliver is called with the watch's own IdleWatch once the wait has started it, and
// returns the end to send -- so a test that needs the child to have reached a state can
// wait for THAT state, by its own observable, before declaring the card idle.
func newIdleSeam(t *testing.T, deliver func(w swarm.IdleWatch) (swarm.IdleEnd, bool)) *idleSeam {
	t.Helper()
	s := &idleSeam{ends: make(chan swarm.IdleEnd, 1)}
	realWatch, realReap, realKill := nativeWatchIdle, nativeReap, nativeKillGroup
	t.Cleanup(func() { nativeWatchIdle, nativeReap, nativeKillGroup = realWatch, realReap, realKill })

	nativeWatchIdle = func(w swarm.IdleWatch, stop <-chan struct{}) <-chan swarm.IdleEnd {
		// The production contract, kept: no window means no watch and a nil channel.
		if w.Idle <= 0 {
			return nil
		}
		s.record("watch-started")
		out := make(chan swarm.IdleEnd, 1)
		go func() {
			end, ok := deliver(w)
			if !ok {
				return
			}
			select {
			case <-stop:
			default:
				s.record("idle-declared")
				out <- end
			}
		}()
		return out
	}
	nativeReap = func(pgid int, started string, grace time.Duration) bool {
		s.record("reap")
		return realReap(pgid, started, grace)
	}
	nativeKillGroup = func(pgid int, started string) {
		s.record("kill")
		realKill(pgid, started)
	}
	return s
}

func (s *idleSeam) record(what string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, what)
}

func (s *idleSeam) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.events...)
}

// TestNativeIdleIsDecidedByTheWatchsEventNotByAClock: the whole of the idle path's wiring,
// asserted as an ORDER of events rather than as a race against `--idle`. The watch declares
// the card idle; the group is REAPED, not shot; the run ends carrying what the watch saw;
// and the deadline branch never ran, which is provable because it is the only branch that
// calls nativeKillGroup.
func TestNativeIdleIsDecidedByTheWatchsEventNotByAClock(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-SLEEP 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The end the watch would have sent for `js-under-20-bytes`: a card still for four
	// minutes that never moved past a refusal. It is CONSTRUCTED here, because what the
	// watch decides is internal/swarm's question (TestWatchIdleCarriesTheRefusalTheCard
	// NeverMovedPast) and what the RUN does with the answer is this one's.
	want := swarm.IdleEnd{Idle: 240 * time.Second, Step: "3", Kind: "write", Path: "/etc/hosts", Refused: true}
	seam := newIdleSeam(t, func(swarm.IdleWatch) (swarm.IdleEnd, bool) { return want, true })

	args := []string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin,
		"--model", "fake/fake-model", "--label", "stillcard", "--card", cardPath, "--slot", slot,
		"--root", root, "--deadline", "30s", "--idle", "2s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	_ = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())

	// (1) THE ORDER. Not "it happened within n seconds": this, then this, then this.
	if got := seam.seen(); strings.Join(got, ",") != "watch-started,idle-declared,reap" {
		t.Fatalf("the wait's events, in order, are watch-started then idle-declared then reap; got %v\n%s\n%s", got, stdout.String(), stderr.String())
	}
	// (2) AND THE DEADLINE NEVER FIRED. nativeKillGroup is reached from one branch only.
	for _, e := range seam.seen() {
		if e == "kill" {
			t.Fatalf("the deadline branch ran: an idle card is reaped, never shot\n%s", stdout.String())
		}
	}
	// (3) THE RUN CARRIES WHAT THE WATCH SAW, in its own lines and in the report it owes.
	// This end was REFUSED, so the run takes the wall branch (main.go:1846) and reports it
	// as the refusal the card never moved past, not as a card that simply went still --
	// both typed lines, byte for byte, built from the end the seam delivered and nothing
	// else. `TestNativeIdleSaysACardThatSimplyWentStillWentStill` holds the other branch.
	for _, want := range []string{
		"WALL task=stillcard path=/etc/hosts step=3",
		"WALL REFUSED write /etc/hosts task=stillcard step=3",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("the run reports the watch's own end on %q:\n%s", want, stdout.String())
		}
	}
	if strings.Contains(stdout.String(), "CARD IDLE") {
		t.Fatalf("a refused end is a wall death, not a card that went quiet:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE NOTE: the card published no report of its own") {
		t.Fatalf("a card the watch ended is given the report it owes:\n%s\n%s", stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE OK") {
		t.Fatalf("a card the watch ended still prints the NATIVE OK line:\n%s", stdout.String())
	}
	job := filepath.Join(slot, "jobs", "stillcard")
	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	if err != nil {
		t.Fatalf("a card the machinery ended is given a report naming the block: %v\n%s", err, stdout.String())
	}
	// The report names the DELIVERED duration, which is the one number in the end that no
	// part of this run could have invented: 240s never appears in the arguments.
	for _, w := range []string{"RESULT: BLOCKED stillcard", "WALL REFUSED write /etc/hosts", "written-by: nova-swarm native",
		"the wall refused write /etc/hosts and the card wrote nothing for 240s after it"} {
		if !strings.Contains(string(raw), w) {
			t.Fatalf("the blocked report carries %q:\n%s", w, raw)
		}
	}
	if _, err := os.Stat(filepath.Join(slot, "usage.tsv")); err != nil {
		t.Fatalf("usage.tsv absent after an idle end: %v", err)
	}
}

// TestNativeIdleSaysACardThatSimplyWentStillWentStill: the OTHER branch of the same
// report (main.go:1846-1850). `js-under-20-bytes` died in a provider stall and was
// reported `WALL task=... path=/var/db/xcode_select_link` -- a path it had worked past
// sixteen steps earlier -- and a shift went looking at the wall for a death the wall had
// nothing to do with. An end the watch declares with NO refusal in it must therefore say
// exactly that, on a line that is deliberately not a WALL line.
//
// internal/swarm holds the shape of that line (TestCardIdleLineIsNotAWallLine); this
// holds that the RUN reaches it, which no test could ask before the seam existed: a
// no-refusal idle end cannot be arranged by a clock and a fixture at all.
func TestNativeIdleSaysACardThatSimplyWentStillWentStill(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-SLEEP 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := swarm.IdleEnd{Idle: 300 * time.Second, Step: "16"}
	seam := newIdleSeam(t, func(swarm.IdleWatch) (swarm.IdleEnd, bool) { return want, true })

	args := []string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin,
		"--model", "fake/fake-model", "--label", "quietcard", "--card", cardPath, "--slot", slot,
		"--root", root, "--deadline", "30s", "--idle", "2s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	_ = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())

	if got := seam.seen(); strings.Join(got, ",") != "watch-started,idle-declared,reap" {
		t.Fatalf("the wait's events, in order, are watch-started then idle-declared then reap; got %v\n%s\n%s", got, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "CARD IDLE task=quietcard step=16 idle=300s") {
		t.Fatalf("a card that simply went still is reported on its own line:\n%s", stdout.String())
	}
	if strings.Contains(stdout.String(), "WALL ") {
		t.Fatalf("no wall refused this card, so no WALL line may name one:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "NATIVE NOTE: the card published no report of its own") {
		t.Fatalf("a card the watch ended is given the report it owes:\n%s\n%s", stdout.String(), stderr.String())
	}
}

// TestNativeIdleReapsTheCardInsteadOfShootingIt keeps the REAL signal, because the point of
// the HOLD (johnny-b9716b436e56: "Idle kill is `KillGroup` ... not `swarm.Reap`
// (TERM-wait-KILL)") is what the operating system delivers, and a recorder cannot show
// that. Only the WATCH is seamed here; nativeReap and nativeKillGroup stay real.
//
// The observable is the one thing that tells a TERM from a KILL from outside the process:
// FAKE-NOTE-ON-TERM writes `termed` into the job directory on SIGTERM. Under a bare
// KillGroup that file cannot exist, because SIGKILL is not a signal any process gets to
// handle. And the card is declared idle only once it has ARMED that handler -- it writes
// `term-armed` when it does -- so this waits for the thing itself and never for a clock.
func TestNativeIdleReapsTheCardInsteadOfShootingIt(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-NOTE-ON-TERM\nFAKE-SLEEP 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	job := filepath.Join(slot, "jobs", "politecard")
	newIdleSeam(t, func(w swarm.IdleWatch) (swarm.IdleEnd, bool) {
		waitForFile(t, filepath.Join(job, "term-armed"), "the fixture harness arming its TERM handler")
		return swarm.IdleEnd{Idle: 240 * time.Second}, true
	})

	args := []string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin,
		"--model", "fake/fake-model", "--label", "politecard", "--card", cardPath, "--slot", slot,
		"--root", root, "--deadline", "30s", "--idle", "2s", "--no-wall"}
	var stdout, stderr bytes.Buffer
	_ = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())

	if !strings.Contains(stdout.String(), "NATIVE NOTE: the card published no report of its own") {
		t.Fatalf("the idle watch is what ended this card:\n%s\n%s", stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(job, "termed")); err != nil {
		t.Fatalf("a card the watch ended is terminated before it is killed, so its harness can flush: %v\n%s", err, stdout.String())
	}
}

// TestNativeIdleZeroWatchesNothing: --idle 0 is the behaviour every run had before the watch
// existed, and a caller can still type it. The card runs to its own end.
func TestNativeIdleZeroWatchesNothing(t *testing.T) {
	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte("FAKE-SAY sh: 1: cannot create /etc/hosts: Permission denied\nFAKE-SLEEP 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"native", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin,
		"--model", "fake/fake-model", "--label", "unwatched", "--card", cardPath, "--slot", slot,
		"--root", root, "--deadline", "60s", "--idle", "0", "--no-wall"}
	var stdout, stderr bytes.Buffer
	_ = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if strings.Contains(stdout.String(), "CARD IDLE") {
		t.Fatalf("a run with no idle window ends nothing for idleness:\n%s", stdout.String())
	}
	raw, err := os.ReadFile(filepath.Join(slot, "jobs", "unwatched", "RESULT.md"))
	if err == nil && strings.Contains(string(raw), "RESULT: BLOCKED") {
		t.Fatalf("a run that was never ended by the watch writes no blocked report:\n%s", raw)
	}
	// The refusal is still NAMED, because naming it costs the card nothing -- and this is
	// now the test that holds native.go|nativeRun|line byte for byte in the audit, so it
	// asserts the WHOLE line. A run with no idle window cannot race the watch for it.
	if !strings.Contains(stderr.String(), "WALL REFUSED write /etc/hosts task=unwatched") {
		t.Fatalf("a refusal is announced whether or not anything acts on it:\n%s", stderr.String())
	}
}
