//go:build !windows && functional

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

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
// the bench, under the gate's whole-suite load (GOMAXPROCS=8 -p 2 -parallel 4 across the repo),
// and took #1831 out of the batch. Its own ci-ok had been green because ci.yml:302's CL
// tier shards run only the touched packages, lightly loaded -- the one condition the
// assumption survives.
//
// So the idle end is now DELIVERED, not waited for. The watch, the reap and the group kill
// are fields of the run's own value (native.go); a test hands its own run the very IdleEnd
// the watch would have sent and asserts the ORDER of what follows. Nothing sleeps and
// compares, and no package var is swapped, so the test opens with t.Parallel() beside the
// rest. What the real watch decides, and when, is internal/swarm's question and
// internal/swarm's tests answer it under their own injected clock.

// idleSeam builds the three seams one run is handed and records the order they are called
// in. Every recorder CALLS THROUGH to the real function, so the run still reaps a real
// process group: the seam observes, it does not simulate.
type idleSeam struct {
	mu     sync.Mutex
	events []string
	watch  func(swarm.IdleWatch, <-chan struct{}) <-chan swarm.IdleEnd
	reap   func(pgid int, started string, grace time.Duration) bool
	kill   func(pgid int, started string)
}

// newIdleSeam builds the seam for one test. deliver is called with the watch's own IdleWatch
// once the wait has started it, and returns the end to send -- so a test that needs the child
// to have reached a state can wait for THAT state, by its own observable, before declaring
// the card idle.
func newIdleSeam(t *testing.T, deliver func(w swarm.IdleWatch) (swarm.IdleEnd, bool)) *idleSeam {
	t.Helper()
	s := &idleSeam{}
	s.watch = func(w swarm.IdleWatch, stop <-chan struct{}) <-chan swarm.IdleEnd {
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
	s.reap = func(pgid int, started string, grace time.Duration) bool {
		s.record("reap")
		return swarm.Reap(pgid, started, grace)
	}
	s.kill = func(pgid int, started string) {
		s.record("kill")
		swarm.KillGroup(pgid, started)
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
// calls the group kill.
func TestNativeIdleIsDecidedByTheWatchsEventNotByAClock(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	card := "FAKE-SAY sh: 1: cannot create /etc/hosts: Permission denied\nFAKE-SLEEP 60\n"
	require.NoError(t, os.WriteFile(cardPath, []byte(card), 0o644))
	// The end the watch would have sent for `js-under-20-bytes`: a card still for four
	// minutes that never moved past a refusal. It is CONSTRUCTED here, because what the
	// watch decides is internal/swarm's question (TestWatchIdleCarriesTheRefusalTheCard
	// NeverMovedPast) and what the RUN does with the answer is this one's.
	//
	// The card is declared idle only once the harness HAS SPOKEN -- it writes `said` the
	// moment its words are on the pipe -- because a card goes still after doing something,
	// never before starting, and because everything the run decides from its capture is
	// undecided until then. That is the thing itself, waited for; not a clock.
	job := filepath.Join(slot, "jobs", "stillcard")
	want := swarm.IdleEnd{Idle: 240 * time.Second, Step: "3", Kind: "write", Path: "/etc/hosts", Refused: true}
	seam := newIdleSeam(t, func(swarm.IdleWatch) (swarm.IdleEnd, bool) {
		waitForFile(t, filepath.Join(job, "said"), "the fixture harness naming the refusal")
		return want, true
	})

	var errOut bytes.Buffer
	res, _ := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "stillcard",
		card: []byte(card), slotDir: slot, root: root,
		deadline: 30 * time.Second, idle: 30 * time.Second, noWall: true,
		watchIdle: seam.watch, reap: seam.reap, killGroup: seam.kill,
	}, &errOut)

	// (1) THE ORDER. Not "it happened within n seconds": this, then this, then this.
	got := seam.seen()
	require.Equal(t, "watch-started,idle-declared,reap", strings.Join(got, ","), "the wait's events, in order, are watch-started then idle-declared then reap; got %v\n%s", got, errOut.String())
	// (2) AND THE DEADLINE NEVER FIRED. The group kill is reached from one branch only.
	for _, e := range seam.seen() {
		require.NotEqual(t, "kill", e, "the deadline branch ran: an idle card is reaped, never shot\n%s", errOut.String())
	}
	// (3) THE RUN CARRIES WHAT THE WATCH SAW, in its own fields and in the report it owes.
	// This end was REFUSED, so the run takes the wall branch (main.go:1846) and reports it
	// as the refusal the card never moved past, not as a card that simply went still --
	// driven by the end the seam delivered and nothing else.
	// `TestNativeIdleSaysACardThatSimplyWentStillWentStill` holds the other branch.
	require.True(t, res.idled, "the watch is what ended this card\n%s", errOut.String())
	require.Equal(t, want, res.idleEnd, "the run carries the watch's own end\n%s", errOut.String())
	require.Equal(t, swarm.WallRefusal{Path: "/etc/hosts", Step: "3"}, res.wallRefusal, "a refused end is a wall death\n%s", errOut.String())
	require.Equal(t, -1, res.rc, "a card the watch ended carries rc=-1\n%s", errOut.String())
	require.NotEmpty(t, res.blockedPath, "a card the watch ended is given the report it owes:\n%s", errOut.String())
	// The typed line main.go prints for this end, built from the fields the run produced.
	require.Equal(t, "WALL REFUSED write /etc/hosts task=stillcard step=3",
		swarm.WallRefusedLine("stillcard", res.idleEnd.Kind, res.idleEnd.Path, res.idleEnd.Step),
		"a refused end is a wall death, not a card that went quiet:\n%s", errOut.String())
	raw, err := os.ReadFile(filepath.Join(job, "RESULT.md"))
	require.NoError(t, err, "a card the machinery ended is given a report naming the block:\n%s", errOut.String())
	// The report names the DELIVERED duration, which is the one number in the end that no
	// part of this run could have invented: 240s never appears in the arguments.
	for _, w := range []string{"RESULT: BLOCKED stillcard", "WALL REFUSED write /etc/hosts", "written-by: nova-swarm native",
		"the wall refused write /etc/hosts and the card wrote nothing for 240s after it"} {
		require.Contains(t, string(raw), w, "the blocked report carries %q:\n%s", w, raw)
	}
	_, err = os.Stat(filepath.Join(slot, "usage.tsv"))
	require.NoError(t, err, "usage.tsv absent after an idle end")
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
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	// The card SPEAKS and then stops -- and what it says names no path, because the point
	// of this branch is a card no wall refused anything to. `js-under-20-bytes` died in a
	// provider stall exactly like this one.
	card := "FAKE-SAY thinking about the sixteenth step\nFAKE-SLEEP 60\n"
	require.NoError(t, os.WriteFile(cardPath, []byte(card), 0o644))
	job := filepath.Join(slot, "jobs", "quietcard")
	want := swarm.IdleEnd{Idle: 300 * time.Second, Step: "16"}
	seam := newIdleSeam(t, func(swarm.IdleWatch) (swarm.IdleEnd, bool) {
		waitForFile(t, filepath.Join(job, "said"), "the fixture harness speaking before it goes still")
		return want, true
	})

	var errOut bytes.Buffer
	res, _ := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "quietcard",
		card: []byte(card), slotDir: slot, root: root,
		deadline: 30 * time.Second, idle: 30 * time.Second, noWall: true,
		watchIdle: seam.watch, reap: seam.reap, killGroup: seam.kill,
	}, &errOut)

	got := seam.seen()
	require.Equal(t, "watch-started,idle-declared,reap", strings.Join(got, ","), "the wait's events, in order, are watch-started then idle-declared then reap; got %v\n%s", got, errOut.String())
	require.True(t, res.idled, "the watch is what ended this card\n%s", errOut.String())
	require.False(t, res.idleEnd.Refused, "this end named no refusal\n%s", errOut.String())
	require.NotEmpty(t, res.blockedPath, "a card the watch ended is given the report it owes:\n%s", errOut.String())
	// The typed line main.go prints for this end: CARD IDLE, deliberately not a WALL line.
	require.Equal(t, "CARD IDLE task=quietcard step=16 idle=300s",
		swarm.CardIdleLine("quietcard", res.idleEnd),
		"a card that simply went still is reported on its own line:\n%s", errOut.String())
	require.False(t, res.lost, "a card that went still is not an unknown provider acceptance\n%s", errOut.String())
	_, err := os.Stat(filepath.Join(job, "provider-acceptance"))
	require.True(t, os.IsNotExist(err), "a quiet card wrote an acceptance mark: %v", err)
}

