package pulse

// The bench row: the four readers behind its columns, the Prometheus textfile, and the two
// things bin/bench-row could not do -- say that a push failed, and count a card the same
// way whichever of the two places its result survives in.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// CountResults counts BOTH places a finished card's RESULT.md can be: under a job root,
// while the card's job directory is still there, and under <results>/<label>/ after hygiene
// deleted it at card end.
func TestCountResultsReadsJobRootsAndTheResultsDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	since := filepath.Join(dir, "SPRINT-START")
	if err := os.WriteFile(since, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// The stamp's mtime is the clock, so everything written after it counts. A filesystem
	// with second granularity would otherwise make a same-second write ambiguous.
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(since, old, old); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(dir, "swarm-root")
	writeResult(t, filepath.Join(root, "slot-1", "x", "jobs", "job-a", "RESULT.md"), "RESULT: DONE\n")
	writeResult(t, filepath.Join(root, "slot-1", "x", "jobs", "job-b", "RESULT.md"), "RESULT: RED\n")
	results := filepath.Join(dir, "results")
	writeResult(t, filepath.Join(results, "card-7", "RESULT.md"), "RESULT: DONE\n")

	done, ok, fail := CountResults([]string{root}, results, since)
	if done != 3 || ok != 2 || fail != 1 {
		t.Fatalf("done=%d ok=%d fail=%d, want 3/2/1", done, ok, fail)
	}
}

// With no sprint stamp there is no sprint, and the counts are ZERO -- never "everything
// ever", which on a bench with a year of job roots is a number nobody can act on.
func TestCountResultsWithNoSprintStampCountsNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	results := filepath.Join(dir, "results")
	writeResult(t, filepath.Join(results, "card-7", "RESULT.md"), "RESULT: DONE\n")
	done, ok, fail := CountResults(nil, results, filepath.Join(dir, "no-such-stamp"))
	if done != 0 || ok != 0 || fail != 0 {
		t.Fatalf("done=%d ok=%d fail=%d, want zeros", done, ok, fail)
	}
}

// A result written BEFORE the sprint started is not this sprint's.
func TestCountResultsIgnoresWhatIsOlderThanTheSprint(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	results := filepath.Join(dir, "results")
	old := filepath.Join(results, "card-old", "RESULT.md")
	writeResult(t, old, "RESULT: DONE\n")
	before := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, before, before); err != nil {
		t.Fatal(err)
	}
	since := filepath.Join(dir, "SPRINT-START")
	if err := os.WriteFile(since, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	hourAgo := time.Now().Add(-time.Hour)
	if err := os.Chtimes(since, hourAgo, hourAgo); err != nil {
		t.Fatal(err)
	}
	if done, _, _ := CountResults(nil, results, since); done != 0 {
		t.Fatalf("a result older than the stamp was counted: done=%d", done)
	}
}

// ONE fail set for both sources. bin/bench-row used a narrower set under the job roots than
// under <results>, so a `sprint-requeue` or a `RESULT: SILENT` card was a FAIL in one place
// and OK in the other -- the same card changing column when its job directory was swept.
func TestTheSameResultIsAFailWhereverItLives(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"written-by: nova-swarm native\n",
		"written-by: sprint-requeue\n",
		"written-by: bench-sweep\n",
		"RESULT: BLOCKED\n",
		"RESULT: FAILED\n",
		"RESULT: RED\n",
		"RESULT: SILENT\n",
	} {
		t.Run(strings.TrimSpace(body), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			since := filepath.Join(dir, "SPRINT-START")
			if err := os.WriteFile(since, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(since, old, old); err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(dir, "swarm-root")
			writeResult(t, filepath.Join(root, "slot-1", "x", "jobs", "job-a", "RESULT.md"), body)
			results := filepath.Join(dir, "results")
			writeResult(t, filepath.Join(results, "card-7", "RESULT.md"), body)

			done, ok, fail := CountResults([]string{root}, results, since)
			if done != 2 || ok != 0 || fail != 2 {
				t.Fatalf("%q under a job root and under results: done=%d ok=%d fail=%d, want 2/0/2", body, done, ok, fail)
			}
		})
	}
}

