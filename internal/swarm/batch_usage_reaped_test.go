package swarm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A REAPED CARD'S SPEND IS NOT ZERO (Studio, 2026-09-19 14:58Z).
//
// The same run that produced the idle-kill break produced this one:
//
//	BATCH tools10-c1b n=1 done=0 abstain=1 in=0 out=0 usd=0.0000 idle=1 stalled=0
//
// The card had run for minutes and made real paid calls. `in=0 out=0 usd=0.0000` is not an
// absence being reported honestly -- it is a number, and a shift that trusted it would
// under-report its spend. Glenn asked for a ledger; a ledger that silently reports zero for
// every reaped card is worse than no ledger.
//
// THE CAUSE. The BATCH line sums `usage.tsv`, and `usage.tsv` is written by `native` AT THE
// END of a run (writeNativeUsage). A card the batch kills -- idle or deadline -- never
// reaches that write, so the file is not there and `readCardUsage` answers zeroes. Every
// card the batch reaps is therefore free, on the line that says what the batch cost.
//
// THE FIX. The provider's own numbers are in the harness's store under the card's data home,
// which is exactly where `native` reads them from (#1712, `ReadCardUsage`). When the card
// wrote no `usage.tsv`, the batch reads the same store the same way.
func TestBatchReadsAReapedCardsSpendFromItsHarnessStore(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The store the card's harness wrote before the batch killed it: three assistant turns
	// the provider billed. Its rows are stamped NOW, because the reader's window is the
	// run's own start and end.
	fixture := loadCardUsageSQL(t, filepath.Join(dir, "store", "opencode.db"), reapedStoreSQL(time.Now()))
	// A card that writes its store and then goes still: no RESULT.md and no usage.tsv, which
	// is exactly what `native` leaves behind when it is killed before it can write either.
	runner := runnerDoing(t, dir, "reaped",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "copy", Path: "{root}/{slot}/data/opencode/opencode.db", Body: fixture},
		runnerStep{Op: "write", Path: "{root}/reaped-started"},
		runnerStep{Op: "sleep", Ms: 30000},
	)
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "reaped-started"))
		clk.waitTick()
		clk.tick()
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 1 {
		t.Fatalf("a batch with a reaped card exits 1, got %d:\n%s", code, out)
	}
	// The fixture's three turns sum to tokens_in 108, tokens_out 55 and usd 1.0000.
	if !strings.Contains(out, "in=108 out=55 usd=1.0000") {
		t.Fatalf("the BATCH line reports what the provider billed a reaped card, never zero:\n%s", out)
	}
	if strings.Contains(out, "in=0 out=0 usd=0.0000") {
		t.Fatalf("a card that spent is never reported as free:\n%s", out)
	}
}

// THE NEGATIVE CONTROL, and the one that matters: usage.tsv WINS. A card that finished wrote
// its own row, `native` composed it from the store with the run's own window and its own
// provider and model, and that row is the record. The store must never be read over the top
// of it -- a second reader summing the same database with a slightly different window would
// double-count a card that already reported, which is the same false ledger in the other
// direction.
func TestAFinishedCardsUsageRowIsNotRecountedFromTheStore(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := loadCardUsageSQL(t, filepath.Join(dir, "store", "opencode.db"), reapedStoreSQL(time.Now()))
	// This card writes BOTH: the store its harness kept, and the usage.tsv `native` composed
	// at the end. The row says 7 and 3 and 0.0500; the store says 108 and 55 and 1.0000.
	usageRow := "job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n" +
		"a\t1\t2026-09-19T14:00:00Z\t2026-09-19T14:01:00Z\t0\topencode\tm\t7\t3\t0\t0\t0\t0.0500\n"
	runner := runnerDoing(t, dir, "finished",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "copy", Path: "{root}/{slot}/data/opencode/opencode.db", Body: fixture},
		runnerStep{Op: "write", Path: "{job}/usage.tsv", Body: usageRow},
		publishCard("{job}"),
	)
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {})
	if code != 0 {
		t.Fatalf("a finished card exits 0, got %d:\n%s", code, out)
	}
	if !strings.Contains(out, "in=7 out=3 usd=0.0500") {
		t.Fatalf("the row the card wrote is the record, and the store is not read over it:\n%s", out)
	}
}

// reapedStoreSQL is the harness store a card left behind: the schema the real one has and
// three assistant turns stamped inside the batch's own run window, summing to tokens_in 108,
// tokens_out 55, cache_write 10, cache_read 20, reasoning 5 and usd 1.0000.
func reapedStoreSQL(at time.Time) string {
	ms := at.UnixMilli()
	row := func(id int, in, out, cw, cr, re int, usd string, offset int64) string {
		return fmt.Sprintf(
			"INSERT INTO message VALUES (%d, '{\"role\":\"assistant\",\"providerID\":\"opencode\",\"modelID\":\"m\","+
				"\"tokens\":{\"input\":%d,\"output\":%d,\"reasoning\":%d,\"cache\":{\"write\":%d,\"read\":%d}},\"cost\":%s}', %d);\n",
			id, in, out, re, cw, cr, usd, ms+offset)
	}
	return cardUsageSchema + "\n" +
		row(1, 100, 50, 10, 20, 5, "0.9000", 0) +
		row(2, 5, 3, 0, 0, 0, "0.0600", 10) +
		row(3, 3, 2, 0, 0, 0, "0.0400", 20)
}

