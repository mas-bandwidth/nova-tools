package pulse

// status-fast-on-a-real-root (#1088): status --oneline reads a per-root index
// (<root>/status-index.tsv) and refreshes only the jobs whose dir mtime moved. The
// index behaviour is asserted here with no wall bound, so the fast suite never leans
// on the machine's load; the 2,000-job wall budget is a slow-tier test below.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// requireSqlite skips the calling test BY NAME when the sqlite3 CLI is not on PATH, so a
// host without the store reader never runs a fixture that needs it; the msg is one bounded
// line and the remedy names the binary, as SPEC-SWARM's readers already do.
func requireSqlite(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skipf("sqlite3 is not on PATH: the fixture needs the harness store data/opencode/opencode.db; install sqlite3 to run this test here")
	}
}

// fixtureDBSeed builds one real sqlite store with the sqlite3 CLI and returns its bytes,
// so every fixture job carries the data home a real job carries
// (data/opencode/opencode.db) without one sqlite3 process per job. A caller that asserts on
// the store calls requireSqlite first; a caller that reads only files (the class comes from
// RESULT.md, not the database) runs everywhere, and the seed is absent and said so in one
// bounded line.
func fixtureDBSeed(t *testing.T) []byte {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Logf("sqlite3 is not on PATH: fixture jobs carry no data/opencode/opencode.db")
		return nil
	}
	seed := filepath.Join(t.TempDir(), "seed")
	if err := os.MkdirAll(seed, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(seed, "opencode.db")
	cmd := exec.Command("sqlite3", path,
		"CREATE TABLE message (id TEXT, data TEXT, time_created INTEGER);")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed sqlite store: %v: %s", err, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fixtureLog is the 300 KB harness log every fixture job carries: the file a real job's
// harness writes, which status must never open on a tick that already has the index.
func fixtureLog(label string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "harness %s step 0\n", label)
	const target = 300 * 1024
	for b.Len() < target {
		b.WriteString("harness log line; the harness writes this after usage.tsv is final\n")
	}
	return b.String()
}