// TestNativeIdleReapsTheCardInsteadOfShootingIt keeps the REAL signal, because the point of
// the HOLD ("Idle kill is `KillGroup` ... not `swarm.Reap`
// (TERM-wait-KILL)") is what the operating system delivers, and a recorder cannot show
// that. Only the WATCH is seamed here; the run's reap and group kill call through to the
// real swarm.Reap and swarm.KillGroup.
//
// The observable is the one thing that tells a TERM from a KILL from outside the process:
// FAKE-NOTE-ON-TERM writes `termed` into the job directory on SIGTERM. Under a bare
// KillGroup that file cannot exist, because SIGKILL is not a signal any process gets to
// handle. And the card is declared idle only once it has ARMED that handler -- it writes
// `term-armed` when it does -- so this waits for the thing itself and never for a clock.
func TestNativeIdleReapsTheCardInsteadOfShootingIt(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-NOTE-ON-TERM\nFAKE-SLEEP 60\n"), 0o644))
	job := filepath.Join(slot, "jobs", "politecard")
	seam := newIdleSeam(t, func(w swarm.IdleWatch) (swarm.IdleEnd, bool) {
		waitForFile(t, filepath.Join(job, "term-armed"), "the fixture harness arming its TERM handler")
		return swarm.IdleEnd{Idle: 240 * time.Second}, true
	})

	var errOut bytes.Buffer
	res, _ := nativeRun(nativeRunConfig{
		binary: bin, model: "fake/fake-model", label: "politecard",
		card: []byte("FAKE-NOTE-ON-TERM\nFAKE-SLEEP 60\n"), slotDir: slot, root: root,
		deadline: 30 * time.Second, idle: 30 * time.Second, noWall: true,
		watchIdle: seam.watch, reap: seam.reap, killGroup: seam.kill,
	}, &errOut)

	require.True(t, res.idled, "the idle watch is what ended this card:\n%s", errOut.String())
	_, err := os.Stat(filepath.Join(job, "termed"))
	require.NoError(t, err, "a card the watch ended is terminated before it is killed, so its harness can flush:\n%s", errOut.String())
}

