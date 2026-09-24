package pulse

// NO EVIDENCE IS NOT NEGATIVE EVIDENCE (Stella's HOLD 7 on #2622 at 7bba9a90). Each of the
// bench row's three counted cells -- queue, working, and done/ok/fail -- has three answers,
// and each is tested here:
//
//   - present: the source was read, and the number is the count;
//   - absent: the source is intentionally not there (no queue given, no slot store, no
//     sprint stamp) and the cell is `-`;
//   - unavailable: the source is there and could not be read (a permission or I/O error)
//     and the cell is `?`, with the source and the error said on stderr. The row is still
//     pushed, so the bench's presence on the table is not lost with its count.
//
// Before this, every one of the unreadable cases printed 0, and an unreadable queue or lease
// store rendered as a healthy idle bench.

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// unreadable makes dir (creating it) mode 000 for the rest of the test and restores it in
// Cleanup so t.TempDir can remove it. Root reads a 000 directory anyway, and Windows has no
// such mode, so both skip: the test would pass for the wrong reason.
func unreadable(t *testing.T, dir string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no 000-mode directories on windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root reads a 000-mode directory; the permission case cannot be made")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
}

// sprintStamp writes a SPRINT-START an hour old, so everything the test writes after it
// counts.
func sprintStamp(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path
}

// rowOnce runs one --once row into a capturing sink and returns the exit, stdout, stderr and
// the number of rows the sink was handed.
func rowOnce(t *testing.T, in BenchRowInput) (code int, out, errb string, pushed int) {
	t.Helper()
	var o, e strings.Builder
	in.Host = "hulk"
	in.Once = true
	in.Stdout, in.Stderr = &o, &e
	in.Push = func(context.Context, BenchRow) error { pushed++; return nil }
	code = Row(in)
	return code, o.String(), e.String(), pushed
}

// An unavailable cell is `?`, is said with its source and reason, exits non-zero, and the
// row is still pushed.
func wantUnavailable(t *testing.T, code int, out, errb string, pushed int, cell, source string) {
	t.Helper()
	if !strings.Contains(out, cell) {
		t.Errorf("an unreadable %s must print %q, not a number: %s", source, cell, out)
	}
	if !strings.Contains(errb, "unavailable: "+source+": ") || !strings.Contains(errb, "permission denied") {
		t.Errorf("stderr must say `unavailable: %s: <err>`: %q", source, errb)
	}
	if code == 0 {
		t.Errorf("a row with an unreadable source must not exit 0")
	}
	if pushed != 1 {
		t.Errorf("the row must still be pushed so the bench stays on the table: pushed %d", pushed)
	}
}

func TestQueueCellPresentAbsentUnavailable(t *testing.T) {
	t.Parallel()
	t.Run("present", func(t *testing.T) {
		t.Parallel()
		q := filepath.Join(t.TempDir(), "queue")
		writeResult(t, filepath.Join(q, "ready", "card-1.md"), "x")
		code, out, errb, _ := rowOnce(t, BenchRowInput{Queue: q})
		if code != 0 || !strings.Contains(out, " queue=1 ") {
			t.Fatalf("exit %d: %s %s", code, out, errb)
		}
	})
	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		q := filepath.Join(t.TempDir(), "no-queue")
		code, out, errb, _ := rowOnce(t, BenchRowInput{Queue: q})
		if code != 0 || !strings.Contains(out, " queue=- ") {
			t.Fatalf("a queue that is not there is `-`, not 0: exit %d: %s %s", code, out, errb)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		t.Parallel()
		q := filepath.Join(t.TempDir(), "queue")
		unreadable(t, filepath.Join(q, "ready"))
		code, out, errb, pushed := rowOnce(t, BenchRowInput{Queue: q})
		wantUnavailable(t, code, out, errb, pushed, " queue=? ", "queue")
	})
}

