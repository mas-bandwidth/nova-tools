package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// AN EXPLORE CARD THAT IS READING IS NOT AN IDLE CARD (Studio, 2026-09-19 14:58Z).
//
// The measured run: a `MODE: explore` card under `nova-swarm batch` had cloned, branched
// and was walking `cmd/nova-tokens/main.go` when the idle watchdog killed it --
//
//	tools10-c1 slot=920: ABSTAIN reason=idle=300 log=1730
//	BATCH tools10-c1b n=1 done=0 abstain=1 in=0 out=0 usd=0.0000 idle=1 stalled=0
//
// -- for 300 seconds of quiet on `native.log`. The card was working the whole time, and an
// explore card reads for longer than that between writes BY DESIGN: it spends the window
// waiting on the provider, which is neither a byte on its log nor a percent of a core.
//
// The two signals the monitor had were both blind to it. The LOG is the runner's stdout,
// and a harness mid-turn writes nothing to it. The PROCESS TREE'S CPU is the #593/#916
// signal, and a harness waiting on an HTTP response spends none of it -- the 1%-of-interval
// threshold that separates a working tree from bookkeeping jitter is exactly what a
// blocked-on-the-network process does not clear.
//
// THE THIRD SIGNAL IS THE HARNESS'S OWN STORE. The harness records every turn it takes into
// its database under the card's data home -- the same store `nova-swarm native` samples its
// own usage from (#1712) -- so a card whose store grew since the last poll has had an answer
// from the provider and is working, whatever its log and its CPU say. This is provider
// progress, measured where the provider's answers actually land.
//
// The negative control is in the same test: a card with NO store movement, no log growth and
// no CPU is still killed. The signal must not become "never kill anything".
func TestIdleWatchCountsHarnessStoreProgress(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"explore", "RESULT: explore\nread for a long time, then answered"},
		{"stuck", "RESULT: stuck\nMISSING"},
	})
	// `explore` writes NOTHING to its log and burns NO CPU. All it does is grow the harness
	// store the way a harness taking turns does, then publish. `stuck` sleeps past the idle
	// window and touches nothing at all.
	store := "{root}/{slot}/data/opencode/opencode.db"
	runner := runnerDoing(t, dir, "explore",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "mkdir", Path: "{root}/{slot}/data/opencode", When: "label==explore"},
		runnerStep{Op: "write", Path: store, Body: "turn 1", When: "label==explore"},
		runnerStep{Op: "write", Path: "{root}/explore-started", When: "label==explore"},
		runnerStep{Op: "sleep", Ms: 30000, When: "label==stuck"},
		runnerStep{Op: "exit", N: 0, When: "label==stuck"},
		// The turn the provider answered DURING the idle window: the store grows and
		// nothing else moves.
		runnerStep{Op: "appendn", Path: store, Body: "turn {i}", N: 40, Ms: 25, When: "label==explore"},
		publishCard("{job}"),
	)
	// Neither card's process tree moves: the sampler answers a constant for both, so CPU
	// can save neither of them and the store is the only thing that can.
	sampler := &fakeTreeSampler{}
	sampler.cpuForCard = func(cardIndex, pid int) (uint64, bool) { return 50_000_000, true }
	var rounds atomic.Int64
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
		snapshot: func() activitySnapshot { rounds.Add(1); return sampler },
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "explore-started"))
		db := filepath.Join(root, "1", "data", "opencode", "opencode.db")
		clk.waitTick()
		clk.tick() // the first reading of both cards
		// The clock is injected, so two ticks in a row are the same instant of REAL time and
		// the store would not have moved between them for reasons that have nothing to do
		// with the monitor. The test waits for one more turn to actually land on disk, which
		// is the event it is about, and only then advances the virtual idle window.
		waitForGrowth(t, db)
		clk.advance(testIdleBudget)
		clk.tick() // a whole idle window later: explore's store grew, stuck's did not
	})
	if code != 1 {
		t.Fatalf("a batch holding one idle card exits 1, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "explore slot=1: read for a long time, then answered") {
		t.Fatalf("a card whose harness store is still growing is never idle-killed, however silent its log and its CPU:\n%s", out)
	}
	if !strings.Contains(out, "stuck slot=2: "+idleReason) {
		t.Fatalf("the negative control: a card whose log, tree and store all sat still for --idle is still idle-killed:\n%s", out)
	}
}

// A STORE THAT EXISTS AND NEVER GROWS IS NOT A LIVENESS CERTIFICATE. The store is read for
// its GROWTH, exactly as the log is. A card that wrote its database once, at startup, and
// has been still ever since is the card the idle window exists for, and a monitor that took
// "a store is there" for "the card is working" would never kill anything again.
func TestAStoreThatStoppedGrowingIsStillIdle(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := runnerDoing(t, dir, "oncethenstill",
		runnerStep{Op: "mkdir", Path: "{root}/{slot}/data/opencode"},
		runnerStep{Op: "write", Path: "{root}/{slot}/data/opencode/opencode.db", Body: "one turn and no more"},
		runnerStep{Op: "write", Path: "{root}/still-started"},
		runnerStep{Op: "sleep", Ms: 30000},
	)
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "still-started"))
		clk.waitTick()
		clk.tick()
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 1 {
		t.Fatalf("a batch with an idle-killed card exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "a slot=1: "+idleReason) {
		t.Fatalf("a card whose store was written once and never again is idle:\n%s", out)
	}
}

// waitForGrowth blocks until the named file is larger than it is now, so a test that means
// "one more turn landed" waits for that event rather than for a duration.
func waitForGrowth(t *testing.T, path string) {
	t.Helper()
	start := fileSize(path)
	deadline := time.Now().Add(10 * time.Second) // wall-ok: a test's own give-up, not a product timeout
	for time.Now().Before(deadline) {
		if fileSize(path) > start {
			return
		}
		time.Sleep(5 * time.Millisecond) // wall-ok: polling a file in a test
	}
	t.Fatalf("the harness store %s never grew past %d bytes", path, start)
}

func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return -1
	}
	return fi.Size()
}