// A STORE THE READER COULD NOT OPEN IS NAMED, NEVER A SILENT ZERO (Fable's cold read of this
// change, MEDIUM). `ReadCardUsage` maps a locked database, a query that did not answer and a
// missing `sqlite3` all onto a reason, and the first version of `storeCardSpend` dropped it
// on the floor and returned zeroes -- which is this change's own fault, a card that spent
// reported as free, put straight back for every unreadable store.
//
// The card here has a file where its database should be that is NOT a database, so the
// reader reaches it, fails on it, and has something to say.
func TestAReapedCardsUnreadableStoreIsNoted(t *testing.T) {
	windowsIsNotABench(t)
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		t.Skipf("%s is not on PATH", SQLiteBinary)
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runner := runnerDoing(t, dir, "unreadable",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "mkdir", Path: "{root}/{slot}/data/opencode"},
		runnerStep{Op: "write", Path: "{root}/{slot}/data/opencode/opencode.db",
			Body: "this is not a database, and a reader that reaches it has something to say"},
		runnerStep{Op: "write", Path: "{root}/unreadable-started"},
		runnerStep{Op: "sleep", Ms: 30000},
	)
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "unreadable-started"))
		clk.waitTick()
		clk.tick()
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 1 {
		t.Fatalf("a batch with a reaped card exits 1, got %d:\n%s", code, out)
	}
	if !strings.Contains(errs, "BATCH NOTE a store unread:") {
		t.Fatalf("a store the reader could not open is named on stderr, never a silent zero:\n%s", errs)
	}
	// And the line is honest about having no number: it says zero AND it does not claim the
	// zero is a measured floor, because nothing was measured.
	if strings.Contains(out, "partial=") {
		t.Fatalf("a card whose store could not be read has no lower bound to report:\n%s", out)
	}
}

// THE REAPED SUM IS A LOWER BOUND AND THE LINE SAYS SO (Fable's cold read, MEDIUM). The card
// was killed mid-turn. The assistant row for the turn in flight has no `tokens` object --
// the harness writes those when the turn completes -- so the provider charged for work that
// is in nobody's database. `partial=<n>` counts the cards whose numbers are a floor.
func TestReapedSpendIsALowerBound(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Three completed turns, and a FOURTH row with no tokens object: the turn that was in
	// flight when the batch killed the card. It adds nothing to the sum, which is precisely
	// why the sum is a floor.
	inFlight := `INSERT INTO message VALUES (4, '{"role":"assistant","providerID":"opencode","modelID":"m"}', ` +
		strconv.FormatInt(time.Now().UnixMilli()+30, 10) + ");\n"
	fixture := loadCardUsageSQL(t, filepath.Join(dir, "store", "opencode.db"),
		reapedStoreSQL(time.Now())+inFlight)
	runner := runnerDoing(t, dir, "lowerbound",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "copy", Path: "{root}/{slot}/data/opencode/opencode.db", Body: fixture},
		runnerStep{Op: "write", Path: "{root}/lb-started"},
		runnerStep{Op: "sleep", Ms: 30000},
	)
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "lb-started"))
		clk.waitTick()
		clk.tick()
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 1 {
		t.Fatalf("a batch with a reaped card exits 1, got %d:\n%s", code, out)
	}
	// The three completed turns are counted; the fourth contributes nothing.
	if !strings.Contains(out, "in=108 out=55 usd=1.0000") {
		t.Fatalf("the completed turns are summed and the turn in flight adds nothing:\n%s", out)
	}
	if !strings.Contains(out, "partial=1") {
		t.Fatalf("a reaped card's spend is a floor and the BATCH line says so:\n%s", out)
	}
}

// A STALE SLOT usage.tsv FROM AN EARLIER CARD IS NOT THIS CARD'S ROW (Fable's cold read,
// LOW). Slot directories are reused across batches. A `usage.tsv` left at the slot root by a
// previous card exists, so the first version read it as "this card reported" -- twice wrong:
// it blocked the store fallback AND it put another card's numbers on this card's row. Every
// row carries a `job` column naming the label that wrote it, so the question is mechanical.
func TestAStaleSlotUsageRowIsNotThisCardsRow(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The previous occupant of slot 1 left its row behind, saying 999 and 999 and 9.9999.
	slot := filepath.Join(root, "1")
	if err := os.MkdirAll(slot, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := UsageRow{
		"job": "some-earlier-card", "attempt": "1", "started": "2026-09-01T00:00:00Z",
		"ended": "2026-09-01T00:01:00Z", "rc": "0", "provider": "opencode", "model": "m",
		"tokens_in": "999", "tokens_out": "999",
		"cache_write": "-", "cache_read": "-", "reasoning": "-", "usd": "9.9999",
	}
	if err := WriteCardUsage(filepath.Join(slot, "usage.tsv"), stale); err != nil {
		t.Fatal(err)
	}
	fixture := loadCardUsageSQL(t, filepath.Join(dir, "store", "opencode.db"), reapedStoreSQL(time.Now()))
	runner := runnerDoing(t, dir, "stale",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "copy", Path: "{root}/{slot}/data/opencode/opencode.db", Body: fixture},
		runnerStep{Op: "write", Path: "{root}/stale-started"},
		runnerStep{Op: "sleep", Ms: 30000},
	)
	clk := newManualClock()
	code, out, _ := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "stale-started"))
		clk.waitTick()
		clk.tick()
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 1 {
		t.Fatalf("a batch with a reaped card exits 1, got %d:\n%s", code, out)
	}
	if strings.Contains(out, "in=999") || strings.Contains(out, "usd=9.9999") {
		t.Fatalf("an earlier card's row is never this card's spend:\n%s", out)
	}
	if !strings.Contains(out, "in=108 out=55 usd=1.0000") {
		t.Fatalf("a stale slot row must not block this card's own store:\n%s", out)
	}
}