func TestWorkingCellPresentAbsentUnavailable(t *testing.T) {
	t.Parallel()
	t.Run("present", func(t *testing.T) {
		t.Parallel()
		store := filepath.Join(t.TempDir(), "slots")
		if err := swarm.MakeSlotLease(store, "lease-1", "rowan", os.Getpid(), "card-1", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		code, out, errb, _ := rowOnce(t, BenchRowInput{Slots: store})
		if code != 0 || !strings.Contains(out, " working=1 ") {
			t.Fatalf("exit %d: %s %s", code, out, errb)
		}
	})
	t.Run("absent", func(t *testing.T) {
		t.Parallel()
		store := filepath.Join(t.TempDir(), "no-slots")
		code, out, errb, _ := rowOnce(t, BenchRowInput{Slots: store})
		if code != 0 || !strings.Contains(out, " working=- ") {
			t.Fatalf("a slot store that is not there is `-`, not 0: exit %d: %s %s", code, out, errb)
		}
	})
	t.Run("unavailable", func(t *testing.T) {
		t.Parallel()
		store := filepath.Join(t.TempDir(), "slots")
		if err := swarm.MakeSlotLease(store, "lease-1", "rowan", os.Getpid(), "card-1", time.Now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		unreadable(t, store)
		code, out, errb, pushed := rowOnce(t, BenchRowInput{Slots: store})
		wantUnavailable(t, code, out, errb, pushed, " working=? ", "slots")
	})
}

func TestResultsCellsPresentAbsentUnavailable(t *testing.T) {
	t.Parallel()
	t.Run("present", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		since := sprintStamp(t, filepath.Join(dir, "SPRINT-START"))
		results := filepath.Join(dir, "results")
		writeResult(t, filepath.Join(results, "card-1", "RESULT.md"), "RESULT: DONE\n")
		code, out, errb, _ := rowOnce(t, BenchRowInput{Since: since, Results: results})
		if code != 0 || !strings.Contains(out, " done=1 ok=1 fail=0 ") {
			t.Fatalf("exit %d: %s %s", code, out, errb)
		}
	})
	t.Run("absent: no sprint", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		results := filepath.Join(dir, "results")
		writeResult(t, filepath.Join(results, "card-1", "RESULT.md"), "RESULT: DONE\n")
		code, out, errb, _ := rowOnce(t, BenchRowInput{Since: filepath.Join(dir, "no-stamp"), Results: results})
		if code != 0 || !strings.Contains(out, " done=- ok=- fail=- ") {
			t.Fatalf("no sprint is `-`, not 0: exit %d: %s %s", code, out, errb)
		}
	})
	t.Run("present: sprint with no results directory yet is a real zero", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		since := sprintStamp(t, filepath.Join(dir, "SPRINT-START"))
		code, out, errb, _ := rowOnce(t, BenchRowInput{
			Since: since, Results: filepath.Join(dir, "no-results"), Roots: []string{filepath.Join(dir, "no-root")},
		})
		if code != 0 || !strings.Contains(out, " done=0 ok=0 fail=0 ") {
			t.Fatalf("exit %d: %s %s", code, out, errb)
		}
	})

	// Every place a result is read from, made unreadable in turn.
	for _, tc := range []struct {
		name   string
		source string
		setup  func(t *testing.T, dir string) BenchRowInput
	}{
		{"unavailable: results directory", "results", func(t *testing.T, dir string) BenchRowInput {
			since := sprintStamp(t, filepath.Join(dir, "SPRINT-START"))
			results := filepath.Join(dir, "results")
			writeResult(t, filepath.Join(results, "card-1", "RESULT.md"), "RESULT: DONE\n")
			unreadable(t, results)
			return BenchRowInput{Since: since, Results: results}
		}},
		{"unavailable: a result's label directory", "results", func(t *testing.T, dir string) BenchRowInput {
			since := sprintStamp(t, filepath.Join(dir, "SPRINT-START"))
			results := filepath.Join(dir, "results")
			writeResult(t, filepath.Join(results, "card-1", "RESULT.md"), "RESULT: DONE\n")
			unreadable(t, filepath.Join(results, "card-1"))
			return BenchRowInput{Since: since, Results: results}
		}},
		{"unavailable: a job root", "roots", func(t *testing.T, dir string) BenchRowInput {
			since := sprintStamp(t, filepath.Join(dir, "SPRINT-START"))
			root := filepath.Join(dir, "swarm-root")
			writeResult(t, filepath.Join(root, "slot-1", "x", "jobs", "job-a", "RESULT.md"), "RESULT: DONE\n")
			unreadable(t, root)
			return BenchRowInput{Since: since, Roots: []string{root}}
		}},
		{"unavailable: a directory under a job root", "roots", func(t *testing.T, dir string) BenchRowInput {
			since := sprintStamp(t, filepath.Join(dir, "SPRINT-START"))
			root := filepath.Join(dir, "swarm-root")
			writeResult(t, filepath.Join(root, "slot-1", "x", "jobs", "job-a", "RESULT.md"), "RESULT: DONE\n")
			unreadable(t, filepath.Join(root, "slot-1"))
			return BenchRowInput{Since: since, Roots: []string{root}}
		}},
		{"unavailable: the sprint stamp", "since", func(t *testing.T, dir string) BenchRowInput {
			stampDir := filepath.Join(dir, "nova-bench")
			since := sprintStamp(t, filepath.Join(stampDir, "SPRINT-START"))
			unreadable(t, stampDir)
			return BenchRowInput{Since: since}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := tc.setup(t, t.TempDir())
			code, out, errb, pushed := rowOnce(t, in)
			wantUnavailable(t, code, out, errb, pushed, " done=? ok=? fail=? ", tc.source)
		})
	}
}