// writeAt writes a fixture file and sets its mtime explicitly, so the index keys the test
// asserts on are the ones the test set and never the clock's sub-second guess (#1088).
func writeAt(t *testing.T, path, body string, mod time.Time) {
	t.Helper()
	write(t, path, body)
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

// fixtureBase is the fixed clock origin every indexed fixture file is stamped from: the
// status index keys on the tuples, so the test sets them rather than trusting the clock.
var fixtureBase = time.Date(2026, 9, 16, 8, 0, 0, 0, time.UTC)

// bigStatusRoot writes 2,000 finished jobs across 40 slots, each with RESULT.md,
// usage.tsv, a real data/opencode/opencode.db and a 300 KB harness log, and returns the
// bench root. The db and the log are what a real job carries and the fixture used to miss;
// the index must not touch either on a warm tick (#1088). Each job's three indexed files
// get explicit, distinct mtimes so the key is deterministic on any filesystem.
func bigStatusRoot(t *testing.T, day string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "bench")
	dbSeed := fixtureDBSeed(t)
	logBlob := fixtureLog("fixture")
	const slots, perSlot = 40, 50
	for s := 0; s < slots; s++ {
		for j := 0; j < perSlot; j++ {
			label := fmt.Sprintf("card-%d-%d", s, j)
			dir := filepath.Join(root, strconv.Itoa(s), "jobs", label)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			base := fixtureBase.Add(time.Duration(s*perSlot+j) * time.Minute)
			writeAt(t, filepath.Join(dir, "RESULT.md"),
				"RESULT: "+label+" done\nDONE\nBRANCH rowan/"+label+"\nREPO mas-bandwidth/nova-tools\n", base.Add(2*time.Second))
			writeAt(t, filepath.Join(dir, "usage.tsv"),
				"job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n"+
					label+"\t1\t"+day+"T09:00:00Z\t"+day+"T09:10:00Z\t0\t-\tgo\t-\t-\t-\t-\t-\t0.0010\n", base)
			if dbSeed != nil {
				dataDir := filepath.Join(dir, "data", "opencode")
				if err := os.MkdirAll(dataDir, 0o755); err != nil {
					t.Fatal(err)
				}
				dbPath := filepath.Join(dataDir, "opencode.db")
				if err := os.WriteFile(dbPath, dbSeed, 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(dbPath, base.Add(time.Second), base.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			write(t, filepath.Join(dir, "harness.log"), logBlob)
		}
	}
	return root
}

// bigStatusQueue writes a queue with 1,000 pending cards and the state files status reads.
func bigStatusQueue(t *testing.T) string {
	t.Helper()
	queue := filepath.Join(t.TempDir(), "queue")
	if err := os.MkdirAll(filepath.Join(queue, "pending"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		write(t, filepath.Join(queue, "pending", fmt.Sprintf("card-%04d.md", i)),
			fmt.Sprintf("CARD %d\n", i))
	}
	write(t, filepath.Join(queue, "COORDINATOR"), "glenn\n")
	write(t, filepath.Join(queue, "MERGED"), "2026-09-16T10:00:00Z\tMERGED\tnova-tools#700\n")
	return queue
}

func runOnelineStatus(queue, root, day string) (string, int) {
	var out, errs bytes.Buffer
	code := StatusLine(StatusInput{
		Queue: queue, Roots: root, Day: day,
		Stdout: &out, Stderr: &errs,
		Now: func() time.Time { return time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC) },
	})
	return out.String(), code
}

// indexDataRows counts the job rows in a status index, header excluded.
func indexDataRows(raw string) int {
	n := 0
	for _, l := range strings.Split(raw, "\n") {
		if l == "" || strings.HasPrefix(l, "job\t") {
			continue
		}
		n++
	}
	return n
}

// TestStatusUsesThePerRootIndex is the fast half of #1088 with no wall bound: the
// first tick builds the index, the next answers from it without opening a job file,
// and only a job whose directory mtime moved is re-read.
func TestStatusUsesThePerRootIndex(t *testing.T) {
	const day = "2026-09-16"
	root := bigStatusRoot(t, day)
	queue := bigStatusQueue(t)

	// Cold: a root with no index is walked once and the index written.
	out, code := runOnelineStatus(queue, root, day)
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	raw, err := os.ReadFile(filepath.Join(root, statusIndexName))
	if err != nil {
		t.Fatalf("cold status must write %s: %v", statusIndexName, err)
	}
	if rows := indexDataRows(string(raw)); rows != 2000 {
		t.Fatalf("%s has %d job rows after the first tick, want 2000", statusIndexName, rows)
	}
	if !strings.Contains(out, "spend=2.0000") {
		t.Errorf("cold status must sum the day's rows (2000 x 0.0010):\n%s", out)
	}

	// Warm: the index answers the tick and no job file is opened.
	before, err := os.Stat(filepath.Join(root, statusIndexName))
	if err != nil {
		t.Fatal(err)
	}
	atomic.StoreInt64(&statusIndexReads, 0)
	out, code = runOnelineStatus(queue, root, day)
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if got := atomic.LoadInt64(&statusIndexReads); got != 0 {
		t.Fatalf("warm status opened %d job files, want 0: the index must answer the tick", got)
	}
	if !strings.Contains(out, "spend=2.0000") {
		t.Errorf("warm status must answer from the index (spend=2.0000):\n%s", out)
	}
	after, err := os.Stat(filepath.Join(root, statusIndexName))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("warm status rewrote %s; the index is touched only when a job moves", statusIndexName)
	}

	// A job whose usage.tsv moved is re-read and its new row answered. The mtime is set
	// explicitly, so the key moves on any filesystem however fast the fixture writes.
	dir := filepath.Join(root, "0", "jobs", "card-0-0")
	moved := fixtureBase.Add(24 * time.Hour)
	writeAt(t, filepath.Join(dir, "usage.tsv"),
		"job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n"+
			"card-0-0\t1\t"+day+"T09:00:00Z\t"+day+"T09:20:00Z\t0\t-\tgo\t-\t-\t-\t-\t-\t0.0050\n", moved)
	atomic.StoreInt64(&statusIndexReads, 0)
	out, code = runOnelineStatus(queue, root, day)
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if got := atomic.LoadInt64(&statusIndexReads); got != 1 {
		t.Fatalf("status opened %d job files after one dir moved, want 1", got)
	}
	if !strings.Contains(out, "spend=2.0040") {
		t.Errorf("status must fold the moved job's new usd (2.0000 - 0.0010 + 0.0050 = 2.0040):\n%s", out)
	}
}

// indexLine returns the index's row for one job path, or "" when it is not indexed.
func indexLine(raw, job string) string {
	for _, l := range strings.Split(raw, "\n") {
		if strings.HasPrefix(l, job+"\t") {
			return l
		}
	}
	return ""
}

// TestStatusIndexNeverReopensAFinishedJob is #1088's real cost, against an honest fixture:
// each job carries a real data/opencode/opencode.db and a 300 KB harness log. A finished
// job's directory still moves after finalize -- the harness rotates its log -- but
// usage.tsv, the store and RESULT.md do not change, so the second status call must make
// zero usage reads: no sqlite3, no log read. The index used to key on the job directory's
// mtime, so any move re-opened the job and shelled out per job on a real root.
func TestStatusIndexNeverReopensAFinishedJob(t *testing.T) {
	requireSqlite(t)
	const day = "2026-09-16"
	root := bigStatusRoot(t, day)
	queue := bigStatusQueue(t)

	if _, code := runOnelineStatus(queue, root, day); code != 0 {
		t.Fatalf("cold status exit = %d", code)
	}

	// The harness keeps touching the job directory after usage.tsv is final: it rotates its
	// log, which moves the directory's mtime but leaves the three indexed files alone.
	dir := filepath.Join(root, "0", "jobs", "card-0-0")
	if err := os.Rename(filepath.Join(dir, "harness.log"), filepath.Join(dir, "harness.log.1")); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "harness.log"), "the harness restarted and logged one line\n")
	moved := fixtureBase.Add(48 * time.Hour)
	if err := os.Chtimes(dir, moved, moved); err != nil {
		t.Fatal(err)
	}

	atomic.StoreInt64(&statusIndexReads, 0)
	out, code := runOnelineStatus(queue, root, day)
	if code != 0 {
		t.Fatalf("warm status exit = %d: %s", code, out)
	}
	if got := atomic.LoadInt64(&statusIndexReads); got != 0 {
		t.Fatalf("a finished job was re-opened %d times after its log moved; the index keys on usage.tsv, data/opencode/opencode.db and RESULT.md, none of which moved", got)
	}
	if !strings.Contains(out, "spend=2.0000") {
		t.Errorf("the index must still answer the day's spend:\n%s", out)
	}
}

