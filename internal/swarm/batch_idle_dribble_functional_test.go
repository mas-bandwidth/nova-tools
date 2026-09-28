//go:build functional

package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests exec whole programs -- the fake runner this package builds
// (testdata/fakerunner), git, sqlite3 or sh -- so they are the functional tier's,
// not unit tests (Glenn 2026-09-26, nova-tools#4328: unit tests under 2 s and
// frugal with the machine's cores). The rest of the file's tests stay in the
// unit tier.

func TestIdleWatchEndsACardThatOnlyDribblesIntoItsLog(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (a 30 s poll for the card's start; it
	// waited it out under load on the 2026-09-25 Studio run). Skipped 2026-09-25 by
	// Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	t.Run("one byte at a time", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "root")
		tsv := writeCards(t, dir, [][2]string{
			{"dribble", "RESULT: dribble\nstill here"},
			{"stuck", "RESULT: stuck\nMISSING"},
		})
		// dribble keeps appending one byte to native.log and never publishes.
		// stuck writes nothing. CPU is pinned constant so the tree cannot save
		// either card; only the log reading is on trial.
		runner := runnerDoing(t, dir, "log-dribble",
			runnerStep{Op: "mkdir", Path: "{job}"},
			runnerStep{Op: "appendn", Path: "{root}/{slot}/native.log", Body: ".", N: 1000, Ms: 20, When: "label==dribble"},
			runnerStep{Op: "sleep", Ms: 30000},
		)
		sampler := &fakeTreeSampler{cpuForCard: func(cardIndex, pid int) (uint64, bool) {
			return 50_000_000, true
		}}
		log := filepath.Join(root, "1", "native.log")
		clk := newManualClock()
		code, out, errs := runBatchClock(BatchInput{
			ID: "B-DRIBBLE", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner, Tokens: "unmetered",
			snapshot: func() activitySnapshot { return sampler },
		}, clk, func() {
			clk.waitTick()
			waitForLog(t, log, 1)
			clk.tick()
			waitForLog(t, log, 5)
			clk.advance(testIdleBudget)
			clk.tick()
			// Backstop: if the dribble fed the watch, the deadline still ends the wait.
			clk.waitDeadline()
			clk.advance(30 * time.Second)
		})
		if code != 1 {
			t.Fatalf("a batch of two idle cards exits 1, got %d; stderr: %s\n%s", code, errs, out)
		}
		if !strings.Contains(out, "dribble slot=1: "+idleReason) {
			t.Fatalf("a card appending one byte at a time into the log the monitor watches is still idle; want it reaped:\n%s", out)
		}
		if !strings.Contains(out, "stuck slot=2: "+idleReason) {
			t.Fatalf("the silent sibling is idle-killed:\n%s", out)
		}
		if !strings.Contains(out, "idle=2") {
			t.Fatalf("both cards are idle kills, not a deadline for the dribbler:\n%s", out)
		}
	})

	t.Run("rewriting the log shorter", func(t *testing.T) {
		dir := t.TempDir()
		root := filepath.Join(dir, "root")
		tsv := writeCards(t, dir, [][2]string{
			{"rewrite", "RESULT: rewrite\nMISSING"},
		})
		log := filepath.Join(root, "1", "native.log")
		started := filepath.Join(root, "rewrite-started")
		runner := runnerDoing(t, dir, "log-rewrite",
			runnerStep{Op: "mkdir", Path: "{job}"},
			runnerStep{Op: "write", Path: "{root}/{slot}/native.log", Body: strings.Repeat("x", 64*1024)},
			runnerStep{Op: "write", Path: started},
			runnerStep{Op: "sleep", Ms: 30000},
		)
		sampler := &fakeTreeSampler{cpuForCard: func(cardIndex, pid int) (uint64, bool) {
			return 50_000_000, true
		}}
		clk := newManualClock()
		code, out, errs := runBatchClock(BatchInput{
			ID: "B-REWRITE", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner, Tokens: "unmetered",
			snapshot: func() activitySnapshot { return sampler },
		}, clk, func() {
			waitForFile(t, started)
			clk.waitTick()
			clk.tick()
			if err := os.Truncate(log, 64*1024-512); err != nil {
				t.Fatal(err)
			}
			clk.advance(testIdleBudget)
			clk.tick()
			clk.waitDeadline()
			clk.advance(30 * time.Second)
		})
		if code != 1 {
			t.Fatalf("a card that only rewrites its log is idle-killed, exits 1, got %d; stderr: %s\n%s", code, errs, out)
		}
		if !strings.Contains(out, "rewrite slot=1: "+idleReason) {
			t.Fatalf("a card truncating the log the monitor watches is not working; want it reaped:\n%s", out)
		}
		if !strings.Contains(out, "idle=1") {
			t.Fatalf("the rewrite is an idle kill, not a deadline:\n%s", out)
		}
	})
}

