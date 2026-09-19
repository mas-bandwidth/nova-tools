package swarm

import (
	"encoding/binary"
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

// A WAL COMMIT AT A CONSTANT SIZE IS STILL A COMMIT (Fable's cold read of #1736, HIGH).
//
// The first version of this monitor stamped the three store files' SIZES and nothing else,
// and that is blind exactly where it matters. With stock SQLite settings
// (`journal_size_limit = -1`) the write-ahead log is RESET IN PLACE after the first
// autocheckpoint: it keeps its high-water mark and the next transactions are written back
// over the front of it. The cold read probed it -- 200 rows, then six commits, and none of
// `<db>`, `<db>-wal` or `<db>-shm` changed size -- while the wal-index header in `-shm`
// advanced on every one of them. Apple's `sqlite3` CLI sets a 32768-byte limit and hides
// this; the SQLite inside OpenCode is unverified, so the safe reading is the stock one.
//
// A harness at a steady state of commits would therefore read as STILL, which is the exact
// fault class this PR exists to close, reintroduced one layer down.
//
// THE WAL-INDEX HEADER IS THE SIGNAL. Its first 48 bytes hold two copies of the header
// SQLite rewrites at each commit -- a change counter at bytes 4-7 and `mxFrame` at 16-19 --
// and they move at a CONSTANT FILE SIZE, which is the case sizes cannot see. This test pins
// all three sizes and moves only those bytes.
func TestIdleWatchCountsWALCommitAtConstantSize(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	tsv := writeCards(t, dir, [][2]string{
		{"committing", "RESULT: committing\ncommitted at a constant size"},
		{"stuck", "RESULT: stuck\nMISSING"},
	})
	// Two wal-index headers of EQUAL LENGTH whose change counter and mxFrame differ, which is
	// what one commit does to that file and all it does.
	before := fixtureWALIndexHeader(1, 7)
	after := fixtureWALIndexHeader(2, 19)
	if len(before) != len(after) {
		t.Fatalf("the fixture must move no byte of length: %d vs %d", len(before), len(after))
	}
	db := "{root}/{slot}/data/opencode/opencode.db"
	runner := runnerDoing(t, dir, "wal",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "mkdir", Path: "{root}/{slot}/data/opencode", When: "label==committing"},
		// The store at rest: a database, a WAL at its high-water mark, and the index.
		runnerStep{Op: "write", Path: db, Body: "a database that will not change size", When: "label==committing"},
		runnerStep{Op: "write", Path: db + "-wal", Body: "a wal at its high-water mark", When: "label==committing"},
		runnerStep{Op: "write", Path: db + "-shm", Body: before, When: "label==committing"},
		runnerStep{Op: "write", Path: "{root}/at-rest", When: "label==committing"},
		runnerStep{Op: "sleep", Ms: 30000, When: "label==stuck"},
		runnerStep{Op: "exit", N: 0, When: "label==stuck"},
		// THE TEST decides when the commit happens, so the clock it injected is the only
		// clock in this test and nothing races a sleep.
		runnerStep{Op: "waitfile", Path: "{root}/commit-now", When: "label==committing"},
		runnerStep{Op: "write", Path: db + "-shm", Body: after, When: "label==committing"},
		runnerStep{Op: "write", Path: "{root}/committed", When: "label==committing"},
		// The test says when it has finished looking; then the card publishes. Without this
		// the card would race the second tick with its own RESULT.md, and a published result
		// makes the monitor skip the kill for a different reason (issue #916) -- which would
		// let this test pass with the defect still in place.
		runnerStep{Op: "waitfile", Path: "{root}/finish-now", When: "label==committing"},
		publishCard("{job}"),
	)
	// Neither card's tree moves, so CPU saves neither and the store is the only thing that can.
	sampler := &fakeTreeSampler{}
	sampler.cpuForCard = func(cardIndex, pid int) (uint64, bool) { return 50_000_000, true }
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
		snapshot: func() activitySnapshot { return sampler },
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "at-rest"))
		atRest := storeSizes(root)
		clk.waitTick()
		clk.tick() // the first reading: the store at rest
		if err := os.WriteFile(filepath.Join(root, "commit-now"), []byte("go"), 0o644); err != nil {
			t.Fatal(err)
		}
		waitForFile(t, filepath.Join(root, "committed"))
		// THE PROOF THE TEST IS ABOUT WHAT IT SAYS: not one of the three files changed
		// length across the commit, so anything the monitor notices below it noticed in the
		// wal-index header and nowhere else. Without this the test could pass on a size
		// change and prove nothing.
		if committed := storeSizes(root); committed != atRest {
			t.Fatalf("the fixture moved a size across the commit (%v then %v), so a size could have carried the signal", atRest, committed)
		}
		clk.advance(testIdleBudget)
		clk.tick() // one commit later, at exactly the same three sizes
		if err := os.WriteFile(filepath.Join(root, "finish-now"), []byte("go"), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	if code != 1 {
		t.Fatalf("a batch holding one idle card exits 1, got %d; stderr: %s", code, errs)
	}
	if !strings.Contains(out, "committing slot=1: committed at a constant size") {
		t.Fatalf("a harness that committed is working, and a WAL reset in place moves no size:\n%s", out)
	}
	if !strings.Contains(out, "stuck slot=2: "+idleReason) {
		t.Fatalf("the negative control: a card with no store and no CPU is still idle-killed:\n%s", out)
	}
}

// fixtureWALIndexHeader is a wal-index header of the shape SQLite keeps in `<db>-shm`: 48 bytes
// holding two copies of the same record, with a change counter at bytes 4-7 and `mxFrame` at
// 16-19 of each. Only those fields differ between two of these, which is what one commit
// does to that file at a constant length.
func fixtureWALIndexHeader(change, mxFrame uint32) string {
	var b [48]byte
	for _, base := range []int{0, 24} {
		binary.LittleEndian.PutUint32(b[base:], 3007000) // the version field, constant
		binary.LittleEndian.PutUint32(b[base+4:], change)
		binary.LittleEndian.PutUint32(b[base+16:], mxFrame)
	}
	return string(b[:])
}

// storeSizes is the three store files' lengths for slot 1, comparable as one value.
func storeSizes(root string) [3]int64 {
	db := filepath.Join(root, "1", "data", "opencode", "opencode.db")
	return [3]int64{fileSize(db), fileSize(db + "-wal"), fileSize(db + "-shm")}
}
