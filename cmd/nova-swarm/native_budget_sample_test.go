package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// THE LIVE SAMPLER AND WHAT THE LINE REPORTS (SPEC-SWARM rule 13d, demanded test 13d,
// issue #1545). Slice 3 of the cap.
//
// The clauses this file holds:
//
//	"a fake harness that writes no usage runs to its deadline and prints `budget=-/<n>`"
//	"one that reports only `tokens_in` prints `budget=<n>+/<n>`"
//	"token columns and a `usd` that all report `0` print `budget=0/<n>` and never
//	 `unmetered`"
//	"a database with a write-ahead log beside it for the whole run is sampled and its reads
//	 are answers, never failures"
//	"a usage reader that never returns does not move the deadline, and the run still ends
//	 inside the bound the deadline's own test holds (issue #779)"
//
// EVERY ONE OF THEM READS A REAL SQLITE DATABASE in the harness's own shape, written by the
// fake harness's `FAKE-USAGE-DB` directive: the reader under test runs `sqlite3 -readonly`
// and folds JSON out of a `data` column, and a fixture that short-circuited either would
// prove nothing about either.

// needsSQLite skips a case on a bench with no reader, naming it. On such a bench a numeric
// budget is a NATIVE REFUSED (rule 13d, slice 2), which is the rule working.
func needsSQLite(t *testing.T) {
	t.Helper()
	if !swarm.SQLiteOnPath() {
		t.Skipf("%s is not on PATH, and a numeric budget is refused without it (rule 13d)", swarm.SQLiteBinary)
	}
}

// budgetOf is the `budget=` field of a NATIVE OK line.
func budgetOf(t *testing.T, out string) string {
	t.Helper()
	for _, f := range strings.Fields(nativeOKLine(t, out)) {
		if v, ok := strings.CutPrefix(f, "budget="); ok {
			return v
		}
	}
	t.Fatalf("the NATIVE OK line carries no budget= field:\n%s", out)
	return ""
}

// runBudgetCard runs one native launch whose card holds the directives given, under the
// budget word given, and returns its exit code and the two captures.
func runBudgetCard(t *testing.T, tokens, directives string, extra ...string) (int, string, string) {
	t.Helper()
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	card := filepath.Join(root, "card.md")
	if err := os.WriteFile(card, []byte("a card\n"+directives), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(budgetNativeArgs(t, bin, card, slot, root, tokens), extra...)
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	return rc, stdout.String(), stderr.String()
}

// TestNativeLineReportsWhatTheFinalReadSaw holds the three spellings rule 13d names against
// a number, each from a harness that reported exactly that much.
//
// THE FIGURE IS THE FINAL READ'S, not a sample's: "`<spent>` is the sum over every launch
// at the final read, never the sum at the stop".
func TestNativeLineReportsWhatTheFinalReadSaw(t *testing.T) {
	needsSQLite(t)
	for _, tc := range []struct {
		name       string
		directives string
		want       string
		why        string
	}{
		{
			// A harness that writes no usage at all: nothing observed, the budget cannot
			// fire, the deadline is the only stop, and the line says so with the dash.
			name: "no_usage_at_all", directives: "FAKE-FINDINGS 1\n", want: "-/50000",
			why: "a harness that reported nothing is never reported as under budget",
		},
		{
			// Only tokens_in. The sum counts the column it has and the PLUS says it can
			// never show the card stayed under the number.
			name: "only_tokens_in", directives: "FAKE-USAGE-DB 900 - - - -\nFAKE-FINDINGS 1\n", want: "900+/50000",
			why: "a partial observation carries the plus",
		},
		{
			// EVERY COLUMN A REPORTED ZERO. Rule 13d: "token columns and a `usd` that all
			// report `0` print `budget=0/<n>` and never `unmetered`". A zero is a
			// measurement; it is not a licence to drop the budget.
			name: "all_zeroes", directives: "FAKE-USAGE-DB 0 0 0 0 0 0\nFAKE-FINDINGS 1\n", want: "0/50000",
			why: "a reported zero is a measurement, and never a reason to print unmetered",
		},
		{
			// THE SUM IS tokens_in + tokens_out + reasoning. cache_write and cache_read
			// stand in the row and are never in it (rule 13, BudgetColumns).
			name: "the_sum_is_three_columns", directives: "FAKE-USAGE-DB 100 50 9000 90000 7 0.5\nFAKE-FINDINGS 1\n", want: "157/50000",
			why: "cache_write and cache_read are in the row and never in the sum",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rc, stdout, stderr := runBudgetCard(t, "50000", tc.directives)
			if rc != 0 {
				t.Fatalf("this launch exits 0, got %d:\n%s", rc, stderr)
			}
			if got := budgetOf(t, stdout); got != tc.want {
				t.Fatalf("budget= is %q, want %q (%s):\n%s", got, tc.want, tc.why, stdout)
			}
		})
	}
}