// An ordinary DONE is OK on both sides.
func TestAPlainDoneResultIsOK(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	since := filepath.Join(dir, "SPRINT-START")
	if err := os.WriteFile(since, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(since, old, old); err != nil {
		t.Fatal(err)
	}
	results := filepath.Join(dir, "results")
	writeResult(t, filepath.Join(results, "card-7", "RESULT.md"), "label: card-7\nRESULT: DONE\nwritten-by: the harness\n")
	done, ok, fail := CountResults(nil, results, since)
	if done != 1 || ok != 1 || fail != 0 {
		t.Fatalf("done=%d ok=%d fail=%d", done, ok, fail)
	}
}

// The queue column is the .md cards waiting in ready and ready-pro. A bench with no pro
// queue is an ordinary bench, not an error.
func TestQueueCountsBothReadyDirectoriesAndToleratesAMissingOne(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeResult(t, filepath.Join(dir, "ready", "card-1.md"), "x")
	writeResult(t, filepath.Join(dir, "ready", "card-2.md"), "x")
	writeResult(t, filepath.Join(dir, "ready", "notes.txt"), "not a card")
	if got := countReadyCards(dir); got != 2 {
		t.Fatalf("queue = %d, want 2 (only .md files, and a missing ready-pro is zero)", got)
	}
	writeResult(t, filepath.Join(dir, "ready-pro", "card-3.md"), "x")
	if got := countReadyCards(dir); got != 3 {
		t.Fatalf("queue = %d, want 3", got)
	}
	if got := countReadyCards(""); got != 0 {
		t.Fatalf("no queue directory is zero, got %d", got)
	}
}

// A push that fails is SAID, every time, and the verb exits non-zero. bin/bench-row sent
// redis-cli's output to /dev/null, so a bench whose ACL had lapsed looked exactly like a
// bench with nothing to do.
func TestARowPushThatFailsIsLoudAndExitsNonZero(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	since := filepath.Join(dir, "SPRINT-START")
	if err := os.WriteFile(since, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb strings.Builder
	code := Row(BenchRowInput{
		Host: "hulk", Once: true, Since: since, Stdout: &out, Stderr: &errb,
		Push: func(context.Context, BenchRow) error { return errors.New("NOPERM this user has no permissions") },
	})
	if code == 0 {
		t.Fatal("a row that never reached the store must not exit 0")
	}
	if !strings.Contains(errb.String(), "ROW PUSH FAILED bench=hulk") ||
		!strings.Contains(errb.String(), "NOPERM") {
		t.Fatalf("the failure must name the bench and the reason: %s", errb.String())
	}
}

// --print measures and prints without a store: the answer to "why does my bench say 0
// done?". It pushes nothing, and says so with a dash rather than pushed=0.
func TestRowPrintMeasuresWithoutAStore(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	since := filepath.Join(dir, "SPRINT-START")
	if err := os.WriteFile(since, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(since, old, old); err != nil {
		t.Fatal(err)
	}
	results := filepath.Join(dir, "results")
	writeResult(t, filepath.Join(results, "card-1", "RESULT.md"), "RESULT: DONE\n")

	var out, errb strings.Builder
	code := Row(BenchRowInput{
		Host: "hulk", Print: true, Since: since, Results: results, Stdout: &out, Stderr: &errb,
	})
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "done=1 ok=1 fail=0") || !strings.Contains(out.String(), "pushed=-") {
		t.Fatalf("print receipt: %s", out.String())
	}
}

// The Prometheus textfile carries the same five counts, and is written atomically so
// node_exporter's periodic read of the directory never sees half a file.
func TestTextfileCarriesTheSameCountsAndIsWrittenWhole(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "nova_cards.prom")
	row := BenchRow{Host: "hulk", Queue: 12, Working: 8, Done: 140, OK: 119, Fail: 21}
	if err := WriteBenchTextfile(path, row); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, want := range []string{
		`nova_cards_queue{bench="hulk"} 12`,
		`nova_cards_working{bench="hulk"} 8`,
		`nova_cards_done{bench="hulk"} 140`,
		`nova_cards_ok{bench="hulk"} 119`,
		`nova_cards_fail{bench="hulk"} 21`,
		"# TYPE nova_cards_queue gauge",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the textfile does not carry %q:\n%s", want, body)
		}
	}
	// No temporary file is left behind for the collector to read as a second bench.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("the atomic write left something behind: %v", entries)
	}
}

// The load is text read from the machine, and a machine neither reader can answer for gives
// a dash -- never 0, because a zero load reads as an idle bench.
func TestReadLoad1IsANumberOrADash(t *testing.T) {
	t.Parallel()
	got := ReadLoad1()
	if got == "" {
		t.Fatal("the load is never empty")
	}
	if got == "0" {
		t.Fatal("a load of exactly \"0\" is the unread case the dash exists for")
	}
}