// AN EMPTY STORE WAS READ FINE (Rowan's re-read of this change). `ReadCardUsage` collapsed
// TWO different things onto the one token `no-rows`: a query that FAILED, and a query that
// succeeded against a table with nothing in it. They are not the same fact.
//
// A harness killed before its first answer leaves a store with the schema and no message
// rows -- read perfectly, holding nothing. Reporting that as `store unread: no-rows` names a
// reader that stopped where there was none, and a note that cries wolf on every early kill
// is a note people learn to scroll past. The one that matters -- a locked database, a query
// that timed out -- then goes unread with it.
//
// So a failed query has its own reason, and an empty store is an absence like a missing one:
// zero, counted, and silent.
func TestAnEmptyStoreIsNotUnread(t *testing.T) {
	windowsIsNotABench(t)
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		t.Skipf("%s is not on PATH", SQLiteBinary)
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "root")
	card := writeCard(t, dir, "a.card", "RESULT: a\nall green")
	tsv := filepath.Join(dir, "cards.tsv")
	if err := os.WriteFile(tsv, []byte("a\t1\tmodel\t"+card+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The schema and not one row: the store a harness killed before its first answer leaves.
	fixture := loadCardUsageSQL(t, filepath.Join(dir, "store", "opencode.db"), cardUsageSchema)
	runner := runnerDoing(t, dir, "emptystore",
		runnerStep{Op: "mkdir", Path: "{job}"},
		runnerStep{Op: "copy", Path: "{root}/{slot}/data/opencode/opencode.db", Body: fixture},
		runnerStep{Op: "write", Path: "{root}/empty-started"},
		runnerStep{Op: "sleep", Ms: 30000},
	)
	clk := newManualClock()
	code, out, errs := runBatchClock(BatchInput{
		ID: "B1", Deadline: 30 * time.Second, Idle: testIdleBudget, Cards: tsv, Root: root, Runner: runner,
	}, clk, func() {
		waitForFile(t, filepath.Join(root, "empty-started"))
		clk.waitTick()
		clk.tick()
		clk.advance(testIdleBudget)
		clk.tick()
	})
	if code != 1 {
		t.Fatalf("a batch with a reaped card exits 1, got %d:\n%s", code, out)
	}
	if strings.Contains(errs, "store unread") {
		t.Fatalf("a store that was read and held nothing is an absence, not a reader that stopped:\n%s", errs)
	}
	// And it is still zero, and still not claimed as a measured floor.
	if !strings.Contains(out, "in=0 out=0 usd=0.0000") {
		t.Fatalf("an empty store contributes zero:\n%s", out)
	}
	if strings.Contains(out, "partial=") {
		t.Fatalf("a store with no turns in it is no lower bound:\n%s", out)
	}
}

// AND THE READER THAT REALLY STOPPED KEEPS ITS OWN REASON, distinct from the empty store
// above. This is the pair: one token each, so the note fires for exactly one of them.
func TestAFailedQueryAndAnEmptyTableAreDifferentReasons(t *testing.T) {
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		t.Skipf("%s is not on PATH", SQLiteBinary)
	}
	dir := t.TempDir()
	started, ended := cardUsageWindow()

	// A store that is not a database at all: the query fails.
	bad := filepath.Join(dir, "bad", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, _, badReason := ReadCardUsage(filepath.Join(dir, "bad"), started, ended)

	// A store with the schema and no rows: the query succeeds and answers nothing.
	loadCardUsageSQL(t, filepath.Join(dir, "empty", "opencode", "opencode.db"), cardUsageSchema)
	_, _, _, emptyReason := ReadCardUsage(filepath.Join(dir, "empty"), started, ended)

	if badReason == emptyReason {
		t.Fatalf("a failed query and an empty table are different facts and want different reasons; both said %q", badReason)
	}
	if emptyReason != "no-rows" {
		t.Errorf("a store read fine and holding nothing is no-rows, got %q", emptyReason)
	}
	if badReason == "" || badReason == "no-rows" {
		t.Errorf("a query that failed has its own reason, got %q", badReason)
	}
}