// classStatusRoot writes the two files the index's class is read from -- usage.tsv and
// RESULT.md -- and no harness store, because the class is RESULT.md's verdict and not the
// database's. Its fixture needs no sqlite3, so the class claim holds on every platform.
func classStatusRoot(t *testing.T, day string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "bench")
	dir := filepath.Join(root, "0", "jobs", "card-0-0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := fixtureBase
	writeAt(t, filepath.Join(dir, "RESULT.md"),
		"RESULT: card-0-0 done\nDONE\nBRANCH rowan/card-0-0\nREPO mas-bandwidth/nova-tools\n", base.Add(2*time.Second))
	writeAt(t, filepath.Join(dir, "usage.tsv"),
		"job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n"+
			"card-0-0\t1\t"+day+"T09:00:00Z\t"+day+"T09:10:00Z\t0\t-\tgo\t-\t-\t-\t-\t-\t0.0010\n", base)
	return root
}

// TestStatusIndexCarriesResultClass is the class half of the reopen test with a fixture that
// needs no database: the index's class is RESULT.md's verdict, so it holds everywhere -- the
// reopen half, whose fixture carries a real store, skips by name where sqlite3 is not on PATH.
func TestStatusIndexCarriesResultClass(t *testing.T) {
	const day = "2026-09-16"
	root := classStatusRoot(t, day)
	queue := t.TempDir()

	if _, code := runOnelineStatus(queue, root, day); code != 0 {
		t.Fatalf("cold status exit = %d", code)
	}

	// A job that wrote ABSTAIN answers abstain, and RESULT.md moving is what refreshes it.
	dir := filepath.Join(root, "0", "jobs", "card-0-0")
	future := fixtureBase.Add(72 * time.Hour)
	writeAt(t, filepath.Join(dir, "RESULT.md"),
		"RESULT: card-0-0 done\nABSTAIN the fixture changed its mind\nBRANCH rowan/card-0-0\n", future)
	if _, code := runOnelineStatus(queue, root, day); code != 0 {
		t.Fatalf("status exit = %d after RESULT.md moved", code)
	}
	raw, err := os.ReadFile(filepath.Join(root, statusIndexName))
	if err != nil {
		t.Fatal(err)
	}
	job := filepath.Join("0", "jobs", "card-0-0", "usage.tsv")
	if row := indexLine(string(raw), job); !strings.Contains(row, "\tabstain\t") {
		t.Errorf("the index must carry RESULT.md's class per job, got %q", row)
	}
}

// TestStatusOnelineTwoThousandJobRootIsFast is the wall budget, and it is a slow-tier
// test: a wall bound asserts the machine's load, not the code, so the fast suite never
// runs it. It needs a non-short run AND NOVA_SLOW_TESTS.
func TestStatusOnelineTwoThousandJobRootIsFast(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: the 2,000-job wall budget asserts the machine, not the code")
	}
	if os.Getenv("NOVA_SLOW_TESTS") == "" {
		t.Skip("slow: set NOVA_SLOW_TESTS=1 to run the 2,000-job wall budget")
	}
	const day = "2026-09-16"
	root := bigStatusRoot(t, day)
	queue := bigStatusQueue(t)

	// Cold: a root with no index is walked once and the index written.
	start := time.Now()
	out, code := runOnelineStatus(queue, root, day)
	cold := time.Since(start)
	t.Logf("cold status --oneline wall = %s", cold)
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if cold > 10*time.Second { // wall-ok: performance budget on a fixture
		t.Fatalf("status --oneline took %s on a 2,000-job root, want under 10s", cold)
	}
	if !strings.Contains(out, "spend=2.0000") {
		t.Errorf("spend must still sum the day's rows (2000 x 0.0010):\n%s", out)
	}

	// Warm: the index answers the tick without opening a job file.
	start = time.Now()
	out, code = runOnelineStatus(queue, root, day)
	warm := time.Since(start)
	t.Logf("warm status --oneline wall = %s", warm)
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if warm > 10*time.Second { // wall-ok: performance budget on a fixture
		t.Fatalf("warm status --oneline took %s on a 2,000-job root, want under 10s", warm)
	}
	if !strings.Contains(out, "spend=2.0000") {
		t.Errorf("warm status must answer from the index (spend=2.0000):\n%s", out)
	}
}
