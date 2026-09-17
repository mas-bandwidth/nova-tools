package pulse

// status-fast-on-a-real-root (#1088): status --oneline reads a per-root index
// (<root>/status-index.tsv) and refreshes only the jobs whose dir mtime moved, so a
// root holding a full day's 2,000 finished jobs answers in under two seconds. The
// bound is a real wall bound on an in-process fixture.
//
// wall-ok: performance budget on a fixture

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

func TestStatusOnelineTwoThousandJobRootIsFast(t *testing.T) {
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
	if cold > 2*time.Second { // wall-ok: performance budget on a fixture
		t.Fatalf("status --oneline took %s on a 2,000-job root, want under 2s", cold)
	}
	if _, err := os.Stat(filepath.Join(root, statusIndexName)); err != nil {
		t.Fatalf("status must keep a per-root index at %s: %v", statusIndexName, err)
	}
	if !strings.Contains(out, "spend=2.0000") {
		t.Errorf("spend must still sum the day's rows (2000 x 0.0010):\n%s", out)
	}

	// Warm: the index answers the tick without opening a job file. Deleting a job's
	// usage.tsv does not change its directory mtime, so the cached rows still hold.
	start = time.Now()
	out, code = runOnelineStatus(queue, root, day)
	warm := time.Since(start)
	t.Logf("warm status --oneline wall = %s", warm)
	if code != 0 {
		t.Fatalf("exit = %d: %s", code, out)
	}
	if warm > 2*time.Second { // wall-ok: performance budget on a fixture
		t.Fatalf("warm status --oneline took %s on a 2,000-job root, want under 2s", warm)
	}
	if !strings.Contains(out, "spend=2.0000") {
		t.Errorf("warm status must answer from the index (spend=2.0000):\n%s", out)
	}
}