// The textfile never writes a 0 for a count nobody took: an unreadable or absent cell's
// metric is left out, which Prometheus reads as "no data", not as an idle bench.
func TestTextfileLeavesOutACountNobodyTook(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	q := filepath.Join(dir, "queue")
	unreadable(t, filepath.Join(q, "ready"))
	prom := filepath.Join(dir, "nova_cards.prom")
	code, _, errb, _ := rowOnce(t, BenchRowInput{Queue: q, Textfile: prom, Since: filepath.Join(dir, "no-stamp")})
	if code == 0 {
		t.Fatalf("exit 0 with an unreadable queue: %s", errb)
	}
	raw, err := os.ReadFile(prom)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{`nova_cards_queue{`, `nova_cards_done{`, `nova_cards_ok{`, `nova_cards_fail{`} {
		if strings.Contains(string(raw), gone) {
			t.Errorf("the textfile wrote %s for a count nobody took:\n%s", gone, raw)
		}
	}
}

// Over the wire: the `?` a bench pushed is the `?` the table prints, the reason rides with
// it, and a total with an unreadable part is `?` too.
func TestAnUnreadableSourceReachesTheTableAsAQuestionMark(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	dir := t.TempDir()
	q := filepath.Join(dir, "queue")
	unreadable(t, filepath.Join(q, "ready"))
	var out, errb strings.Builder
	code := Row(BenchRowInput{
		Store: StoreOptions{Addr: mr.Addr()}, Host: "test-bench", Once: true, Queue: q,
		Since:  sprintStamp(t, filepath.Join(dir, "SPRINT-START")),
		Stdout: &out, Stderr: &errb,
	})
	if code == 0 {
		t.Fatalf("exit 0 with an unreadable queue: %s", out.String())
	}
	if !mr.Exists("bench:test-bench") {
		t.Fatal("the row was not pushed: the bench would vanish from the table with its count")
	}
	rdb, err := DialStore(context.Background(), StoreOptions{Addr: mr.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	defer rdb.Close()
	now := at("2026-09-22T18:00:00Z")
	st, err := ReadSwarmState(context.Background(), redisSwarmReader{rdb: rdb}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	table := RenderSwarmTable(st, now)
	var line, total string
	for _, l := range strings.Split(table, "\n") {
		if strings.HasPrefix(l, "test-bench ") {
			line = l
		}
		if strings.HasPrefix(l, "total ") {
			total = l
		}
	}
	if !strings.HasPrefix(line, "test-bench |     ? |") {
		t.Errorf("the row's queue cell must be `?`:\n%s", table)
	}
	if !strings.HasPrefix(total, "total      |     ? |") {
		t.Errorf("a total with an unreadable part is `?`:\n%s", table)
	}
	if !strings.Contains(table, "unavailable: test-bench queue: ") || !strings.Contains(table, "permission denied") {
		t.Errorf("the table must say which source and why:\n%s", table)
	}
}

// The reader keeps the marks a bench pushed, and a count that is missing or will not parse
// is `?` on the table, never 0. An absent (`-`) cell adds nothing to a total and does not
// make the total unknown.
func TestTheTableKeepsMarksAndTotalsThem(t *testing.T) {
	t.Parallel()
	s := newFakeStore()
	s.hashes["bench:hulk"] = map[string]string{"host": "hulk", "queue": "-", "working": "3", "done": "4", "ok": "4", "fail": "0", "load1": "1.0"}
	s.hashes["bench:space"] = map[string]string{"host": "space", "queue": "5", "working": "?", "done": "2", "ok": "1", "fail": "1", "load1": "1.0"}
	now := at("2026-09-22T18:00:00Z")
	st, err := ReadSwarmState(context.Background(), s, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	table := RenderSwarmTable(st, now)
	for _, want := range []string{
		"hulk       |     - |       3 |     4 |     4 |     0 | 100% |",
		"space      |     5 |       ? |     2 |     1 |     1 |  50% |",
		"total      |     5 |       ? |     6 |     5 |     1 |  83% |",
	} {
		if !strings.Contains(table, want) {
			t.Errorf("want %q in:\n%s", want, table)
		}
	}
}
