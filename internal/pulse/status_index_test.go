package pulse

// status-fast-on-a-real-root (#1088): status --oneline reads a per-root index
// (<root>/status-index.tsv) and refreshes only the jobs whose dir mtime moved. The
// index behaviour is asserted here with no wall bound, so the fast suite never leans
// on the machine's load; the 2,000-job wall budget is a slow-tier test below.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// bigStatusRoot writes 2,000 finished jobs across 40 slots, each with RESULT.md,
// usage.tsv and a 200-line harness log, and returns the bench root.
func bigStatusRoot(t *testing.T, day string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "bench")
	const slots, perSlot = 40, 50
	for s := 0; s < slots; s++ {
		for j := 0; j < perSlot; j++ {
			label := fmt.Sprintf("card-%d-%d", s, j)
			dir := filepath.Join(root, strconv.Itoa(s), "jobs", label)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(dir, "RESULT.md"),
				"RESULT: "+label+" done\nDONE\nBRANCH rowan/"+label+"\nREPO mas-bandwidth/nova-tools\n")
			write(t, filepath.Join(dir, "usage.tsv"),
				"job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n"+
					label+"\t1\t"+day+"T09:00:00Z\t"+day+"T09:10:00Z\t0\t-\tgo\t-\t-\t-\t-\t-\t0.0010\n")
			var log strings.Builder
			for l := 0; l < 200; l++ {
				fmt.Fprintf(&log, "harness %s step %d\n", label, l)
			}
			write(t, filepath.Join(dir, "harness.log"), log.String())
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

	// A job whose directory mtime moved is re-read and its new row answered.
	dir := filepath.Join(root, "0", "jobs", "card-0-0")
	write(t, filepath.Join(dir, "usage.tsv"),
		"job\tattempt\tstarted\tended\trc\tprovider\tmodel\ttokens_in\ttokens_out\tcache_write\tcache_read\treasoning\tusd\n"+
			"card-0-0\t1\t"+day+"T09:00:00Z\t"+day+"T09:20:00Z\t0\t-\tgo\t-\t-\t-\t-\t-\t0.0050\n")
	moved := time.Now().Add(time.Second)
	if err := os.Chtimes(dir, moved, moved); err != nil {
		t.Fatal(err)
	}
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