// TestIdleWatchKeepsACardWhoseLogGrowsAPage: the other side of #1893. A card that is
// actually stepping writes by the page, and that still resets the still-clock. CPU is
// pinned constant so only the log reading is on trial.
func TestIdleWatchKeepsACardWhoseLogGrowsAPage(t *testing.T) {
	t.Parallel()
	// SLEEPS: this test waits on the wall clock (calls time.Sleep). Skipped 2026-09-25
	// by Glenn's rule ("unit tests must not have real sleeps or waits"): it becomes a
	// mocked-clock unit test or a functional program (nova-tools #4221).
	t.Skip("SLEEPS: needs a mocked clock or a functional test (nova-tools #4221)")

	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"working", "RESULT: working\nstill here"},
		{"stuck", "RESULT: stuck\nMISSING"},
	})
	page := strings.Repeat("a harness step, printed\n", 256) // ~6 KiB, past idleLogDribble
	started := filepath.Join(root, "working-started")
	continued := filepath.Join(root, "working-continued")
	release := filepath.Join(root, "working-release")
	cont := filepath.Join(root, "working-continue")
	runner := runnerDoing(t, dir, "page-log",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "write", Path: "{root}/{slot}/native.log", Body: page, When: "label==working"},
		runnerStep{Op: "write", Path: started, When: "label==working"},
		runnerStep{Op: "waitfile", Path: cont, When: "label==working"},
		runnerStep{Op: "append", Path: "{root}/{slot}/native.log", Body: page, When: "label==working"},
		runnerStep{Op: "write", Path: continued, When: "label==working"},
		runnerStep{Op: "waitfile", Path: release, When: "label==working"},
		func() runnerStep { s := publishCard("{job}"); s.When = "label==working"; return s }(),
		runnerStep{Op: "sleep", Ms: 30000, When: "label==stuck"},
	)
	sampler := &fakeTreeSampler{cpuForCard: func(cardIndex, pid int) (uint64, bool) {
		return 50_000_000, true
	}}
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B-PAGE", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner, Tokens: "unmetered",
		snapshot: func() activitySnapshot { return sampler },
	}, clk, func() {
		waitForFile(t, started)
		clk.waitTick()
		clk.tick()
		// tick returns when the monitor RECEIVES the poll, not when it has read the log: a
		// second poll at the same instant is taken only after the first is settled, and it
		// moves nothing, so page 2 cannot land inside the first reading (#1893).
		clk.tick()
		if err := os.WriteFile(cont, []byte("go\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		waitForFile(t, continued)
		clk.advance(testIdleBudget)
		clk.tick()
		if err := os.WriteFile(release, []byte("go\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		waitForFile(t, filepath.Join(root, "1", "jobs", "working", "RESULT.md"))
	})
	if code != 1 {
		t.Fatalf("one idle sibling exits 1, got %d; stderr: %s\n%s", code, errs, out)
	}
	if !strings.Contains(out, "working slot=1: still here") {
		t.Fatalf("a card writing a page of log in the window is not idle:\n%s", out)
	}
	if !strings.Contains(out, "stuck slot=2: "+idleReason) {
		t.Fatalf("the silent sibling is idle-killed:\n%s", out)
	}
	if !strings.Contains(out, "idle=1") {
		t.Fatalf("exactly one card is idle:\n%s", out)
	}
}

// waitForLogBytes blocks until the log holds at least want bytes. A line count is not
// enough for the page tests (#1893): 257 lines is one line of the second page, and the
// monitor's tick after it would see a dribble, not a page, while the write is still landing.
func waitForLogBytes(t *testing.T, path string, want int64) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if logSize(path) >= want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("waiting for %s to hold %d bytes: timed out at %d", path, want, logSize(path))
}