// TestNativeUnmeteredIsNeverAFigure: a card launched `unmetered` prints the word whatever
// the harness reported, because `unmetered` is a statement of the caller's and not a
// measurement. The same harness under a number prints the number, which is the control.
func TestNativeUnmeteredIsNeverAFigure(t *testing.T) {
	needsSQLite(t)
	rc, stdout, stderr := runBudgetCard(t, "unmetered", "FAKE-USAGE-DB 100 50 0 0 7 0.5\nFAKE-FINDINGS 1\n")
	if rc != 0 {
		t.Fatalf("the launch exits 0, got %d:\n%s", rc, stderr)
	}
	if got := budgetOf(t, stdout); got != "unmetered" {
		t.Fatalf("an unmetered card prints the word whatever the harness reported, got %q:\n%s", got, stdout)
	}
}

// TestNativeSamplesThroughAWriteAheadLog: rule 13d, "a database with a write-ahead log
// beside it for the whole run is sampled and its reads are answers, never failures", and
// "a sample reads what the database holds through whatever write-ahead log lies beside it,
// which is the ordinary state of a database a live harness has open, and it waits for no
// checkpoint".
//
// THE -wal IS PUT THERE AND LEFT THERE, for the whole run, by putting the database into WAL
// journal mode before the card starts: that is what a live harness's database looks like,
// and it is the state in which a reader that waited for a checkpoint would wait forever.
func TestNativeSamplesThroughAWriteAheadLog(t *testing.T) {
	needsSQLite(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	// The data home and the store BEFORE the launch, so the -wal is beside the database
	// for the whole of it rather than appearing partway through.
	dataHome := filepath.Join(slot, "data")
	db := filepath.Join(dataHome, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	sql := `PRAGMA journal_mode=WAL;
CREATE TABLE message (id INTEGER PRIMARY KEY, data TEXT NOT NULL, time_created INTEGER NOT NULL);
INSERT INTO message (data, time_created) VALUES (json_object('role','assistant','providerID','fake','modelID','fake-model','tokens',json_object('input',1000,'output',500,'cache',json_object('write',0,'read',0),'reasoning',0),'cost',0.1), ` +
		strconv.FormatInt(time.Now().UnixMilli(), 10) + `);
`
	cmd := exec.Command(swarm.SQLiteBinary, db)
	cmd.Stdin = strings.NewReader(sql)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the WAL fixture store: %v\n%s", err, out)
	}
	// A connection that keeps the -wal in place for the run: sqlite removes it on the last
	// clean close, so a fixture that closed would be testing the absence of a -wal.
	hold, err := os.OpenFile(db+"-wal", os.O_RDONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	if _, err := os.Stat(db + "-wal"); err != nil {
		t.Fatalf("the fixture wanted a -wal beside the database for the whole run: %v", err)
	}

	card := filepath.Join(root, "card.md")
	if err := os.WriteFile(card, []byte("a card\nFAKE-SLEEP 2500ms\nFAKE-FINDINGS 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(budgetNativeArgs(t, bin, card, slot, root, "50000"), "--usage-interval", "1s")
	var stdout, stderr bytes.Buffer
	rc := run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("a card whose database has a -wal beside it runs, got exit %d:\n%s", rc, stderr.String())
	}
	// THE READS WERE ANSWERS. The final read saw the 1500 the fixture wrote; had the reads
	// been failures the line would carry the dash.
	if got := budgetOf(t, stdout.String()); got != "1500/50000" {
		t.Fatalf("the reads through the -wal are answers, so the line carries the figure; got %q:\n%s", got, stdout.String())
	}
	if _, err := os.Stat(db + "-wal"); err != nil {
		t.Errorf("the -wal was beside the database for the whole run, and is still: %v", err)
	}
}

// TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline: rule 13d, "no sample starts while
// one is unanswered", and "a usage reader that never returns does not move the deadline, and
// the run still ends inside the bound the deadline's own test holds (issue #779)".
//
// THE READER THAT NEVER RETURNS is a `sqlite3` on PATH that sleeps past every bound. The
// sampler gives one read 5 seconds and abandons it; the card's deadline is 3 seconds and is
// the thing under test, so a sampler that could hold the ending open would show here as a
// run that outlived its own deadline.
func TestNativeSamplerNeverOverlapsAndNeverDelaysTheDeadline(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	// A database must EXIST for the reader to be run at all: an absent one is an absence
	// and never a read.
	db := filepath.Join(slot, "data", "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(swarm.SQLiteBinary, db)
	cmd.Stdin = strings.NewReader("CREATE TABLE message (id INTEGER PRIMARY KEY, data TEXT NOT NULL, time_created INTEGER NOT NULL);\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture store: %v\n%s", err, out)
	}
	// A `sqlite3` on PATH that never answers, ahead of the real one.
	slow := t.TempDir()
	stall := filepath.Join(slow, swarm.SQLiteBinary)
	if err := os.WriteFile(stall, []byte("#!/bin/sh\nsleep 600\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", slow+string(os.PathListSeparator)+os.Getenv("PATH"))

	card := filepath.Join(root, "card.md")
	if err := os.WriteFile(card, []byte("a card\nFAKE-IGNORE-TERM\nFAKE-SLEEP 60\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	args := append(budgetNativeArgs(t, bin, card, slot, root, "50000"), "--usage-interval", "1s")
	for i := range args {
		if args[i] == "--deadline" {
			args[i+1] = "3s"
		}
	}
	// THE EVENT, NOT THE CLOCK. The reader sleeps ten minutes and the card's deadline is
	// three seconds. What is asserted is that the run RETURNS and that its DEADLINE is what
	// ended the card: `rc=-1` with NO `stopped=` field, which is the deadline's own shape and
	// not a sampler's. A sampler that could hold the ending open would not reach either
	// assertion at all -- the go test timeout is this repo's bound on a hang, and it is a
	// better one than a number written here, which is why the repo refuses the number.
	var stdout, stderr bytes.Buffer
	run(args, strings.NewReader(""), &stdout, &stderr, time.Now())
	line := nativeOKLine(t, stdout.String())
	if got := fieldOf(line, "rc"); got != "-1" {
		t.Fatalf("the DEADLINE ended this card, so the line prints rc=-1; got %q:\n%s", got, line)
	}
	if got := fieldOf(line, "stopped"); got != "" {
		t.Fatalf("a reader that never answers is not three FAILED reads while the card still had time; the deadline ended it and the line carries no stopped= field, got %q:\n%s", got, line)
	}
}

// TestLiveSamplerNeverRunsTwoReadsAtOnce holds the same clause at the unit, where it can be
// asserted rather than inferred: the loop is driven against a reader that is slower than
// its own interval, and no two reads are ever in flight together.
func TestLiveSamplerNeverRunsTwoReadsAtOnce(t *testing.T) {
	windowsIsNotABench(t)
	needsSQLite(t)
	dataHome := t.TempDir()
	db := filepath.Join(dataHome, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(swarm.SQLiteBinary, db)
	cmd.Stdin = strings.NewReader("CREATE TABLE message (id INTEGER PRIMARY KEY, data TEXT NOT NULL, time_created INTEGER NOT NULL);\n")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the fixture store: %v\n%s", err, out)
	}
	// A reader that takes a good deal longer than the interval below, so a loop that
	// queued its ticks would show two reads in flight at once.
	slow := t.TempDir()
	if err := os.WriteFile(filepath.Join(slow, swarm.SQLiteBinary), []byte("#!/bin/sh\nsleep 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", slow+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := startLiveSampler(dataHome, 50*time.Millisecond, nativeRunConfig{tokens: 1 << 30}, filepath.Join(dataHome, "harness-output.log"))
	// THE BARRIER IS THE COUNT, not a clock: wait for the loop to have ANSWERED two reads,
	// then stop it. A fixed sleep would be a bet on the bench's load, which this repo's own
	// law refuses.
	deadline := time.Now().Add(30 * time.Second)
	for {
		if answered, _ := s.Counts(); answered >= 2 {
			break
		}
		if time.Now().After(deadline) {
			answered, _ := s.Counts()
			s.Stop()
			t.Fatalf("the sampler answered %d reads in 30s; it wants two", answered)
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.Stop()
	answered, maxFlight := s.Counts()
	if maxFlight > 1 {
		t.Fatalf("no sample starts while one is unanswered (rule 13d): %d were in flight at once over %d reads", maxFlight, answered)
	}
}

// TestLiveSamplerCountsAFailedReadAndAnAnswerResetsIt: the two halves of rule 13d's
// unverifiable end, at the unit. A read that FAILS is not a source that reported nothing,
// and "two failures and then an answer end nothing" is the reset this asserts.
func TestLiveSamplerCountsAFailedReadAndAnAnswerResetsIt(t *testing.T) {
	windowsIsNotABench(t)
	dataHome := t.TempDir()
	db := filepath.Join(dataHome, "opencode", "opencode.db")
	if err := os.MkdirAll(filepath.Dir(db), 0o755); err != nil {
		t.Fatal(err)
	}
	// A file that is not a database, so every read of it FAILS -- as distinct from a
	// database that is not there, which is an absence.
	if err := os.WriteFile(db, []byte("not a database\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A reader that refuses, the way sqlite3 refuses bytes that are not a database.
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, swarm.SQLiteBinary),
		[]byte("#!/bin/sh\necho 'Error: file is not a database' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bad+string(os.PathListSeparator)+os.Getenv("PATH"))

	s := startLiveSampler(dataHome, 20*time.Millisecond, nativeRunConfig{tokens: 1 << 30}, filepath.Join(dataHome, "harness-output.log"))
	defer s.Stop()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, _, _, failures, err := s.Observed(); failures >= 3 {
			if err == nil {
				t.Fatalf("a failed read carries the reason it gave")
			}
			return
		}
		if time.Now().After(deadline) {
			_, _, _, failures, _ := s.Observed()
			t.Fatalf("three consecutive failed reads were counted; got %d in 30s", failures)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