// TestNativeIdleZeroWatchesNothing: --idle 0 is the behaviour every run had before the watch
// existed, and a caller can still type it. The card runs to its own end.
func TestNativeIdleZeroWatchesNothing(t *testing.T) {
	t.Parallel()

	windowsIsNotABench(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	require.NoError(t, os.WriteFile(cardPath, []byte("FAKE-SAY sh: 1: cannot create /etc/hosts: Permission denied\nFAKE-SLEEP 3\n"), 0o644))
	args := []string{"native", "--tokens", "unmetered", "--slots-store", nativeStore(t), "--owner", "fake-1", "--harness", bin,
		"--model", "fake/fake-model", "--label", "unwatched", "--card", cardPath, "--slot", slot,
		"--root", root, "--deadline", "60s", "--idle", "0", "--no-wall"}
	var stdout, stderr bytes.Buffer
	_ = run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	require.NotContains(t, stdout.String(), "CARD IDLE", "a run with no idle window ends nothing for idleness:\n%s", stdout.String())
	raw, err := os.ReadFile(filepath.Join(slot, "jobs", "unwatched", "RESULT.md"))
	require.False(t, err == nil && strings.Contains(string(raw), "RESULT: BLOCKED"), "a run that was never ended by the watch writes no blocked report:\n%s", raw)
	// The refusal is still NAMED, because naming it costs the card nothing -- and this is
	// now the test that holds native.go|nativeRun|line byte for byte in the audit, so it
	// asserts the WHOLE line. A run with no idle window cannot race the watch for it.
	require.Contains(t, stderr.String(), "WALL REFUSED write /etc/hosts task=unwatched", "a refusal is announced whether or not anything acts on it:\n%s", stderr.String())
}

// waitForFile waits for a file this test owns to appear, and gives up on its own. It is a
// wait on an OBSERVABLE (what the fixture harness writes when it reaches a point) and never
// a sleep racing a process. The 30s bound is a safety net for a harness that never gets
// there; the assertion is on the file, not the elapsed time.
func waitForFile(t *testing.T, path, what string) {
	t.Helper()
	for waited := time.Duration(0); waited < 30*time.Second; waited += 5 * time.Millisecond {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("%s: %s never appeared", what, path)
}
